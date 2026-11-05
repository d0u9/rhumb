package deploy

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The manifest is the whole contract: this package imports nothing else of
// rhumb's, so a deployment cannot come to depend on the inventory.
func TestDeploy_ImportsNothingElseOfRhumb(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", ".").Output()
	if err != nil {
		t.Fatal(err)
	}
	for _, dep := range strings.Fields(string(out)) {
		if strings.HasPrefix(dep, "github.com/d0u9/rhumb/") && dep != "github.com/d0u9/rhumb/deploy" {
			t.Errorf("deploy depends on %s", dep)
		}
	}
}

// testDefs holds invented definitions: the tests exercise rhumb, not a program.
const testDefs = "testdata/services"

func writeExport(t *testing.T) string {
	t.Helper()
	src := t.TempDir()
	os.WriteFile(filepath.Join(src, "config.json"), []byte(`{"local_port": 1080}`), 0o600)
	os.WriteFile(filepath.Join(src, ManifestFile), []byte(`schema: 1
node: laptop
instance: laptop-sea-01-ss-example-server-json-browser
service: example-client
runtime: host
files:
  - path: config.json
`), 0o600)
	return src
}

func TestBuild_WritesABundleThatKeepsVar(t *testing.T) {
	src := writeExport(t)
	dst := filepath.Join(t.TempDir(), "b")
	fake := filepath.Join(t.TempDir(), "example-client")
	os.WriteFile(fake, []byte("#!/bin/sh\n"), 0o755)
	opt := Options{Services: testDefs, Platform: "darwin/arm64", Binary: fake}
	if err := Build(src, dst, opt); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"ctl", "bin/example-client", "conf/config.json", "manifest.yaml"} {
		if _, err := os.Stat(filepath.Join(dst, f)); err != nil {
			t.Errorf("bundle lacks %s", f)
		}
	}
	ctl, _ := os.ReadFile(filepath.Join(dst, "ctl"))
	if !strings.Contains(string(ctl), `set -- "$BIN"'/example-client' '-c' "$DIR/conf"'/config.json'`) {
		t.Errorf("ctl command line:\n%s", ctl)
	}
	if out, err := exec.Command("sh", "-n", filepath.Join(dst, "ctl")).CombinedOutput(); err != nil {
		t.Fatalf("ctl does not parse: %s", out)
	}
	// On macOS install only puts the program in place, where it was
	// unpacked; start and enable are asked for.
	if !strings.Contains(string(ctl), "\nDIR=\"$HERE\"\n") {
		t.Error("a macOS bundle with no dir does not stay where it is")
	}
	_, rest, _ := strings.Cut(string(ctl), "\ninstall)")
	install, _, _ := strings.Cut(rest, ";;")
	for _, never := range []string{"launchctl", "write_plist", "; start", "\tstart"} {
		if strings.Contains(install, never) {
			t.Errorf("install runs %s:\n%s", never, install)
		}
	}
	os.MkdirAll(filepath.Join(dst, "var"), 0o755)
	os.WriteFile(filepath.Join(dst, "var", "state"), nil, 0o600)
	if err := Build(src, dst, opt); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, "var", "state")); err != nil {
		t.Error("rebuilding removed var/")
	}
}

// A Linux bundle takes its platform from the manifest and hands the
// service's env files to systemd rather than writing them into the unit.
func TestBuild_LinuxFromTheManifest(t *testing.T) {
	src := t.TempDir()
	os.WriteFile(filepath.Join(src, "server.env"), []byte("EXAMPLE_PORT=8080\n"), 0o600)
	os.WriteFile(filepath.Join(src, ManifestFile), []byte(`schema: 1
node: sea
instance: example-web-01
service: example-web
runtime: host
platform: linux/amd64
files:
  - path: server.env
`), 0o600)
	dst := filepath.Join(t.TempDir(), "b")
	if err := Build(src, dst, Options{Services: testDefs, Binary: "/bin/sh"}); err != nil {
		t.Fatal(err)
	}
	ctl, _ := os.ReadFile(filepath.Join(dst, "ctl"))
	for _, want := range []string{"systemctl", `set -- "$DIR/conf"'/server.env'`, "LABEL=rhumb.sea.example-web-01\n", "DIR='/srv/rhumb/example-web'\n", "copy_in;"} {
		if !strings.Contains(string(ctl), want) {
			t.Errorf("ctl lacks %q:\n%s", want, ctl)
		}
	}
	if !strings.Contains(string(ctl), `echo "CapabilityBoundingSet="`) {
		t.Error("a unit without capabilities does not drop them all")
	}
	if strings.Contains(string(ctl), "EXAMPLE_PORT") {
		t.Error("ctl carries the env file's contents")
	}
	if out, err := exec.Command("sh", "-n", filepath.Join(dst, "ctl")).CombinedOutput(); err != nil {
		t.Fatalf("ctl does not parse: %s", out)
	}
}

func TestBuild_RefusesAnotherBundlesDirectory(t *testing.T) {
	dst := t.TempDir()
	os.WriteFile(filepath.Join(dst, "ctl"), []byte("LABEL=rhumb.other.x\n"), 0o755)
	err := Build(writeExport(t), dst, Options{Services: testDefs, Platform: "darwin/arm64", Binary: "/bin/sh"})
	if err == nil || !strings.Contains(err.Error(), "another bundle") {
		t.Fatalf("got %v", err)
	}
}

func TestLeftovers_OnlyWhereTheBundleIsGoneAndItsDiskIsNot(t *testing.T) {
	home := t.TempDir()
	parent := t.TempDir()
	agents := filepath.Join(home, "Library", "LaunchAgents")
	bin := filepath.Join(home, ".local", "bin")
	os.MkdirAll(agents, 0o755)
	os.MkdirAll(bin, 0o755)
	os.MkdirAll(filepath.Join(parent, "alive"), 0o755)
	os.WriteFile(filepath.Join(parent, "alive", "ctl"), nil, 0o755)
	// What launchd recreates for a deleted bundle that kept running.
	os.MkdirAll(filepath.Join(parent, "gone", "var", "log"), 0o755)
	plist := func(label, bundle string) {
		os.WriteFile(filepath.Join(agents, label+".plist"),
			[]byte("<key>RhumbBundle</key><string>"+bundle+"</string>"), 0o644)
	}
	plist("rhumb.n.gone", filepath.Join(parent, "gone"))
	plist("rhumb.n.alive", filepath.Join(parent, "alive"))
	plist("rhumb.n.unmounted", "/Volumes/not-mounted-rhumb-test/b")
	os.WriteFile(filepath.Join(bin, "gone"), []byte("#!/bin/sh\n"+"# rhumb-bundle: "+filepath.Join(parent, "gone")+"\n"), 0o755)
	os.WriteFile(filepath.Join(bin, "other"), []byte("#!/bin/sh\necho hi\n"), 0o755)

	got, err := Leftovers(home, "", "")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, l := range got {
		names = append(names, l.Kind+":"+filepath.Base(l.Path))
	}
	if strings.Join(names, " ") != "launchd:rhumb.n.gone.plist shim:gone" {
		t.Errorf("got %v", names)
	}
}

// A definition writes one source, and what only the machine can do is
// written into ctl install.
func TestBuild_BinarySources(t *testing.T) {
	for source, want := range map[string]string{
		"apt: shadowsocks-rust":        "apt-get install -y 'shadowsocks-rust'",
		"path: /opt/ss/example-client": "BIN='/opt/ss'",
		"apt: x\n  path: /y":           "",
	} {
		defs := t.TempDir()
		os.WriteFile(filepath.Join(defs, "example-client.yaml"), []byte("binary:\n  name: example-client\n  "+source+"\ncommand: [\"{bin}/example-client\"]\n"), 0o644)
		dst := filepath.Join(t.TempDir(), "b")
		err := Build(writeExport(t), dst, Options{Platform: "linux/amd64", Services: defs})
		if want == "" {
			if err == nil {
				t.Errorf("%q: built from two sources", source)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%q: %v", source, err)
		}
		if _, err := os.Stat(filepath.Join(dst, "bin", "example-client")); err == nil {
			t.Errorf("%q: the bundle carries the binary", source)
		}
		ctl, _ := os.ReadFile(filepath.Join(dst, "ctl"))
		if !strings.Contains(string(ctl), want) {
			t.Errorf("%q: ctl lacks %q:\n%s", source, want, ctl)
		}
		if out, err := exec.Command("sh", "-n", filepath.Join(dst, "ctl")).CombinedOutput(); err != nil {
			t.Fatalf("%q: ctl does not parse: %s", source, out)
		}
	}
}

func TestRelease_GitHubAssetName(t *testing.T) {
	r := Release{GitHub: "o/r", Version: "2.1.0", URL: "p-v{version}-{target}.tar.gz", Targets: map[string]string{"linux/amd64": "x86_64"}}
	got, err := r.URLFor("linux/amd64")
	if err != nil || got != "https://github.com/o/r/releases/download/v2.1.0/p-v2.1.0-x86_64.tar.gz" {
		t.Fatalf("URLFor = %q, %v", got, err)
	}
	if _, err := r.URLFor("darwin/arm64"); err == nil {
		t.Fatal("a platform without a build gave a URL")
	}
}

// A release downloaded on the machine leaves bin/ empty, and ctl install
// looks up "latest" and fetches the platform's archive itself.
func TestBuild_ReleaseDownloadedOnTheMachine(t *testing.T) {
	defs := t.TempDir()
	os.WriteFile(filepath.Join(defs, "example-client.yaml"), []byte(`binary:
  name: example-client
  release:
    github: o/r
    version: latest
    url: ss-v{version}-{target}.tar.gz
    targets: {linux/amd64: x86_64}
command: ["{bin}/example-client"]
`), 0o644)
	dst := filepath.Join(t.TempDir(), "b")
	if err := Build(writeExport(t), dst, Options{Platform: "linux/amd64", Services: defs, Download: DownloadInstall}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, "bin", "example-client")); err == nil {
		t.Error("the bundle carries the binary")
	}
	ctl, _ := os.ReadFile(filepath.Join(dst, "ctl"))
	for _, want := range []string{"api.github.com/repos/o/r/releases/latest", `'https://github.com/o/r/releases/download/v'"$VERSION"'/ss-v'"$VERSION"'-x86_64.tar.gz'`} {
		if !strings.Contains(string(ctl), want) {
			t.Errorf("ctl lacks %q:\n%s", want, ctl)
		}
	}
	if out, err := exec.Command("sh", "-n", filepath.Join(dst, "ctl")).CombinedOutput(); err != nil {
		t.Fatalf("ctl does not parse: %s", out)
	}
}

func TestDescribe(t *testing.T) {
	m, err := ParseManifest([]byte("schema: 1\nnode: n\ninstance: i\nservice: example-web\nruntime: host\nplatform: linux/amd64\n"))
	if err != nil {
		t.Fatal(err)
	}
	d := Describe(m, Options{Services: testDefs, Download: DownloadInstall})
	if d.Err != nil || d.Manager != "systemd" || d.Platform != "linux/amd64" || d.Binary != "example-web latest release, downloaded on the machine" {
		t.Fatalf("Describe = %+v", d)
	}
	m.Platform = "plan9/386"
	if Describe(m, Options{Services: testDefs}).Err == nil {
		t.Fatal("a platform with no service manager described as buildable")
	}
}

// A proxy listens below 1024: its unit gives it that capability and no other,
// and a launchd bundle, with nothing to give it, refuses.
func TestBuild_Capabilities(t *testing.T) {
	src := t.TempDir()
	os.WriteFile(filepath.Join(src, "proxy.conf"), nil, 0o600)
	os.WriteFile(filepath.Join(src, "proxy.env"), nil, 0o600)
	os.WriteFile(filepath.Join(src, ManifestFile), []byte("schema: 1\nnode: sea\ninstance: example-proxy-01\nservice: example-proxy\nruntime: host\nfiles:\n  - path: proxy.conf\n  - path: proxy.env\n"), 0o600)
	dst := filepath.Join(t.TempDir(), "b")
	if err := Build(src, dst, Options{Services: testDefs, Platform: "linux/amd64", Binary: "/bin/sh"}); err != nil {
		t.Fatal(err)
	}
	ctl, _ := os.ReadFile(filepath.Join(dst, "ctl"))
	for _, want := range []string{`echo "AmbientCapabilities=CAP_NET_BIND_SERVICE"`, `echo "CapabilityBoundingSet=CAP_NET_BIND_SERVICE"`, "XDG_DATA_HOME=$DIR/var/share"} {
		if !strings.Contains(string(ctl), want) {
			t.Errorf("ctl lacks %q", want)
		}
	}
	if err := Build(src, filepath.Join(t.TempDir(), "m"), Options{Services: testDefs, Platform: "darwin/arm64", Binary: "/bin/sh"}); err == nil {
		t.Error("a launchd bundle took capabilities")
	}
}

func TestRelease_TagAndBareBinary(t *testing.T) {
	r := Release{GitHub: "o/r", Version: "2.12.3", Tag: "app/v{version}", URL: "p-{target}", Targets: map[string]string{"linux/amd64": "linux-amd64"}}
	got, err := r.URLFor("linux/amd64")
	if err != nil || got != "https://github.com/o/r/releases/download/app/v2.12.3/p-linux-amd64" {
		t.Fatalf("got %q, %v", got, err)
	}
	if r.Archive() {
		t.Error("a bare binary is taken for an archive")
	}
	if !(Release{URL: "c_{version}.tar.gz"}).Archive() {
		t.Error("a tar.gz is not taken for an archive")
	}
}

func TestDownload_NodeDecidesUnlessTheBuilderDoes(t *testing.T) {
	m := Manifest{Download: DownloadBuild}
	for _, c := range []struct {
		m    Manifest
		opt  string
		want string
	}{
		{Manifest{}, "", DownloadInstall},
		{m, "", DownloadBuild},
		{m, DownloadInstall, DownloadInstall},
	} {
		if got := downloadFor(c.m, Options{Download: c.opt}); got != c.want {
			t.Errorf("node %q, builder %q: got %q, want %q", c.m.Download, c.opt, got, c.want)
		}
	}
}

func TestBuild_SelfSignedOnlyWhereDefined(t *testing.T) {
	src := t.TempDir()
	os.WriteFile(filepath.Join(src, "config.yaml"), nil, 0o600)
	os.WriteFile(filepath.Join(src, ManifestFile), []byte(`schema: 1
node: n
instance: tunnel-01
service: example-tunnel
runtime: host
platform: linux/amd64
files:
  - path: config.yaml
ports:
  - {name: users, port: 443, published: a.example}
`), 0o600)
	dst := filepath.Join(t.TempDir(), "b")
	if err := Build(src, dst, Options{Services: testDefs, Binary: "/bin/sh"}); err != nil {
		t.Fatal(err)
	}
	ctl, _ := os.ReadFile(filepath.Join(dst, "ctl"))
	for _, want := range []string{"self_signed; write_unit", "'subjectAltName=DNS:a.example'", `"$DIR/var/"'tls/fullchain.pem'`} {
		if !strings.Contains(string(ctl), want) {
			t.Errorf("ctl lacks %q", want)
		}
	}
}

func TestBuild_HooksRunAsRootAndOnUninstall(t *testing.T) {
	src := t.TempDir()
	os.WriteFile(filepath.Join(src, "config.yaml"), nil, 0o600)
	os.WriteFile(filepath.Join(src, ManifestFile), []byte("schema: 1\nnode: n\ninstance: h\nservice: example-tunnel\nruntime: host\nplatform: linux/amd64\nfiles:\n  - path: config.yaml\n"), 0o600)
	dst := filepath.Join(t.TempDir(), "b")
	if err := Build(src, dst, Options{Services: testDefs, Binary: "/bin/sh"}); err != nil {
		t.Fatal(err)
	}
	ctl, _ := os.ReadFile(filepath.Join(dst, "ctl"))
	for _, want := range []string{
		`hook ExecStartPre "$DIR/conf"'/hopping.sh' 'up'`,
		`hook ExecStopPost "$DIR/conf"'/hopping.sh' 'down'`,
		"\trun_stop_hook\n",
	} {
		if !strings.Contains(string(ctl), want) {
			t.Errorf("ctl lacks %q", want)
		}
	}
	if err := Build(src, filepath.Join(t.TempDir(), "m"), Options{Services: testDefs, Platform: "darwin/arm64", Binary: "/bin/sh"}); err == nil {
		t.Error("a launchd bundle took hooks")
	}
}

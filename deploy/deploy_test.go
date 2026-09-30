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

func writeExport(t *testing.T) string {
	t.Helper()
	src := t.TempDir()
	os.WriteFile(filepath.Join(src, "config.json"), []byte(`{"local_port": 1080}`), 0o600)
	os.WriteFile(filepath.Join(src, ManifestFile), []byte(`schema: 1
node: laptop
instance: laptop-sea-01-ss-ssserver-json-browser
service: sslocal
runtime: host
files:
  - path: config.json
`), 0o600)
	return src
}

func TestBuild_WritesABundleThatKeepsVar(t *testing.T) {
	src := writeExport(t)
	dst := filepath.Join(t.TempDir(), "b")
	fake := filepath.Join(t.TempDir(), "sslocal")
	os.WriteFile(fake, []byte("#!/bin/sh\n"), 0o755)
	opt := Options{Platform: "darwin/arm64", Binary: fake}
	if err := Build(src, dst, opt); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"ctl", "bin/sslocal", "conf/config.json", "manifest.yaml"} {
		if _, err := os.Stat(filepath.Join(dst, f)); err != nil {
			t.Errorf("bundle lacks %s", f)
		}
	}
	ctl, _ := os.ReadFile(filepath.Join(dst, "ctl"))
	if !strings.Contains(string(ctl), `set -- "$BIN"'/sslocal' '-c' "$DIR/conf"'/config.json'`) {
		t.Errorf("ctl command line:\n%s", ctl)
	}
	if out, err := exec.Command("sh", "-n", filepath.Join(dst, "ctl")).CombinedOutput(); err != nil {
		t.Fatalf("ctl does not parse: %s", out)
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
	os.WriteFile(filepath.Join(src, "server.env"), []byte("MICROBIN_PORT=8080\n"), 0o600)
	os.WriteFile(filepath.Join(src, ManifestFile), []byte(`schema: 1
node: sea
instance: microbin-01
service: microbin
runtime: host
platform: linux/amd64
files:
  - path: server.env
`), 0o600)
	dst := filepath.Join(t.TempDir(), "b")
	if err := Build(src, dst, Options{Binary: "/bin/sh"}); err != nil {
		t.Fatal(err)
	}
	ctl, _ := os.ReadFile(filepath.Join(dst, "ctl"))
	for _, want := range []string{"systemctl", `set -- "$DIR/conf"'/server.env'`, "LABEL=rhumb.sea.microbin-01\n"} {
		if !strings.Contains(string(ctl), want) {
			t.Errorf("ctl lacks %q:\n%s", want, ctl)
		}
	}
	if strings.Contains(string(ctl), "MICROBIN_PORT") {
		t.Error("ctl carries the env file's contents")
	}
	if out, err := exec.Command("sh", "-n", filepath.Join(dst, "ctl")).CombinedOutput(); err != nil {
		t.Fatalf("ctl does not parse: %s", out)
	}
}

func TestBuild_RefusesAnotherBundlesDirectory(t *testing.T) {
	dst := t.TempDir()
	os.WriteFile(filepath.Join(dst, "ctl"), []byte("LABEL=rhumb.other.x\n"), 0o755)
	err := Build(writeExport(t), dst, Options{Platform: "darwin/arm64", Binary: "/bin/sh"})
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
	os.WriteFile(filepath.Join(bin, "gone"), []byte("#!/bin/sh\n"+shimMark+filepath.Join(parent, "gone")+"\n"), 0o755)
	os.WriteFile(filepath.Join(bin, "other"), []byte("#!/bin/sh\necho hi\n"), 0o755)

	got, err := Leftovers(home)
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
		"apt: shadowsocks-rust": "apt-get install -y 'shadowsocks-rust'",
		"path: /opt/ss/sslocal": "BIN='/opt/ss'",
		"apt: x\n  path: /y":    "",
	} {
		defs := t.TempDir()
		os.WriteFile(filepath.Join(defs, "sslocal.yaml"), []byte("binary:\n  name: sslocal\n  "+source+"\ncommand: [\"{bin}/sslocal\"]\n"), 0o644)
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
		if _, err := os.Stat(filepath.Join(dst, "bin", "sslocal")); err == nil {
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
	os.WriteFile(filepath.Join(defs, "sslocal.yaml"), []byte(`binary:
  name: sslocal
  release:
    github: o/r
    version: latest
    url: ss-v{version}-{target}.tar.gz
    targets: {linux/amd64: x86_64}
command: ["{bin}/sslocal"]
`), 0o644)
	dst := filepath.Join(t.TempDir(), "b")
	if err := Build(writeExport(t), dst, Options{Platform: "linux/amd64", Services: defs, Download: DownloadInstall}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, "bin", "sslocal")); err == nil {
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

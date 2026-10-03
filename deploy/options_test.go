package deploy

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// writeHostExport writes a linux host export of service microbin on node
// host-a, with dir written into its manifest when dir is not empty.
func writeHostExport(t *testing.T, dir string) string {
	t.Helper()
	src := t.TempDir()
	man := "schema: 1\nnode: host-a\ninstance: app-01\nservice: microbin\nruntime: host\nplatform: linux/amd64\n"
	if dir != "" {
		man += "dir: " + dir + "\n"
	}
	if err := os.WriteFile(filepath.Join(src, ManifestFile), []byte(man), 0o600); err != nil {
		t.Fatal(err)
	}
	return src
}

func buildCtl(t *testing.T, src string, opt Options) string {
	t.Helper()
	opt.Binary = "/bin/sh"
	dst := filepath.Join(t.TempDir(), "b")
	if err := Build(src, dst, opt); err != nil {
		t.Fatal(err)
	}
	ctl, err := os.ReadFile(filepath.Join(dst, "ctl"))
	if err != nil {
		t.Fatal(err)
	}
	return string(ctl)
}

func TestBuild_InstallRoot(t *testing.T) {
	for _, tc := range []struct {
		name, dir, root, want string
	}{
		{"default", "", "", "DIR='/srv/rhumb/microbin'\n"},
		{"root", "", "/srv/example", "DIR='/srv/example/microbin'\n"},
		{"manifest dir wins", "/opt/example/app", "/srv/example", "DIR='/opt/example/app'\n"},
	} {
		ctl := buildCtl(t, writeHostExport(t, tc.dir), Options{InstallRoot: tc.root})
		if !strings.Contains(ctl, tc.want) {
			t.Errorf("%s: ctl lacks %q", tc.name, tc.want)
		}
	}
}

func TestBuild_InstallRootMustBeAbsolute(t *testing.T) {
	err := Build(writeHostExport(t, ""), filepath.Join(t.TempDir(), "b"), Options{Binary: "/bin/sh", InstallRoot: "srv/example"})
	if err == nil || !strings.Contains(err.Error(), "not absolute") {
		t.Fatalf("got %v", err)
	}
}

func TestBuild_LabelPrefix(t *testing.T) {
	ctl := buildCtl(t, writeHostExport(t, ""), Options{LabelPrefix: "example"})
	for _, want := range []string{"LABEL=example.host-a.app-01\n", `UNIT="/etc/systemd/system/$LABEL.service"`} {
		if !strings.Contains(ctl, want) {
			t.Errorf("ctl lacks %q", want)
		}
	}
	if strings.Contains(ctl, "LABEL=rhumb.") {
		t.Error("ctl still carries the default prefix")
	}
	err := Build(writeHostExport(t, ""), filepath.Join(t.TempDir(), "b"), Options{Binary: "/bin/sh", LabelPrefix: "a b"})
	if err == nil || !strings.Contains(err.Error(), "label prefix") {
		t.Fatalf("a prefix with a space: got %v", err)
	}
}

func TestLeftovers_LabelPrefix(t *testing.T) {
	home := t.TempDir()
	agents := filepath.Join(home, "Library", "LaunchAgents")
	os.MkdirAll(agents, 0o755)
	gone := filepath.Join(t.TempDir(), "gone")
	for _, label := range []string{"example.host-a.app-01", "rhumb.host-a.app-02"} {
		os.WriteFile(filepath.Join(agents, label+".plist"), []byte("<key>RhumbBundle</key><string>"+gone+"</string>"), 0o644)
	}
	for prefix, want := range map[string]string{"example": "example.host-a.app-01", "": "rhumb.host-a.app-02"} {
		got, err := Leftovers(home, prefix, "")
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].Label != want {
			t.Errorf("Leftovers(%q) = %v, want only %s", prefix, got, want)
		}
	}
}

// Relabel replaces the same instance built under another prefix, and only
// that: another instance, or a macOS bundle, is still refused.
func TestBuild_Relabel(t *testing.T) {
	build := func(dst, platform string, opt Options) error {
		src := writeHostExport(t, "")
		man, _ := os.ReadFile(filepath.Join(src, ManifestFile))
		os.WriteFile(filepath.Join(src, ManifestFile), []byte(strings.Replace(string(man), "linux/amd64", platform, 1)), 0o600)
		defs := t.TempDir()
		os.WriteFile(filepath.Join(defs, "microbin.yaml"), []byte("binary:\n  name: microbin\n  path: /opt/example/bin/microbin\ncommand: [microbin]\n"), 0o644)
		opt.Binary, opt.Services = "/bin/sh", defs
		return Build(src, dst, opt)
	}
	for _, tc := range []struct {
		name, platform, old string
		relabel             bool
		ok                  bool
	}{
		{"without relabel", "linux/amd64", "rhumb.host-a.app-01", false, false},
		{"same instance", "linux/amd64", "rhumb.host-a.app-01", true, true},
		{"another instance", "linux/amd64", "rhumb.host-a.app-02", true, false},
		{"macOS", "darwin/arm64", "rhumb.host-a.app-01", true, false},
	} {
		dst := t.TempDir()
		os.WriteFile(filepath.Join(dst, "ctl"), []byte("#!/bin/sh\nLABEL="+tc.old+"\n"), 0o755)
		err := build(dst, tc.platform, Options{LabelPrefix: "example", Relabel: tc.relabel})
		if tc.ok != (err == nil) {
			t.Errorf("%s: err = %v", tc.name, err)
		}
		if !tc.ok && (err == nil || !strings.Contains(err.Error(), "another bundle")) {
			t.Errorf("%s: err = %v, want another bundle", tc.name, err)
		}
	}
}

func TestBuild_Tool(t *testing.T) {
	for _, platform := range []string{"linux/amd64", "darwin/arm64"} {
		t.Run(platform, func(t *testing.T) {
			src := writeHostExport(t, "")
			defs := t.TempDir()
			if err := os.WriteFile(filepath.Join(defs, "microbin.yaml"), []byte("binary:\n  name: microbin\n  path: /opt/example/bin/microbin\ncommand: [microbin]\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			defaults := buildCtl(t, src, Options{Platform: platform, Services: defs})
			explicit := buildCtl(t, src, Options{Platform: platform, Services: defs, Tool: "rhumb"})
			if defaults != explicit {
				t.Fatal("empty tool changed default output")
			}
			if !strings.Contains(defaults, "written by rhumb deploy.") || !strings.Contains(defaults, `MARK="# rhumb-bundle: $DIR"`) {
				t.Fatal("historical default changed")
			}
			ctl := buildCtl(t, src, Options{Platform: platform, Services: defs, Tool: "example", InstallRoot: "/srv/example"})
			for _, want := range []string{"written by example.", `MARK="# example-bundle: $DIR"`} {
				if !strings.Contains(ctl, want) {
					t.Fatalf("ctl lacks %q", want)
				}
			}
			if platform == "darwin/arm64" && !strings.Contains(ctl, "<key>ExampleBundle</key>") {
				t.Fatal("plist uses wrong key")
			}
			if strings.Contains(ctl, "rhumb-bundle") || strings.Contains(ctl, "RhumbBundle") || strings.Contains(ctl, "written by rhumb") {
				t.Fatal("custom tool still contains default branding")
			}
		})
	}
	src := writeHostExport(t, "")
	man, _ := ReadManifest(src)
	man.Container = &Container{Dir: "/srv/example", Name: "app-01", Image: "example/app:1"}
	ctl, err := renderDockerCtl(man, "example", "example")
	if err != nil || !strings.Contains(string(ctl), "written by example.") {
		t.Fatalf("docker tool: %v", err)
	}
	for _, tool := range []string{"a b", "a\nb", "$(id)", "a.b"} {
		err := Build(src, filepath.Join(t.TempDir(), "b"), Options{Platform: "linux/amd64", Tool: tool})
		if err == nil || !strings.Contains(err.Error(), "tool") {
			t.Fatalf("tool %q: err = %v, want a tool error", tool, err)
		}
	}
}

func TestLeftovers_Tool(t *testing.T) {
	home := t.TempDir()
	agents := filepath.Join(home, "Library", "LaunchAgents")
	shims := filepath.Join(home, ".local", "bin")
	for _, dir := range []string{agents, shims} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	bundle := filepath.Join(t.TempDir(), "gone")
	for _, tool := range []string{"example", "rhumb", "other"} {
		label := "example.host-a." + tool
		if err := os.WriteFile(filepath.Join(agents, label+".plist"), []byte("<key>"+toolBundleKey(tool)+"</key><string>"+bundle+"</string>"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(shims, tool), []byte("#!/bin/sh\n# "+tool+"-bundle: "+bundle+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Leftovers(home, "example", "example")
	if err != nil || len(got) != 2 {
		t.Fatalf("leftovers = %v, %v", got, err)
	}
	for _, l := range got {
		if l.Path != filepath.Join(shims, "example") && l.Path != filepath.Join(agents, "example.host-a.example.plist") {
			t.Fatal("gc recognized another tool")
		}
	}
	if _, err := Leftovers(home, "example", "bad name"); err == nil {
		t.Fatal("gc accepted invalid tool")
	}
}

func TestCtl_ToolLinkAndUnlink(t *testing.T) {
	home, dst, defs := t.TempDir(), t.TempDir(), t.TempDir()
	var err error
	dst, err = filepath.EvalSymlinks(dst)
	if err != nil {
		t.Fatal(err)
	}
	src := writeHostExport(t, "")
	if err := os.WriteFile(filepath.Join(defs, "microbin.yaml"), []byte("binary:\n  name: microbin\n  path: /opt/example/bin/microbin\ncommand: [microbin]\nexpose: [demo]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Build(src, dst, Options{Platform: "darwin/arm64", Binary: "/bin/sh", Services: defs, Tool: "example", LabelPrefix: "example"}); err != nil {
		t.Fatal(err)
	}
	agent := filepath.Join(home, "Library", "LaunchAgents", "example.host-a.app-01.plist")
	if err := os.MkdirAll(filepath.Dir(agent), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(agent, []byte("<dict>\n\t<key>RhumbBundle</key><string>/srv/example</string>\n\t\t<key>PATH</key><string>/opt/example/bin</string>\n</dict>\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(action string, wantOK bool) {
		t.Helper()
		cmd := exec.Command("/bin/sh", filepath.Join(dst, "ctl"), action)
		cmd.Env = append(os.Environ(), "HOME="+home)
		out, err := cmd.CombinedOutput()
		if (err == nil) != wantOK {
			t.Fatalf("%s: %v: %s", action, err, out)
		}
	}
	run("link", true)
	shim := filepath.Join(home, ".local", "bin", "demo")
	data, err := os.ReadFile(shim)
	if err != nil || !strings.Contains(string(data), "# example-bundle: "+dst) {
		t.Fatal("link did not write custom marker")
	}
	data, err = os.ReadFile(agent)
	if err != nil || !strings.Contains(string(data), "<key>ExampleBundle</key>") || strings.Contains(string(data), "RhumbBundle") {
		t.Fatal("link did not update existing plist")
	}
	if !strings.Contains(string(data), "<string>/opt/example/bin</string>") {
		t.Fatal("link changed plist beyond its bundle key")
	}
	run("unlink", true)
	if _, err := os.Stat(shim); !os.IsNotExist(err) {
		t.Fatal("unlink did not recognize new marker")
	}
	if err := os.WriteFile(shim, []byte("#!/bin/sh\n# rhumb-bundle: "+dst+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	run("unlink", true)
	if _, err := os.Stat(shim); err != nil {
		t.Fatal("unlink removed another tool's shim")
	}
	run("link", false)
}

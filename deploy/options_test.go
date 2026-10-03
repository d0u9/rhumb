package deploy

import (
	"os"
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
		got, err := Leftovers(home, prefix)
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

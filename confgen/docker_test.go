package confgen

import (
	"path/filepath"
	"strings"
	"testing"
)

func loadDockerYAML(t *testing.T, content string) (*Docker, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), DockerFilename)
	writeFile(t, path, content)
	return loadDocker(path)
}

func TestLoadDocker_ReadsEveryShape(t *testing.T) {
	d, err := loadDockerYAML(t, `
ports: {web: tcp, dns: both}
mounts:
  conf: {target: /etc/app, files: [app.conf], mode: "0600"}
files:
  app.env: {env_file: true}
  config.json: {target: /etc/app/config.json}
state:
  data: /data
  db: {target: /var/lib/db, named: true}
privileges: {cap_add: [NET_ADMIN]}
setup: setup.sh
defaults: {image: example:1, container_name: srv.app}
`)
	if err != nil {
		t.Fatal(err)
	}
	if d.State["data"].Target != "/data" || d.State["data"].Named {
		t.Errorf("a bare state is a directory at that path: %+v", d.State["data"])
	}
	if !d.State["db"].Named {
		t.Errorf("state db: %+v", d.State["db"])
	}
}

func TestLoadDocker_OwnCompose(t *testing.T) {
	d, err := loadDockerYAML(t, "compose: c.yaml\noneshot: true\nsetup: s.sh\nteardown: t.sh\nfiles: {run: {mode: \"0700\"}}\ndefaults: {dir: /srv/x}\n")
	if err != nil {
		t.Fatal(err)
	}
	if d.Compose != "c.yaml" || !d.OneShot || d.Teardown != "t.sh" {
		t.Errorf("%+v", d)
	}
}

func TestLoadDocker_Refuses(t *testing.T) {
	for _, tc := range []struct{ name, yaml, want string }{
		{"a transport", `ports: {web: http}`, "not tcp, udp or both"},
		{"a file placed twice", "mounts: {conf: {target: /c, files: [a]}}\nfiles: {a: {env_file: true}}", "placed twice"},
		{"two places for one file", `files: {a: {env_file: true, target: /a}}`, "exclude each other"},
		{"a mount without a target", `mounts: {conf: {files: [a]}}`, "target is required"},
		{"a privilege not allowed", `privileges: {network_mode: host}`, "not one of"},
		{"a deploy key not known", `defaults: {root: /srv}`, "unknown root"},
		{"an unknown key", `start: always`, "not found"},
		{"a field its own compose would ignore", "compose: c.yaml\nports: {web: tcp}\ndefaults: {image: x}", "defaults.image, ports would be ignored"},
		{"oneshot without its own compose", `oneshot: true`, "only for a service that writes its own compose"},
		{"its own compose placed elsewhere", "compose: c.yaml\nfiles: {c.yaml: {host: /etc}}", "placed elsewhere"},
		{"setup inside a mount", "mounts: {c: {target: /c, files: [s.sh]}}\nsetup: s.sh", "in a mount"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadDockerYAML(t, tc.yaml)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %q", err, tc.want)
			}
		})
	}
}

// A service starts its containers from docker.yaml or from deploy/, never
// both: two descriptions of one container would disagree sooner or later.
func TestLoad_RefusesDockerBesideDeploy(t *testing.T) {
	root := t.TempDir()
	svc := filepath.Join(root, ServicesDir, "app")
	writeFile(t, filepath.Join(svc, ManifestFilename), "template: t.tmpl\ndefaults: document\noutput: app.conf\nauth: none\n")
	writeFile(t, filepath.Join(svc, DeployDir, ManifestFilename), "defaults: document\nfiles: [{template: c.tmpl, output: compose.yaml}]\n")
	writeFile(t, filepath.Join(svc, DockerFilename), "defaults: {image: example:1}\n")
	r, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	s := r.Services[0]
	if s.Docker != nil || !strings.Contains(s.DockerBroken, "also holds deploy/") {
		t.Fatalf("Docker = %v, DockerBroken = %q", s.Docker, s.DockerBroken)
	}
}

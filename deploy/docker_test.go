package deploy

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const dockerManifest = `schema: 1
node: home
instance: app-01
service: app
runtime: docker
files:
  - {path: app.conf, place: conf/app.conf, mode: "0600"}
  - {path: app.env, place: app.env, mode: "0600"}
  - {path: setup.sh, place: setup.sh, mode: "0644"}
ports:
  - {name: dns, port: 53, bind: [10.0.0.1], transport: both}
  - {name: web, port: 80, bind: [10.0.0.1, 127.0.0.1], host_port: 8080, transport: tcp}
  - {name: admin, port: 81, bind: [], transport: tcp}
networks:
  - {name: apps, subnet: 172.29.0.0/24}
  - {name: dmz, subnet: 172.31.0.0/24, gateway: 172.31.0.1, address: 172.31.0.10}
container:
  dir: /srv/docker/app
  name: srv.app
  hostname: srv.app
  image: example:1
  restart: unless-stopped
  user: "1000:1000"
  env_files: [app.env]
  environment: {MODE: prod}
  mounts:
    - {source: /srv/docker/app/conf, target: /etc/app, ro: true, create: true, mode: "0700"}
    - {source: /media, target: /media, propagation: rslave}
  volumes: {data: /var/lib/app}
  privileges: {cap_add: [NET_ADMIN]}
  reload: [app, reload]
  setup: setup.sh
`

func writeDockerExport(t *testing.T) string {
	t.Helper()
	src := t.TempDir()
	for _, f := range []string{"app.conf", "app.env", "setup.sh"} {
		os.WriteFile(filepath.Join(src, f), []byte(f+"\n"), 0o600)
	}
	os.WriteFile(filepath.Join(src, ManifestFile), []byte(dockerManifest), 0o600)
	return src
}

func TestCompose_WritesEveryPartOfTheContainer(t *testing.T) {
	m, err := ReadManifest(writeDockerExport(t))
	if err != nil {
		t.Fatal(err)
	}
	out, err := Compose(m)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Services map[string]map[string]any `yaml:"services"`
		Networks map[string]map[string]any `yaml:"networks"`
		Volumes  map[string]any            `yaml:"volumes"`
	}
	if err := yaml.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	svc := doc.Services["srv.app"]
	if svc == nil {
		t.Fatalf("no service srv.app:\n%s", out)
	}
	ports := strings.Join(toStrings(svc["ports"]), " ")
	for _, want := range []string{"10.0.0.1:53:53/tcp", "10.0.0.1:53:53/udp", "10.0.0.1:8080:80/tcp", "127.0.0.1:8080:80/tcp"} {
		if !strings.Contains(ports, want) {
			t.Errorf("ports %s lack %s", ports, want)
		}
	}
	if strings.Contains(ports, ":81/") {
		t.Errorf("a port bound nowhere is published: %s", ports)
	}
	if got := toStrings(svc["env_file"]); len(got) != 1 || got[0] != "/srv/docker/app/app.env" {
		t.Errorf("env_file = %v", got)
	}
	if svc["user"] != "1000:1000" || svc["cap_add"] == nil {
		t.Errorf("user or privileges missing:\n%s", out)
	}
	nets := svc["networks"].(map[string]any)
	if nets["dmz"].(map[string]any)["ipv4_address"] != "172.31.0.10" {
		t.Errorf("dmz address: %v", nets["dmz"])
	}
	if doc.Networks["apps"]["external"] != true {
		t.Errorf("networks are external: %v", doc.Networks)
	}
	if _, ok := doc.Volumes["data"]; !ok {
		t.Errorf("named volume data is not declared: %v", doc.Volumes)
	}
	if !strings.Contains(string(out), "propagation: rslave") || !strings.Contains(string(out), "read_only: true") {
		t.Errorf("mount options lost:\n%s", out)
	}
}

func TestCompose_RefusesAPrivilegeOverwritingAKey(t *testing.T) {
	m, _ := ReadManifest(writeDockerExport(t))
	m.Container.Privileges = map[string]any{"image": "other"}
	if _, err := Compose(m); err == nil || !strings.Contains(err.Error(), "rhumb writes") {
		t.Fatalf("got %v", err)
	}
}

func TestBuild_DockerBundle(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "b")
	if err := Build(writeDockerExport(t), dst, Options{}); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"ctl", "compose.yaml", "manifest.yaml", "files/app.conf", "files/app.env", "files/setup.sh"} {
		if _, err := os.Stat(filepath.Join(dst, f)); err != nil {
			t.Errorf("bundle lacks %s", f)
		}
	}
	ctl, _ := os.ReadFile(filepath.Join(dst, "ctl"))
	for _, want := range []string{
		"docker network create --subnet '172.31.0.0/24' --gateway '172.31.0.1' 'dmz'",
		"mkdir -p '/srv/docker/app/conf'\n\tchmod 0700 '/srv/docker/app/conf'",
		"place 'app.conf' '/srv/docker/app/conf/app.conf' 0600",
		"compose exec 'srv.app' 'app' 'reload'",
		"CONTAINER='srv.app' sh ./'setup.sh'",
	} {
		if !strings.Contains(string(ctl), want) {
			t.Errorf("ctl lacks %q", want)
		}
	}
	if strings.Contains(string(ctl), "mkdir -p '/media'") {
		t.Error("ctl creates a mount source it does not own")
	}
	if out, err := exec.Command("sh", "-n", filepath.Join(dst, "ctl")).CombinedOutput(); err != nil {
		t.Fatalf("ctl does not parse: %s", out)
	}
}

func TestBuild_RefusesAContainerWithoutOne(t *testing.T) {
	src := t.TempDir()
	os.WriteFile(filepath.Join(src, ManifestFile), []byte("schema: 1\nnode: n\ninstance: i\nservice: s\nruntime: docker\n"), 0o600)
	err := Build(src, t.TempDir(), Options{})
	if err == nil || !strings.Contains(err.Error(), "no docker.yaml") {
		t.Fatalf("got %v", err)
	}
}

func toStrings(v any) []string {
	var out []string
	list, _ := v.([]any)
	for _, x := range list {
		s, _ := x.(string)
		out = append(out, s)
	}
	return out
}

// A service that writes its own compose file: the bundle carries that file as
// compose.yaml, and a one-shot project is set up but not brought up.
func TestBuild_OwnCompose(t *testing.T) {
	src := t.TempDir()
	own := "services:\n  step:\n    image: example:1\n    profiles: [manual]\n"
	for f, body := range map[string]string{"compose.yaml": own, "run": "run\n", "setup.sh": "setup\n", "teardown.sh": "teardown\n"} {
		os.WriteFile(filepath.Join(src, f), []byte(body), 0o600)
	}
	os.WriteFile(filepath.Join(src, ManifestFile), []byte(`schema: 1
node: home
instance: job-01
service: job
runtime: docker
files:
  - {path: compose.yaml, mode: "0644"}
  - {path: run, place: run, mode: "0700"}
  - {path: setup.sh, place: setup.sh, mode: "0644"}
  - {path: teardown.sh, place: teardown.sh, mode: "0644"}
networks:
  - {name: apps, subnet: 172.29.0.0/24}
container:
  dir: /srv/docker/job
  compose: compose.yaml
  oneshot: true
  setup: setup.sh
  teardown: teardown.sh
`), 0o600)
	dst := filepath.Join(t.TempDir(), "b")
	if err := Build(src, dst, Options{}); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(filepath.Join(dst, "compose.yaml")); string(got) != own {
		t.Errorf("compose.yaml is not the service's own:\n%s", got)
	}
	ctl, _ := os.ReadFile(filepath.Join(dst, "ctl"))
	install := string(ctl)[strings.Index(string(ctl), "install)"):strings.Index(string(ctl), "up)")]
	for _, want := range []string{
		"docker network create --subnet '172.29.0.0/24' 'apps'",
		"place 'run' '/srv/docker/job/run' 0700",
		"(cd \"$DIR\" && sh ./'setup.sh')",
		"(cd \"$DIR\" && sh ./'teardown.sh')\n\tcompose down",
	} {
		if !strings.Contains(string(ctl), want) {
			t.Errorf("ctl lacks %q", want)
		}
	}
	if strings.Contains(install, "compose up") || strings.Contains(install, "place 'compose.yaml'") {
		t.Errorf("install of a one-shot project:\n%s", install)
	}
	if out, err := exec.Command("sh", "-n", filepath.Join(dst, "ctl")).CombinedOutput(); err != nil {
		t.Fatalf("ctl does not parse: %s", out)
	}
}

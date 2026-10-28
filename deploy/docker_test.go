package deploy

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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

// restartManifest is an invented DNS server that reads its configuration only
// at start: install restarts it when a placed file changed.
func restartManifest(dir string, restart bool) string {
	return `schema: 1
node: host-a
instance: example-dns
service: example-dns
runtime: docker
files:
  - {path: dns.conf, place: conf/dns.conf, mode: "0644"}
container:
  dir: ` + dir + `
  name: srv.example-dns
  hostname: srv.example-dns
  image: example:1
  user: alice
  restart_on_change: ` + strconv.FormatBool(restart) + `
`
}

func buildRestartBundle(t *testing.T, dir string, restart bool) string {
	t.Helper()
	src := t.TempDir()
	os.WriteFile(filepath.Join(src, "dns.conf"), []byte("zone a\n"), 0o600)
	os.WriteFile(filepath.Join(src, ManifestFile), []byte(restartManifest(dir, restart)), 0o600)
	dst := filepath.Join(t.TempDir(), "b")
	if err := Build(src, dst, Options{}); err != nil {
		t.Fatal(err)
	}
	return dst
}

func TestBuild_RestartOnChangeOnlyWhenSet(t *testing.T) {
	const branch = "compose restart 'srv.example-dns'"
	on, _ := os.ReadFile(filepath.Join(buildRestartBundle(t, "/srv/docker/dns", true), "ctl"))
	if !strings.Contains(string(on), branch) {
		t.Errorf("ctl lacks %q:\n%s", branch, on)
	}
	off, _ := os.ReadFile(filepath.Join(buildRestartBundle(t, "/srv/docker/dns", false), "ctl"))
	if strings.Contains(string(off), "compose restart") {
		t.Errorf("ctl restarts without restart_on_change")
	}
}

// The fake compose keeps the configuration its process read at start, so
// successful installs must update that state, including after a failed attempt.
func TestCtl_RestartsOnlyWhenAFileChanged(t *testing.T) {
	for _, tc := range []struct {
		name          string
		unchanged     bool
		removeCompose bool
		stopped       bool
		fail          string
	}{
		{name: "unchanged", unchanged: true},
		{name: "changed"},
		{name: "compose missing", removeCompose: true},
		{name: "stopped", stopped: true},
		{name: "retry failed restart", fail: "restart"},
		{name: "retry failed up", fail: "up"},
		{name: "retry failed state query", fail: "ps"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "dns")
			b := buildRestartBundle(t, dir, true)
			fake := filepath.Join(t.TempDir(), "compose")
			write := func(path, body string, mode os.FileMode) {
				t.Helper()
				if err := os.WriteFile(path, []byte(body), mode); err != nil {
					t.Fatal(err)
				}
			}
			read := func(path string) string {
				t.Helper()
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				return string(data)
			}
			write(fake, `#!/bin/sh
set -eu
dir=$2
shift 4
echo "$*" >> "$dir/calls"
[ ! -f "$dir/fail-$1" ] || exit 1
case $1 in
ps) if [ -f "$dir/running" ]; then echo container-id; fi ;;
up)
	if [ ! -f "$dir/running" ]; then
		cp "$dir/conf/dns.conf" "$dir/active.conf"
		touch "$dir/running"
	fi
	;;
restart) cp "$dir/conf/dns.conf" "$dir/active.conf" ;;
*) exit 2 ;;
esac
`, 0o755)
			install := func(wantFailure bool) string {
				t.Helper()
				os.Remove(filepath.Join(dir, "calls"))
				cmd := exec.Command("sh", filepath.Join(b, "ctl"), "install")
				cmd.Env = append(os.Environ(), "COMPOSE="+fake)
				out, err := cmd.CombinedOutput()
				if (err != nil) != wantFailure {
					t.Fatalf("install: %v (want failure %t)\n%s", err, wantFailure, out)
				}
				return read(filepath.Join(dir, "calls"))
			}
			if got := install(false); !strings.Contains(got, "up -d") || strings.Contains(got, "restart") {
				t.Fatalf("first install: %s", got)
			}
			want := "zone a\n"
			if !tc.unchanged {
				want = "zone b\n"
				write(filepath.Join(b, "files", "dns.conf"), want, 0o644)
			}
			for path, remove := range map[string]bool{"compose.yaml": tc.removeCompose, "running": tc.stopped} {
				if remove {
					if err := os.Remove(filepath.Join(dir, path)); err != nil {
						t.Fatal(err)
					}
				}
			}
			if tc.fail != "" {
				flag := filepath.Join(dir, "fail-"+tc.fail)
				write(flag, "", 0o600)
				install(true)
				if err := os.Remove(flag); err != nil {
					t.Fatal(err)
				}
			}
			got := install(false)
			wantRestart := !tc.unchanged && !tc.stopped
			if strings.Contains(got, "restart srv.example-dns") != wantRestart {
				t.Errorf("want restart %t, calls:\n%s", wantRestart, got)
			}
			if got := read(filepath.Join(dir, "active.conf")); got != want {
				t.Errorf("running configuration = %q, want %q", got, want)
			}
			if got := install(false); strings.Contains(got, "restart") {
				t.Errorf("unchanged install restarted after success: %s", got)
			}
		})
	}
}

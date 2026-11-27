package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/d0u9/rhumb/secretstore"
	"github.com/d0u9/rhumb/validate"
)

// dialAccountRoot is a broker and two clients dialling it by type on one
// node, on a shared container network or as host processes, and a person
// granted the broker's port by a route. auth is the broker's.
func dialAccountRoot(t *testing.T, auth string, containers bool) (Loaded, Renderer, string) {
	t.Helper()
	root := t.TempDir()
	write := func(path, body string) {
		t.Helper()
		p := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("services/broker/confgen.yaml", `
auth: `+auth+`
template: templates/broker.tmpl
defaults: document
output: accounts.txt
secret:
  kind: base64
  bytes: 32
`)
	write("services/broker/defaults.yaml", "{}\n")
	write("services/broker/templates/broker.tmpl",
		"{{ range principals \"mqtt\" }}{{ .Name }}={{ .Secret }}\n{{ end }}")
	write("services/mqtt-client/confgen.yaml", `
auth: none
template: templates/client.tmpl
defaults: document
output: client.txt
dials:
  mqtt: {service: broker, port: mqtt}
`)
	write("services/mqtt-client/defaults.yaml", "{}\n")
	write("services/mqtt-client/templates/client.tmpl",
		"{{ with dial \"mqtt\" }}{{ .Address }}:{{ .Number }} {{ .Account }} {{ .Secret }}{{ end }}\n")
	node := `
id: srv
host: host-a
instances:
  - id: broker-01
    service: broker
    ports:
      mqtt: 1883
  - id: app-01
    service: mqtt-client
  - id: hub-01
    service: mqtt-client
`
	if containers {
		node = strings.Replace(node, "instances:", "runtime: docker\ncontainers:\n  - {name: mqtt, subnet: \"10.0.0.0/24\", gateway: \"10.0.0.1\"}\ninstances:", 1)
		node = strings.ReplaceAll(node, "    service: ", "    containers: {mqtt: \"\"}\n    service: ")
	}
	write("nodes/srv.yaml", node)
	write("routes.yaml", "routes:\n  mqtt:\n    hops: [srv/broker-01:mqtt]\n")
	write("users.yaml", "users:\n  yak:\n    devices: none\n    access: [mqtt]\n")
	write("networks.yaml", "networks: [{name: internet}]\nuniversal: internet\n")
	write("hosts.yaml", "hosts: {host-a: {endpoints: {internet: {address: 203.0.113.10}}}}\n")

	secrets := t.TempDir()
	for group, v := range map[string]string{"srv-app-01": "app-secret", "srv-hub-01": "hub-secret", "yak": "yak-secret"} {
		p := filepath.Join(secrets, "srv", "broker-01", "mqtt", group, "default")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(v), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	l, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if issues := validate.Validate(l.Inv, l.Manifests, l.Exports, l.Derived, nil); len(issues) != 0 {
		t.Fatalf("issues = %v", issues)
	}
	return l, Renderer{Data: l, RootPath: root, SecretsDir: secrets}, secrets
}

func renderOne(t *testing.T, m Renderer, id string) string {
	t.Helper()
	out, err := m.RenderTarget(id)
	if err != nil {
		t.Fatalf("%s: %v", id, err)
	}
	if len(out) != 1 {
		t.Fatalf("%s: %d files", id, len(out))
	}
	return string(out[0].Bytes)
}

// A dial into a port that authenticates per principal is the caller's own
// account there, keyed by its flat id. Each client reads only its own; the
// broker lists every client's beside the person a route grants.
func TestDial_AccountOnAuthenticatingPort(t *testing.T) {
	l, m, secrets := dialAccountRoot(t, "per-principal", true)

	if got, want := renderOne(t, m, "srv/broker-01"), "srv-app-01=app-secret\nsrv-hub-01=hub-secret\nyak-default=yak-secret\n"; got != want {
		t.Fatalf("broker = %q, want %q", got, want)
	}
	if got, want := renderOne(t, m, "srv/app-01"), "broker-01:1883 srv-app-01 app-secret\n"; got != want {
		t.Fatalf("app = %q, want %q", got, want)
	}
	if got, want := renderOne(t, m, "srv/hub-01"), "broker-01:1883 srv-hub-01 hub-secret\n"; got != want {
		t.Fatalf("hub = %q, want %q", got, want)
	}

	// Secret sync and --check see each client's account as any grant's.
	implied := map[string]bool{}
	for _, p := range secretstore.ImpliedPaths(l.Inv, l.Manifests, l.Derived) {
		implied[p.Instance+"/"+p.Port+"/"+p.Group] = true
	}
	for _, group := range []string{"srv-app-01", "srv-hub-01", "yak"} {
		if !implied["srv/broker-01/mqtt/"+group] {
			t.Fatalf("implied paths lack %s: %v", group, implied)
		}
	}

	// A missing account fails the render naming the dial.
	if err := os.Remove(filepath.Join(secrets, "srv", "broker-01", "mqtt", "srv-hub-01", "default")); err != nil {
		t.Fatal(err)
	}
	if _, err := m.RenderTarget("srv/hub-01"); err == nil || !strings.Contains(err.Error(), `dial "mqtt"`) {
		t.Fatalf("hub without its account: err = %v", err)
	}
}

// Host processes on one node dial loopback, with the same accounts.
func TestDial_AccountOverLoopback(t *testing.T) {
	_, m, _ := dialAccountRoot(t, "per-principal", false)
	if got, want := renderOne(t, m, "srv/app-01"), "127.0.0.1:1883 srv-app-01 app-secret\n"; got != want {
		t.Fatalf("app = %q, want %q", got, want)
	}
}

// A dial into a port that authenticates nobody grants nothing, and a
// template reading its secret fails rather than rendering an empty one.
func TestDial_NoAccountOnUnauthenticatedPort(t *testing.T) {
	l, m, _ := dialAccountRoot(t, "none", true)
	for _, g := range l.Derived.Grants {
		if g.Principal.ID == "srv/app-01" || g.Principal.ID == "srv/hub-01" {
			t.Fatalf("grant from a dial into an unauthenticated port: %+v", g)
		}
	}
	if _, err := m.RenderTarget("srv/app-01"); err == nil || !strings.Contains(err.Error(), "does not authenticate per principal") {
		t.Fatalf("err = %v, want no account", err)
	}
}

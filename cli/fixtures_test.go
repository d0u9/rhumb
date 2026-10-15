package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/d0u9/rhumb/inventory"
	"gopkg.in/yaml.v3"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func buildInspectRoot(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "services", "ssserver", "confgen.yaml"), `
secret:
  kind: base64
  bytes: 32
template: templates/config.json.tmpl
defaults: element
output: config.json
auth: per-principal
self:
  psk: {set: true}
`)
	writeFile(t, filepath.Join(dir, "services", "ssserver", "exports", "ss-json", "confgen.yaml"), `
template: templates/config.json.tmpl
defaults: element
output: config.json
`)
	writeFile(t, filepath.Join(dir, "nodes", "srv.yaml"), `
id: srv
host: host-a
instances:
  - id: ss-srv
    service: ssserver
    ports:
      main: {port: 38250, self: [psk.main]}
      alt: {port: 49217, self: [psk.alt]}
`)
	writeFile(t, filepath.Join(dir, "nodes", "laptop.yaml"), `
id: laptop
host: host-b
owner: dana
`)
	writeFile(t, filepath.Join(dir, "nodes", "bad.yaml"), "id: [unterminated\n")
	writeFile(t, filepath.Join(dir, "users.yaml"), `
users:
  dana:
    access: [sea]
  yak:
    devices: none
    access: [sea]
`)
	writeFile(t, filepath.Join(dir, "routes.yaml"), `
routes:
  sea:
    hops: [srv/ss-srv:main]
`)
	writeFile(t, filepath.Join(dir, "networks.yaml"), `
networks: [{name: internet}]
universal: internet
`)
	writeTestHost(t, dir, "host-a", inventory.Host{Endpoints: map[string]inventory.Endpoint{"internet": {Address: "203.0.113.10"}}})
	writeTestHost(t, dir, "host-b", inventory.Host{})
	return dir
}

func buildRenderableRoot(t *testing.T) (root, secretsDir string) {
	t.Helper()
	root = t.TempDir()
	writeFile(t, filepath.Join(root, "services", "hysteria2", "confgen.yaml"), `
template: templates/server.yaml.tmpl
defaults: document
output: config.yaml
auth: per-principal
`)
	writeFile(t, filepath.Join(root, "services", "hysteria2", "templates", "server.yaml.tmpl"),
		"listen: {{ .listen }} on {{ (node).id }}\n")
	writeFile(t, filepath.Join(root, "services", "hysteria2", "defaults.yaml"), "listen: :443\n")
	writeFile(t, filepath.Join(root, "nodes", "srv.yaml"), `
id: srv
host: host-a
instances:
  - id: u-node-group-09
    service: hysteria2
    ports:
      main: 443
`)

	writeTestHost(t, root, "host-a", inventory.Host{Endpoints: map[string]inventory.Endpoint{"internet": {Address: "203.0.113.10"}}})
	secretsDir = t.TempDir()
	return root, secretsDir
}

func buildSharedRoot(t *testing.T) (root, secretsDir string) {
	t.Helper()
	root = t.TempDir()
	writeFile(t, filepath.Join(root, "services", "ssserver", "confgen.yaml"), `
template: templates/server.json.tmpl
defaults: element
output: config.json
auth: per-principal
self:
  psk: {set: true}
`)
	writeFile(t, filepath.Join(root, "services", "ssserver", "exports", "ss-json", "confgen.yaml"), `
template: templates/client.json.tmpl
defaults: element
output: config.json
upstream:
  shared: {}
`)
	writeFile(t, filepath.Join(root, "services", "ssserver", "templates", "server.json.tmpl"), "{}\n")
	writeFile(t, filepath.Join(root, "services", "ssserver", "defaults.yaml"), "{}\n")
	writeFile(t, filepath.Join(root, "services", "ssserver", "exports", "ss-json", "templates", "client.json.tmpl"),
		"password: {{ join \":\" (upstream).shared }}:{{ (upstream).secret }}\n")
	writeFile(t, filepath.Join(root, "services", "ssserver", "exports", "ss-json", "defaults.yaml"), "{}\n")
	writeFile(t, filepath.Join(root, "nodes", "srv.yaml"), `
id: srv
host: host-a
instances:
  - id: ss-srv
    service: ssserver
    ports:
      main: {port: 38250, self: [psk.main]}
`)
	writeFile(t, filepath.Join(root, "nodes", "laptop.yaml"), `
id: laptop
host: host-b
owner: dana
`)
	writeFile(t, filepath.Join(root, "users.yaml"), `
users:
  dana:
    access: [sea]
`)
	writeFile(t, filepath.Join(root, "routes.yaml"), `
routes:
  sea:
    hops: [srv/ss-srv:main]
`)
	writeFile(t, filepath.Join(root, "networks.yaml"), `
networks: [{name: internet}]
universal: internet
`)

	writeTestHost(t, root, "host-a", inventory.Host{Endpoints: map[string]inventory.Endpoint{"internet": {Address: "203.0.113.10"}}})
	writeTestHost(t, root, "host-b", inventory.Host{})
	secretsDir = t.TempDir()
	writeFile(t, filepath.Join(secretsDir, "srv/ss-srv", "main", "dana", "default"), "user-psk")
	return root, secretsDir
}

// writeTestHost updates explicit host declarations without adapting node YAML.
func writeTestHost(t *testing.T, root, id string, host inventory.Host) {
	t.Helper()
	path := filepath.Join(root, inventory.HostsFilename)
	doc := struct {
		Hosts map[string]inventory.Host `yaml:"hosts"`
	}{}
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err == nil {
		if err := yaml.Unmarshal(data, &doc); err != nil {
			t.Fatal(err)
		}
	}
	if doc.Hosts == nil {
		doc.Hosts = map[string]inventory.Host{}
	}
	doc.Hosts[id] = host
	data, err = yaml.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, string(data))
}

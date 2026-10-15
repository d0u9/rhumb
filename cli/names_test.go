package cli

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/d0u9/rhumb/engine"
)

func TestDNSNamesLoadCheckAndRender(t *testing.T) {
	root := t.TempDir()
	secrets := t.TempDir()
	writeFile(t, filepath.Join(root, "networks.yaml"), "networks: [{name: home}, {name: lab}]\nuniversal: home\n")
	writeFile(t, filepath.Join(root, "nodes", "node-1.yaml"), `id: node-1
host: host-a
instances:
  - id: resolver
    service: dns
    ports:
      main: {port: 53, published: service.example.test}
`)
	writeFile(t, filepath.Join(root, "hosts.yaml"), `hosts:
  host-a:
    names:
      device.example.test:
        - {interface: en, address: home-v4}
        - {interface: en2, address: lab-v4}
    interfaces:
      en:
        addresses:
          home-v4: {network: home, ip: 10.0.0.10, names: [wired.example.test]}
      en2:
        addresses:
          lab-v4: {network: lab, ip: 10.1.0.10, names: [lab.example.test]}
    endpoints:
      home: {from: {interface: en, address: home-v4}}
      lab: {from: {interface: en2, address: lab-v4}}
`)
	writeFile(t, filepath.Join(root, "names.yaml"), "names: {alias.example.test: device.example.test}\n")
	writeFile(t, filepath.Join(root, "services", "dns", "confgen.yaml"), "auth: none\ndefaults: document\ntemplate: templates/dns.tmpl\noutput: dns.txt\n")
	writeFile(t, filepath.Join(root, "services", "dns", "templates", "dns.tmpl"), `{{ range names "home" }}{{ .Name }} {{ .Type }} {{ .Value }}
{{ end }}{{ range flatnames "lab" }}{{ .Name }} {{ .Type }} {{ .Value }}
{{ end }}`)
	var check bytes.Buffer
	if err := Check(&check, Settings{Root: root, Secrets: secrets}); err != nil {
		t.Fatalf("check: %v: %s", err, &check)
	}
	loaded, err := engine.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	renderer := engine.Renderer{Data: loaded, RootPath: root}
	files, err := renderer.RenderTarget("node-1/resolver")
	if err != nil {
		t.Fatal(err)
	}
	want := "alias.example.test CNAME device.example.test\ndevice.example.test CNAME wired.example.test\nservice.example.test CNAME device.example.test\nwired.example.test A 10.0.0.10\nalias.example.test A 10.1.0.10\ndevice.example.test A 10.1.0.10\nlab.example.test A 10.1.0.10\nservice.example.test A 10.1.0.10\n"
	if len(files) != 1 || string(files[0].Bytes) != want {
		t.Fatalf("rendered %v, want %q", files, want)
	}
	before, err := MigrationInputDigest(root)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "names.yaml"), "names: {alias.example.test: absent.example.test}\n")
	after, err := MigrationInputDigest(root)
	if err != nil || before == after {
		t.Fatalf("DNS alias edit did not invalidate migration inputs: %v", err)
	}
	check.Reset()
	if err := Check(&check, Settings{Root: root, Secrets: secrets}); err == nil || !strings.Contains(check.String(), "absent from every network") {
		t.Fatalf("bad DNS accepted: %v: %s", err, &check)
	}
	writeFile(t, filepath.Join(root, "names.yaml"), "names: [")
	check.Reset()
	if err := Check(&check, Settings{Root: root, Secrets: secrets}); err == nil || !strings.Contains(check.String(), "names.yaml") {
		t.Fatalf("bad DNS YAML accepted: %v: %s", err, &check)
	}
}

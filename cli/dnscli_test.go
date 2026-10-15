package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestDNSJSONUsesAccessEndpoint(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "networks.yaml"), "networks: [{name: internet}, {name: home}]\nuniversal: internet\n")
	writeFile(t, filepath.Join(root, "hosts.yaml"), "hosts:\n  host-a:\n    names:\n      host-a.dev.example.test: [{endpoint: internet}]\n    endpoints:\n      internet: {address: origin.example.net}\n")
	writeFile(t, filepath.Join(root, "nodes", "node-1.yaml"), `id: node-1
host: host-a
instances:
  - id: web
    service: example
    ports:
      main: {port: 443, published: [web.example.test, files.example.test]}
`)
	writeFile(t, filepath.Join(root, "services", "example", "confgen.yaml"), "auth: none\n")
	var out bytes.Buffer
	if err := DNS(nil, &out, []string{"internet"}, nil, Settings{Root: root}); err != nil {
		t.Fatal(err)
	}
	var data struct {
		Network string           `json:"network"`
		Records []map[string]any `json:"records"`
	}
	if err := json.Unmarshal(out.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	if data.Network != "internet" || len(data.Records) != 3 {
		t.Fatal(data)
	}
	for _, r := range data.Records {
		expectedValue, expectedSource := "host-a.dev.example.test", "node-1/web:main"
		if r["name"] == "host-a.dev.example.test" {
			expectedValue = "origin.example.net"
			expectedSource = "hosts.yaml: hosts.host-a.names.host-a.dev.example.test"
		}
		if len(r) != 4 || r["type"] != "CNAME" || r["value"] != expectedValue || r["source"] != expectedSource {
			t.Fatal(r)
		}
	}
	before := out.String()
	out.Reset()
	if err := DNS(nil, &out, []string{"internet"}, nil, Settings{Root: root}); err != nil || out.String() != before {
		t.Fatal("nondeterministic JSON", err)
	}
	out.Reset()
	if err := DNS(nil, &out, []string{"home"}, nil, Settings{Root: root}); err != nil || !strings.Contains(out.String(), `"records": []`) {
		t.Fatal("empty network", err, &out)
	}
	out.Reset()
	if err := DNS(nil, &out, []string{"missing"}, nil, Settings{Root: root}); err == nil || out.Len() != 0 {
		t.Fatal("unknown network accepted")
	}
	writeFile(t, filepath.Join(root, "names.yaml"), "names: {web.example.test: files.example.test}\n")
	out.Reset()
	if err := DNS(nil, &out, []string{"internet"}, nil, Settings{Root: root}); err == nil || out.Len() != 0 {
		t.Fatal("conflict emitted JSON")
	}
}

func TestOrdinaryDNSExportRejectsConflictsWithoutOutput(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "networks.yaml"), "networks: [{name: internet}]\nuniversal: internet\n")
	writeFile(t, filepath.Join(root, "hosts.yaml"), "hosts:\n  host-a:\n    endpoints:\n      internet: {address: origin.example.net}\n")
	writeFile(t, filepath.Join(root, "nodes", "node-1.yaml"), "id: node-1\nhost: host-a\ninstances:\n  - id: resolver\n    service: dns\n    ports:\n      main: {port: 53, published: service.example.test}\n")
	writeFile(t, filepath.Join(root, "services", "dns", "confgen.yaml"), "auth: none\ndefaults: document\ntemplate: dns.tmpl\noutput: dns.txt\n")
	writeFile(t, filepath.Join(root, "services", "dns", "dns.tmpl"), "partial {{range names \"internet\"}}{{.Name}} {{.Type}} {{.Value}}{{end}}")
	writeFile(t, filepath.Join(root, "names.yaml"), "names: {service.example.test: missing.example.test}\n")
	var out bytes.Buffer
	err := Export(nil, &out, []string{"*"}, map[string]string{"to": "-", "yes": "true"}, Settings{Root: root})
	if err == nil || !strings.Contains(err.Error(), "duplicate") || out.Len() != 0 {
		t.Fatalf("invalid DNS exported: error=%v output=%q", err, out.String())
	}
}

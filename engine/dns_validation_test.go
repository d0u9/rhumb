package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func invalidDNSRenderer(t *testing.T, template string) Renderer {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"networks.yaml":                             "networks: [{name: home}]\nuniversal: home\n",
		"hosts.yaml":                                "hosts:\n  host-a:\n    endpoints:\n      home: {address: origin.example.net}\n",
		"nodes/node-a.yaml":                         "id: node-a\nhost: host-a\ninstances:\n  - id: resolver\n    service: dns\n    runtime: docker\n    ports:\n      main: {port: 53, published: service.example.test}\n",
		"services/dns/confgen.yaml":                 "auth: none\ndefaults: document\ntemplate: templates/dns.tmpl\noutput: dns.txt\n",
		"services/dns/templates/dns.tmpl":           template,
		"services/dns/deploy/confgen.yaml":          "defaults: document\nfiles:\n  - {template: templates/deploy.tmpl, output: deploy.txt}\n",
		"services/dns/deploy/templates/deploy.tmpl": template,
		"names.yaml":                                "names: {service.example.test: missing.example.test}\n",
	}
	for path, content := range files {
		path = filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	loaded, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	return Renderer{Data: loaded, RootPath: root}
}

func TestDNSInvalidGraphStopsInstanceAndDeployRendering(t *testing.T) {
	for _, function := range []string{"names", "flatnames"} {
		t.Run(function, func(t *testing.T) {
			renderer := invalidDNSRenderer(t, "prefix {{range "+function+" \"home\"}}{{.Name}} {{.Type}} {{.Value}}{{end}}")
			files, err := renderer.RenderTarget("node-a/resolver")
			if err == nil || !strings.Contains(err.Error(), "duplicate") || len(files) != 0 {
				t.Fatalf("instance files=%v error=%v", files, err)
			}
			deploy, err := renderer.deployFor("node-a/resolver")
			if err == nil || !strings.Contains(err.Error(), "duplicate") || len(deploy) != 0 {
				t.Fatalf("deploy files=%v error=%v", deploy, err)
			}
		})
	}
}

func TestDNSInvalidGraphDoesNotStopUnrelatedRendering(t *testing.T) {
	renderer := invalidDNSRenderer(t, "ordinary configuration")
	files, err := renderer.RenderTarget("node-a/resolver")
	if err != nil || len(files) != 1 || string(files[0].Bytes) != "ordinary configuration" {
		t.Fatalf("instance files=%v error=%v", files, err)
	}
	deploy, err := renderer.deployFor("node-a/resolver")
	if err != nil || len(deploy) != 1 || string(deploy[0].Bytes) != "ordinary configuration" {
		t.Fatalf("deploy files=%v error=%v", deploy, err)
	}
}

func TestDNSInvalidGraphStopsProcessRendering(t *testing.T) {
	renderer := invalidDNSRenderer(t, `{{range names "home"}}{{.Name}}{{end}}`)
	path := filepath.Join(renderer.RootPath, "nodes", "node-a.yaml")
	node, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := strings.Replace(string(node), "instances:", "processes:\n  dns-process: {service: dns}\ninstances:", 1)
	content = strings.Replace(content, "    runtime: docker", "    process: dns-process\n    runtime: docker", 1)
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	renderer.Data, err = Load(renderer.RootPath)
	if err != nil {
		t.Fatal(err)
	}
	files, err := renderer.RenderTarget("node-a/dns-process")
	if err == nil || !strings.Contains(err.Error(), "duplicate") || len(files) != 0 {
		t.Fatalf("process files=%v error=%v", files, err)
	}
}

func TestDNSBrokenAuthoredFileStopsNamesRendering(t *testing.T) {
	renderer := invalidDNSRenderer(t, `{{range names "home"}}{{.Name}}{{end}}`)
	if err := os.WriteFile(filepath.Join(renderer.RootPath, "names.yaml"), []byte("names: ["), 0600); err != nil {
		t.Fatal(err)
	}
	var err error
	renderer.Data, err = Load(renderer.RootPath)
	if err != nil {
		t.Fatal(err)
	}
	files, err := renderer.RenderTarget("node-a/resolver")
	if err == nil || len(files) != 0 {
		t.Fatalf("broken names file produced artifacts: %v %v", files, err)
	}
}

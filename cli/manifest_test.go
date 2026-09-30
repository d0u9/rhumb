package cli

import (
	"bytes"
	"fmt"
	"github.com/d0u9/rhumb/deploy"
	"github.com/d0u9/rhumb/engine"
	"os"
	"path"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestManifest_AgreesWithCompose is the proof that a manifest is enough:
// for every containerised instance, the ports, networks, addresses,
// container name and deploy volumes in the compose.yaml rendered beside it
// are all in its manifest, and the reverse. A mismatch means the manifest
// is missing a derived value a deployment needs.
func TestManifest_AgreesWithCompose(t *testing.T) {
	checkManifests(t, renderExamples(t), 2)
}

// TestManifest_AgreesWithComposeInARealRoot runs the same check over a
// generator root outside this repository, when RHUMB_ROOT and
// RHUMB_SECRETS name one.
func TestManifest_AgreesWithComposeInARealRoot(t *testing.T) {
	root, secrets := os.Getenv("RHUMB_ROOT"), os.Getenv("RHUMB_SECRETS")
	if root == "" || secrets == "" {
		t.Skip("RHUMB_ROOT and RHUMB_SECRETS not set")
	}
	dest := t.TempDir()
	var out bytes.Buffer
	if err := Export(strings.NewReader(""), &out, []string{"*"}, map[string]string{"to": dest, "yes": "true"}, Settings{Root: root, Secrets: secrets}); err != nil {
		t.Fatalf("export: %v\n%s", err, out.String())
	}
	files := map[string]string{}
	for _, p := range filesUnder(t, dest) {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		files[strings.TrimPrefix(p, dest+"/")] = string(data)
	}
	checkManifests(t, files, 1)
}

func checkManifests(t *testing.T, files map[string]string, wantAtLeast int) {
	t.Helper()
	checked := 0
	for p, body := range files {
		if path.Base(p) != engine.ManifestFile {
			continue
		}
		var man engine.DeployManifest
		if err := yaml.Unmarshal([]byte(body), &man); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		if man.Schema != engine.ManifestSchema {
			t.Errorf("%s: schema %d", p, man.Schema)
		}
		compose, ok := files[path.Join(path.Dir(p), "compose.yaml")]
		if man.Runtime == "host" {
			if ok {
				t.Errorf("%s: host instance renders a compose.yaml", p)
			}
			continue
		}
		if man.Container != nil {
			if ok {
				t.Errorf("%s: a docker.yaml service renders a compose.yaml of its own", p)
			}
			compose, ok = composeFromManifest(t, body), true
		}
		if !ok {
			continue
		}
		for _, problem := range compareCompose(man, compose) {
			t.Errorf("%s: %s", p, problem)
		}
		checked++
	}
	if checked < wantAtLeast {
		t.Fatalf("compared %d containerised instances, want at least %d", checked, wantAtLeast)
	}
	t.Logf("compared %d containerised instances", checked)
}

type composeFile struct {
	Services map[string]struct {
		ContainerName string      `yaml:"container_name"`
		Ports         []string    `yaml:"ports"`
		Volumes       []yaml.Node `yaml:"volumes"`
		Networks      yaml.Node   `yaml:"networks"`
	} `yaml:"services"`
}

func compareCompose(man engine.DeployManifest, body string) []string {
	var c composeFile
	if err := yaml.Unmarshal([]byte(body), &c); err != nil {
		return []string{err.Error()}
	}
	var problems []string
	var gotPorts, volumes, names []string
	named := false
	gotNets := map[string]string{}
	for name, s := range c.Services {
		names = append(names, name, s.ContainerName)
		named = named || s.ContainerName != ""
		for _, p := range s.Ports {
			gotPorts = append(gotPorts, strings.TrimSuffix(strings.TrimSuffix(p, "/tcp"), "/udp"))
		}
		for _, v := range s.Volumes {
			volumes = append(volumes, volumeString(v))
		}
		var nets map[string]struct {
			IPv4 string `yaml:"ipv4_address"`
		}
		if s.Networks.Kind == yaml.MappingNode && s.Networks.Decode(&nets) == nil {
			for n, v := range nets {
				gotNets[n] = v.IPv4
			}
		}
	}

	var wantPorts []string
	for _, p := range man.Ports {
		host := p.Port
		if p.HostPort != 0 {
			host = p.HostPort
		}
		for _, addr := range p.Bind {
			wantPorts = append(wantPorts, fmt.Sprintf("%s:%d:%d", addr, host, p.Port))
		}
	}
	slices.Sort(gotPorts)
	gotPorts = slices.Compact(gotPorts)
	slices.Sort(wantPorts)
	if !slices.Equal(gotPorts, wantPorts) {
		problems = append(problems, fmt.Sprintf("ports: compose %v, manifest %v", gotPorts, wantPorts))
	}

	for _, n := range man.Networks {
		addr, ok := gotNets[n.Name]
		if !ok {
			problems = append(problems, "compose joins no network "+n.Name)
		} else if addr != n.Address {
			problems = append(problems, fmt.Sprintf("network %s: compose address %q, manifest %q", n.Name, addr, n.Address))
		}
	}
	if len(gotNets) > len(man.Networks) {
		problems = append(problems, fmt.Sprintf("compose joins %d networks, manifest names %d", len(gotNets), len(man.Networks)))
	}

	// A service of one-shot containers (compose run --rm) names none, and
	// its deploy values' container_name then has nothing to agree with.
	if name, ok := man.Deploy["container_name"].(string); ok && named && !slices.Contains(names, name) {
		problems = append(problems, "no container named "+name)
	}
	if vs, ok := man.Deploy["volumes"].([]any); ok {
		for _, v := range vs {
			var n yaml.Node
			_ = n.Encode(v)
			if !slices.Contains(volumes, volumeString(n)) {
				problems = append(problems, fmt.Sprintf("deploy volume %v is not mounted", v))
			}
		}
	}
	return problems
}

// composeFromManifest is what rhumb deploy writes for a manifest's
// container.
func composeFromManifest(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(path.Join(dir, deploy.ManifestFile), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := deploy.ReadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	out, err := deploy.Compose(m)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// volumeString is a compose volume as its short form, source:target, so a
// long-form entry compares against the manifest the same way.
func volumeString(n yaml.Node) string {
	if n.Kind == yaml.ScalarNode {
		return n.Value
	}
	var long struct{ Source, Target string }
	_ = n.Decode(&long)
	return long.Source + ":" + long.Target
}

// TestManifest_ExampleContent pins what one containerised and one host
// instance write, so a change to the manifest's shape is a visible change.
func TestManifest_ExampleContent(t *testing.T) {
	files := renderExamples(t)
	got := exampleFile(t, files, "microbin-sea01/"+engine.ManifestFile)
	for _, want := range []string{
		"schema: 1\n",
		"service: microbin\n",
		"runtime: docker\n",
		"  - path: server.env\n",
		"    place: server.env\n",
		"      - 127.0.0.1\n",
		"    transport: tcp\n",
		"  image: danielszabo99/microbin:2.0.4\n",
		"    data: /var/lib/microbin/data_dir\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("microbin manifest has no %q:\n%s", want, got)
		}
	}
	host := exampleFile(t, files, "hy2-sea01/"+engine.ManifestFile)
	for _, want := range []string{"runtime: host\n", "  - path: config.yaml\n"} {
		if !strings.Contains(host, want) {
			t.Errorf("hysteria2 manifest has no %q:\n%s", want, host)
		}
	}
	for _, absent := range []string{"deploy:", "accounts:"} {
		if strings.Contains(host, absent) {
			t.Errorf("host manifest carries %q:\n%s", absent, host)
		}
	}
}

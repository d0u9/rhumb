package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/d0u9/rhumb/inventory"
)

func hostApplyRoot(t *testing.T, hosts, bound string) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "networks.yaml"), "networks: [{name: home}]\n")
	writeFile(t, filepath.Join(root, "nodes", "node-a.yaml"), "id: node-a\nhost: "+bound+"\n")
	writeFile(t, filepath.Join(root, "hosts.yaml"), hosts)
	return root
}

func TestHostEndpointEditAndNetworkRenameApplyAndRetry(t *testing.T) {
	root := hostApplyRoot(t, `hosts:
  host-a:
    names:
      host-a.example.test: [{endpoint: home}]
    endpoints:
      home: {address: origin.example.test}
`, "host-a")
	flags := map[string]string{"node": "from=node-a,to=node-a", "network": `["from=home,to=lan"]`, "host-edit": `[{"kind":"endpoint","network":"home","from":"origin.example.test","to":"new-origin.example.test"}]`}
	applyHostMigration(t, root, "", flags)
	inv, err := inventory.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	h := inv.Hosts["host-a"]
	if h.Endpoints["lan"].Address != "new-origin.example.test" || h.Names["host-a.example.test"][0].Endpoint != "lan" {
		t.Fatal(h)
	}
	if _, ok := h.Endpoints["home"]; ok {
		t.Fatal("old endpoint retained")
	}
	before := map[string][]byte{}
	for _, p := range []string{"hosts.yaml", "networks.yaml", "nodes/node-a.yaml"} {
		data, err := os.ReadFile(filepath.Join(root, p))
		if err != nil {
			t.Fatal(err)
		}
		before[p] = data
	}
	report := applyHostMigration(t, root, "", flags)
	if !strings.Contains(report, "already present") {
		t.Fatal(report)
	}
	for p, data := range before {
		after, err := os.ReadFile(filepath.Join(root, p))
		if err != nil || !bytes.Equal(data, after) {
			t.Fatalf("retry modified %s: %v", p, err)
		}
	}
}

func TestHostScopedEditsDoNotChangeAliasPeers(t *testing.T) {
	for _, tc := range []struct{ name, hosts, bound string }{
		{"anchor owner", `hosts:
  host-a:
    endpoints: &targets # owner comment
      home: {address: origin.example.test}
  host-b:
    endpoints: *targets # peer comment
`, "host-a"},
		{"alias consumer", `hosts:
  host-a:
    endpoints: &targets # owner comment
      home: {address: origin.example.test}
  host-b:
    endpoints: *targets # peer comment
`, "host-b"},
		{"whole host alias", `hosts:
  host-a: &base # owner comment
    endpoints:
      home: {address: origin.example.test}
  host-b: *base # peer comment
`, "host-b"},
		{"root merge consumer", `<<:
  hosts:
    host-a: &base # owner comment
      endpoints:
        home: {address: origin.example.test}
    host-b:
      <<: *base # peer comment
`, "host-b"},
		{"merge consumer", `hosts:
  host-a: &base # owner comment
    endpoints:
      home: {address: origin.example.test}
  host-b:
    <<: *base # peer comment
`, "host-b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := hostApplyRoot(t, tc.hosts, tc.bound)
			before, err := inventory.Load(root)
			if err != nil || before.HostsBroken != "" {
				t.Fatal(err, before.HostsBroken)
			}
			flags := map[string]string{"node": "from=node-a,to=node-a", "host-edit": `[{"kind":"endpoint","network":"home","from":"origin.example.test","to":"new-origin.example.test"}]`}
			applyHostMigration(t, root, "", flags)
			after, err := inventory.Load(root)
			if err != nil || after.HostsBroken != "" {
				t.Fatal(err, after.HostsBroken)
			}
			for id, h := range before.Hosts {
				if id == tc.bound {
					if after.Hosts[id].Endpoints["home"].Address != "new-origin.example.test" {
						t.Fatal(after.Hosts[id])
					}
				} else if !reflect.DeepEqual(h, after.Hosts[id]) {
					t.Fatalf("peer %s changed", id)
				}
			}
			data, err := os.ReadFile(filepath.Join(root, "hosts.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(data, []byte("owner comment")) || !bytes.Contains(data, []byte("peer comment")) {
				t.Fatalf("comments lost:\n%s", data)
			}
		})
	}
}

func TestNetworkRenameRemovesInheritedEndpointKey(t *testing.T) {
	root := hostApplyRoot(t, `hosts:
  host-a:
    endpoints: &targets
      home: {address: origin.example.test}
  host-b:
    endpoints:
      <<: *targets
`, "host-b")
	applyHostMigration(t, root, "", map[string]string{"node": "from=node-a,to=node-a", "network": `["from=home,to=lan"]`})
	inv, err := inventory.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	for id, h := range inv.Hosts {
		if _, ok := h.Endpoints["home"]; ok {
			t.Fatalf("%s inherited deleted network", id)
		}
		if h.Endpoints["lan"].Address != "origin.example.test" {
			t.Fatal(h)
		}
	}
}

func TestMissingEndpointEditIsRejectedWithoutWriting(t *testing.T) {
	root := hostApplyRoot(t, "hosts:\n  host-a:\n    endpoints:\n      home: {address: origin.example.test}\n", "host-a")
	path := filepath.Join(root, "hosts.yaml")
	before, _ := os.ReadFile(path)
	var out bytes.Buffer
	err := Migrate(nil, &out, []string{"node"}, map[string]string{"node": "from=node-a,to=node-a", "host-edit": `[{"kind":"endpoint","network":"missing","from":"origin.example.test","to":"new-origin.example.test"}]`, "apply": "true", "yes": "true"}, Settings{Root: root})
	after, _ := os.ReadFile(path)
	if err == nil || !bytes.Equal(before, after) {
		t.Fatalf("missing authored source: error=%v changed=%t", err, !bytes.Equal(before, after))
	}
}

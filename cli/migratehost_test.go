package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/d0u9/rhumb/inventory"
)

func TestHostMigrationExplicitEmptyMAC(t *testing.T) {
	root, secrets := buildExportableRoot(t)
	writeFile(t, filepath.Join(root, "hosts.yaml"), `hosts:
  host-a:
    interfaces:
      lan:
        mac: "" # keep this note
        addresses:
          primary: {network: internet, ip: 203.0.113.10}
    endpoints:
      internet: {from: {interface: lan, address: primary}}
`)
	changes, _ := json.Marshal([]HostChange{{Kind: "mac", Interface: "lan", From: "(none)", To: "02:00:00:00:00:31"}})
	applyHostMigration(t, root, secrets, map[string]string{"node": "from=srv,to=srv", "host-edit": string(changes)})
	data, err := os.ReadFile(filepath.Join(root, "hosts.yaml"))
	if err != nil || bytes.Count(data, []byte("mac:")) != 1 || !bytes.Contains(data, []byte("# keep this note")) {
		t.Fatalf("MAC edit lost source structure: %s (%v)", data, err)
	}
}

func applyHostMigration(t *testing.T, root, secrets string, flags map[string]string) string {
	t.Helper()
	flags["apply"], flags["yes"] = "true", "true"
	var out bytes.Buffer
	if err := Migrate(nil, &out, []string{"node"}, flags, Settings{Root: root, Secrets: secrets}); err != nil {
		t.Fatalf("migration: %v\n%s", err, out.String())
	}
	return out.String()
}

func TestHostMigrationInputDigests(t *testing.T) {
	for _, operation := range []string{"create", "change", "delete"} {
		t.Run(operation, func(t *testing.T) {
			root, _ := buildExportableRoot(t)
			path := filepath.Join(root, "hosts.yaml")
			if operation != "create" {
				writeFile(t, path, "hosts: {}\n")
			}
			digests, err := migrationInputDigests(root)
			if err != nil {
				t.Fatal(err)
			}
			if operation == "delete" {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			} else {
				writeFile(t, path, "# changed\nhosts: {}\n")
			}
			if err := checkMigrationInputDigests(root, digests); err == nil {
				t.Fatal("host mutation not detected")
			}
		})
	}
}

func TestHostTypedEditsAndNetworkRename(t *testing.T) {
	root, secrets := buildExportableRoot(t)
	path := filepath.Join(root, "hosts.yaml")
	writeFile(t, path, `hosts:
  host-a:
    interfaces:
      wired:
        mac: "02:00:00:00:00:11"
        addresses:
          primary: {network: internet, ip: 203.0.113.10, dhcp_reservation: true, names: [host-a.example.test]}
          secondary: {network: internet, ip: 203.0.113.12}
    endpoints:
      internet:
        from: {interface: wired, address: primary}
      home: {address: host-a.home.test}
    reaches: [home]
`)
	writeFile(t, filepath.Join(root, "networks.yaml"), "networks: [{name: internet}, {name: home}]\nuniversal: internet\n")
	changes := []HostChange{{Kind: "ip", Interface: "wired", Address: "primary", From: "203.0.113.10", To: "203.0.113.20"}, {Kind: "mac", Interface: "wired", From: "02:00:00:00:00:11", To: "02:00:00:00:00:21"}, {Kind: "target", Network: "internet", From: "wired/primary", To: "wired/secondary"}, {Kind: "endpoint", Network: "home", From: "host-a.home.test", To: "host-b.home.test"}}
	raw, _ := json.Marshal(changes)
	flags := map[string]string{"node": "from=srv,to=srv", "host-edit": string(raw)}
	applyHostMigration(t, root, secrets, flags)
	inv, err := inventory.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	h := inv.Hosts["host-a"]
	i := h.Interfaces["wired"]
	if i.Addresses["primary"].IP != "203.0.113.20" || i.MAC != "02:00:00:00:00:21" || inv.Nodes[0].Networks["internet"] != "203.0.113.12" || h.Endpoints["home"].Address != "host-b.home.test" {
		t.Fatalf("wrong edited host: %+v", h)
	}
	if out := applyHostMigration(t, root, secrets, flags); !strings.Contains(out, "already present") {
		t.Fatal("typed edit retry not recognized")
	}
	applyHostMigration(t, root, secrets, map[string]string{"node": "from=srv,to=srv-next", "network": `["from=internet,to=wan"]`})
	inv, err = inventory.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	h = inv.Hosts["host-a"]
	if inv.Nodes[0].Host != "host-a" || inv.Nodes[0].ID != "srv-next" || h.Interfaces["wired"].Addresses["primary"].Network != "wan" || h.Endpoints["wan"].From == nil {
		t.Fatalf("rename changed host identity or missed refs: %+v", inv.Nodes[0])
	}
	if _, ok := h.Endpoints["internet"]; ok {
		t.Fatal("old endpoint scope remains")
	}
	applyHostMigration(t, root, secrets, map[string]string{"node": "from=srv-next,to=srv-next", "network": `["from=home,to=lan"]`})
	inv, err = inventory.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	h = inv.Hosts["host-a"]
	if !reflect.DeepEqual(h.Reaches, []string{"lan"}) || h.Endpoints["lan"].Address != "host-b.home.test" {
		t.Fatalf("network rename missed reachability/literal scope: %+v", h)
	}
}

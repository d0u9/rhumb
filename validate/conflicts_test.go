package validate

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/d0u9/rhumb/confgen"
	"github.com/d0u9/rhumb/derive"
	"github.com/d0u9/rhumb/inventory"
)

func TestValidateBindConflicts(t *testing.T) {
	for _, tc := range []struct {
		name, a, b, protocolA, protocolB string
		conflict                         bool
	}{
		{"same IPv6", "2001:db8::1", "2001:0db8:0:0:0:0:0:1", "", "", true},
		{"mapped IPv4", "192.0.2.1", "::ffff:192.0.2.1", "", "", true},
		{"IPv4 wildcard", "0.0.0.0", "192.0.2.1", "", "", true},
		{"IPv4 wildcard reversed", "192.0.2.1", "0.0.0.0", "", "", true},
		{"IPv4 wildcard with mapped address", "0.0.0.0", "::ffff:192.0.2.1", "", "", true},
		{"same IPv4 wildcard", "0.0.0.0", "0.0.0.0", "", "", true},
		{"IPv6 wildcard", "::", "2001:db8::1", "", "", true},
		{"IPv6 wildcard reversed", "2001:db8::1", "::", "", "", true},
		{"same IPv6 wildcard", "::", "0:0:0:0:0:0:0:0", "", "", true},
		{"UDP wildcard", "0.0.0.0", "192.0.2.1", "udp", "udp", true},
		{"UDP IPv6", "2001:db8::1", "2001:0db8:0:0:0:0:0:1", "udp", "udp", true},
		{"different addresses", "192.0.2.1", "192.0.2.2", "", "", false},
		{"different protocols", "0.0.0.0", "192.0.2.1", "", "udp", false},
		{"different families", "0.0.0.0", "2001:db8::1", "", "", false},
		{"unknown default", "", "192.0.2.1", "", "", false},
		{"unknown default with wildcard", "", "0.0.0.0", "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inv := &inventory.Root{Nodes: []inventory.Node{{ID: "node-1", Instances: []inventory.Instance{
				{ID: "node-1/app-a", Service: "example", Bind: tc.a, Ports: inventory.Ports{"main": {Number: 443, Protocol: tc.protocolA}}},
				{ID: "node-1/app-b", Service: "example", Bind: tc.b, Ports: inventory.Ports{"main": {Number: 443, Protocol: tc.protocolB}}},
			}}}}
			issues := Validate(inv, map[string]confgen.Manifest{"example": {}}, nil, &derive.Model{}, nil)
			want := 0
			if tc.conflict {
				want = 1
			}
			if len(issues) != want {
				t.Fatalf("issues = %v, want %d", messages(issues), want)
			}
			if tc.conflict && (!containsSubstring(issues, "app-a:main") || !containsSubstring(issues, "app-b:main") ||
				!containsSubstring(issues, "443/"+inv.Nodes[0].Instances[0].Ports["main"].ProtocolOr())) {
				t.Fatalf("conflict omits an owner or transport: %v", messages(issues))
			}
		})
	}
}

func TestValidateListenerScopes(t *testing.T) {
	for _, tc := range []struct {
		name, node string
		port       int
	}{
		{"different nodes", "node-2", 443},
		{"different ports", "node-1", 8443},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inv := &inventory.Root{Nodes: []inventory.Node{{ID: "node-1", Instances: []inventory.Instance{
				{ID: "node-1/app-a", Service: "example", Bind: "0.0.0.0", Ports: inventory.Ports{"main": {Number: 443}}},
			}}}}
			other := inventory.Instance{ID: tc.node + "/app-b", Service: "example", Bind: "192.0.2.1", Ports: inventory.Ports{"main": {Number: tc.port}}}
			if tc.node == "node-1" {
				inv.Nodes[0].Instances = append(inv.Nodes[0].Instances, other)
			} else {
				inv.Nodes = append(inv.Nodes, inventory.Node{ID: tc.node, Instances: []inventory.Instance{other}})
			}
			issues := Validate(inv, map[string]confgen.Manifest{"example": {}}, nil, &derive.Model{}, nil)
			if len(issues) != 0 {
				t.Fatalf("independent listeners rejected: %v", messages(issues))
			}
		})
	}
}

func TestValidateProcessContainerAddresses(t *testing.T) {
	for _, tc := range []struct {
		name, secondAddress string
		independent         bool
		want                string
	}{
		{"single member", "", false, ""},
		{"shared address", "2001:db8::5", false, ""},
		{"equivalent addresses", "2001:0db8:0:0:0:0:0:5", false, ""},
		{"different addresses", "2001:db8::6", false, "differ in container addresses"},
		{"fixed and dynamic", "dynamic", false, "differ in container addresses"},
		{"different containers", "2001:db8::5", true, "both hold"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.Mkdir(filepath.Join(root, "nodes"), 0700); err != nil {
				t.Fatal(err)
			}
			body := `id: node-1
runtime: docker
containers:
  - {name: apps, subnet: "2001:db8::/64", gateway: "2001:db8::1"}
processes:
  program-a: {service: example}
instances:
  - id: app-a
    service: example
    process: program-a
    containers: {apps: "2001:db8::5"}
    ports: {main: 8080}
`
			if tc.secondAddress != "" {
				body += "  - id: app-b\n    service: example\n    ports: {other: 8443}\n"
				if !tc.independent {
					body += "    process: program-a\n"
				}
				address := tc.secondAddress
				if address == "dynamic" {
					address = ""
				}
				body += "    containers: {apps: \"" + address + "\"}\n"
			}
			if err := os.WriteFile(filepath.Join(root, "nodes", "node-1.yaml"), []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			inv, err := inventory.Load(root)
			if err != nil {
				t.Fatal(err)
			}
			manifests := map[string]confgen.Manifest{"example": {Auth: confgen.AuthNone, Template: "example.tmpl"}}
			issues := Validate(inv, manifests, nil, derived(t, inv, manifests), nil)
			if tc.want == "" {
				if len(issues) != 0 {
					t.Fatalf("valid process rejected: %v", messages(issues))
				}
			} else if !containsSubstring(issues, tc.want) {
				t.Fatalf("issues = %v, want %q", messages(issues), tc.want)
			}
		})
	}
}

func TestValidateContainerGatewayEquivalentIPv6(t *testing.T) {
	inv := &inventory.Root{Nodes: []inventory.Node{{ID: "node-1",
		Containers: []inventory.ContainerNetwork{{Name: "apps", Subnet: "2001:db8::/64", Gateway: "2001:db8::1"}},
		Instances: []inventory.Instance{{ID: "node-1/app-a", Service: "example", Runtime: inventory.RuntimeDocker,
			Containers: map[string]string{"apps": "2001:0db8:0:0:0:0:0:1"}}},
	}}}
	issues := Validate(inv, map[string]confgen.Manifest{"example": {}}, nil, &derive.Model{}, nil)
	if !containsSubstring(issues, "is the network's gateway") {
		t.Fatalf("issues = %v", messages(issues))
	}
}

func TestValidateCanonicalContainerHostMappings(t *testing.T) {
	inv := &inventory.Root{Networks: []string{"lan-a", "lan-b"}, Nodes: []inventory.Node{{ID: "node-1",
		Networks:   inventory.Networks{"lan-a": "2001:db8::1", "lan-b": "2001:0db8:0:0:0:0:0:1"},
		Containers: []inventory.ContainerNetwork{{Name: "apps", Subnet: "10.0.0.0/24"}},
		Instances: []inventory.Instance{{ID: "node-1/app-a", Service: "example", Runtime: inventory.RuntimeDocker,
			Containers: map[string]string{"apps": ""}, Ports: inventory.Ports{"main": {Number: 443}}}},
	}}}
	manifests := map[string]confgen.Manifest{"example": {Template: "example.tmpl", Auth: confgen.AuthNone}}
	model := derived(t, inv, manifests)
	if issues := Validate(inv, manifests, nil, model, nil); len(issues) != 0 {
		t.Fatalf("one listener reached over two scopes rejected: %v", messages(issues))
	}
	if addresses := model.Mappings(inv, "node-1/app-a")["main"].Addresses; len(addresses) != 1 || addresses[0] != "2001:db8::1" {
		t.Fatalf("host mappings = %v, want one normalized IP", addresses)
	}
}

func TestValidateWildcardContainerHostMapping(t *testing.T) {
	inv := &inventory.Root{Networks: []string{"lan"}, Nodes: []inventory.Node{{ID: "node-1",
		Networks:   inventory.Networks{"lan": "node.example.test"},
		Containers: []inventory.ContainerNetwork{{Name: "apps", Subnet: "10.0.0.0/24"}},
		Instances: []inventory.Instance{
			{ID: "node-1/app-a", Service: "example", Runtime: inventory.RuntimeDocker, Containers: map[string]string{"apps": ""}, Ports: inventory.Ports{"main": {Number: 443}}},
			{ID: "node-1/app-b", Service: "example", Bind: "192.0.2.1", Ports: inventory.Ports{"main": {Number: 443}}},
		},
	}}}
	manifests := map[string]confgen.Manifest{"example": {Template: "example.tmpl", Auth: confgen.AuthNone}}
	issues := Validate(inv, manifests, nil, derived(t, inv, manifests), nil)
	if !containsSubstring(issues, "bind overlapping addresses on port 443/tcp") {
		t.Fatalf("issues = %v, want wildcard host mapping conflict", messages(issues))
	}
}

func TestValidateContainerScopedPortDoesNotOccupyHost(t *testing.T) {
	inv := &inventory.Root{Nodes: []inventory.Node{{ID: "node-1",
		Containers: []inventory.ContainerNetwork{{Name: "apps", Subnet: "10.0.0.0/24"}},
		Instances: []inventory.Instance{
			{ID: "node-1/app-a", Service: "example", Runtime: inventory.RuntimeDocker, Bind: "0.0.0.0", Containers: map[string]string{"apps": ""}, Ports: inventory.Ports{"main": {Number: 443}}},
			{ID: "node-1/app-b", Service: "example", Bind: "0.0.0.0", Ports: inventory.Ports{"main": {Number: 443}}},
		},
	}}, Routes: map[string]inventory.Route{"apps/main": {Scope: "apps", Hops: []string{"node-1/app-a:main"}}}}
	manifests := map[string]confgen.Manifest{"example": {Template: "example.tmpl", Auth: confgen.AuthNone}}
	model := derived(t, inv, manifests)
	if issues := Validate(inv, manifests, nil, model, nil); len(issues) != 0 {
		t.Fatalf("unpublished container port occupies host: %v", messages(issues))
	}
	if mapping := model.Mappings(inv, "node-1/app-a")["main"]; len(mapping.Addresses) != 0 {
		t.Fatalf("container-scoped port has host mapping: %+v", mapping)
	}
	inv.Routes = nil
	issues := Validate(inv, manifests, nil, derived(t, inv, manifests), nil)
	if len(issues) != 1 || !containsSubstring(issues, "bind the same address") {
		t.Fatalf("published port should conflict: %v", messages(issues))
	}
}

func TestValidateContainerBridgeInterface(t *testing.T) {
	for _, tc := range []struct {
		name, subnet, gateway string
	}{
		{"IPv4", "10.0.0.0/24", "10.0.0.1"},
		{"IPv6", "2001:db8::/64", "2001:db8::1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inv := &inventory.Root{Networks: []string{"bridge-lan"}, NetworkInfo: map[string]inventory.Network{"bridge-lan": {Subnet: tc.subnet, Gateway: tc.gateway}},
				Hosts: map[string]inventory.Host{"host-a": {
					Interfaces: map[string]inventory.Interface{"bridge": {Addresses: map[string]inventory.InterfaceAddress{
						"gateway": {Network: "bridge-lan", IP: tc.gateway},
					}}},
				}},
				Nodes: []inventory.Node{{ID: "node-1", Host: "host-a", Containers: []inventory.ContainerNetwork{{Name: "apps", Subnet: tc.subnet, Gateway: tc.gateway}}}},
			}
			inv.ResolveHosts()
			issues := Validate(inv, nil, nil, &derive.Model{}, nil)
			if len(issues) != 0 {
				t.Fatalf("host bridge gateway rejected: %v", messages(issues))
			}
		})
	}
}

func TestValidateContainerAddressConflictOncePerOwner(t *testing.T) {
	for _, independent := range []string{"app-0", "app-z"} {
		t.Run(independent, func(t *testing.T) {
			instance := func(name, process, address string) inventory.Instance {
				return inventory.Instance{ID: "node-1/" + name, Name: name, Service: "example", Process: process,
					Runtime: inventory.RuntimeDocker, Containers: map[string]string{"apps": address}}
			}
			inv := &inventory.Root{Nodes: []inventory.Node{{ID: "node-1",
				Processes:  map[string]inventory.Process{"program-a": {Service: "example"}},
				Containers: []inventory.ContainerNetwork{{Name: "apps", Subnet: "2001:db8::/64"}},
				Instances: []inventory.Instance{
					instance(independent, "", "2001:db8::5"),
					instance("app-a", "program-a", "2001:db8::5"),
					instance("app-b", "program-a", "2001:0db8:0:0:0:0:0:5"),
				},
			}}}
			manifests := map[string]confgen.Manifest{"example": {Template: "example.tmpl", Auth: confgen.AuthNone}}
			issues := Validate(inv, manifests, nil, &derive.Model{}, nil)
			if len(issues) != 1 || !containsSubstring(issues, "both hold") || !containsSubstring(issues, "program-a") || !containsSubstring(issues, independent) {
				t.Fatalf("issues = %v, want one conflict between the independent instance and process", messages(issues))
			}
		})
	}
}

func TestValidateProcessAddressOwnershipBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name  string
		edit  func(*inventory.Root)
		count int
		want  []string
	}{
		{"dynamic members", func(inv *inventory.Root) {
			for i := range inv.Nodes[0].Instances {
				inv.Nodes[0].Instances[i].Containers["apps"] = ""
			}
		}, 0, nil},
		{"undeclared process cannot merge claims", func(inv *inventory.Root) {
			inv.Nodes[0].Processes = nil
		}, 3, []string{"both hold", `process "program-a" is not one of`}},
		{"second network address mismatch", func(inv *inventory.Root) {
			n := &inv.Nodes[0]
			n.Containers = append(n.Containers, inventory.ContainerNetwork{Name: "jobs", Subnet: "10.0.1.0/24"})
			n.Instances[0].Containers["jobs"] = "10.0.1.5"
			n.Instances[1].Containers["jobs"] = "10.0.1.6"
		}, 1, []string{"differ in container addresses"}},
		{"distinct processes still conflict", func(inv *inventory.Root) {
			inv.Nodes[0].Processes["program-b"] = inventory.Process{Service: "example"}
			inv.Nodes[0].Instances[1].Process = "program-b"
		}, 1, []string{"both hold", "node-1/program-a", "node-1/program-b"}},
		{"same local process name on another node", func(inv *inventory.Root) {
			other := inv.Nodes[0]
			other.ID = "node-2"
			other.Instances = append([]inventory.Instance(nil), other.Instances[1:]...)
			other.Instances[0].ID = "node-2/app-b"
			inv.Nodes[0].Instances = inv.Nodes[0].Instances[:1]
			inv.Nodes = append(inv.Nodes, other)
		}, 0, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inv := &inventory.Root{Nodes: []inventory.Node{{ID: "node-1",
				Processes:  map[string]inventory.Process{"program-a": {Service: "example"}},
				Containers: []inventory.ContainerNetwork{{Name: "apps", Subnet: "10.0.0.0/24"}},
			}}}
			for _, name := range []string{"app-a", "app-b"} {
				inv.Nodes[0].Instances = append(inv.Nodes[0].Instances, inventory.Instance{ID: "node-1/" + name,
					Name: name, Service: "example", Process: "program-a", Runtime: inventory.RuntimeDocker,
					Containers: map[string]string{"apps": "10.0.0.5"}})
			}
			tc.edit(inv)
			issues := Validate(inv, map[string]confgen.Manifest{"example": {Template: "example.tmpl", Auth: confgen.AuthNone}}, nil, &derive.Model{}, nil)
			if len(issues) != tc.count {
				t.Fatalf("issues = %v, want %d", messages(issues), tc.count)
			}
			for _, want := range tc.want {
				if !containsSubstring(issues, want) {
					t.Fatalf("issues = %v, want %q", messages(issues), want)
				}
			}
		})
	}
}

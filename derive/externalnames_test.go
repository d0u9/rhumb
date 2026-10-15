package derive

import (
	"reflect"
	"strings"
	"testing"

	"github.com/d0u9/rhumb/inventory"
)

func externalInventory(address string) *inventory.Root {
	inv := &inventory.Root{Networks: []string{"internet", "home"}, Hosts: map[string]inventory.Host{"host-a": {Endpoints: map[string]inventory.Endpoint{"internet": {Address: address}}}}, Nodes: []inventory.Node{{ID: "node-1", Host: "host-a", Instances: []inventory.Instance{
		{ID: "node-1/web", Service: "web", Ports: inventory.Ports{"web": {Names: []string{"web.example.test"}}}},
		{ID: "node-1/files", Service: "files", Ports: inventory.Ports{"main": {Names: []string{"files.example.test"}}}},
	}}}}
	inv.ResolveHosts()
	return inv
}
func TestPublishedNamesFollowMachineAccessAddress(t *testing.T) {
	for _, tc := range []struct{ address, kind string }{{"origin.example.net", "CNAME"}, {"203.0.113.10", "A"}, {"2001:db8::10", "AAAA"}} {
		t.Run(tc.kind, func(t *testing.T) {
			inv := externalInventory(tc.address)
			inv.Names = map[string]string{"alias.example.test": "web.example.test"}
			tables, issues := Names(inv, func(string) bool { return false })
			if len(issues) > 0 {
				t.Fatal(issues)
			}
			if len(tables["internet"]) != 3 || len(tables["home"]) != 0 {
				t.Fatal(tables)
			}
			for _, r := range tables["internet"] {
				if r.Name == "alias.example.test" {
					continue
				}
				if r.Type != tc.kind || r.Value != tc.address {
					t.Fatalf("unexpected record: %+v", r)
				}
			}
			flat, issues := FlattenNames(tables["internet"])
			if tc.kind == "CNAME" {
				if flat != nil || len(issues) == 0 {
					t.Fatal("external domain cannot be flattened without DNS lookups")
				}
			} else if len(issues) > 0 || len(flat) != 3 {
				t.Fatal(flat, issues)
			}
		})
	}
}
func TestExternalEndpointDoesNotHideLocalCycles(t *testing.T) {
	inv := externalInventory("origin.example.net")
	inv.Names = map[string]string{"origin.example.net": "web.example.test"}
	_, issues := Names(inv, func(string) bool { return false })
	if !strings.Contains(strings.Join(issues, "\n"), "cycle") {
		t.Fatal(issues)
	}
	inv = externalInventory("origin.example.net")
	inv.Names = map[string]string{"missing.example.test": "not-authored.example.net"}
	_, issues = Names(inv, func(string) bool { return false })
	if !strings.Contains(strings.Join(issues, "\n"), "absent") {
		t.Fatal("unmarked external alias accepted", issues)
	}
}

func TestCanonicalAccessNameIsNotASelfCNAME(t *testing.T) {
	inv := externalInventory("web.example.test")
	tables, issues := Names(inv, func(string) bool { return false })
	if len(issues) > 0 {
		t.Fatal(issues)
	}
	for _, r := range tables["internet"] {
		if r.Name == "web.example.test" {
			t.Fatal("canonical endpoint became a self-CNAME")
		}
	}
}
func TestPublishedPortsShareOneRecord(t *testing.T) {
	inv := externalInventory("origin.example.net")
	inst := &inv.Nodes[0].Instances[0]
	inst.Ports["other"] = inventory.Port{Names: []string{"web.example.test"}}
	tables, issues := Names(inv, func(string) bool { return false })
	if len(issues) > 0 || len(tables["internet"]) != 2 {
		t.Fatal(tables, issues)
	}
	for range 20 {
		again, issues := Names(inv, func(string) bool { return false })
		if len(issues) > 0 || !reflect.DeepEqual(tables, again) {
			t.Fatal("unstable shared-record source", again, issues)
		}
	}
}

func TestDeviceNameReferencesAccessEndpoint(t *testing.T) {
	for _, tc := range []struct{ address, kind string }{{"origin.example.net", "CNAME"}, {"203.0.113.10", "A"}, {"2001:db8::10", "AAAA"}} {
		t.Run(tc.kind, func(t *testing.T) {
			inv := externalInventory(tc.address)
			h := inv.Hosts["host-a"]
			h.Names = map[string][]inventory.NameRef{"host-a.dev.example.test": {{Endpoint: "internet"}}}
			inv.Hosts["host-a"] = h
			tables, issues := Names(inv, func(string) bool { return false })
			if len(issues) > 0 {
				t.Fatal(issues)
			}
			records := dnsValues(tables["internet"])
			if len(records) != 3 || records["host-a.dev.example.test"] != tc.kind+" "+tc.address || records["web.example.test"] != "CNAME host-a.dev.example.test" || records["files.example.test"] != "CNAME host-a.dev.example.test" {
				t.Fatal(records)
			}
			if len(tables["home"]) != 0 {
				t.Fatal("endpoint name leaked into another network")
			}
			flat, issues := FlattenNames(tables["internet"])
			if tc.kind == "CNAME" {
				if flat != nil || len(issues) == 0 {
					t.Fatal("external endpoint flattened")
				}
			} else if len(issues) > 0 || len(flat) != 3 {
				t.Fatal(flat, issues)
			}
		})
	}
}
func TestNameEndpointReferenceFailures(t *testing.T) {
	for _, tc := range []struct {
		name string
		refs []inventory.NameRef
		want string
	}{
		{"missing", []inventory.NameRef{{Endpoint: "missing"}}, "does not exist"},
		{"mixed", []inventory.NameRef{{Endpoint: "internet", Interface: "en", Address: "v4"}}, "not both"},
		{"duplicate network", []inventory.NameRef{{Endpoint: "internet"}, {Endpoint: "internet"}}, "multiple references"},
		{"empty", []inventory.NameRef{{}}, "does not exist"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inv := externalInventory("origin.example.net")
			h := inv.Hosts["host-a"]
			h.Names = map[string][]inventory.NameRef{"host-a.dev.example.test": tc.refs}
			inv.Hosts["host-a"] = h
			_, issues := Names(inv, func(string) bool { return false })
			if !strings.Contains(strings.Join(issues, "\n"), tc.want) {
				t.Fatal(issues)
			}
		})
	}
	inv := externalInventory("web.example.test")
	h := inv.Hosts["host-a"]
	h.Names = map[string][]inventory.NameRef{"host-a.dev.example.test": {{Endpoint: "internet"}}}
	inv.Hosts["host-a"] = h
	tables, issues := Names(inv, func(string) bool { return false })
	if len(issues) > 0 {
		t.Fatal(issues)
	}
	if _, exists := dnsValues(tables["internet"])["web.example.test"]; exists {
		t.Fatal("canonical access domain became a self-reference through device name")
	}
}
func TestNameEndpointReferencePreservesLocalInterfaceChain(t *testing.T) {
	inv := layeredDNSInventory()
	before := dnsRecords(t, inv)
	h := inv.Hosts["host-a"]
	h.Names["host-a.dev.example.com"][0] = inventory.NameRef{Endpoint: "home"}
	inv.Hosts["host-a"] = h
	after := dnsRecords(t, inv)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("endpoint reference changed local DNS chain", after)
	}
}

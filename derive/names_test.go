package derive

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/d0u9/rhumb/inventory"
)

func layeredDNSInventory() *inventory.Root {
	ref := func(iface string) inventory.AddressRef { return inventory.AddressRef{Interface: iface, Address: "v4"} }
	home, lab := ref("en"), ref("en2")
	inv := &inventory.Root{
		Networks: []string{"home", "lab"},
		Hosts: map[string]inventory.Host{"host-a": {
			Names: map[string][]inventory.NameRef{"host-a.dev.example.com": {{Interface: home.Interface, Address: home.Address}, {Interface: lab.Interface, Address: lab.Address}}},
			Interfaces: map[string]inventory.Interface{
				"en":  {MAC: "02:00:00:00:00:11", Addresses: map[string]inventory.InterfaceAddress{"v4": {Network: "home", IP: "10.0.0.10", Names: []string{"host-a-en.dev.example.com"}}}},
				"en2": {MAC: "02:00:00:00:00:12", Addresses: map[string]inventory.InterfaceAddress{"v4": {Network: "lab", IP: "10.1.0.10", Names: []string{"host-a-en2.dev.example.com"}}}},
			},
			Endpoints: map[string]inventory.Endpoint{"home": {From: &home}, "lab": {From: &lab}},
		}},
		Nodes: []inventory.Node{{ID: "node-a", Host: "host-a", Instances: []inventory.Instance{{ID: "service-a", Service: "web", Ports: map[string]inventory.Port{"web": {Names: []string{"server.example.com"}}}}}}},
		Names: map[string]string{"nas.example.com": "host-a.dev.example.com", "files.example.com": "nas.example.com"},
	}
	inv.ResolveHosts()
	return inv
}

func dnsRecords(t *testing.T, inv *inventory.Root) map[string][]Name {
	t.Helper()
	tables, issues := Names(inv, func(string) bool { return false })
	if len(issues) > 0 {
		t.Fatalf("Names: %v", issues)
	}
	return tables
}
func dnsValues(records []Name) map[string]string {
	out := map[string]string{}
	for _, r := range records {
		out[r.Name] = r.Type + " " + r.Value
	}
	return out
}

func TestNamesLayeredNetworksAndFlatten(t *testing.T) {
	tables := dnsRecords(t, layeredDNSInventory())
	for network, address := range map[string]string{"home": "10.0.0.10", "lab": "10.1.0.10"} {
		iface := "host-a-en.dev.example.com"
		if network == "lab" {
			iface = "host-a-en2.dev.example.com"
		}
		want := map[string]string{iface: "A " + address, "host-a.dev.example.com": "CNAME " + iface, "server.example.com": "CNAME host-a.dev.example.com", "nas.example.com": "CNAME host-a.dev.example.com", "files.example.com": "CNAME nas.example.com"}
		if got := dnsValues(tables[network]); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %v, want %v", network, got, want)
		}
		flat, issues := FlattenNames(tables[network])
		if len(issues) > 0 || len(flat) != len(want) {
			t.Fatalf("flatten %s: %v %v", network, flat, issues)
		}
		for _, r := range flat {
			if r.Type != "A" || r.Value != address || r.Source == "" {
				t.Errorf("flatten %s: %+v", network, r)
			}
		}
	}
}

func TestNamesDeviceReferenceSwitch(t *testing.T) {
	inv := layeredDNSInventory()
	h := inv.Hosts["host-a"]
	h.Interfaces["wifi"] = inventory.Interface{Addresses: map[string]inventory.InterfaceAddress{"v4": {Network: "home", IP: "10.0.0.11", Names: []string{"host-a-wifi.dev.example.com"}}}}
	inv.Hosts["host-a"] = h
	// Establish ownership before switching only the device and endpoint references.
	before := dnsRecords(t, inv)
	members := inv.NetworkMembers()
	ref := inventory.AddressRef{Interface: "wifi", Address: "v4"}
	h.Names["host-a.dev.example.com"][0] = inventory.NameRef{Interface: ref.Interface, Address: ref.Address}
	h.Endpoints["home"] = inventory.Endpoint{From: &ref}
	inv.Hosts["host-a"] = h
	inv.ResolveHosts()
	after := dnsRecords(t, inv)
	if !reflect.DeepEqual(members, inv.NetworkMembers()) {
		t.Fatal("reference switch changed address ownership")
	}
	if !reflect.DeepEqual(before["lab"], after["lab"]) {
		t.Fatal("reference switch changed lab records")
	}
	old, new := dnsValues(before["home"]), dnsValues(after["home"])
	old["host-a.dev.example.com"] = "CNAME host-a-wifi.dev.example.com"
	if !reflect.DeepEqual(old, new) {
		t.Fatalf("switch changed other DNS layers: %v -> %v", old, new)
	}
}

func TestNamesProxyUsesFrontHostDevice(t *testing.T) {
	inv := layeredDNSInventory()
	inv.Nodes = append(inv.Nodes, inventory.Node{ID: "backend", Networks: inventory.Networks{"home": "10.0.0.20"}, Instances: []inventory.Instance{{ID: "backend-web", Service: "web", Ports: map[string]inventory.Port{"web": {Names: []string{"backend.example.com"}}}}}})
	inv.Routes = map[string]inventory.Route{"proxy": {Hops: []string{"service-a:web", "backend-web:web"}}}
	tables, issues := Names(inv, func(id string) bool { return id == "service-a" })
	if len(issues) > 0 {
		t.Fatal(issues)
	}
	for _, network := range []string{"home", "lab"} {
		if got := dnsValues(tables[network])["backend.example.com"]; got != "CNAME host-a.dev.example.com" {
			t.Errorf("%s proxy DNS = %s", network, got)
		}
	}
}

func TestNamesRejectInvalidLayers(t *testing.T) {
	tests := []struct {
		name, issue string
		change      func(*inventory.Root)
	}{
		{"duplicate interface name", "duplicate name", func(inv *inventory.Root) {
			h := inv.Hosts["host-a"]
			i := h.Interfaces["en"]
			a := i.Addresses["v4"]
			a.Names = append(a.Names, "HOST-A-EN.DEV.EXAMPLE.COM.")
			i.Addresses["v4"] = a
		}},
		{"duplicate published name", "duplicate name", func(inv *inventory.Root) {
			p := inv.Nodes[0].Instances[0].Ports["web"]
			p.Names = append(p.Names, "SERVER.EXAMPLE.COM.")
			inv.Nodes[0].Instances[0].Ports["web"] = p
		}},
		{"duplicate network ref", "multiple references", func(inv *inventory.Root) {
			h := inv.Hosts["host-a"]
			h.Names["host-a.dev.example.com"] = append(h.Names["host-a.dev.example.com"], h.Names["host-a.dev.example.com"][0])
		}},
		{"missing interface", "does not exist", func(inv *inventory.Root) {
			inv.Hosts["host-a"].Names["host-a.dev.example.com"][0].Interface = "missing"
		}},
		{"missing address", "does not exist", func(inv *inventory.Root) { inv.Hosts["host-a"].Names["host-a.dev.example.com"][0].Address = "missing" }},
		{"unnamed interface", "no names", func(inv *inventory.Root) {
			h := inv.Hosts["host-a"]
			i := h.Interfaces["en"]
			a := i.Addresses["v4"]
			a.Names = nil
			i.Addresses["v4"] = a
		}},
		{"A CNAME collision", "name", func(inv *inventory.Root) {
			h := inv.Hosts["host-a"]
			i := h.Interfaces["en"]
			a := i.Addresses["v4"]
			a.Names = append(a.Names, "host-a.dev.example.com")
			i.Addresses["v4"] = a
		}},
		{"absent alias", "absent", func(inv *inventory.Root) { inv.Names["missing.example.com"] = "absent.example.com" }},
		{"published alias collision", "name", func(inv *inventory.Root) { inv.Names["server.example.com"] = "host-a.dev.example.com" }},
		{"device alias collision", "name", func(inv *inventory.Root) { inv.Names["host-a.dev.example.com"] = "host-a-en.dev.example.com" }},
		{"interface alias collision", "name", func(inv *inventory.Root) { inv.Names["host-a-en.dev.example.com"] = "host-a.dev.example.com" }},
		{"orphan cycle", "cycle", func(inv *inventory.Root) {
			inv.Names["x.example.com"] = "y.example.com"
			inv.Names["y.example.com"] = "x.example.com"
		}},
		{"case trailing dot collision", "name", func(inv *inventory.Root) { inv.Names["HOST-A.DEV.EXAMPLE.COM."] = "host-a-en.dev.example.com" }},
		{"ambiguous endpoint", "multiple device names", func(inv *inventory.Root) {
			inv.Hosts["host-a"].Names["other.dev.example.com"] = []inventory.NameRef{{Interface: "en", Address: "v4"}}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inv := layeredDNSInventory()
			tt.change(inv)
			_, issues := Names(inv, func(string) bool { return false })
			if !strings.Contains(strings.Join(issues, "\n"), tt.issue) {
				t.Fatalf("issues=%v, want %q", issues, tt.issue)
			}
		})
	}
}

func TestFlattenNamesRejectsIncompleteAndCyclicGraphs(t *testing.T) {
	for _, tt := range []struct {
		name, issue string
		records     []Name
	}{
		{"missing same network target", "absent", []Name{{Name: "a.example.com", Type: "CNAME", Value: "elsewhere.example.com"}}},
		{"cycle", "cycle", []Name{{Name: "a.example.com", Type: "CNAME", Value: "b.example.com"}, {Name: "b.example.com", Type: "CNAME", Value: "a.example.com"}}},
		{"canonical collision", "duplicate", []Name{{Name: "A.example.com.", Type: "A", Value: "10.0.0.10"}, {Name: "a.example.com", Type: "CNAME", Value: "A.example.com."}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			out, issues := FlattenNames(tt.records)
			if out != nil || !strings.Contains(strings.Join(issues, "\n"), tt.issue) {
				t.Fatalf("flatten=%v %v", out, issues)
			}
		})
	}
}

func TestNamesCNAMEHopLimit(t *testing.T) {
	for _, hops := range []int{8, 9} {
		t.Run(fmt.Sprint(hops), func(t *testing.T) {
			records := []Name{{Name: "end.example.com", Type: "A", Value: "10.0.0.10"}}
			target := "end.example.com"
			for i := 0; i < hops; i++ {
				name := fmt.Sprintf("alias-%d.example.com", i)
				records = append(records, Name{Name: name, Type: "CNAME", Value: target})
				target = name
			}
			out, issues := FlattenNames(records)
			if hops == 8 && (len(issues) > 0 || len(out) != len(records)) {
				t.Fatalf("eight hops rejected: %v", issues)
			}
			if hops == 9 && (out != nil || !strings.Contains(strings.Join(issues, "\n"), "exceeds 8")) {
				t.Fatalf("nine hops accepted: %v %v", out, issues)
			}
		})
	}
}

func TestNamesIPFallbackAndIPv6(t *testing.T) {
	inv := layeredDNSInventory()
	h := inv.Hosts["host-a"]
	h.Names = nil
	h.Endpoints["lab"] = inventory.Endpoint{Address: "host-a.example.net"}
	h.Interfaces["v6"] = inventory.Interface{Addresses: map[string]inventory.InterfaceAddress{"main": {Network: "home", IP: "2001:db8::10", Names: []string{"ipv6.example.test"}}}}
	inv.Hosts["host-a"] = h
	inv.Names = nil
	inv.ResolveHosts()
	tables := dnsRecords(t, inv)
	if got := dnsValues(tables["home"])["server.example.com"]; got != "A 10.0.0.10" {
		t.Fatalf("fallback: %s", got)
	}
	if got := dnsValues(tables["lab"])["server.example.com"]; got != "CNAME host-a.example.net" {
		t.Fatalf("domain endpoint CNAME: %s", got)
	}
	if got := dnsValues(tables["home"])["ipv6.example.test"]; got != "AAAA 2001:db8::10" {
		t.Fatalf("IPv6: %s", got)
	}
	if _, issues := FlattenNames(tables["home"]); len(issues) > 0 {
		t.Fatal(issues)
	}
}

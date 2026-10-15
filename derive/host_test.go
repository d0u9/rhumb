package derive

import (
	"reflect"
	"testing"

	"github.com/d0u9/rhumb/inventory"
)

func TestHostDerivedNetworkValues(t *testing.T) {
	inst := inventory.Instance{ID: "node-1/service", Service: "example", Ports: inventory.Ports{"main": {Names: []string{"service.home.test"}}}}
	inv := &inventory.Root{Networks: []string{"home"}, Nodes: []inventory.Node{{ID: "node-1", Host: "host-a", Instances: []inventory.Instance{inst}}}, Hosts: map[string]inventory.Host{"host-a": {
		Interfaces: map[string]inventory.Interface{"wired": {MAC: "02:00:00:00:00:11", Addresses: map[string]inventory.InterfaceAddress{"primary": {Network: "home", IP: "10.0.0.10", DHCPReservation: true}}}},
		Endpoints:  map[string]inventory.Endpoint{"home": {From: &inventory.AddressRef{Interface: "wired", Address: "primary"}}},
	}}}
	inv.ResolveHosts()
	names, conflicts := Names(inv, func(string) bool { return false })
	if len(conflicts) != 0 || len(names["home"]) != 1 || names["home"][0].Value != "10.0.0.10" {
		t.Fatalf("DNS: %v %v", names, conflicts)
	}
	rows := Reservations(inv, "home")
	if len(rows) != 1 || rows[0].ID != "host-a/wired/primary" {
		t.Fatalf("reservation: %v", rows)
	}
	caller := inventory.Node{ID: "caller", Reaches: []string{"home"}}
	address, network, err := ResolveAddress(inv, inventory.Instance{}, caller, inst, inv.Nodes[0])
	if err != nil || address != "10.0.0.10" || network != "home" {
		t.Fatalf("dial: %s %s %v", address, network, err)
	}
}

func TestHostEndpointSwitchPreservesAddressOwners(t *testing.T) {
	inv := &inventory.Root{Nodes: []inventory.Node{{ID: "node-1", Host: "host-a"}}, Hosts: map[string]inventory.Host{"host-a": {
		Interfaces: map[string]inventory.Interface{
			"wired": {MAC: "02:00:00:00:00:11", Addresses: map[string]inventory.InterfaceAddress{"a": {Network: "home", IP: "10.0.0.10", DHCPReservation: true, Names: []string{"wired.home.test"}}}},
			"wifi":  {MAC: "02:00:00:00:00:12", Addresses: map[string]inventory.InterfaceAddress{"a": {Network: "home", IP: "10.0.0.11", DHCPReservation: true, Names: []string{"wifi.home.test"}}}},
		}, Endpoints: map[string]inventory.Endpoint{"home": {From: &inventory.AddressRef{Interface: "wired", Address: "a"}}},
	}}}
	inv.ResolveHosts()
	before := Reservations(inv, "home")
	if len(before) != 2 || before[0].ID != "host-a/wired/a" || before[1].ID != "host-a/wifi/a" {
		t.Fatalf("interface reservation labels = %+v", before)
	}
	names, conflicts := Names(inv, func(string) bool { return false })
	if len(conflicts) != 0 || len(names["home"]) != 2 {
		t.Fatalf("device names = %v %v", names, conflicts)
	}
	h := inv.Hosts["host-a"]
	h.Endpoints["home"] = inventory.Endpoint{From: &inventory.AddressRef{Interface: "wifi", Address: "a"}}
	inv.Hosts["host-a"] = h
	inv.ResolveHosts()
	if inv.Nodes[0].Networks["home"] != "10.0.0.11" {
		t.Fatal("endpoint did not switch")
	}
	if after := Reservations(inv, "home"); !reflect.DeepEqual(before, after) {
		t.Fatalf("switch changed reservations: %v / %v", before, after)
	}
	after, afterConflicts := Names(inv, func(string) bool { return false })
	if !reflect.DeepEqual(names, after) || !reflect.DeepEqual(conflicts, afterConflicts) {
		t.Fatalf("switch changed device names: %v / %v", names, after)
	}
}

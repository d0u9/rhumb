package validate

import (
	"reflect"
	"strings"
	"testing"

	"github.com/d0u9/rhumb/inventory"
)

func TestHostIssuesMultipleAddresses(t *testing.T) {
	inv := &inventory.Root{Networks: []string{"home"}, Hosts: map[string]inventory.Host{"host-a": {
		Interfaces: map[string]inventory.Interface{"wired": {MAC: "02:00:00:00:00:11", Addresses: map[string]inventory.InterfaceAddress{
			"primary":   {Network: "home", IP: "10.0.0.10", DHCPReservation: true},
			"secondary": {Network: "home", IP: "10.0.0.12"},
		}}},
		Endpoints: map[string]inventory.Endpoint{"home": {From: &inventory.AddressRef{Interface: "wired", Address: "primary"}}},
	}}}
	before := inv.Hosts["host-a"].Interfaces["wired"].Addresses["secondary"]
	if got := hostIssues(inv); len(got) != 0 {
		t.Fatalf("valid multi-address interface: %v", got)
	}
	if !reflect.DeepEqual(before, inv.Hosts["host-a"].Interfaces["wired"].Addresses["secondary"]) {
		t.Fatal("validation mutated address")
	}
	secondary := before
	secondary.DHCPReservation = true
	inv.Hosts["host-a"].Interfaces["wired"].Addresses["secondary"] = secondary
	if got := strings.Join(hostIssues(inv), "\n"); !strings.Contains(got, "multiple IPv4") {
		t.Fatalf("missing reservation conflict: %s", got)
	}
}

func TestHostIssuesInvalidFacts(t *testing.T) {
	tests := []struct {
		name string
		host inventory.Host
		want string
	}{
		{"mac-only", inventory.Host{Interfaces: map[string]inventory.Interface{"lan": {MAC: "bad"}}}, "invalid MAC"},
		{"missing-network", inventory.Host{Reaches: []string{"missing"}}, "unknown network"},
		{"missing-reference", inventory.Host{Endpoints: map[string]inventory.Endpoint{"home": {From: &inventory.AddressRef{Interface: "missing", Address: "a"}}}}, "does not exist"},
		{"ambiguous-endpoint", inventory.Host{Endpoints: map[string]inventory.Endpoint{"home": {Address: "host-a.example.net", From: &inventory.AddressRef{Interface: "lan", Address: "a"}}}}, "exactly one"},
		{"invalid-interface", inventory.Host{Interfaces: map[string]inventory.Interface{"../lan": {}}}, "invalid interface identifier"},
		{"invalid-address", inventory.Host{Interfaces: map[string]inventory.Interface{"lan": {Addresses: map[string]inventory.InterfaceAddress{"a": {Network: "home", IP: "host-a.example.net"}}}}}, "IP literal"},
		{"reservation-without-mac", inventory.Host{Interfaces: map[string]inventory.Interface{"lan": {Addresses: map[string]inventory.InterfaceAddress{"a": {Network: "home", IP: "10.0.0.10", DHCPReservation: true}}}}}, "requires a valid MAC"},
		{"ipv6-reservation", inventory.Host{Interfaces: map[string]inventory.Interface{"lan": {MAC: "02:00:00:00:00:11", Addresses: map[string]inventory.InterfaceAddress{"a": {Network: "home", IP: "2001:db8::1", DHCPReservation: true}}}}}, "requires a valid MAC"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inv := &inventory.Root{Networks: []string{"home"}, Hosts: map[string]inventory.Host{"host-a": tt.host}}
			if got := strings.Join(hostIssues(inv), "\n"); !strings.Contains(got, tt.want) {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestHostIssuesBinding(t *testing.T) {
	inv := &inventory.Root{Hosts: map[string]inventory.Host{"host-a": {}}, Nodes: []inventory.Node{{ID: "node-a", Host: "host-a"}, {ID: "node-b", Host: "host-a"}, {ID: "node-c", Host: "missing"}}}
	got := strings.Join(hostIssues(inv), "\n")
	if !strings.Contains(got, "bound by nodes") || !strings.Contains(got, "does not exist") {
		t.Fatalf("binding checks: %s", got)
	}
	inv = &inventory.Root{Hosts: map[string]inventory.Host{"../host": {}}}
	if got := strings.Join(hostIssues(inv), "\n"); !strings.Contains(got, "invalid host identifier") {
		t.Fatalf("unsafe host ID: %s", got)
	}
}

// Rule 28: on one network an IP has one address declaration and a MAC one
// interface, however many hosts are involved.
func TestNetworkIssuesRule28(t *testing.T) {
	type ifaces = map[string]inventory.Interface
	type addrs = map[string]inventory.InterfaceAddress
	iface := func(mac, network, ip string) inventory.Interface {
		return inventory.Interface{MAC: mac, Addresses: addrs{"a": {Network: network, IP: ip}}}
	}
	const macA, macB, macC = "02:00:00:00:00:01", "02:00:00:00:00:02", "02:00:00:00:00:03"
	for _, tc := range []struct {
		name  string
		hosts map[string]inventory.Host
		want  string
	}{
		{"an IP on two hosts", map[string]inventory.Host{
			"host-a": {Interfaces: ifaces{"eth0": iface(macA, "lan", "10.0.0.1")}},
			"host-b": {Interfaces: ifaces{"eth0": iface(macB, "lan", "10.0.0.1")}},
		}, "address 10.0.0.1 is held by"},
		{"an IP on two interfaces of one host", map[string]inventory.Host{
			"host-a": {Interfaces: ifaces{"eth0": iface(macA, "lan", "10.0.0.1"), "eth1": iface(macB, "lan", "10.0.0.1")}},
		}, "address 10.0.0.1 is held by"},
		{"an IP twice on one interface", map[string]inventory.Host{
			"host-a": {Interfaces: ifaces{"eth0": {MAC: macA, Addresses: addrs{
				"a": {Network: "lan", IP: "10.0.0.1"}, "b": {Network: "lan", IP: "10.0.0.1"},
			}}}},
		}, "address 10.0.0.1 is held by"},
		{"one IPv6 address written two ways", map[string]inventory.Host{
			"host-a": {Interfaces: ifaces{"eth0": iface(macA, "lan", "2001:db8::1")}},
			"host-b": {Interfaces: ifaces{"eth0": iface(macB, "lan", "2001:0db8:0::1")}},
		}, "address 2001:db8::1 is held by"},
		{"a MAC on two hosts", map[string]inventory.Host{
			"host-a": {Interfaces: ifaces{"eth0": iface(macA, "lan", "10.0.0.1")}},
			"host-b": {Interfaces: ifaces{"eth0": iface(macA, "lan", "10.0.0.2")}},
		}, "mac " + macA + " is held by"},
		{"a MAC on two interfaces of one host", map[string]inventory.Host{
			"host-a": {Interfaces: ifaces{"eth0": iface(macA, "lan", "10.0.0.1"), "eth1": iface(macA, "lan", "10.0.0.2")}},
		}, "mac " + macA + " is held by"},
		{"one MAC written two ways", map[string]inventory.Host{
			"host-a": {Interfaces: ifaces{"eth0": iface("02:00:00:00:00:0A", "lan", "10.0.0.1")}},
			"host-b": {Interfaces: ifaces{"eth0": iface("02-00-00-00-00-0a", "lan", "10.0.0.2")}},
		}, "mac 02:00:00:00:00:0a is held by"},
		{"several IPs sharing one interface's MAC", map[string]inventory.Host{
			"host-a": {Interfaces: ifaces{"eth0": {MAC: macA, Addresses: addrs{
				"a": {Network: "lan", IP: "10.0.0.1"}, "b": {Network: "lan", IP: "10.0.0.2"},
			}}}},
		}, ""},
		{"one IP and one MAC on two networks", map[string]inventory.Host{
			"host-a": {Interfaces: ifaces{"eth0": iface(macC, "lan", "10.0.0.1")}},
			"host-b": {Interfaces: ifaces{"eth0": iface(macC, "wan", "10.0.0.1")}},
		}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inv := &inventory.Root{Networks: []string{"lan", "wan"}, Hosts: tc.hosts}
			got := strings.Join(networkIssues(inv, nil, func(string) bool { return false }), "\n")
			if tc.want == "" && strings.Contains(got, "held by") {
				t.Fatalf("unexpected conflict: %s", got)
			}
			if !strings.Contains(got, tc.want) {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

package derive

import (
	"testing"

	"github.com/d0u9/rhumb/confgen"
	"github.com/d0u9/rhumb/inventory"
)

// mappingInventory holds the three cases docs/export.md#a-second-file-what-deploys-it
// names, on one node written at the given address:
//
//   - bin-sea01:web is entered only by the proxy on its own machine, and so
//     publishes on loopback.
//   - ss-sea01:main is entered by a relay on another machine, and so
//     publishes on this node's own address — or on 0.0.0.0 when what this
//     node is written at is a name rather than an address.
//   - caddy-sea01:https is entered by nothing at all — it is reached from a
//     browser — and publishes the way the second case does.
func mappingInventory(address string) *inventory.Root {
	return &inventory.Root{
		Nodes: []inventory.Node{
			{
				ID:       "sea1",
				Networks: inventory.Networks{"internet": address},
				Instances: []inventory.Instance{
					{ID: "caddy-sea01", Service: "caddy", Runtime: inventory.RuntimeDocker,
						Ports: inventory.PortsOf(map[string]int{"https": 443})},
					{ID: "bin-sea01", Service: "microbin", Runtime: inventory.RuntimeDocker,
						Ports: inventory.PortsOf(map[string]int{"web": 8080})},
					{ID: "ss-sea01", Service: "ssserver",
						Ports: inventory.PortsOf(map[string]int{"main": 38250})},
				},
			},
			{
				ID:       "hel1",
				Networks: inventory.Networks{"internet": "203.0.113.20"},
				Instances: []inventory.Instance{
					{ID: "fwd-hel01", Service: "realm", Ports: inventory.PortsOf(map[string]int{"ss": 38250})},
				},
			},
			{ID: "laptop", Owner: "dana"},
		},
		Users: map[string]inventory.User{
			"dana": {Username: "dana", Access: []string{"paste", "hel-sea"}},
		},
		Routes: map[string]inventory.Route{
			"paste":   {Hops: []string{"caddy-sea01:https", "bin-sea01:web"}},
			"hel-sea": {Hops: []string{"fwd-hel01:ss", "ss-sea01:main"}},
		},
		Networks:  []string{"internet"},
		Universal: "internet",
	}
}

// mappingManifests is workedManifests plus the two services this fixture
// runs that it does not: a reverse proxy that fans out, and a relay that
// terminates nothing.
func mappingManifests() map[string]confgen.Manifest {
	manifests := workedManifests()
	manifests["caddy"] = confgen.Manifest{Auth: confgen.AuthNone, Downstreams: confgen.DownstreamsMany, Template: "t"}
	manifests["realm"] = confgen.Manifest{Forwards: true, Template: "t"}
	return manifests
}

func mappingsOf(t *testing.T, inv *inventory.Root, instance string) map[string]Mapping {
	t.Helper()
	m, err := Derive(inv, mappingManifests())
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	return m.Mappings(inv, instance)
}

// TestMappings_PortEnteredOnlyFromItsOwnNodePublishesOnLoopback is the case
// the derivation exists for: a backend behind a proxy on the same machine,
// published on every interface because someone typed it, is what writing the
// mapping by hand gets wrong.
func TestMappings_PortEnteredOnlyFromItsOwnNodePublishesOnLoopback(t *testing.T) {
	got := mappingsOf(t, mappingInventory("203.0.113.10"), "bin-sea01")
	wantMapping(t, got, "web", 8080, "127.0.0.1")
}

// wantMapping is one port's whole mapping: the addresses it publishes on, in
// order, and the number, which is the port's own on both sides.
func wantMapping(t *testing.T, got map[string]Mapping, port string, number int, addresses ...string) {
	t.Helper()
	m, ok := got[port]
	if !ok {
		t.Fatalf("port %q has no mapping; got %+v", port, got)
	}
	if m.Number != number {
		t.Fatalf("port %q maps number %d, want %d", port, m.Number, number)
	}
	if len(m.Addresses) != len(addresses) {
		t.Fatalf("port %q publishes on %v, want %v", port, m.Addresses, addresses)
	}
	for i, want := range addresses {
		if m.Addresses[i] != want {
			t.Fatalf("port %q publishes on %v, want %v", port, m.Addresses, addresses)
		}
	}
}

// TestMappings_PortEnteredFromAnotherNodePublishesOnThisNodesAddress pins
// the second case, with the address the edge resolved on.
func TestMappings_PortEnteredFromAnotherNodePublishesOnThisNodesAddress(t *testing.T) {
	got := mappingsOf(t, mappingInventory("203.0.113.10"), "ss-sea01")
	wantMapping(t, got, "main", 38250, "203.0.113.10")
}

// TestMappings_ANameIsNotAnAddressToBind is the same case with a node
// written as a DNS name: a container runtime binds addresses, and a node
// naming itself sea1.example.net has not given it one.
func TestMappings_ANameIsNotAnAddressToBind(t *testing.T) {
	got := mappingsOf(t, mappingInventory("sea1.example.net"), "ss-sea01")
	wantMapping(t, got, "main", 38250, "0.0.0.0")
}

// TestMappings_PortNoEdgeEntersPublishesLikeOneEnteredFromElsewhere is the
// third case: a port reached from a browser. Nothing in the inventory says
// where it is reached from, so it is published the way anything reached from
// outside is.
func TestMappings_PortNoEdgeEntersPublishesLikeOneEnteredFromElsewhere(t *testing.T) {
	got := mappingsOf(t, mappingInventory("203.0.113.10"), "caddy-sea01")
	wantMapping(t, got, "https", 443, "203.0.113.10")
	got = mappingsOf(t, mappingInventory("sea1.example.net"), "caddy-sea01")
	wantMapping(t, got, "https", 443, "0.0.0.0")
}

// TestMappings_ANodeWithNoAddressAtAllPublishesOnEveryInterface: there is
// nothing to bind but everything, and the alternative — publishing nothing —
// would render a container no one can reach.
func TestMappings_ANodeWithNoAddressAtAllPublishesOnEveryInterface(t *testing.T) {
	inv := mappingInventory("203.0.113.10")
	inv.Nodes[0].Networks = nil
	inv.Routes = map[string]inventory.Route{"paste": {Hops: []string{"caddy-sea01:https", "bin-sea01:web"}}}
	inv.Users = map[string]inventory.User{"dana": {Username: "dana", Access: []string{"paste"}}}
	got := mappingsOf(t, inv, "caddy-sea01")
	wantMapping(t, got, "https", 443, "0.0.0.0")
}

// TestMappings_UnknownInstanceHasNone keeps a caller from having to check
// first: an instance this inventory does not hold maps nothing.
func TestMappings_UnknownInstanceHasNone(t *testing.T) {
	if got := mappingsOf(t, mappingInventory("203.0.113.10"), "nonesuch"); len(got) != 0 {
		t.Fatalf("Mappings(nonesuch) = %v, want none", got)
	}
}

// twoSegmentInventory is one machine on two networks, which is a second
// physical port and a second subnet. Nothing enters its DNS port — the
// clients that dial a resolver are not in this inventory — so it is the
// case that decides what a port reached from outside publishes on when the
// node is on more than one network.
func twoSegmentInventory() *inventory.Root {
	return &inventory.Root{
		Nodes: []inventory.Node{
			{
				ID:       "network-1",
				Networks: inventory.Networks{"home": "10.0.30.10", "lab": "10.0.50.10"},
				Instances: []inventory.Instance{
					{ID: "adguard-network-1", Service: "microbin", Runtime: inventory.RuntimeDocker,
						Ports: inventory.PortsOf(map[string]int{"dns": 53})},
				},
			},
		},
		Networks: []string{"home", "lab", "internet"},
	}
}

// TestMappings_ANodeOnTwoNetworksPublishesOnBoth: a machine with a port on
// each of two segments serves both, and one address would leave the second
// one with nothing listening. The order is the inventory's preference
// order, so a rendered file does not change because a network was added
// above another.
func TestMappings_ANodeOnTwoNetworksPublishesOnBoth(t *testing.T) {
	got := mappingsOf(t, twoSegmentInventory(), "adguard-network-1")
	wantMapping(t, got, "dns", 53, "10.0.30.10", "10.0.50.10")
}

// TestMappings_ANameAmongAddressesTakesTheWholePort: 0.0.0.0 already covers
// every interface, so listing it beside a literal address would publish the
// same port twice and the second bind would fail.
func TestMappings_ANameAmongAddressesTakesTheWholePort(t *testing.T) {
	inv := twoSegmentInventory()
	inv.Nodes[0].Networks["lab"] = "network-1.lab.example"
	got := mappingsOf(t, inv, "adguard-network-1")
	wantMapping(t, got, "dns", 53, "0.0.0.0")
}

// TestMappings_EnteredFromBothItsOwnNodeAndAnotherPublishesOnBoth is the
// case one address gets wrong in the other direction. A same-node hop
// resolves to 127.0.0.1, so a port reached by a local proxy and by another
// machine needs loopback as well as the address that machine dials — with
// only the second, the proxy beside it dials a number nothing published.
func TestMappings_EnteredFromBothItsOwnNodeAndAnotherPublishesOnBoth(t *testing.T) {
	inv := mappingInventory("203.0.113.10")
	// A second route reaching the same port from the proxy on its own node,
	// beside the relay on hel1 that already enters it.
	inv.Routes["local-ss"] = inventory.Route{Hops: []string{"caddy-sea01:https", "ss-sea01:main"}}
	inv.Users["dana"] = inventory.User{Username: "dana", Access: []string{"paste", "hel-sea", "local-ss"}}
	got := mappingsOf(t, inv, "ss-sea01")
	wantMapping(t, got, "main", 38250, "127.0.0.1", "203.0.113.10")
}

// TestMappings_ARouteStartingAtAProxiedPortStillPublishesOnThisNodesAddress
// is a backend behind the proxy on its own node that a second route also
// enters directly — a web interface kept reachable without the proxy, as a
// way in when the proxy is down. The proxy's edge alone would publish it on
// loopback only, and the direct route would dial a number nothing published.
func TestMappings_ARouteStartingAtAProxiedPortStillPublishesOnThisNodesAddress(t *testing.T) {
	inv := mappingInventory("203.0.113.10")
	inv.Routes["paste-direct"] = inventory.Route{Hops: []string{"bin-sea01:web"}}
	got := mappingsOf(t, inv, "bin-sea01")
	wantMapping(t, got, "web", 8080, "127.0.0.1", "203.0.113.10")
}

// containerInventory is mappingInventory with the proxy and its backend on
// one container network, the way a machine running both in containers is
// written.
func containerInventory() *inventory.Root {
	inv := mappingInventory("203.0.113.10")
	inv.Nodes[0].Containers = []inventory.ContainerNetwork{{Name: "web", Subnet: "172.20.0.0/24"}}
	inv.Nodes[0].Instances[0].Containers = map[string]string{"web": ""}
	inv.Nodes[0].Instances[1].Containers = map[string]string{"web": ""}
	return inv
}

// TestMappings_EdgeInsideAContainerNetworkPublishesNothing: the proxy dials
// its backend by name on the network they share, which never reaches the
// host, so the backend's port is published nowhere.
func TestMappings_EdgeInsideAContainerNetworkPublishesNothing(t *testing.T) {
	got := mappingsOf(t, containerInventory(), "bin-sea01")
	if m, ok := got["web"]; !ok || len(m.Addresses) != 0 || m.Number != 8080 {
		t.Fatalf("web = %+v, want 8080 published nowhere", got["web"])
	}
}

// TestMappings_DirectRouteIntoAContainerStillPublishesOnThisNodesAddress: the
// edge inside the container network asks for nothing, but a route entered at
// the port from outside still does.
func TestMappings_DirectRouteIntoAContainerStillPublishesOnThisNodesAddress(t *testing.T) {
	inv := containerInventory()
	inv.Routes["paste-direct"] = inventory.Route{Hops: []string{"bin-sea01:web"}}
	got := mappingsOf(t, inv, "bin-sea01")
	wantMapping(t, got, "web", 8080, "203.0.113.10")
}

// TestDerive_EdgeInsideAContainerNetworkDialsByName: the address is the
// downstream's own name on the network, and the edge says which network.
func TestDerive_EdgeInsideAContainerNetworkDialsByName(t *testing.T) {
	m, err := Derive(containerInventory(), mappingManifests())
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	for _, e := range m.Edges {
		if e.Route == "paste" && e.From.Instance == "caddy-sea01" {
			if e.Address != "bin-sea01" || e.Container != "web" || e.Network != "" {
				t.Fatalf("edge = %+v, want bin-sea01 on container network web", e)
			}
			return
		}
	}
	t.Fatal("no edge out of caddy-sea01 on route paste")
}

// TestDerive_ContainerDialingItsHostIsAnError: loopback inside a container on
// a container network is the container itself, so an edge from it to a host
// process on the same node has no address this model can give.
func TestDerive_ContainerDialingItsHostIsAnError(t *testing.T) {
	inv := containerInventory()
	inv.Nodes[0].Instances[1].Runtime = ""
	inv.Nodes[0].Instances[1].Containers = nil
	if _, err := Derive(inv, mappingManifests()); err == nil {
		t.Fatal("Derive succeeded, want an error for a container dialling a host process beside it")
	}
}

// TestMappings_RouteScopedToTheNodePublishesOnLoopback: a route written under
// its entry's node is for clients on that machine, so the entry publishes on
// loopback rather than on the node's addresses.
func TestMappings_RouteScopedToTheNodePublishesOnLoopback(t *testing.T) {
	inv := mappingInventory("203.0.113.10")
	inv.Routes["sea1/local"] = inventory.Route{Hops: []string{"ss-sea01:main"}, Scope: "sea1"}
	delete(inv.Routes, "hel-sea")
	inv.Users["dana"] = inventory.User{Username: "dana", Access: []string{"paste"}}
	got := mappingsOf(t, inv, "ss-sea01")
	wantMapping(t, got, "main", 38250, "127.0.0.1")
}

// TestDerive_TwoSharedContainersDialOverTheNodesFirst: a proxy and a backend
// on two common container networks dial over the one the node lists first.
func TestDerive_TwoSharedContainersDialOverTheNodesFirst(t *testing.T) {
	inv := containerInventory()
	inv.Nodes[0].Containers = append([]inventory.ContainerNetwork{{Name: "tailnet", Subnet: "172.29.250.0/24"}}, inv.Nodes[0].Containers...)
	inv.Nodes[0].Instances[0].Containers = map[string]string{"web": "", "tailnet": "172.29.250.10"}
	inv.Nodes[0].Instances[1].Containers = map[string]string{"web": "", "tailnet": ""}
	m, err := Derive(inv, mappingManifests())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range m.Edges {
		if e.Container != "" && e.Container != "tailnet" {
			t.Fatalf("edge %v runs over %q, want tailnet, the node's first", e, e.Container)
		}
		if e.Container == "tailnet" {
			return
		}
	}
	t.Fatal("no edge inside a container network")
}

// TestMappings_RouteScopedToAContainerNetworkPublishesNothing: a route
// written under a container network its entry joins is for clients on that
// bridge, behind a router the model does not carry, so the entry publishes
// on no host address.
func TestMappings_RouteScopedToAContainerNetworkPublishesNothing(t *testing.T) {
	inv := containerInventory()
	inv.Routes["web/direct"] = inventory.Route{Hops: []string{"bin-sea01:web"}, Scope: "web"}
	got := mappingsOf(t, inv, "bin-sea01")
	if m, ok := got["web"]; !ok || len(m.Addresses) != 0 || m.Number != 8080 {
		t.Fatalf("web = %+v, want 8080 published nowhere", got["web"])
	}
}

// TestContainerNames_AProxiedNameAnswersAtTheProxysFixedAddress: a name
// behind a proxy enters a container network's table at the proxy's fixed
// address there; a network the proxy joins at a runtime-assigned address
// holds no record.
func TestContainerNames_AProxiedNameAnswersAtTheProxysFixedAddress(t *testing.T) {
	inv := containerInventory()
	inv.Nodes[0].Containers = append(inv.Nodes[0].Containers, inventory.ContainerNetwork{Name: "tailnet", Subnet: "172.29.250.0/24"})
	inv.Nodes[0].Instances[0].Containers = map[string]string{"web": "", "tailnet": "172.29.250.10"}
	web := inv.Nodes[0].Instances[1].Ports["web"]
	web.Names = []string{"paste.example.org"}
	inv.Nodes[0].Instances[1].Ports["web"] = web
	fansOut := func(instance string) bool { return instance == "caddy-sea01" }

	got, conflicts := ContainerNames(inv, "sea1", fansOut)
	if len(conflicts) != 0 {
		t.Fatalf("conflicts = %v, want none", conflicts)
	}
	want := []Name{{Name: "paste.example.org", Address: "172.29.250.10", Source: "bin-sea01:web"}}
	if len(got) != 1 || len(got["tailnet"]) != 1 || got["tailnet"][0] != want[0] {
		t.Fatalf("ContainerNames = %+v, want tailnet holding %+v alone", got, want)
	}
	if other, _ := ContainerNames(inv, "hel1", fansOut); len(other) != 0 {
		t.Fatalf("ContainerNames on another node = %+v, want none", other)
	}
}

package derive

import (
	"testing"

	"github.com/d0u9/rhumb/confgen"
	"github.com/d0u9/rhumb/inventory"
)

func principalInventory() *inventory.Root {
	return &inventory.Root{
		Nodes: []inventory.Node{
			{ID: "home", Instances: []inventory.Instance{
				{ID: "home/local", Service: "local", Ports: inventory.PortsOf(map[string]int{"socks": 1080})},
				{ID: "home/proxy", Service: "proxy", Ports: inventory.PortsOf(map[string]int{"http": 80})},
			}},
			{ID: "far", Instances: []inventory.Instance{
				{ID: "far/server", Service: "server", Ports: inventory.PortsOf(map[string]int{"users": 443})},
			}},
		},
		Routes: map[string]inventory.Route{
			"out": {Hops: []string{"home/local:socks", "far/server:users"}},
			"web": {Hops: []string{"home/proxy:http", "far/server:users"}},
		},
		Users: map[string]inventory.User{
			"repeater": {Access: []string{"out"}},
			"dana":     {Access: []string{"web"}},
		},
	}
}

var principalManifests = map[string]confgen.Manifest{
	"local": {Upstream: confgen.UpstreamDecls{confgen.UpstreamShared: {}}},
	"proxy": {},
}

// TestFillPrincipals_OneUserIsFilled: the only user granted the route an
// upstream-dialling instance enters becomes its principal; a service with no
// upstream gets none even though one user holds its route.
func TestFillPrincipals_OneUserIsFilled(t *testing.T) {
	inv := principalInventory()
	FillPrincipals(inv, principalManifests)
	local, proxy := inv.Nodes[0].Instances[0], inv.Nodes[0].Instances[1]
	if local.Principal != "repeater" || !local.PrincipalDerived {
		t.Fatalf("local principal = %q derived %v, want repeater derived", local.Principal, local.PrincipalDerived)
	}
	if proxy.Principal != "" {
		t.Fatalf("proxy principal = %q, want none", proxy.Principal)
	}
}

// TestFillPrincipals_SeveralUsersFillNothing: two users on the route leave
// the instance dialling as itself.
func TestFillPrincipals_SeveralUsersFillNothing(t *testing.T) {
	inv := principalInventory()
	inv.Users["dana"] = inventory.User{Access: []string{"out", "web"}}
	FillPrincipals(inv, principalManifests)
	if p := inv.Nodes[0].Instances[0].Principal; p != "" {
		t.Fatalf("principal = %q, want none", p)
	}
}

// TestFillPrincipals_KeepsAWrittenPrincipal: a written principal wins.
func TestFillPrincipals_KeepsAWrittenPrincipal(t *testing.T) {
	inv := principalInventory()
	inv.Nodes[0].Instances[0].Principal = "dana"
	FillPrincipals(inv, principalManifests)
	local := inv.Nodes[0].Instances[0]
	if local.Principal != "dana" || local.PrincipalDerived {
		t.Fatalf("principal = %q derived %v, want dana written", local.Principal, local.PrincipalDerived)
	}
}

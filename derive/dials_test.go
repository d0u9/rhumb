package derive

import (
	"strings"
	"testing"

	"github.com/d0u9/rhumb/confgen"
	"github.com/d0u9/rhumb/inventory"
)

func dialInventory() *inventory.Root {
	return &inventory.Root{
		Nodes: []inventory.Node{
			{ID: "home", Networks: inventory.Networks{"lan": "10.0.0.2"}, Containers: []inventory.ContainerNetwork{{Name: "web", Subnet: "172.20.0.0/24"}}, Instances: []inventory.Instance{
				{ID: "home/digest", Service: "digest", Runtime: inventory.RuntimeDocker, Containers: map[string]string{"web": ""}},
				{ID: "home/rss", Service: "rss", Runtime: inventory.RuntimeDocker, Containers: map[string]string{"web": ""}, Ports: inventory.PortsOf(map[string]int{"web": 80})},
			}},
			{ID: "far", Networks: inventory.Networks{"lan": "10.0.0.3"}, Instances: []inventory.Instance{
				{ID: "far/rss", Service: "rss", Ports: inventory.PortsOf(map[string]int{"web": 80})},
			}},
		},
		Networks: []string{"lan"},
	}
}

var rssDial = confgen.DialDecl{Service: "rss", Port: "web"}

// TestServiceDial_InnermostScopeWins: an instance on the caller's container
// network is chosen over one elsewhere on the LAN.
func TestServiceDial_InnermostScopeWins(t *testing.T) {
	inv := dialInventory()
	got, err := ServiceDial(inv, inv.Nodes[0].Instances[0], inv.Nodes[0], rssDial)
	if err != nil || got != "home/rss:web" {
		t.Fatalf("ServiceDial = %q, %v; want home/rss:web", got, err)
	}
}

// TestServiceDial_TwoInOneScopeIsAnError: with nothing nearer, two on the
// same network are ambiguous.
func TestServiceDial_TwoInOneScopeIsAnError(t *testing.T) {
	inv := dialInventory()
	inv.Nodes[0].Instances = inv.Nodes[0].Instances[:1]
	inv.Nodes = append(inv.Nodes, inventory.Node{ID: "other", Networks: inventory.Networks{"lan": "10.0.0.4"}, Instances: []inventory.Instance{
		{ID: "other/rss", Service: "rss", Ports: inventory.PortsOf(map[string]int{"web": 80})},
	}})
	_, err := ServiceDial(inv, inv.Nodes[0].Instances[0], inv.Nodes[0], rssDial)
	if err == nil || !strings.Contains(err.Error(), "far/rss, other/rss") {
		t.Fatalf("err = %v, want both candidates named", err)
	}
}

// TestFillServiceDials_KeepsAWrittenDial: a dial the instance writes wins.
func TestFillServiceDials_KeepsAWrittenDial(t *testing.T) {
	inv := dialInventory()
	inv.Nodes[0].Instances[0].Dials = map[string]string{"rss": "far/rss:web"}
	FillServiceDials(inv, map[string]confgen.Manifest{"digest": {Dials: map[string]confgen.DialDecl{"rss": rssDial}}})
	d := inv.Nodes[0].Instances[0]
	if d.Dials["rss"] != "far/rss:web" || d.DialsDerived["rss"] {
		t.Fatalf("dials = %v derived %v, want the written one kept", d.Dials, d.DialsDerived)
	}
}

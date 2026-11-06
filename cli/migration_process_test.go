package cli

import (
	"slices"
	"testing"

	"github.com/d0u9/rhumb/inventory"
)

func TestMigrationNodeRenameRequalifiesProcessMembers(t *testing.T) {
	members := []string{"node-1/app-a", "node-1/app-b"}
	inv := &inventory.Root{Nodes: []inventory.Node{{ID: "node-2", Instances: []inventory.Instance{
		{ID: "node-1/app-a", Name: "app-a", Process: "program-a"},
		{ID: "node-1/app-b", Name: "app-b", Process: "program-a"},
		{ID: "node-1/program-a", Name: "program-a", Members: members},
	}}}}
	if err := renameMigrationNode(inv, 0, "node-1", "node-2"); err != nil {
		t.Fatal(err)
	}
	process := inv.Nodes[0].Instances[2]
	if !slices.Equal(process.Members, []string{"node-2/app-a", "node-2/app-b"}) {
		t.Fatalf("renamed process references old members: %v", process.Members)
	}
	if !slices.Equal(members, []string{"node-1/app-a", "node-1/app-b"}) {
		t.Fatalf("rename mutated the source snapshot: %v", members)
	}
	if inv.Routes != nil {
		t.Fatal("rename changed an absent routes file into an empty route table")
	}
}

func TestMigrationInstanceRenameUpdatesProcessMembers(t *testing.T) {
	for _, targetNode := range []string{"node-1", "node-2"} {
		t.Run(targetNode, func(t *testing.T) {
			members := []string{"node-1/app-a", "node-1/app-b"}
			hops := []string{"node-1/app-a:main"}
			dials := map[string]string{"backend": "node-1/app-a:main"}
			inv := &inventory.Root{Nodes: []inventory.Node{
				{ID: targetNode, Instances: []inventory.Instance{
					{ID: "node-1/app-a", Name: "app-a", Service: "example", Process: "program-a"},
					{ID: "node-1/app-b", Name: "app-b", Service: "example", Process: "program-a"},
					{ID: "node-1/program-a", Name: "program-a", Service: "example", Members: members},
				}},
				{ID: "node-3", Instances: []inventory.Instance{{ID: "node-3/client", Dials: dials}}},
			}, Routes: map[string]inventory.Route{"entry": {Hops: hops}}}
			if err := renameMigrationNode(inv, 0, "node-1", targetNode); err != nil {
				t.Fatal(err)
			}
			if _, err := renameMigrationInstances(inv, 0, []instanceChange{{From: "app-a", To: "app-c"}}); err != nil {
				t.Fatal(err)
			}
			process := inv.Nodes[0].Instances[2]
			if !slices.Equal(process.Members, []string{targetNode + "/app-c", targetNode + "/app-b"}) {
				t.Fatalf("process references stale members: %v", process.Members)
			}
			if inv.Routes["entry"].Hops[0] != targetNode+"/app-c:main" || inv.Nodes[1].Instances[0].Dials["backend"] != targetNode+"/app-c:main" {
				t.Fatal("member rename did not update typed route and dial references")
			}
			if !slices.Equal(members, []string{"node-1/app-a", "node-1/app-b"}) || hops[0] != "node-1/app-a:main" || dials["backend"] != "node-1/app-a:main" {
				t.Fatal("member rename mutated the source snapshot")
			}
		})
	}
}

// Package topology turns an inventory and its derivation into the
// connectivity graph an embedding program draws: one container per node, one shape per instance, one edge per
// resolved connection. It reads no filesystem and knows nothing of the TUI
// or the web page that render it.
package topology

import (
	"sort"

	"github.com/d0u9/rhumb/derive"
	"github.com/d0u9/rhumb/inventory"
)

// Container is one node, holding every instance that runs on it. An
// unmanaged user's derived instances belong to no container; see Shape.
type Container struct {
	ID    string
	Owner string
	// Group is the directory this node's file sits in under nodes/: the
	// person whose devices these are, or whoever the machines belong to.
	// Empty for a file directly in nodes/.
	Group string
}

// Shape is one instance, real or derived — one node of the graph.
type Shape struct {
	Instance string
	Service  string
	// Container is the node this shape runs on, or empty for an unmanaged
	// user's derived instance, which has none.
	Container string
	// Owner is the user this shape's principal identity belongs to: a
	// managed node's owner, or an unmanaged user's own key. Empty for an
	// instance nobody owns — an authored server.
	Owner string
}

// Edge is one resolved connection between two shapes.
type Edge struct {
	Route string
	From  string
	To    string
	// ToPort is the port name on the To instance this edge lands on: a port
	// is what a grant is written against, so a picture or a report naming
	// only the instance loses which one was reached.
	ToPort  string
	Address string
	Port    int
	// Network is the network Address was chosen on, and Container the
	// container network both ends share; both are empty for loopback.
	Network   string
	Container string
	// Link names the link this edge rides, and Dialer the link's far end,
	// which dials To. A picture draws the edge along the link, so a home
	// server behind NAT is not shown being dialled from outside.
	Link   string
	Dialer string
}

// Link is one link between two shapes on two nodes: From opens it, To's
// port accepts it.
type Link struct {
	Name   string
	From   string
	To     string
	ToPort string
}

// Graph is the whole connectivity picture one inventory and its derivation
// produce.
type Graph struct {
	Containers []Container
	Shapes     []Shape
	Edges      []Edge
	Links      []Link
}

// Build computes Graph from inv and model. Broken nodes contribute no
// container and no shapes — the same way they contribute no target to
// target.List.
func Build(inv *inventory.Root, model *derive.Model) *Graph {
	g := &Graph{}

	owner := map[string]string{}
	for _, n := range inv.Nodes {
		if n.Broken != "" {
			continue
		}
		owner[n.ID] = n.Owner
		g.Containers = append(g.Containers, Container{ID: n.ID, Owner: n.Owner, Group: n.Group})

		for _, inst := range n.Instances {
			if inst.Service == "" {
				continue // an override, not a shape of its own — see target.List.
			}
			g.Shapes = append(g.Shapes, Shape{
				Instance:  inst.ID,
				Service:   inst.Service,
				Container: n.ID,
				Owner:     n.Owner,
			})
		}
	}

	for _, ci := range model.ExportInstances {
		shapeOwner := ci.User
		if shapeOwner == "" {
			shapeOwner = owner[ci.Node]
		}
		g.Shapes = append(g.Shapes, Shape{
			Instance:  ci.ID,
			Service:   ci.Export,
			Container: ci.Node,
			Owner:     shapeOwner,
		})
	}

	for _, e := range model.Edges {
		from := e.FromInstance
		if from == "" {
			from = e.From.Instance
		}
		g.Edges = append(g.Edges, Edge{
			Route:     e.Route,
			From:      from,
			To:        e.To.Instance,
			ToPort:    e.To.Port,
			Address:   e.Address,
			Port:      e.Port,
			Network:   e.Network,
			Container: e.Container,
			Link:      e.Link,
			Dialer:    e.Dialer,
		})
	}
	for _, l := range model.Links {
		g.Links = append(g.Links, Link{Name: l.Name, From: l.From, To: l.To.Instance, ToPort: l.To.Port})
	}

	sort.Slice(g.Containers, func(i, j int) bool { return g.Containers[i].ID < g.Containers[j].ID })
	sort.Slice(g.Shapes, func(i, j int) bool { return g.Shapes[i].Instance < g.Shapes[j].Instance })
	sort.Slice(g.Edges, func(i, j int) bool {
		if g.Edges[i].From != g.Edges[j].From {
			return g.Edges[i].From < g.Edges[j].From
		}
		return g.Edges[i].To < g.Edges[j].To
	})
	return g
}

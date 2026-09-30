package cli

import (
	"github.com/d0u9/rhumb/inventory"
	"github.com/d0u9/rhumb/target"
)

// instanceNode is one instance under a node or an unmanaged user.
type instanceNode struct {
	name string
	// label is what the tree draws. A file rendered for a person is named
	// for the device or credential, the route, the service and the export,
	// and the tree already says the first by where the row hangs, so the
	// row is the route and the detail says the rest. The full name is still
	// the row's ID and the detail pane's title.
	label  string
	detail string // "service / role"
	broken string
}

// nodeGroup is one top-level tree entry: a node, or an unmanaged user
// holding the instances derived for them. broken is a node file's own parse
// error, if any; a broken node has no instances to hold.
type nodeGroup struct {
	name string
	// key is what this entry is called in the inventory — a node ID, or an
	// unmanaged user's key. It is not always the name shown: an unmanaged
	// user's row is drawn as a device called inventory.DefaultCredential under
	// a group of their own, so that a person's devices sit at one level
	// whether or not this inventory has a file for them.
	key string
	// user is set when this entry is an unmanaged user rather than a node.
	// They hold instances the same way and group the same way, but nothing
	// else about them is a node's: there is no node file, no networks, and
	// no node detail to show. See
	// docs/inventory.md#managed-and-unmanaged-devices.
	user bool
	// group is the directory this node's file sits in under nodes/: whose
	// machines these are. owner is the person a device belongs to, which is
	// the group when the group names a user. Both are empty for an
	// unmanaged user's entry, which has no node file.
	group     string
	owner     string
	broken    string
	expanded  bool
	instances []*instanceNode
}

// buildTree turns target.List's result into the tree the page walks: nodes
// (and unmanaged users, who have none) at the top level, their instances
// under them.
func buildTree(targets []target.Target) []*nodeGroup {
	var nodes []*nodeGroup
	for _, g := range target.GroupByNode(targets) {
		// A holder starts closed. An index opens on whose machines these are
		// and which machines they are — one level under each group — and what
		// runs on a machine is the answer to a question about that machine,
		// asked by opening it.
		n := &nodeGroup{name: g.Node, key: g.Node}
		for _, t := range g.Targets {
			// GroupByNode keys an unmanaged user's targets by the user,
			// since they have no node; a target with no node is how the
			// group says which of the two it is.
			if t.Node == "" && t.User != "" {
				// An unmanaged user has no node file, so the level a
				// device would occupy is filled by one named `default` —
				// the same way a directory with no page of its own still
				// answers at its index.
				n.user = true
				n.key = t.User
				n.group = t.User
				n.name = inventory.DefaultCredential
			}
			if t.Instance == "" {
				// The one synthetic target a broken node file contributes —
				// see target.List.
				n.broken = t.Broken
				continue
			}
			// A deployment says the program it runs; a file written for a
			// person says the way it was written, which is what tells two
			// of them for one route apart.
			label, detail := inventory.LocalName(t.Instance), t.Service
			if t.Export != "" {
				detail = t.Service + " " + t.Export
				if len(t.Routes) == 1 {
					label = t.Routes[0]
				}
			}
			n.instances = append(n.instances, &instanceNode{
				name:   t.Instance,
				label:  label,
				detail: detail,
				broken: t.Broken,
			})
		}
		nodes = append(nodes, n)
	}
	return nodes
}

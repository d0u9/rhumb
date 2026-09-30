package cli

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/d0u9/rhumb/derive"
	"github.com/d0u9/rhumb/inventory"
)

type instanceChange struct{ From, To string }

func parseInstanceChanges(encoded string) ([]instanceChange, error) {
	if encoded == "" {
		return nil, nil
	}
	var specs []string
	if err := json.Unmarshal([]byte(encoded), &specs); err != nil {
		return nil, fmt.Errorf("--instance values: %w", err)
	}
	var changes []instanceChange
	fromSeen, toSeen := map[string]bool{}, map[string]bool{}
	for _, spec := range specs {
		fields := map[string]string{}
		for _, part := range strings.Split(spec, ",") {
			key, value, ok := strings.Cut(part, "=")
			if !ok || value == "" || (key != "from" && key != "to") || fields[key] != "" {
				return nil, fmt.Errorf("--instance %q: want from=<old>,to=<new>", spec)
			}
			fields[key] = value
		}
		if fields["from"] == "" || fields["to"] == "" || strings.ContainsAny(fields["to"], "/:\\") {
			return nil, fmt.Errorf("--instance %q: want from=<old>,to=<new> with a plain instance ID", spec)
		}
		if fromSeen[fields["from"]] || toSeen[fields["to"]] {
			return nil, fmt.Errorf("--instance %q: instance given more than once", spec)
		}
		fromSeen[fields["from"]], toSeen[fields["to"]] = true, true
		if fields["from"] != fields["to"] {
			changes = append(changes, instanceChange{fields["from"], fields["to"]})
		}
	}
	return changes, nil
}

// renameMigrationInstances changes authored IDs and typed route-hop and dial references
// in the preview snapshot. Opaque values, paths and secrets remain manual work.
func renameMigrationInstances(inv *inventory.Root, nodeIndex int, changes []instanceChange) (map[string][]string, error) {
	refs := map[string][]string{}
	if len(changes) == 0 {
		return refs, nil
	}
	node := &inv.Nodes[nodeIndex]
	all := map[string]bool{}
	for _, inst := range node.Instances {
		all[inventory.LocalName(inst.ID)] = true
	}
	changesByID := map[string]string{}
	for _, change := range changes {
		found := false
		for _, inst := range node.Instances {
			if inventory.LocalName(inst.ID) == change.From && inst.Service != "" {
				found = true
				refs[change.From] = append(refs[change.From], inst.Path+": id")
				if strings.Contains(filepath.Base(inst.Path), change.From) {
					refs[change.From] = append(refs[change.From], "manual review: filename "+inst.Path+" retains old instance ID")
				}
			}
		}
		if !found {
			return nil, fmt.Errorf("node %q has no authored instance %q", node.ID, change.From)
		}
		if all[change.To] {
			return nil, fmt.Errorf("instance %q already exists", change.To)
		}
		changesByID[node.ID+inventory.QualifiedSep+change.From] = node.ID + inventory.QualifiedSep + change.To
	}
	node.Instances = append([]inventory.Instance(nil), node.Instances...)
	for i := range node.Instances {
		if to := changesByID[node.Instances[i].ID]; to != "" {
			node.Instances[i].ID = to
			node.Instances[i].Name = inventory.LocalName(to)
		}
	}
	// A dial names its target the way a hop does, and may live on any node.
	inv.Nodes = append([]inventory.Node(nil), inv.Nodes...)
	for n := range inv.Nodes {
		other := &inv.Nodes[n]
		if n != nodeIndex {
			other.Instances = append([]inventory.Instance(nil), other.Instances...)
		}
		for i := range other.Instances {
			inst := &other.Instances[i]
			var dials map[string]string
			for name, raw := range inst.Dials {
				hop, err := derive.ParseHop(raw)
				if err != nil {
					return nil, err
				}
				to := changesByID[hop.Instance]
				if to == "" {
					continue
				}
				if dials == nil {
					dials = make(map[string]string, len(inst.Dials))
					for k, v := range inst.Dials {
						dials[k] = v
					}
				}
				dials[name] = to + ":" + hop.Port
				refs[hop.Instance] = append(refs[hop.Instance], inst.Path+": "+inventory.LocalName(inst.ID)+".dials."+name)
			}
			if dials != nil {
				inst.Dials = dials
			}
		}
	}
	inv.Routes = cloneRoutes(inv.Routes)
	for name, route := range inv.Routes {
		for i, raw := range route.Hops {
			hop, err := derive.ParseHop(raw)
			if err != nil {
				return nil, err
			}
			if to := changesByID[hop.Instance]; to != "" {
				route.Hops[i] = to + ":" + hop.Port
				refs[hop.Instance] = append(refs[hop.Instance], "routes.yaml: "+name+".hops")
			}
		}
		inv.Routes[name] = route
	}
	// refs are reported by the name the operator gave.
	named := make(map[string][]string, len(refs))
	for from, list := range refs {
		local := inventory.LocalName(from)
		named[local] = append(named[local], list...)
	}
	for from := range named {
		sort.Strings(named[from])
	}
	return named, nil
}

// renameMigrationNode moves every instance of a renamed node to the new
// node's name, and every hop and dial that reached it with them.
func renameMigrationNode(inv *inventory.Root, nodeIndex int, oldID, newID string) error {
	if oldID == newID {
		return nil
	}
	requalify := func(raw string) (string, error) {
		hop, err := derive.ParseHop(raw)
		if err != nil {
			return "", err
		}
		node, name, _ := strings.Cut(hop.Instance, inventory.QualifiedSep)
		if node != oldID {
			return raw, nil
		}
		return newID + inventory.QualifiedSep + name + ":" + hop.Port, nil
	}
	node := &inv.Nodes[nodeIndex]
	node.Instances = append([]inventory.Instance(nil), node.Instances...)
	for i := range node.Instances {
		node.Instances[i].ID = newID + inventory.QualifiedSep + inventory.LocalName(node.Instances[i].ID)
	}
	inv.Nodes = append([]inventory.Node(nil), inv.Nodes...)
	for n := range inv.Nodes {
		other := &inv.Nodes[n]
		if n != nodeIndex {
			other.Instances = append([]inventory.Instance(nil), other.Instances...)
		}
		for i := range other.Instances {
			inst := &other.Instances[i]
			if len(inst.Dials) == 0 {
				continue
			}
			dials := make(map[string]string, len(inst.Dials))
			for name, raw := range inst.Dials {
				next, err := requalify(raw)
				if err != nil {
					return err
				}
				dials[name] = next
			}
			inst.Dials = dials
		}
	}
	inv.Routes = cloneRoutes(inv.Routes)
	for name, route := range inv.Routes {
		for i, raw := range route.Hops {
			next, err := requalify(raw)
			if err != nil {
				return err
			}
			route.Hops[i] = next
		}
		inv.Routes[name] = route
	}
	// Routes scoped to the node move with it.
	var scoped []routeChange
	for name, route := range inv.Routes {
		if route.Scope == oldID {
			_, local := inventory.RouteScope(name)
			scoped = append(scoped, routeChange{From: name, To: newID + inventory.QualifiedSep + local})
		}
	}
	_, err := renameMigrationRoutes(inv, scoped)
	return err
}

func cloneRoutes(routes map[string]inventory.Route) map[string]inventory.Route {
	copyOf := make(map[string]inventory.Route, len(routes))
	for name, route := range routes {
		copyOf[name] = inventory.Route{Hops: append([]string(nil), route.Hops...), Scope: route.Scope}
	}
	return copyOf
}

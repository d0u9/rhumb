package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/d0u9/rhumb/engine"

	"github.com/d0u9/rhumb/derive"
	"github.com/d0u9/rhumb/inventory"
)

// migrationDNSReview describes inventory evidence only. DNS zones and their
// current answers are external to conf, so every record action remains a check.
func migrationDNSReview(rep *migrationReport, before, after engine.Loaded, oldID, newID string, nodeChanged bool, instances []instanceChange) {
	// Facts are keyed by the instance's ID before the migration, so the
	// same service compares with itself across a node or instance rename.
	renamed := map[string]string{}
	for _, change := range instances {
		renamed[change.To] = change.From
	}
	reverse := map[string]string{}
	for _, node := range after.Inv.Nodes {
		if node.ID != newID {
			continue
		}
		for _, inst := range node.Instances {
			local := inventory.LocalName(inst.ID)
			if from := renamed[local]; from != "" {
				local = from
			}
			reverse[inst.ID] = oldID + inventory.QualifiedSep + local
		}
	}
	oldFacts := migrationPublishedFacts(before.Inv, nil)
	newFacts := migrationPublishedFacts(after.Inv, reverse)
	keys := map[string]bool{}
	for key := range oldFacts {
		keys[key] = true
	}
	for key := range newFacts {
		keys[key] = true
	}
	for _, key := range sortedKeys(keys) {
		old, hadOld := oldFacts[key]
		new, hasNew := newFacts[key]
		if !migrationDNSAffected(old, new, hadOld, hasNew, oldID, newID, nodeChanged) {
			continue
		}
		item := migrationDNSItem{Name: new.name, OldName: old.name, Service: new.service, HostBefore: old.host, HostAfter: new.host}
		if item.Name == "" {
			item.Name = old.name
		}
		if item.Service == "" {
			item.Service = old.service
		}
		instance, port, _ := strings.Cut(key, ":")
		item.Service += "/" + inventory.LocalName(instance)
		item.Port = port
		if len(old.ingress) == 0 && len(new.ingress) == 0 {
			item.NoIngress = true
		} else {
			item.IngressBefore, item.IngressAfter = migrationIngressList(old.ingress), migrationIngressList(new.ingress)
		}
		for _, nodeID := range migrationDNSNodes(old, new, oldID, newID) {
			oldNode := migrationNode(before.Inv, nodeID, oldID, newID)
			newNode := migrationNode(after.Inv, nodeID, newID, oldID)
			label := nodeID
			if nodeID == oldID && oldID != newID {
				label = oldID + " → " + newID
			}
			role := "Ingress"
			if !migrationIngressOnNode(old.ingress, nodeID) && !migrationIngressOnNode(new.ingress, nodeID) &&
				!(nodeID == oldID && migrationIngressOnNode(new.ingress, newID)) {
				role = "Backend candidate"
			}
			item.Addresses = append(item.Addresses, migrationDNSAddress{Role: role, Node: label, Before: migrationAddresses(oldNode), After: migrationAddresses(newNode)})
		}
		rep.DNS = append(rep.DNS, item)
	}
}

type migrationPublishedFact struct {
	name, host, service string
	ingress             []migrationIngress
}

type migrationIngress struct{ route, node string }

func migrationPublishedFacts(inv *inventory.Root, reverse map[string]string) map[string]migrationPublishedFact {
	facts := map[string]migrationPublishedFact{}
	owner := map[string]string{}
	for _, node := range inv.Nodes {
		for _, inst := range node.Instances {
			owner[inst.ID] = node.ID
		}
	}
	for _, node := range inv.Nodes {
		for _, inst := range node.Instances {
			for portName, port := range inst.Ports {
				if port.Published == "" {
					continue
				}
				identity := inst.ID
				if old := reverse[identity]; old != "" {
					identity = old
				}
				fact := migrationPublishedFact{name: port.Published, host: node.ID, service: inst.Service}
				for routeName, route := range inv.Routes {
					if len(route.Hops) == 0 {
						continue
					}
					entry, err := derive.ParseHop(route.Hops[0])
					if err != nil {
						continue // inventory validation reports malformed hops
					}
					for _, raw := range route.Hops {
						hop, err := derive.ParseHop(raw)
						if err == nil && hop.Instance == inst.ID && hop.Port == portName {
							fact.ingress = append(fact.ingress, migrationIngress{route: routeName, node: owner[entry.Instance]})
							break
						}
					}
				}
				sort.Slice(fact.ingress, func(i, j int) bool { return fact.ingress[i].route < fact.ingress[j].route })
				facts[identity+":"+portName] = fact
			}
		}
	}
	return facts
}

func migrationDNSAffected(old, new migrationPublishedFact, hadOld, hasNew bool, oldID, newID string, nodeChanged bool) bool {
	if hadOld != hasNew || old.name != new.name {
		return true
	}
	if !nodeChanged {
		return false
	}
	if old.host == oldID || new.host == newID {
		return true
	}
	for _, ingress := range old.ingress {
		if ingress.node == oldID {
			return true
		}
	}
	for _, ingress := range new.ingress {
		if ingress.node == newID {
			return true
		}
	}
	return false
}

func migrationPresent(value string) string {
	if value == "" {
		return "(absent)"
	}
	return value
}

func migrationIngressList(ingress []migrationIngress) string {
	if len(ingress) == 0 {
		return "none known"
	}
	parts := make([]string, 0, len(ingress))
	for _, item := range ingress {
		parts = append(parts, fmt.Sprintf("route `%s` via `%s`", item.route, migrationPresent(item.node)))
	}
	return strings.Join(parts, "; ")
}

func migrationIngressOnNode(ingress []migrationIngress, node string) bool {
	for _, item := range ingress {
		if item.node == node {
			return true
		}
	}
	return false
}

func migrationDNSNodes(old, new migrationPublishedFact, oldID, newID string) []string {
	seen := map[string]bool{}
	for _, item := range append(append([]migrationIngress(nil), old.ingress...), new.ingress...) {
		if item.node != "" {
			seen[migrationCanonicalNode(item.node, oldID, newID)] = true
		}
	}
	if old.host != "" {
		seen[migrationCanonicalNode(old.host, oldID, newID)] = true
	}
	if new.host != "" {
		seen[migrationCanonicalNode(new.host, oldID, newID)] = true
	}
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func migrationCanonicalNode(id, oldID, newID string) string {
	if id == newID {
		return oldID
	}
	return id
}

func migrationNode(inv *inventory.Root, id, movedID, otherID string) *inventory.Node {
	lookup := id
	if id == otherID && movedID != otherID {
		lookup = movedID
	}
	for i := range inv.Nodes {
		if inv.Nodes[i].ID == lookup {
			return &inv.Nodes[i]
		}
	}
	return nil
}

func migrationAddresses(node *inventory.Node) string {
	if node == nil || len(node.Networks) == 0 {
		return "(none in inventory)"
	}
	names := make([]string, 0, len(node.Networks))
	for name := range node.Networks {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, "`"+name+" "+node.Networks[name]+"`")
	}
	return strings.Join(parts, ", ")
}

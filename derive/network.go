package derive

import (
	"fmt"
	"net/netip"
	"sort"
	"strings"

	"github.com/d0u9/rhumb/inventory"
)

// ResolveAddress is the address rule an edge follows, for a caller that is
// not an edge: a dial. See
// docs/apps/conf/inventory.md#dialling-a-service-that-is-not-on-a-route.
func ResolveAddress(inv *inventory.Root, from inventory.Instance, fromNode inventory.Node, to inventory.Instance, toNode inventory.Node) (address, network string, err error) {
	address, network, _, err = resolveEndpoint(from, fromNode, to, toNode, inv.Networks, inv.Universal)
	return address, network, err
}

// resolveEndpoint is the address one instance dials another at: the other
// end's address in the innermost scope the two share. Two instances sharing a
// container network dial by the instance's name there, and container is the
// first they share in the node's order. Otherwise one node is loopback, and two nodes are resolveAddress.
//
// A container on a container network reaching anything on its own node off
// that network is the one pairing with no answer: loopback inside it is the
// container itself, and reaching the host takes a gateway this model does
// not carry. A container on no container network shares the host's, and
// dials loopback like a host process.
func resolveEndpoint(from inventory.Instance, fromNode inventory.Node, to inventory.Instance, toNode inventory.Node, networkPref []string, universal string) (address, network, container string, err error) {
	if fromNode.ID != toNode.ID {
		address, network, err = resolveAddress(fromNode, toNode, networkPref, universal)
		return address, network, "", err
	}
	if shared := toNode.SharedContainer(from, to); shared != "" {
		return inventory.LocalName(to.ID), "", shared, nil
	}
	if len(from.Containers) > 0 {
		return "", "", "", fmt.Errorf("%s is on container networks %s and %s shares none of them, so loopback inside it is the container itself: put both on one of %s's containers",
			from.ID, strings.Join(from.ContainerNames(), ", "), to.ID, fromNode.ID)
	}
	return "127.0.0.1", "", "", nil
}

// Name is one entry of a network's name table: a name and the address that
// answers it there. Source says where it was written, for an error.
type Name struct {
	Name    string
	Address string
	Source  string
}

// Names is every network's name table, in name order, and the conflicts
// rule 30 reports: one name reaching two addresses on one network. See
// docs/apps/conf/inventory.md#names-on-a-network.
//
// fansOut says whether an instance is a proxy dispatching by name; only a
// route entered through one moves a name to the proxy's node, since a relay
// forwards bytes and never answers to a name. An address written as a
// hostname is left out: a resolver's record wants an IP address.
func Names(inv *inventory.Root, fansOut func(instance string) bool) (map[string][]Name, []string) {
	nodeOf := map[string]inventory.Node{}
	for _, n := range inv.Nodes {
		if n.Broken != "" {
			continue
		}
		for _, inst := range n.Instances {
			if inst.Service != "" {
				nodeOf[inst.ID] = n
			}
		}
	}
	front := fronts(inv, fansOut)
	tables := newNameTables("network")
	add := tables.add

	for _, n := range inv.Nodes {
		if n.Broken != "" {
			continue
		}
		for _, inst := range n.Instances {
			if inst.Service == "" {
				continue
			}
			for port, p := range inst.Ports {
				at := n
				if proxy, ok := front[inst.ID+":"+port]; ok {
					if pn, ok := nodeOf[proxy]; ok {
						at = pn
					}
				}
				for _, name := range p.Names {
					for network, addr := range at.Networks {
						add(network, Name{Name: name, Address: addr, Source: inst.ID + ":" + port})
					}
				}
			}
		}
	}
	for id, h := range inv.Hosts {
		for _, name := range h.Names {
			add(h.Network, Name{Name: name, Address: h.Address, Source: "host " + id})
		}
	}
	return tables.result()
}

// ContainerNames is the name table of each container network on one node,
// and the conflicts rule 30 reports there. A name enters a container
// network's table when the instance answering to it — the proxy in front of
// the port, or the port's own instance when nothing fronts it — joins that
// network at a fixed address. An address the runtime assigns is no record a
// resolver can hold. See docs/apps/conf/inventory.md#names-on-a-network.
//
// A container network is a scope inside its node, so the tables are the
// node's own: two nodes may each list a network of one name.
func ContainerNames(inv *inventory.Root, nodeID string, fansOut func(instance string) bool) (map[string][]Name, []string) {
	var node inventory.Node
	byID := map[string]inventory.Instance{}
	for _, n := range inv.Nodes {
		if n.ID != nodeID || n.Broken != "" {
			continue
		}
		node = n
		for _, inst := range n.Instances {
			if inst.Service != "" {
				byID[inst.ID] = inst
			}
		}
	}
	front := fronts(inv, fansOut)
	tables := newNameTables("container network")
	for _, inst := range node.Instances {
		if inst.Service == "" {
			continue
		}
		for port, p := range inst.Ports {
			answering := inst
			if proxy, ok := front[inst.ID+":"+port]; ok {
				pi, onNode := byID[proxy]
				if !onNode {
					continue
				}
				answering = pi
			}
			for _, c := range node.JoinedContainers(answering) {
				address := answering.Containers[c.Name]
				if address == "" {
					continue
				}
				for _, name := range p.Names {
					tables.add(c.Name, Name{Name: name, Address: address, Source: inst.ID + ":" + port})
				}
			}
		}
	}
	return tables.result()
}

// fronts is the proxy each proxied port answers through: a port reached
// through a route answers where the route's first hop is, when that hop
// dispatches by name. The first route in name order wins.
func fronts(inv *inventory.Root, fansOut func(instance string) bool) map[string]string {
	front := map[string]string{}
	routeNames := make([]string, 0, len(inv.Routes))
	for name := range inv.Routes {
		routeNames = append(routeNames, name)
	}
	sort.Strings(routeNames)
	for _, name := range routeNames {
		hops := inv.Routes[name].Hops
		for i := 1; i < len(hops); i++ {
			first, err := ParseHop(hops[0])
			if err != nil || !fansOut(first.Instance) {
				continue
			}
			if _, seen := front[hops[i]]; !seen {
				front[hops[i]] = first.Instance
			}
		}
	}
	return front
}

// nameTables collects name tables keyed by network, keeping the first
// address a name reaches and recording any second one as a conflict. kind
// is how a conflict names the table's key.
type nameTables struct {
	kind      string
	byKey     map[string]map[string]Name
	conflicts []string
}

func newNameTables(kind string) *nameTables {
	return &nameTables{kind: kind, byKey: map[string]map[string]Name{}}
}

func (t *nameTables) add(key string, n Name) {
	if _, err := netip.ParseAddr(n.Address); err != nil {
		return
	}
	table := t.byKey[key]
	if table == nil {
		table = map[string]Name{}
		t.byKey[key] = table
	}
	if prev, ok := table[n.Name]; ok {
		if prev.Address != n.Address {
			t.conflicts = append(t.conflicts, fmt.Sprintf("on %s %q, name %q resolves to %s (%s) and %s (%s)",
				t.kind, key, n.Name, prev.Address, prev.Source, n.Address, n.Source))
		}
		return
	}
	table[n.Name] = n
}

func (t *nameTables) result() (map[string][]Name, []string) {
	out := map[string][]Name{}
	for key, table := range t.byKey {
		list := make([]Name, 0, len(table))
		for _, n := range table {
			list = append(list, n)
		}
		sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
		out[key] = list
	}
	sort.Strings(t.conflicts)
	return out, t.conflicts
}

// Reservation is one line of what a network's router is given: a member
// with a hardware address, and the address it is to receive.
type Reservation struct {
	ID      string
	Address string
	MAC     string
}

// Reservations is every node and host on network with a mac, in address
// order. See docs/apps/conf/inventory.md#what-the-router-is-given.
func Reservations(inv *inventory.Root, network string) []Reservation {
	var out []Reservation
	for _, n := range inv.Nodes {
		if mac := n.MACs[network]; n.Broken == "" && mac != "" {
			out = append(out, Reservation{ID: n.ID, Address: n.Networks[network], MAC: mac})
		}
	}
	for id, h := range inv.Hosts {
		if h.Network == network && h.MAC != "" {
			out = append(out, Reservation{ID: id, Address: h.Address, MAC: h.MAC})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, errA := netip.ParseAddr(out[i].Address)
		b, errB := netip.ParseAddr(out[j].Address)
		if errA == nil && errB == nil && a != b {
			return a.Less(b)
		}
		return out[i].Address+out[i].ID < out[j].Address+out[j].ID
	})
	return out
}

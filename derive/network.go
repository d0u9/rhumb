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
// docs/inventory.md#dialling-a-service-that-is-not-on-a-route.
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

// Name is a backend-neutral DNS record. Source identifies its authored origin.
type Name struct {
	Name   string `json:"name"`
	Type   string `json:"type"`
	Value  string `json:"value"`
	Source string `json:"source"`
	// External permits a domain endpoint to terminate outside the authored table.
	External bool `json:"-"`
}

// Names is every network's name table, in name order, and the conflicts
// rule 30 reports: duplicate names and invalid CNAME graphs on a network. See
// docs/inventory.md#names-on-a-network.
//
// fansOut says whether an instance is a proxy dispatching by name; only a
// route entered through one moves a name to the proxy's node, since a relay
// forwards bytes and never answers to a name. A domain endpoint produces a
// CNAME to that explicit external hostname.
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
						// The canonical domain endpoint is already maintained externally.
						if inventory.ValidDNSName(addr) && canonicalName(name) == canonicalName(addr) {
							continue
						}
						record := Name{Name: name, Source: inst.ID + ":" + port}
						h := inv.Hosts[at.Host]
						endpoint := h.Endpoints[network]
						var matches []string
						for device, refs := range h.Names {
							for _, ref := range refs {
								if ref.Endpoint == network || (ref.Endpoint == "" && endpoint.From != nil && ref.Interface == endpoint.From.Interface && ref.Address == endpoint.From.Address) {
									matches = append(matches, canonicalName(device))
									break
								}
							}
						}
						sort.Strings(matches)
						if len(matches) > 1 {
							tables.conflicts = append(tables.conflicts, fmt.Sprintf("%s: endpoint on network %q matches multiple device names: %s", record.Source, network, strings.Join(matches, ", ")))
							continue
						}
						if len(matches) == 1 {
							record.Type, record.Value = "CNAME", matches[0]
						} else {
							var ok bool
							record, ok = addressName(name, addr, record.Source)
							if !ok {
								if !inventory.ValidDNSName(addr) {
									continue
								}
								record = Name{Name: name, Type: "CNAME", Value: addr, Source: inst.ID + ":" + port, External: true}
							}
						}
						tables.addPublished(network, record)
					}
				}
			}
		}
	}
	for _, member := range inv.NetworkMembers() {
		for _, name := range member.Names {
			if record, ok := addressName(name, member.Address, strings.TrimSuffix(member.Source, ".ip")+".names"); ok {
				add(member.Network, record)
			}
		}
	}
	tables.addDeviceNames(inv)
	tables.addAliases(inv.Names)
	return tables.result()
}

// ContainerNames is the name table of each container network on one node,
// and the conflicts rule 30 reports there. A name enters a container
// network's table when the instance answering to it — the proxy in front of
// the port, or the port's own instance when nothing fronts it — joins that
// network at a fixed address. An address the runtime assigns is no record a
// resolver can hold. See docs/inventory.md#names-on-a-network.
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
					if record, ok := addressName(name, address, inst.ID+":"+port); ok {
						tables.addPublished(c.Name, record)
					}
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
// record and reporting every duplicate name as a conflict. kind
// is how a conflict names the table's key.
type nameTables struct {
	kind          string
	byKey         map[string]map[string]Name
	conflicts     []string
	publishedSeen map[[3]string]bool
}

func newNameTables(kind string) *nameTables {
	return &nameTables{kind: kind, byKey: map[string]map[string]Name{}}
}

// Several published ports may describe the same DNS record, including process members.
func (t *nameTables) addPublished(key string, n Name) {
	if t.publishedSeen == nil {
		t.publishedSeen = map[[3]string]bool{}
	}
	declaration := [3]string{key, canonicalName(n.Name), n.Source}
	if t.publishedSeen[declaration] {
		t.conflicts = append(t.conflicts, fmt.Sprintf("on %s %q, duplicate name %q at %s", t.kind, key, n.Name, n.Source))
		return
	}
	t.publishedSeen[declaration] = true
	prev, exists := t.byKey[key][canonicalName(n.Name)]
	value := n.Value
	if n.Type == "CNAME" {
		value = canonicalName(value)
	}
	if exists && prev.Type == n.Type && prev.Value == value {
		if n.Source < prev.Source {
			prev.Source = n.Source
			t.byKey[key][prev.Name] = prev
		}
		return
	}
	t.add(key, n)
}

func (t *nameTables) add(key string, n Name) {
	n.Name = canonicalName(n.Name)
	if !inventory.ValidDNSName(n.Name) {
		t.conflicts = append(t.conflicts, fmt.Sprintf("%s: invalid DNS name %q", n.Source, n.Name))
		return
	}
	if n.Type == "CNAME" {
		n.Value = canonicalName(n.Value)
	}
	table := t.byKey[key]
	if table == nil {
		table = map[string]Name{}
		t.byKey[key] = table
	}
	if prev, ok := table[n.Name]; ok {
		t.conflicts = append(t.conflicts, fmt.Sprintf("on %s %q, duplicate name %q resolves to %s (%s) and %s (%s)",
			t.kind, key, n.Name, prev.Type+" "+prev.Value, prev.Source, n.Type+" "+n.Value, n.Source))
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
		_, issues := resolveNames(list, false)
		for _, issue := range issues {
			t.conflicts = append(t.conflicts, fmt.Sprintf("on %s %q: %s", t.kind, key, issue))
		}
		out[key] = list
	}
	sort.Strings(t.conflicts)
	return out, t.conflicts
}

// Reservation is one line of what a network's router is given: a member
// with a hardware address, and the address it is to receive.
type Reservation struct {
	Host, Interface, AddressID string
	ID                         string
	Address                    string
	MAC                        string
}

// Reservations collects explicit interface reservations,
// in address order. See docs/inventory.md#what-the-router-is-given.
func Reservations(inv *inventory.Root, network string) []Reservation {
	var out []Reservation
	for _, m := range inv.NetworkMembers() {
		if m.Network != network || !m.Reservation {
			continue
		}
		out = append(out, Reservation{ID: m.Host + "/" + m.Interface + "/" + m.AddressID, Address: m.Address, MAC: m.MAC, Host: m.Host, Interface: m.Interface, AddressID: m.AddressID})
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

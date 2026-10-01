package derive

import (
	"net"

	"github.com/d0u9/rhumb/inventory"
)

// PublishEverywhere is the address a mapping binds when there is no single
// address to bind: the node is written at a name rather than an address, or
// it is written at nothing at all. A container runtime binds addresses, and
// a name is not one.
const PublishEverywhere = "0.0.0.0"

// PublishLoopback is the address a port entered only from its own node is
// published at. A reverse proxy's backend is that case, and it is the one
// the derivation exists for: published on every interface because someone
// typed it is what a hand-written mapping gets wrong.
const PublishLoopback = "127.0.0.1"

// Mapping is where an instance's port is reached on the machine the process
// runs on: the addresses a container runtime publishes it at, and the
// number, which is the port's own. There is no second number to choose
// from — the program's own configuration is rendered from the same field,
// so a mapping changing it would point at a listener that does not exist.
// See docs/export.md#a-second-file-what-deploys-it.
type Mapping struct {
	// Addresses is every address this port is published at, in the
	// inventory's network preference order, with loopback first when it is
	// among them. It is a list because one port may be reached over more
	// than one network: a machine with a port on each of two segments
	// serves both, and a port reached by a proxy on its own node and by
	// another machine needs loopback as well as the address that machine
	// dials. It is empty for a port entered only from its own container
	// network: nothing on the host dials it.
	Addresses []string
	Number    int
}

// Mappings is the host mapping of each of one instance's ports, by port
// name, or nil when this inventory holds no such instance. It is derived
// from `ports` and from the edges already resolved, and never written: a
// port written twice is the second truth the deployment file exists to
// remove.
//
// The cases are the ones docs/export.md pins. An edge between two
// instances on one container network asks for nothing: it never reaches the
// host. A port only hops from its own node enter publishes on
// PublishLoopback. A port entered
// from another node publishes on this node's own address, on the network
// that edge resolved. A port no edge enters — one reached from a browser —
// publishes the same way as the second: it is reached from outside, and
// nothing in the inventory says from where. An address that is a name, and
// a node written at no address at all, publish on PublishEverywhere.
//
// It answers for every instance, containerised or not. What runs the
// process decides whether a deployment file is rendered; where its ports
// are reached is the same question either way.
func (m *Model) Mappings(inv *inventory.Root, instance string) map[string]Mapping {
	var node inventory.Node
	var inst inventory.Instance
	found := false
	for _, n := range inv.Nodes {
		if n.Broken != "" {
			continue
		}
		for _, i := range n.Instances {
			if i.ID == instance {
				node, inst, found = n, i, true
			}
		}
	}
	if !found || len(inst.Ports) == 0 {
		return nil
	}

	// Which node each edge leaves from: the hop's own instance for an edge
	// between two hops, and the device for one out of a file written for a
	// person. A file a person carries has no node at all, and is another
	// machine by definition.
	nodeOf := map[string]string{}
	for _, n := range inv.Nodes {
		if n.Broken != "" {
			continue
		}
		for _, i := range n.Instances {
			nodeOf[i.ID] = n.ID
		}
	}
	for _, ci := range m.ExportInstances {
		nodeOf[ci.ID] = ci.Node
	}

	out := make(map[string]Mapping, len(inst.Ports))
	for name, port := range inst.Ports {
		// Which networks this port is entered over, and whether anything
		// on its own node enters it. The two are independent: a port a
		// local proxy dials and another machine dials is reached at
		// loopback and at this node's address, and publishing only one of
		// them leaves the other end dialling a number nothing published.
		local, entered := false, false
		networks := map[string]bool{}
		for _, e := range m.Edges {
			if e.To.Instance != instance || e.To.Port != name {
				continue
			}
			entered = true
			// An edge inside one container network never reaches the
			// host, so it asks nothing of the mapping.
			if e.Container != "" {
				continue
			}
			// What matters is who dials: for an edge riding a link that is
			// the link's far end, on this node, not the route's From.
			if nodeOf[e.DialerOr()] == node.ID {
				local = true
				continue
			}
			networks[e.Network] = true
		}
		// A link's to port is entered from the other node, over the network
		// the link resolved on, as a route's entrance is.
		for _, l := range m.Links {
			if l.To.Instance == instance && l.To.Port == name {
				entered = true
				networks[l.Network] = true
			}
		}

		var addresses []string
		if local {
			addresses = append(addresses, PublishLoopback)
		}
		for _, network := range networkOrder(inv) {
			if networks[network] {
				addresses = append(addresses, bindable(node.Networks[network]))
			}
		}
		// A route starting at this port is entered from outside, whatever
		// else enters it: a web interface behind a proxy beside it, kept
		// reachable directly as well, is dialled at loopback by the proxy
		// and at this node's address by a browser the route leaves
		// unmodelled.
		scopes, entrance := entranceScopes(inv, instance, name)
		if !entered || entrance {
			// No edge chose a network for the way in from outside. A route
			// written under a scope says where its clients are: a network
			// publishes on this node's address there, the node itself on
			// loopback, and a container network this instance joins on
			// nothing: its clients are on that bridge, behind a router
			// there, and never reach the host. Otherwise every network this
			// node answers on.
			outside := 0
			if scopes[node.ID] {
				addresses = append(addresses, PublishLoopback)
				outside++
			}
			for scope := range scopes {
				if _, joined := inst.Containers[scope]; joined && scope != "" {
					outside++
				}
			}
			for _, network := range networkOrder(inv) {
				if len(scopes) > 0 && !scopes[""] && !scopes[network] {
					continue
				}
				if address, ok := node.Networks[network]; ok {
					addresses = append(addresses, bindable(address))
					outside++
				}
			}
			if outside == 0 {
				// A node written at no address at all, reached from
				// outside: there is no interface to name, and publishing
				// nothing would render a container nobody can reach.
				addresses = append(addresses, PublishEverywhere)
			}
		}
		out[name] = Mapping{Addresses: collapse(addresses), Number: port.Number}
	}
	return out
}

// collapse is the addresses a port actually binds: PublishEverywhere alone
// when it is among them, since it already covers every interface and a
// second bind on one of them would fail; the list deduplicated and in the
// order given otherwise; and nothing when nothing asked for an address,
// which is a port entered only from its own container network.
func collapse(addresses []string) []string {
	out := make([]string, 0, len(addresses))
	seen := map[string]bool{}
	for _, address := range addresses {
		if address == PublishEverywhere {
			return []string{PublishEverywhere}
		}
		if seen[address] {
			continue
		}
		seen[address] = true
		out = append(out, address)
	}
	return out
}

// bindable is an address a container runtime can bind, or PublishEverywhere
// when what the node is written at is a name — or nothing.
func bindable(address string) string {
	if net.ParseIP(address) == nil {
		return PublishEverywhere
	}
	return address
}

// networkOrder is networks.yaml's order with the universal network after
// it, which is how resolveAddress reads it too.
func networkOrder(inv *inventory.Root) []string {
	order := inv.Networks
	if inv.Universal != "" && !containsString(order, inv.Universal) {
		order = append(append([]string{}, order...), inv.Universal)
	}
	return order
}

// entranceScopes is the scopes of every route whose first hop is this
// instance's port — a route entered from outside at it — and whether there is
// one. The empty scope is a top-level route, reachable wherever the node is.
func entranceScopes(inv *inventory.Root, instance, port string) (map[string]bool, bool) {
	want := instance + ":" + port
	scopes := map[string]bool{}
	for _, r := range inv.Routes {
		if len(r.Hops) > 0 && r.Hops[0] == want {
			scopes[r.Scope] = true
		}
	}
	return scopes, len(scopes) > 0
}

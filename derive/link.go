package derive

import (
	"fmt"
	"sort"
	"strings"

	"github.com/d0u9/rhumb/confgen"
	"github.com/d0u9/rhumb/inventory"
)

// linkEnds indexes the resolved links by the instances ending them, for
// choosing the link an edge rides.
type linkEnds struct {
	// byInstance is, for each instance ending a link, the link names and the
	// instance at the other end.
	byInstance map[string][]linkEnd
}

type linkEnd struct {
	link  string
	other string
}

// deriveLinks resolves every link in name order into m, with one grant each:
// the from instance, as itself, on the to port. A link's principal is the
// instance whatever its `principal` says, since that chooses whose
// credential a program carries to its route upstream, and a link is the
// program's own session. See docs/links.md#a-link.
func deriveLinks(inv *inventory.Root, instances map[string]instanceRef, m *Model) (linkEnds, error) {
	ends := linkEnds{byInstance: map[string][]linkEnd{}}
	names := make([]string, 0, len(inv.Links))
	for name := range inv.Links {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		l := inv.Links[name]
		from, ok := instances[l.From]
		if !ok {
			return ends, fmt.Errorf("derive: link %q: from %q: no such instance", name, l.From)
		}
		hop, err := ParseHop(l.To)
		if err != nil {
			return ends, fmt.Errorf("derive: link %q: %w", name, err)
		}
		to, ok := instances[hop.Instance]
		if !ok {
			return ends, fmt.Errorf("derive: link %q: to %q: no such instance", name, hop.Instance)
		}
		port, ok := to.inst.Ports[hop.Port]
		if !ok {
			return ends, fmt.Errorf("derive: link %q: %s has no port %q", name, hop.Instance, hop.Port)
		}
		if from.node.ID == to.node.ID {
			return ends, fmt.Errorf("derive: link %q: both ends run on %s, which needs no link", name, from.node.ID)
		}
		address, network, err := resolveAddress(from.node, to.node, inv.Networks, inv.Universal)
		if err != nil {
			return ends, fmt.Errorf("derive: link %q: %w", name, err)
		}
		m.Links = append(m.Links, Link{Name: name, From: l.From, To: hop, Address: address, Network: network, Port: port.Number})
		flat := inventory.FlatID(l.From)
		m.Grants = append(m.Grants, Grant{
			Principal: Principal{Kind: PrincipalInstance, ID: l.From, Name: flat, Group: flat, Slot: inventory.DefaultCredential},
			Instance:  hop.Instance, Port: hop.Port,
		})
		ends.byInstance[l.From] = append(ends.byInstance[l.From], linkEnd{link: name, other: hop.Instance})
		ends.byInstance[hop.Instance] = append(ends.byInstance[hop.Instance], linkEnd{link: name, other: l.From})
	}
	return ends, nil
}

// riding is the link an edge from instance to a node rides, and its far end:
// the one link instance ends whose other end runs on that node. None is an
// edge dialled directly; two is rule 40's ambiguity.
func (ends linkEnds) riding(instance, node string, instances map[string]instanceRef) (string, instanceRef, error) {
	var names []string
	var far instanceRef
	for _, e := range ends.byInstance[instance] {
		if other := instances[e.other]; other.node.ID == node {
			names = append(names, e.link)
			far = other
		}
	}
	switch len(names) {
	case 0:
		return "", instanceRef{}, nil
	case 1:
		return names[0], far, nil
	}
	return "", instanceRef{}, fmt.Errorf("%s ends links %s whose other end runs on %s; an edge from it there would ride one chosen by nothing",
		instance, strings.Join(names, ", "), node)
}

// addLinkMapping records the edge's entrance and target on its link, once
// however many routes cross them.
func (m *Model) addLinkMapping(e Edge) {
	key := inventory.LocalName(e.From.Instance) + "." + e.From.Port + "--" + inventory.LocalName(e.To.Instance) + "." + e.To.Port
	for i := range m.LinkMappings {
		mp := &m.LinkMappings[i]
		if mp.Link == e.Link && mp.Key == key {
			if !containsString(mp.Routes, e.Route) {
				mp.Routes = append(mp.Routes, e.Route)
				sort.Strings(mp.Routes)
			}
			return
		}
	}
	m.LinkMappings = append(m.LinkMappings, LinkMapping{
		Link: e.Link, Key: key, Entrance: e.From, Target: e.To,
		Address: e.Address, Container: e.Container, Port: e.Port, Routes: []string{e.Route},
	})
}

// PortAuthenticates reports whether instance's port holds an account per
// principal granted on it. A link's to port answers from the service's
// link.to, every other port from the service's own auth: a port is a
// route's or a link's, never both.
func PortAuthenticates(manifest confgen.Manifest, m *Model, instance, port string) bool {
	if m.IsLinkPort(instance, port) {
		return manifest.LinkAuthenticates()
	}
	return manifest.Auth == confgen.AuthPerPrincipal
}

// IsLinkPort reports whether some link's to names instance's port.
func (m *Model) IsLinkPort(instance, port string) bool {
	for _, l := range m.Links {
		if l.To.Instance == instance && l.To.Port == port {
			return true
		}
	}
	return false
}

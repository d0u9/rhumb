package engine

import (
	"fmt"
	"sort"

	"github.com/d0u9/rhumb/confgen"
	"github.com/d0u9/rhumb/derive"
	"github.com/d0u9/rhumb/inventory"
	"github.com/d0u9/rhumb/render"
	"github.com/d0u9/rhumb/secretstore"
)

// linksFor is every link instance ends, in name order, as the render context
// gives them. See docs/links.md#the-render-context.
func (m Renderer) linksFor(instance string) ([]render.Link, error) {
	var out []render.Link
	for _, l := range m.Data.Derived.Links {
		var end string
		switch instance {
		case l.From:
			end = "from"
		case l.To.Instance:
			end = "to"
		default:
			continue
		}
		account := inventory.FlatID(l.From)
		link := render.Link{Name: l.Name, End: end, Carries: m.carriesOf(l.Name)}
		if end == "to" {
			link.Peer = render.LinkPeer{Instance: inventory.LocalName(l.From), Account: account}
			out = append(out, link)
			continue
		}

		peer := render.LinkPeer{
			Instance: inventory.LocalName(l.To.Instance), Port: l.To.Port, Number: l.Port,
			Address: l.Address, Account: account,
		}
		if l.Network != "" && l.Network == m.Data.Inv.Universal {
			peer.Published = m.publishedAt(l.To.Instance, l.To.Port)
		}
		to := m.InstanceByID(l.To.Instance)
		var toRole confgen.Manifest
		if to != nil {
			toRole = m.Data.Manifests[to.Service]
		}
		if m.SecretsDir != "" && derive.PortAuthenticates(toRole, m.Data.Derived, l.To.Instance, l.To.Port) {
			v, err := secretstore.ReadValue(m.SecretsDir, m.storedAt(secretstore.Path{
				Instance: l.To.Instance, Port: l.To.Port, Group: account, Name: inventory.DefaultCredential,
			}))
			if err != nil {
				return nil, fmt.Errorf("link %s: %w", l.Name, err)
			}
			peer.Secret = v
		}

		var wants confgen.UpstreamDecls
		if from := m.InstanceByID(instance); from != nil {
			if role := m.Data.Manifests[from.Service]; role.LinkFrom() {
				wants = *role.Link.From
			}
		}
		if wants.Wants(confgen.UpstreamShared) && m.SecretsDir != "" {
			handed := destinationSelf(m, l.To.Instance, l.To.Port)
			own, err := secretstore.ReadSelf(m.SecretsDir, m.secretID(l.To.Instance))
			if err != nil {
				return nil, err
			}
			for _, ref := range handed {
				v, ok := lookupSelf(own, inventory.ParseSelfRef(ref))
				if !ok {
					return nil, fmt.Errorf("link %s needs the shared secrets of %s:%s, and %q is missing", l.Name, l.To.Instance, l.To.Port, ref)
				}
				peer.Shared = append(peer.Shared, v)
			}
		}
		if wants.Wants(confgen.UpstreamValues) {
			peer.Values = map[string]any{}
			if to != nil && len(to.Values) > 0 {
				peer.Values = to.Values
			}
		}
		link.Peer = peer
		out = append(out, link)
	}
	return out, nil
}

// carriesOf is a link's mappings, the same at both ends, in key order.
func (m Renderer) carriesOf(link string) []render.LinkMapping {
	var out []render.LinkMapping
	for _, mp := range m.Data.Derived.LinkMappings {
		if mp.Link != link {
			continue
		}
		entrance := render.LinkEndpoint{Instance: inventory.LocalName(mp.Entrance.Instance), Port: mp.Entrance.Port}
		if inst := m.InstanceByID(mp.Entrance.Instance); inst != nil {
			p := inst.Ports[mp.Entrance.Port]
			entrance.Number, entrance.Protocol = p.Number, p.ProtocolOr()
			if m.Data.Manifests[inst.Service].Dispatch == confgen.DispatchName {
				entrance.Published = m.publishedAt(mp.Target.Instance, mp.Target.Port)
			}
		}
		target := render.LinkEndpoint{Instance: inventory.LocalName(mp.Target.Instance), Port: mp.Target.Port, Number: mp.Port, Address: mp.Address}
		if inst := m.InstanceByID(mp.Target.Instance); inst != nil {
			target.Protocol = inst.Ports[mp.Target.Port].ProtocolOr()
		}
		routes := make([]string, len(mp.Routes))
		for i, r := range mp.Routes {
			routes[i] = inventory.FlatID(r)
		}
		out = append(out, render.LinkMapping{Key: mp.Key, Entrance: entrance, Target: target, Routes: routes})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

package derive

import (
	"fmt"
	"sort"
	"strings"

	"github.com/d0u9/rhumb/confgen"
	"github.com/d0u9/rhumb/inventory"
)

// ServiceDial is the one instance of decl.Service a caller dials when it
// names none: the candidates in the innermost scope the two share — one
// container network, then one node, then each network in preference order the
// caller reaches — and exactly one of them. None anywhere, or two in the first
// scope holding any, is an error saying which to write.
func ServiceDial(inv *inventory.Root, from inventory.Instance, fromNode inventory.Node, decl confgen.DialDecl) (string, error) {
	type candidate struct {
		inst inventory.Instance
		node inventory.Node
	}
	var all []candidate
	for _, n := range inv.Nodes {
		if n.Broken != "" {
			continue
		}
		for _, inst := range n.Instances {
			if inst.Service != decl.Service || inst.ID == from.ID {
				continue
			}
			if _, ok := inst.Ports[decl.Port]; ok {
				all = append(all, candidate{inst, n})
			}
		}
	}
	levels := []struct {
		name string
		in   func(c candidate) bool
	}{
		{"container network", func(c candidate) bool {
			return c.node.ID == fromNode.ID && fromNode.SharedContainer(from, c.inst) != ""
		}},
		{"node " + fromNode.ID, func(c candidate) bool { return c.node.ID == fromNode.ID }},
	}
	for _, network := range networkOrder(inv) {
		network := network
		levels = append(levels, struct {
			name string
			in   func(c candidate) bool
		}{"network " + network, func(c candidate) bool {
			_, has := c.node.Networks[network]
			return has && reaches(fromNode, network, inv.Universal)
		}})
	}
	for _, level := range levels {
		var found []string
		for _, c := range all {
			if level.in(c) {
				found = append(found, c.inst.ID)
			}
		}
		switch len(found) {
		case 0:
			continue
		case 1:
			return found[0] + ":" + decl.Port, nil
		default:
			sort.Strings(found)
			return "", fmt.Errorf("%s instances %s share %s with it: write the dial", decl.Service, strings.Join(found, ", "), level.name)
		}
	}
	return "", fmt.Errorf("no %s instance with port %q shares a scope with it: write the dial, or add one", decl.Service, decl.Port)
}

// FillServiceDials writes into every instance each dial its service declares
// and it does not write, marking it derived. A dial that cannot be resolved
// is left out; ServiceDialIssues reports it.
func FillServiceDials(inv *inventory.Root, manifests map[string]confgen.Manifest) {
	for ni := range inv.Nodes {
		n := &inv.Nodes[ni]
		if n.Broken != "" {
			continue
		}
		for ii := range n.Instances {
			inst := &n.Instances[ii]
			for name, decl := range manifests[inst.Service].Dials {
				if _, written := inst.Dials[name]; written {
					continue
				}
				target, err := ServiceDial(inv, *inst, *n, decl)
				if err != nil {
					continue
				}
				if inst.Dials == nil {
					inst.Dials = map[string]string{}
				}
				if inst.DialsDerived == nil {
					inst.DialsDerived = map[string]bool{}
				}
				inst.Dials[name] = target
				inst.DialsDerived[name] = true
			}
		}
	}
}

// ServiceDialIssues is every declared dial an instance neither writes nor
// resolves, sorted.
func ServiceDialIssues(inv *inventory.Root, manifests map[string]confgen.Manifest) []string {
	var out []string
	for _, n := range inv.Nodes {
		if n.Broken != "" {
			continue
		}
		for _, inst := range n.Instances {
			for name, decl := range manifests[inst.Service].Dials {
				if _, ok := inst.Dials[name]; ok {
					continue
				}
				if _, err := ServiceDial(inv, inst, n, decl); err != nil {
					out = append(out, fmt.Sprintf("instance %q dials %q by service: %s", inst.ID, name, err))
				}
			}
		}
	}
	sort.Strings(out)
	return out
}

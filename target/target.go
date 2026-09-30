// Package target lists the targets an inventory and its derivation hold,
// and matches selectors against them. A target is one instance.
//
// The rules are in docs/export.md#targets-and-selectors.
package target

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/d0u9/rhumb/derive"
	"github.com/d0u9/rhumb/inventory"
)

// Target is one instance, real or derived, with the fields a selector
// matches against.
type Target struct {
	// Node is the owning node's ID, empty for an unmanaged user's derived
	// instance.
	Node string
	// User is the node's owner, or the unmanaged user's own key. Empty for
	// an instance on a node with no owner.
	User string
	// Service is the program at stake: what an authored instance deploys,
	// and, for a derived one, whose credential the file carries. Export is
	// how that file is written, and is set only on a derived one — it is
	// what tells something a machine runs apart from something a person is
	// handed. An export's name is unique only within its service, so the
	// two travel together.
	Service string
	Export  string
	// Profile is the device profile a derived target was written out for,
	// empty for everything else.
	Profile string
	// Instance is the target's identifier — a bare selector term matches
	// this field.
	Instance string
	// Routes is every route this instance takes part in: the routes it is
	// any hop of, or, for a derived instance, the one route it was derived
	// for.
	Routes []string
	// Broken is the node file's parse error, carried over so a selector can
	// still name a broken node's instances and a report can say why they
	// cannot render. Empty for a derived instance — a broken node grants no
	// routes, so it derives nothing to be broken.
	Broken string
}

// String is node/instance, the identifying pair --targets and an error name
// a target by.
func (t Target) String() string {
	node := t.Node
	if node == "" {
		node = t.User
	}
	return node + "/" + inventory.LocalName(t.Instance)
}

// List is every target an inventory and its derivation hold, sorted by
// node, then instance. A broken node contributes one target naming the node
// and carrying its parse error, since a node file that will not parse names
// no instances to list individually.
func List(inv *inventory.Root, model *derive.Model) []Target {
	owner := map[string]string{}
	for _, n := range inv.Nodes {
		if n.Broken == "" {
			owner[n.ID] = n.Owner
		}
	}

	var out []Target
	for _, n := range inv.Nodes {
		if n.Broken != "" {
			// A broken node's own id never parsed; its file path is the only
			// identifying thing left to group and report it by.
			out = append(out, Target{Node: valueOr(n.ID, n.Path), Broken: n.Broken})
			continue
		}
		for _, inst := range n.Instances {
			if inst.Service == "" {
				continue // an override, not a target of its own — the derived instance it pins is.
			}
			out = append(out, Target{
				Node:     n.ID,
				User:     n.Owner,
				Service:  inst.Service,
				Instance: inst.ID,
				Routes:   routesContaining(inv, inst.ID),
			})
		}
	}
	for _, ci := range model.ExportInstances {
		out = append(out, Target{
			Node:     ci.Node,
			User:     valueOr(ci.User, owner[ci.Node]),
			Service:  ci.Service,
			Export:   ci.Export,
			Profile:  ci.Profile,
			Instance: ci.ID,
			Routes:   ci.Routes,
		})
	}

	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Node != b.Node {
			return a.Node < b.Node
		}
		return a.Instance < b.Instance
	})
	return out
}

func valueOr(v, fallback string) string {
	if v != "" {
		return v
	}
	return fallback
}

// routesContaining is every route naming instance as any of its hops.
func routesContaining(inv *inventory.Root, instance string) []string {
	var routes []string
	for name, route := range inv.Routes {
		for _, raw := range route.Hops {
			hop, err := derive.ParseHop(raw)
			if err == nil && hop.Instance == instance {
				routes = append(routes, name)
				break
			}
		}
	}
	sort.Strings(routes)
	return routes
}

// NodeGroup is List's targets grouped by node, the form --targets reports
// them in.
type NodeGroup struct {
	// Node is the node ID, or the unmanaged user's key when Targets have no
	// node.
	Node    string
	Targets []Target
}

// GroupByNode groups targets by Target.Node, falling back to Target.User for
// an unmanaged user's targets, sorted by that key and then as targets
// already are.
func GroupByNode(targets []Target) []NodeGroup {
	index := map[string]int{}
	var groups []NodeGroup
	for _, t := range targets {
		key := t.Node
		if key == "" {
			key = t.User
		}
		i, ok := index[key]
		if !ok {
			i = len(groups)
			index[key] = i
			groups = append(groups, NodeGroup{Node: key})
		}
		groups[i].Targets = append(groups[i].Targets, t)
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].Node < groups[j].Node })
	return groups
}

// Term is one parsed selector term: field:value, or a bare value, which is
// an instance term.
type Term struct {
	Field string
	Value string
}

// fields a selector term may name.
const (
	FieldNode     = "node"
	FieldUser     = "user"
	FieldService  = "service"
	FieldExport   = "export"
	FieldProfile  = "profile"
	FieldInstance = "instance"
	FieldRoute    = "route"
)

var validFields = map[string]bool{
	FieldNode: true, FieldUser: true, FieldService: true, FieldExport: true,
	FieldProfile: true, FieldInstance: true, FieldRoute: true,
}

// ParseSelector splits a selector into its space-separated terms. A term
// with no "field:" prefix is a FieldInstance term.
func ParseSelector(selector string) ([]Term, error) {
	fields := strings.Fields(selector)
	if len(fields) == 0 {
		return nil, fmt.Errorf("empty selector")
	}
	terms := make([]Term, 0, len(fields))
	for _, f := range fields {
		field, value, ok := strings.Cut(f, ":")
		if !ok {
			field, value = FieldInstance, f
		}
		if !validFields[field] {
			return nil, fmt.Errorf("selector term %q: unknown field %q", f, field)
		}
		if value == "" {
			return nil, fmt.Errorf("selector term %q: empty value", f)
		}
		terms = append(terms, Term{Field: field, Value: value})
	}
	return terms, nil
}

// Match returns every target every term of a selector matches — all terms
// must match, and a value may contain "*". A selector matching nothing is
// an error naming the selector, not an empty result.
func Match(selector string, targets []Target) ([]Target, error) {
	terms, err := ParseSelector(selector)
	if err != nil {
		return nil, fmt.Errorf("selector %q: %w", selector, err)
	}

	var matched []Target
	for _, t := range targets {
		if matchesAll(t, terms) {
			matched = append(matched, t)
		}
	}
	if len(matched) == 0 {
		return nil, fmt.Errorf("selector %q matches nothing", selector)
	}
	return matched, nil
}

// Routes is how a selector narrows the routes of the targets it matched:
// for each matched target that is one process over several routes — a
// device profile that runs a program — the routes its route: terms name,
// when the selector has any. A target it does not narrow is absent, and is
// rendered with every route it takes.
func Routes(selector string, matched []Target) (map[string][]string, error) {
	terms, err := ParseSelector(selector)
	if err != nil {
		return nil, fmt.Errorf("selector %q: %w", selector, err)
	}
	var routeTerms []Term
	for _, term := range terms {
		if term.Field == FieldRoute {
			routeTerms = append(routeTerms, term)
		}
	}
	if len(routeTerms) == 0 {
		return nil, nil
	}
	out := map[string][]string{}
	for _, t := range matched {
		if t.Export != "" || t.Profile == "" {
			continue
		}
		var keep []string
		for _, r := range t.Routes {
			for _, term := range routeTerms {
				if globMatch(term.Value, r) {
					keep = append(keep, r)
					break
				}
			}
		}
		out[t.Instance] = keep
	}
	return out, nil
}

// matchesAll is the selector's semantics: terms naming one field are
// alternatives, and different fields narrow each other. It is what makes
// `service:ss-link service:sslocal` mean "either form" rather than the
// impossible "both at once" — and so what makes a shell's own
// `service:{ss-link,sslocal}`, which expands to exactly that, do what it
// looks like it does.
func matchesAll(t Target, terms []Term) bool {
	byField := map[string][]Term{}
	var order []string
	for _, term := range terms {
		if _, seen := byField[term.Field]; !seen {
			order = append(order, term.Field)
		}
		byField[term.Field] = append(byField[term.Field], term)
	}
	for _, field := range order {
		any := false
		for _, term := range byField[field] {
			if matchesOne(t, term) {
				any = true
				break
			}
		}
		if !any {
			return false
		}
	}
	return true
}

func matchesOne(t Target, term Term) bool {
	switch term.Field {
	case FieldNode:
		return globMatch(term.Value, t.Node)
	case FieldUser:
		return globMatch(term.Value, t.User)
	case FieldService:
		// A derived target carries the service whose credential it hands
		// over, because its export's name means nothing without it. It is
		// still not that service: `service:` picks what machines run, and
		// `export:` picks what people are handed.
		if t.Export != "" {
			return false
		}
		return globMatch(term.Value, t.Service)
	case FieldExport:
		return globMatch(term.Value, t.Export)
	case FieldProfile:
		return globMatch(term.Value, t.Profile)
	case FieldInstance:
		// Either the whole <node>/<name> or the name alone.
		return globMatch(term.Value, t.Instance) || globMatch(term.Value, inventory.LocalName(t.Instance))
	case FieldRoute:
		for _, r := range t.Routes {
			if globMatch(term.Value, r) {
				return true
			}
		}
		return false
	default:
		// Unreachable: ParseSelector rejects any field not in validFields
		// before a Term reaches Match, so this never runs.
		return false
	}
}

func globMatch(pattern, value string) bool {
	ok, _ := filepath.Match(pattern, value)
	return ok
}

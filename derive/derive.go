// Package derive computes everything docs/apps/conf/inventory.md says is
// derived, never written by hand: export instances, edges between hops with
// their resolved addresses, grants and per-port principal tables. It is a
// pure function of an already-parsed inventory and the service manifests
// its routes' entry hops name — it touches no filesystem itself.
//
// The rules are in docs/apps/conf/inventory.md#what-is-derived and
// docs/apps/conf/inventory.md#networks-and-how-an-address-is-chosen.
package derive

import (
	"fmt"
	"sort"
	"strings"

	"github.com/d0u9/rhumb/confgen"
	"github.com/d0u9/rhumb/inventory"
)

// Hop is one parsed element of a route's hops list: "<node>/<instance>:<port>".
// Instance is the whole <node>/<instance>, an instance's ID.
type Hop struct {
	Instance string
	Port     string
}

// ParseHop splits "<node>/<instance>:<port>" into instance ID and port.
func ParseHop(s string) (Hop, error) {
	instance, port, ok := strings.Cut(s, ":")
	if !ok || instance == "" || port == "" {
		return Hop{}, fmt.Errorf("hop %q: want <node>/<instance>:<port>", s)
	}
	return Hop{Instance: instance, Port: port}, nil
}

// PrincipalKind is what kind of thing a Principal is, per
// docs/apps/conf/inventory.md#the-model.
type PrincipalKind string

const (
	// PrincipalUser is one of a person's credentials. It is not a device:
	// how many credentials someone keeps is theirs to decide, and two of
	// their devices naming one credential hold one secret between them.
	PrincipalUser PrincipalKind = "user"
	// PrincipalInstance is an instance relaying through a hop.
	PrincipalInstance PrincipalKind = "instance"
)

// Principal is whatever holds a grant: a managed device (its node), an
// unmanaged user, or an upstream instance relaying for a non-terminal hop.
type Principal struct {
	Kind PrincipalKind
	// ID identifies the principal for deduplication: "<user>/<credential>"
	// for a person's credential, or an instance ID.
	ID string
	// Name is the account name a server-side render sees, per
	// docs/apps/conf/inventory.md#managed-and-unmanaged-devices.
	Name string
	// Group is whose this principal is: the node group for a device or for
	// an instance relaying through, the user's own key for an unmanaged
	// user. Slot is what it is called inside that group: the device, the
	// relaying instance, or inventory.DefaultCredential for an unmanaged user,
	// who has no device file. Together they are the credential's place in
	// the secrets tree, and they are a pair for every kind of principal.
	Group string
	Slot  string
}

// Grant is one credential a principal needs for one port of one instance.
type Grant struct {
	Principal Principal
	Instance  string
	Port      string
}

// ExportInstance is one file written out for a person: one credential of
// theirs, one route, one way of writing it. It is not a deployment — nothing
// runs a share URI, and the program that reads a JSON configuration runs on a
// machine this inventory does not model.
type ExportInstance struct {
	// ID is "<node>-<route>-<service>-<export>" for a device,
	// "<node>-<route>-<service>-<export>-<profile>" for one of a device's
	// profiles, and
	// "<username>-<credential>-<route>-<service>-<export>" for a person with
	// no device file. The service is in the name because an export's name is
	// unique only within its service: two services each offering a "link"
	// would otherwise write one file over the other.
	ID string
	// Node is the owning node's ID, empty for an unmanaged user (who has no
	// node file). User is the owning unmanaged user's map key, empty for a
	// managed device. Exactly one of the two is set.
	Node string
	User string
	// Export is how this file is written: one of the ways the entry hop's
	// service offers, narrowed by the device's `export` when it names one.
	// Service is that entry hop's service, which is what decides which
	// export directory the name refers to.
	Service string
	Export  string
	Route   string
	// Credential is the owner's credential this instance authenticates
	// with. Two devices may name the same one.
	Credential string
	// Profile is the device profile this file was written out for, empty
	// for a device with none and for a file a person carries.
	Profile string
	// Ports, Bind and Values come from an authored override with the same
	// ID, on the same node, if one exists — a derived instance has none of
	// them by default. Values start from the profile's own, and an
	// override's win over them key by key. See
	// docs/apps/conf/inventory.md#what-is-derived.
	Ports  inventory.Ports
	Bind   string
	Values map[string]any
}

// Edge is one resolved hop-to-hop connection: either between two adjacent
// hops of a route, or from an export instance to the route's entry hop.
type Edge struct {
	Route string
	// From is the empty Hop for the edge out of an export instance;
	// FromInstance names it instead.
	From         Hop
	FromInstance string
	To           Hop
	// Address is the address the From side dials, per
	// docs/apps/conf/inventory.md#networks-and-how-an-address-is-chosen.
	Address string
	// Network is the network Address is on, or empty when the two ends share
	// a node.
	Network string
	// Container is the container network both ends are on, when they share
	// one: Address is then the To instance's name there, and the edge never
	// leaves the network, so it publishes nothing on the host.
	Container string
	// Port is the numeric port the To hop's instance names for To.Port.
	Port int
	// Terminal is the hop whose credential the From side authenticates
	// against, which is To for every ordinary edge. They differ when To
	// forwards: a relay terminates nothing, so what a client dials and what
	// it authenticates against are two different machines, and the file it
	// is given is built from both. See
	// docs/apps/conf/inventory.md#a-service-that-forwards.
	Terminal Hop
}

// Model is everything Derive computes.
type Model struct {
	ExportInstances []ExportInstance
	Edges           []Edge
	// Grants holds one entry per (principal, instance, port). Two routes
	// entering one port do not make two credentials; see dedupeGrants.
	Grants []Grant
}

// Principals returns the Grants' principals for one instance's port, sorted
// by Name and deduplicated by (Kind, ID) — the account table one port
// renders.
func (m *Model) Principals(instance, port string) []Principal {
	seen := map[[2]string]bool{}
	var out []Principal
	for _, g := range m.Grants {
		if g.Instance != instance || g.Port != port {
			continue
		}
		key := [2]string{string(g.Principal.Kind), g.Principal.ID}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, g.Principal)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// narrowExports is the ways a credential is actually written out: every one
// the service names, or the single one a device asks for. A device asking for
// an export the service does not offer gets nothing rather than a file its
// program cannot read; validate reports it by name.
func narrowExports(exports []string, want string) []string {
	switch {
	case len(exports) == 0, want == inventory.ExportNone:
		return nil
	case want == "":
		return exports
	}
	for _, e := range exports {
		if e == want {
			return []string{e}
		}
	}
	return nil
}

// use is one way a device is written out: the device itself, or one of its
// profiles.
type use struct {
	profile string
	export  string
	values  map[string]any
	access  []string
}

// usesOf is the device itself when it declares no profiles, and each of its
// profiles, in name order, when it does.
func usesOf(n inventory.Node) []use {
	if len(n.Profiles) == 0 {
		return []use{{export: n.Export}}
	}
	out := make([]use, 0, len(n.Profiles))
	for _, name := range n.ProfileNames() {
		p := n.Profiles[name]
		out = append(out, use{profile: name, export: p.Export, values: p.Values, access: p.Access})
	}
	return out
}

// overlay is base with every top-level key of over written onto it, over
// winning. Neither argument is modified.
func overlay(over, base map[string]any) map[string]any {
	if len(base) == 0 {
		return over
	}
	out := make(map[string]any, len(base)+len(over))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range over {
		out[k] = v
	}
	return out
}

// terminalHopFrom walks forward from a hop over every instance whose service
// forwards, and answers the first one that terminates: the hop whose account
// table a client reaching this one authenticates against. A chain that
// forwards to its end answers the last hop it walked — nothing terminates it,
// which validate reports as the mistake it is, and deriving something here
// keeps that report reachable.
func terminalHopFrom(hops []Hop, i int, instances map[string]instanceRef, manifests map[string]confgen.Manifest) Hop {
	for i+1 < len(hops) {
		inst, ok := instances[hops[i].Instance]
		if !ok || manifests[inst.inst.Service].Terminates() {
			return hops[i]
		}
		i++
	}
	return hops[i]
}

// forwards reports whether an instance's service moves bytes through without
// terminating them.
func forwards(instance string, instances map[string]instanceRef, manifests map[string]confgen.Manifest) bool {
	inst, ok := instances[instance]
	if !ok {
		return false
	}
	return manifests[inst.inst.Service].Forwards
}

type instanceRef struct {
	node inventory.Node
	inst inventory.Instance
}

// Derive computes export instances, edges and grants from an inventory
// already loaded by inventory.Load, and the service manifests its routes'
// entry hops name, keyed by service name. Only non-broken nodes are
// considered; a broken node's instances are invisible to Derive, the same
// way a broken service is invisible to confgen's own callers.
func Derive(inv *inventory.Root, manifests map[string]confgen.Manifest) (*Model, error) {
	instances := map[string]instanceRef{}
	nodes := map[string]inventory.Node{}
	for _, n := range inv.Nodes {
		if n.Broken != "" {
			continue
		}
		nodes[n.ID] = n
		for _, inst := range n.Instances {
			if _, dup := instances[inst.ID]; dup {
				return nil, fmt.Errorf("derive: instance %q is defined more than once", inst.ID)
			}
			instances[inst.ID] = instanceRef{node: n, inst: inst}
		}
	}

	m := &Model{}

	userKeys := make([]string, 0, len(inv.Users))
	for key := range inv.Users {
		userKeys = append(userKeys, key)
	}
	sort.Strings(userKeys)

	for _, key := range userKeys {
		user := inv.Users[key]
		routeNames := append([]string(nil), user.Access...)
		sort.Strings(routeNames)
		for _, routeName := range routeNames {
			route, ok := inv.Routes[routeName]
			if !ok || len(route.Hops) == 0 {
				continue
			}
			routeHops := make([]Hop, 0, len(route.Hops))
			for _, raw := range route.Hops {
				hop, err := ParseHop(raw)
				if err != nil {
					return nil, fmt.Errorf("derive: route %q: %w", routeName, err)
				}
				routeHops = append(routeHops, hop)
			}
			entryHop := routeHops[0]
			entry, ok := instances[entryHop.Instance]
			if !ok {
				continue
			}
			// What the client dials is the entry hop; what it authenticates
			// against is the first hop that terminates anything. A relay in
			// front of a server is the two being different machines, and
			// the file this person is given is written for the service that
			// ends the chain, in the format that service offers.
			terminalHop := terminalHopFrom(routeHops, 0, instances, manifests)
			terminal, ok := instances[terminalHop.Instance]
			if !ok {
				continue
			}
			manifest, ok := manifests[terminal.inst.Service]
			if !ok {
				continue
			}
			// A service with no Exports produces no file for anyone —
			// MicroBin is reached from a browser — but the grant and its
			// secret still exist; only the written files do not.
			exports := manifest.Exports

			username := user.UsernameOr(key)

			// A credential is an account because the person declares it,
			// not because a device happens to name it: how many passwords
			// someone keeps is theirs to decide, and a device chooses among
			// them rather than bringing them into being. So every declared
			// credential is a grant on this route's entry port.
			for _, credential := range user.CredentialNames() {
				// A credential may open fewer routes than the person holds:
				// a laptop's password is revocable on its own, and being
				// able to take it off one line without taking it off the
				// rest is why someone keeps a second one at all.
				if !user.OpensRoute(credential, routeName) {
					continue
				}
				principal := Principal{
					Kind: PrincipalUser, ID: key + "/" + credential,
					Name:  accountName(user, key, credential, manifest),
					Group: key, Slot: credential,
				}
				m.Grants = append(m.Grants, Grant{Principal: principal, Instance: terminalHop.Instance, Port: terminalHop.Port})
			}

			var ownedNodeIDs []string
			usedByADevice := map[string]bool{}
			for id, n := range nodes {
				if n.Owner == key {
					ownedNodeIDs = append(ownedNodeIDs, id)
					usedByADevice[n.CredentialOr()] = true
				}
			}
			sort.Strings(ownedNodeIDs)

			for _, nodeID := range ownedNodeIDs {
				n := nodes[nodeID]
				// A device carries one credential and nothing else, so a
				// route that credential does not open is a route this device
				// cannot take, however the person's own access reads.
				if !user.OpensRoute(n.CredentialOr(), routeName) {
					continue
				}
				// The device does not hold a credential of its own: it
				// names one of its owner's, and two devices naming the
				// same one are one principal with one secret between them.
				credential := n.CredentialOr()

				// docs/apps/conf/inventory.md#which-export-a-person-receives:
				// the service names every way it may be written out, and the
				// node's `export` narrows to one of them; export: none
				// narrows to nothing. A service naming none writes no file,
				// and `export` does not bring one back.
				//
				// One file per device per route per export: two devices
				// sharing a credential still each need their own, because
				// their local ports differ.
				//
				// A device with profiles is written out once per profile
				// instead, each profile narrowing in the device's place.
				for _, use := range usesOf(n) {
					if len(use.access) > 0 && !containsString(use.access, routeName) {
						continue
					}
					for _, export := range narrowExports(exports, use.export) {
						derivedID := nodeID + "-" + inventory.FlatID(routeName) + "-" + terminal.inst.Service + "-" + export
						if use.profile != "" {
							derivedID += "-" + use.profile
						}
						ci := ExportInstance{
							ID: derivedID, Node: nodeID, Credential: credential,
							Service: terminal.inst.Service, Export: export, Route: routeName,
							Profile: use.profile, Values: use.values,
						}
						for _, override := range n.Instances {
							if inventory.LocalName(override.ID) == derivedID {
								ci.Ports, ci.Bind = override.Ports, override.Bind
								ci.Values = overlay(override.Values, use.values)
								break
							}
						}
						m.ExportInstances = append(m.ExportInstances, ci)

						address, network, err := resolveAddress(n, entry.node, inv.Networks, inv.Universal)
						if err != nil {
							return nil, fmt.Errorf("derive: route %q for %s: %w", routeName, nodeID, err)
						}
						m.Edges = append(m.Edges, Edge{
							Route:        routeName,
							FromInstance: derivedID,
							To:           entryHop,
							Terminal:     terminalHop,
							Address:      address,
							Network:      network,
							Port:         entry.inst.Ports[entryHop.Port].Number,
						})
					}
				}
			}

			// A credential no device names is one the person carries
			// themselves — the laptop at hand, a machine this inventory does
			// not model — so its file is written for the person rather than
			// for a device. Someone with no device file at all is this case
			// for every credential they keep, which is why it needs no rule
			// of its own. With no node to carry an `export`, the person's
			// own narrows in a device's place.
			for _, credential := range user.CredentialNames() {
				if usedByADevice[credential] {
					continue
				}
				if !user.OpensRoute(credential, routeName) {
					continue
				}
				for _, export := range narrowExports(exports, user.Export) {
					id := username + "-" + credential + "-" + inventory.FlatID(routeName) + "-" + terminal.inst.Service + "-" + export
					m.ExportInstances = append(m.ExportInstances, ExportInstance{
						ID:         id,
						User:       key,
						Credential: credential,
						Service:    terminal.inst.Service,
						Export:     export,
						Route:      routeName,
					})
					// A file not tied to a device reaches the universal
					// network and the networks its credential names in
					// `reaches` — resolve as if dialing from a node that
					// reaches only those.
					carrier := inventory.Node{Reaches: user.Credentials[credential].Reaches}
					address, network, err := resolveAddress(carrier, entry.node, inv.Networks, inv.Universal)
					if err != nil {
						return nil, fmt.Errorf("derive: route %q for %s: %w", routeName, key, err)
					}
					m.Edges = append(m.Edges, Edge{
						Route:        routeName,
						FromInstance: id,
						To:           entryHop,
						Terminal:     terminalHop,
						Address:      address,
						Network:      network,
						Port:         entry.inst.Ports[entryHop.Port].Number,
					})
				}
			}
		}
	}

	routeNames := make([]string, 0, len(inv.Routes))
	for name := range inv.Routes {
		routeNames = append(routeNames, name)
	}
	sort.Strings(routeNames)

	for _, routeName := range routeNames {
		route := inv.Routes[routeName]
		hops := make([]Hop, 0, len(route.Hops))
		for _, raw := range route.Hops {
			hop, err := ParseHop(raw)
			if err != nil {
				return nil, fmt.Errorf("derive: route %q: %w", routeName, err)
			}
			hops = append(hops, hop)
		}

		for i := 0; i+1 < len(hops); i++ {
			from, ok := instances[hops[i].Instance]
			if !ok {
				return nil, fmt.Errorf("derive: route %q: hop %q: no such instance", routeName, hops[i].Instance)
			}
			to, ok := instances[hops[i+1].Instance]
			if !ok {
				return nil, fmt.Errorf("derive: route %q: hop %q: no such instance", routeName, hops[i+1].Instance)
			}
			port, ok := to.inst.Ports[hops[i+1].Port]
			if !ok {
				return nil, fmt.Errorf("derive: route %q: %s has no port %q", routeName, hops[i+1].Instance, hops[i+1].Port)
			}
			address, network, container, err := resolveEndpoint(from.inst, from.node, to.inst, to.node, inv.Networks, inv.Universal)
			if err != nil {
				return nil, fmt.Errorf("derive: route %q: %w", routeName, err)
			}

			terminal := terminalHopFrom(hops, i+1, instances, manifests)
			m.Edges = append(m.Edges, Edge{
				Route: routeName, From: hops[i], To: hops[i+1], Terminal: terminal,
				Address: address, Network: network, Container: container, Port: port.Number,
			})
			// A forwarder holds no credential: it never reads what passes
			// through it, so there is nothing for it to authenticate with.
			// The grant belongs to whatever dials into it, against the hop
			// that terminates the chain.
			if forwards(hops[i].Instance, instances, manifests) {
				continue
			}
			m.Grants = append(m.Grants, Grant{
				Principal: upstreamPrincipal(inv, from.inst, manifests[instances[terminal.Instance].inst.Service]),
				Instance:  terminal.Instance,
				Port:      terminal.Port,
			})
		}
	}

	m.Grants = dedupeGrants(m.Grants)
	return m, nil
}

// upstreamPrincipal is the account an authored instance uses at its next
// terminating hop. Ordinarily the instance owns that account. A principal
// binding instead selects an already-authorised user's default credential;
// validation reports an unknown or unauthorised name, while this safe fallback
// keeps derivation available so the full check report can still be produced.
func upstreamPrincipal(inv *inventory.Root, inst inventory.Instance, terminal confgen.Manifest) Principal {
	if inst.Principal != "" {
		if user, ok := inv.Users[inst.Principal]; ok {
			return Principal{
				Kind:  PrincipalUser,
				ID:    inst.Principal + "/" + inventory.DefaultCredential,
				Name:  accountName(user, inst.Principal, inventory.DefaultCredential, terminal),
				Group: inst.Principal, Slot: inventory.DefaultCredential,
			}
		}
	}
	return Principal{
		Kind: PrincipalInstance, ID: inst.ID, Name: inventory.FlatID(inst.ID),
		Group: inventory.FlatID(inst.ID), Slot: inventory.DefaultCredential,
	}
}

// dedupeGrants keeps one grant per (principal, instance, port), which is
// what docs/apps/conf/inventory.md#what-is-derived states a grant is: one
// credential for one party to reach one port. Two routes entering the same
// port produce the pair twice — the person picks one route or the other and
// connects with the same credential either way — and a caller counting the
// list would otherwise report one credential as two.
func dedupeGrants(grants []Grant) []Grant {
	seen := map[[4]string]bool{}
	out := grants[:0]
	for _, g := range grants {
		key := [4]string{string(g.Principal.Kind), g.Principal.ID, g.Instance, g.Port}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, g)
	}
	return out
}

// resolveAddress is docs/apps/conf/inventory.md's address rule: the same
// node dials loopback; otherwise the downstream's address is used on the
// first network, in networkPref's preference order, that the downstream has
// an address on and the upstream can reach. The rule is one-directional —
// only the downstream needs to be reachable — so a node behind NAT with no
// address anywhere can still dial out. universal is networks.yaml's
// `universal` key: the one network every node reaches without saying so, or
// empty if none is. The network the address was found on is returned with
// it, empty for loopback.
func resolveAddress(from, to inventory.Node, networkPref []string, universal string) (address, network string, err error) {
	if from.ID == to.ID {
		return "127.0.0.1", "", nil
	}

	pref := networkPref
	if universal != "" && !containsString(pref, universal) {
		pref = append(append([]string{}, pref...), universal)
	}

	for _, network := range pref {
		addr, hasAddr := to.Networks[network]
		if !hasAddr {
			continue
		}
		if !reaches(from, network, universal) {
			continue
		}
		return addr, network, nil
	}
	return "", "", fmt.Errorf("%s and %s share no reachable network (%s reaches %s, %s reaches %s)",
		from.ID, to.ID, from.ID, strings.Join(reachSet(from, pref, universal), ", "), to.ID, strings.Join(reachSet(to, pref, universal), ", "))
}

// reaches reports whether node can open a connection on network: it has an
// address there, it is named in Reaches, or network is universal, which
// every node reaches implicitly.
func reaches(node inventory.Node, network, universal string) bool {
	if universal != "" && network == universal {
		return true
	}
	if _, ok := node.Networks[network]; ok {
		return true
	}
	return containsString(node.Reaches, network)
}

func reachSet(node inventory.Node, networks []string, universal string) []string {
	var out []string
	for _, n := range networks {
		if reaches(node, n, universal) {
			out = append(out, n)
		}
	}
	return out
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// accountName is what the service a grant terminates at calls this
// credential's account. It is the service's to say because it is the
// service's account table: most name one account per credential, and one
// whose accounts are POSIX users names them after the person. Which secret
// the account holds does not change with it — the path is the credential's
// either way.
func accountName(user inventory.User, key, credential string, terminal confgen.Manifest) string {
	if terminal.NamesAccountsByPerson() {
		return user.UsernameOr(key)
	}
	return user.Account(key, credential)
}

// Package validate checks an inventory and its derivation against the rules
// in docs/inventory.md#validation. Each rule is one function,
// contributing zero or more Issues; Validate runs all of them and returns
// everything found, rather than stopping at the first problem.
//
// It is a pure function of already-loaded, already-derived values — the
// same inventory.Root, confgen.Manifest set and derive.Model the rest of
// rhumb works with — and touches no filesystem itself.
//
// Rule 15 (the secrets tree matching what the inventory implies, both
// directions) needs secretstore.Sync and is not here; doc's own words are
// "neither is fixed here". Rule 16 needs only a `.previous` file's
// modification time, which Validate takes as a plain value — the one
// concession to touching a filesystem, still made by the caller, not here.
package validate

import (
	"fmt"
	"net/netip"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/d0u9/rhumb/confgen"
	"github.com/d0u9/rhumb/derive"
	"github.com/d0u9/rhumb/inventory"
)

// Issue is one validation failure, naming what it read and where.
type Issue struct {
	Message string
}

func (i Issue) Error() string { return i.Message }

// instRef is one authored instance, with the node it was found on.
type instRef struct {
	nodeID string
	inst   inventory.Instance
}

// isOverride reports whether inst is a client override rather than a full
// instance definition: docs/inventory.md says "service and role
// may not be written in an override", so an instance naming neither is one.
func isOverride(inst inventory.Instance) bool {
	return inst.Service == ""
}

// credentialsCarriedThemselves returns the credentials this person keeps that
// none of their devices names. Their files are written for the person rather
// than for a device, and are dialed from a machine this inventory does not
// model. Someone with no device file at all is this case for every credential
// they keep.
func credentialsCarriedThemselves(inv *inventory.Root, key string, user inventory.User) []string {
	named := map[string]bool{}
	for _, n := range inv.Nodes {
		if n.Broken == "" && n.Owner == key {
			named[n.CredentialOr()] = true
		}
	}
	var out []string
	for _, credential := range user.CredentialNames() {
		if !named[credential] {
			out = append(out, credential)
		}
	}
	return out
}

// carriedNetworks is every network a user's carried credentials that open
// routeName can dial on: the universal network, then each credential's
// `reaches`, without repeats.
func carriedNetworks(universal string, user inventory.User, carried []string, routeName string) []string {
	var out []string
	if universal != "" {
		out = append(out, universal)
	}
	for _, credential := range carried {
		if !user.OpensRoute(credential, routeName) {
			continue
		}
		for _, network := range user.Credentials[credential].Reaches {
			if !containsString(out, network) {
				out = append(out, network)
			}
		}
	}
	return out
}

// deployKeys is an instance's deploy mapping's own keys, sorted, so an
// inventory with two problems reports them in the same order every time.
// Only the top level is read: what a key's value holds belongs to the
// deployment tool, and guessing at a nested `ports` would report a
// service's own legitimate configuration.
func deployKeys(deploy map[string]any) []string {
	out := make([]string, 0, len(deploy))
	for key := range deploy {
		out = append(out, key)
	}
	sort.Strings(out)
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

// Validate runs every rule in docs/inventory.md#validation but
// rule 15, against an inventory already loaded by inventory.Load, the
// service manifests it names, the derive.Model derive.Derive computed from
// both, and every .previous file's modification time — keyed by the path
// of the secret it is the previous value of, exactly what
// secretstore.PreviousModTimes returns — for rule 16. previous may be nil.
func Validate(inv *inventory.Root, manifests map[string]confgen.Manifest, exports map[string]confgen.Export, model *derive.Model, previous map[string]time.Time) []Issue {
	var issues []Issue
	add := func(format string, args ...any) {
		issues = append(issues, Issue{fmt.Sprintf(format, args...)})
	}

	nodeByID := map[string]inventory.Node{}
	var nodeIDsInOrder []string
	for _, n := range inv.Nodes {
		if n.Broken != "" {
			continue
		}
		if _, dup := nodeByID[n.ID]; dup {
			add("node %q is defined more than once", n.ID)
		} else {
			nodeIDsInOrder = append(nodeIDsInOrder, n.ID)
		}
		nodeByID[n.ID] = n
	}
	sort.Strings(nodeIDsInOrder)

	var userKeys []string
	for k := range inv.Users {
		userKeys = append(userKeys, k)
	}
	sort.Strings(userKeys)

	// realInstances is every authored, non-override instance: the ones a hop
	// can name and a service manifest describes.
	realInstances := map[string]instRef{}
	var realDupes []string
	realPaths := map[string][]string{}
	overridesByNode := map[string][]inventory.Instance{}
	portByInstance := map[string]inventory.Ports{}
	for _, n := range inv.Nodes {
		if n.Broken != "" {
			continue
		}
		for _, inst := range n.Instances {
			if isOverride(inst) {
				overridesByNode[n.ID] = append(overridesByNode[n.ID], inst)
				continue
			}
			if strings.ContainsAny(inst.Name, inventory.QualifiedSep+":") {
				add("node %q: instance id %q may not contain %q or %q; the node is already part of its key", n.ID, inst.Name, inventory.QualifiedSep, ":")
			}
			if _, dup := realInstances[inst.ID]; dup {
				realDupes = append(realDupes, inst.ID)
			}
			where := inst.Path
			if where == "" {
				where = "node " + strconv.Quote(n.ID)
			}
			realPaths[inst.ID] = append(realPaths[inst.ID], where)
			realInstances[inst.ID] = instRef{nodeID: n.ID, inst: inst}
			portByInstance[inst.ID] = inst.Ports
		}
	}
	sort.Strings(realDupes)
	for _, id := range realDupes {
		add("instance %q is defined more than once, in %s", id, strings.Join(realPaths[id], ", "))
	}

	// Rule 1: instance identifiers unique, derived ones included, and
	// universal, if written, names a network in the list.
	if inv.Universal != "" && !containsString(inv.Networks, inv.Universal) {
		add("networks.yaml: universal %q does not name a network in the list", inv.Universal)
	}

	// A derived ID matching an authored override on the same node is the
	// intended pin, not a collision; matching a real instance, or another
	// derived instance, is.
	derivedByID := map[string][]derive.ExportInstance{}
	for _, ci := range model.ExportInstances {
		derivedByID[ci.ID] = append(derivedByID[ci.ID], ci)
	}
	var derivedIDs []string
	for id := range derivedByID {
		derivedIDs = append(derivedIDs, id)
	}
	sort.Strings(derivedIDs)
	for _, id := range derivedIDs {
		cis := derivedByID[id]
		if len(cis) > 1 {
			routes := make([]string, len(cis))
			for i, ci := range cis {
				routes[i] = ci.Route
			}
			add("instance %q is derived more than once, by routes %s", id, strings.Join(routes, ", "))
		}
		if r, ok := realInstances[id]; ok {
			add("instance %q is both a real instance (on node %q) and a derived client instance", id, r.nodeID)
		}
	}

	// Rule 2: instance.service names a service; instance.role names one of
	// its roles; a reached_by names another role of the same service; a
	// shared names one of that role's own secrets.
	var realIDs []string
	for id := range realInstances {
		realIDs = append(realIDs, id)
	}
	sort.Strings(realIDs)
	for _, id := range realIDs {
		r := realInstances[id]
		manifest, ok := manifests[r.inst.Service]
		if !ok {
			add("instance %q: service %q is not defined", id, r.inst.Service)
			continue
		}
		role := manifest
		// An instance's `self` declares the keys of the service's `set`
		// secrets. A name outside the service's declarations, or a key on
		// a name that is not a set, would be a credential sync generates
		// and nothing reads.
		for name := range r.inst.Self {
			decl, ok := role.Self[name]
			if !ok {
				add("instance %q: self %q is not one of %q's own secrets, which are %s",
					id, name, r.inst.Service, strings.Join(role.Self.Names(), ", "))
				continue
			}
			if !decl.Set && len(r.inst.Self[name]) > 0 {
				add("instance %q: self %q takes no keys, since %q does not declare it a set",
					id, name, r.inst.Service)
			}
		}
		// A port hands out the instance's own secrets by name, and a set
		// secret by <name>.<key>. A reference to something else is a value
		// the client never receives, and nothing in the rendered file says
		// it was meant to be there.
		for portName, port := range r.inst.Ports {
			for _, ref := range port.Self {
				sel := inventory.ParseSelfRef(ref)
				decl, ok := role.Self[sel.Name]
				if !ok {
					add("instance %q: port %q hands out %q, which is not one of %q's own secrets, which are %s",
						id, portName, sel.Name, r.inst.Service, strings.Join(role.Self.Names(), ", "))
					continue
				}
				switch {
				case decl.Set && sel.Key == "":
					add("instance %q: port %q hands out %q, which is a set: name one of its keys, as %s.<key>",
						id, portName, sel.Name, sel.Name)
				case !decl.Set && sel.Key != "":
					add("instance %q: port %q hands out %q, but %q is one value and takes no key",
						id, portName, ref, sel.Name)
				}
			}
		}
		// A service's exports are the directories under its own exports/,
		// so one that is listed and missing cannot happen. What can is a
		// directory holding a manifest that renders nothing.
		for _, name := range role.Exports {
			export, ok := exports[confgen.ExportKey(r.inst.Service, name)]
			if !ok {
				continue // its manifest is broken, which confgen reports itself.
			}
			if export.Template == "" {
				add("service %q: export %q declares no template, so it writes nothing", r.inst.Service, name)
			}
		}
		if r.inst.Principal != "" && len(role.Upstream) == 0 {
			add("instance %q: principal %q is unused because service %q declares no upstream", id, r.inst.Principal, r.inst.Service)
		}
	}

	// A principal selects whose credential an instance carries; it never
	// grants the selected user access. The user and every route this instance
	// enters must therefore already be present in users.yaml.
	principalRouteNames := make([]string, 0, len(inv.Routes))
	for routeName := range inv.Routes {
		principalRouteNames = append(principalRouteNames, routeName)
	}
	sort.Strings(principalRouteNames)
	for _, id := range realIDs {
		r := realInstances[id]
		if r.inst.Principal == "" {
			continue
		}
		user, ok := inv.Users[r.inst.Principal]
		if !ok {
			add("instance %q: principal %q is not a user", id, r.inst.Principal)
			continue
		}
		for _, routeName := range principalRouteNames {
			route := inv.Routes[routeName]
			if len(route.Hops) < 2 {
				continue
			}
			enters := false
			for _, raw := range route.Hops[:len(route.Hops)-1] {
				hop, err := derive.ParseHop(raw)
				if err == nil && hop.Instance == id {
					enters = true
					break
				}
			}
			if !enters {
				continue
			}
			// The credential derive hands this instance is the
			// principal's DefaultCredential, so the question is whether
			// that one credential opens the route — not whether the person
			// holds it under some other credential that narrows away from
			// it, and not whether they keep a `default` at all.
			if !user.OpensRoute(inventory.DefaultCredential, routeName) {
				add("instance %q: principal %q's %q credential has no access to route %q",
					id, r.inst.Principal, inventory.DefaultCredential, routeName)
			}
		}
	}

	// Rule 3: a `client`, on a node or on a person with no device file,
	// narrows to one of the forms the services its granted routes enter
	// offer, or is "none". The services reached are the routes' entry hops,
	// not what was derived — a `client` naming nothing they offer derives
	// nothing at all, which is the case this exists to name.
	servicesReached := map[string]map[string]bool{}
	reach := func(who, service string) {
		if servicesReached[who] == nil {
			servicesReached[who] = map[string]bool{}
		}
		servicesReached[who][service] = true
	}
	for _, key := range userKeys {
		for _, routeName := range inv.Users[key].Access {
			route, ok := inv.Routes[routeName]
			if !ok || len(route.Hops) == 0 {
				continue
			}
			hop, err := derive.ParseHop(route.Hops[0])
			if err != nil {
				continue
			}
			entry, ok := realInstances[hop.Instance]
			if !ok {
				continue
			}
			// A credential narrowed to fewer routes does not reach what
			// those other routes enter, and neither does the device
			// carrying it.
			for _, credential := range inv.Users[key].CredentialNames() {
				if inv.Users[key].OpensRoute(credential, routeName) {
					reach(key, entry.inst.Service)
					break
				}
			}
			for _, nodeID := range nodeIDsInOrder {
				n := nodeByID[nodeID]
				if n.Owner == key && inv.Users[key].OpensRoute(n.CredentialOr(), routeName) {
					reach(nodeID, entry.inst.Service)
				}
			}
		}
	}
	// An `export` narrows to one of the ways the services this device
	// reaches offer. It names a directory under one of those services'
	// exports/, and the same name under two services is the point: a device
	// saying "link" takes each service's own link. Naming one none of them
	// offers writes nothing at all, silently, which is the failure this
	// reports.
	checkExport := func(subject, export string, services map[string]bool) {
		if export == "" || export == inventory.ExportNone {
			return
		}
		var names []string
		for s := range services {
			names = append(names, s)
		}
		sort.Strings(names)
		for _, service := range names {
			if containsString(manifests[service].Exports, export) {
				return
			}
		}
		if len(names) > 0 {
			add("%s: export %q is not one of the ways %s is written out", subject, export, strings.Join(names, ", "))
		}
	}
	for _, nodeID := range nodeIDsInOrder {
		n := nodeByID[nodeID]
		checkExport(fmt.Sprintf("node %q", nodeID), n.Export, servicesReached[nodeID])
		if len(n.Profiles) == 0 {
			continue
		}
		// A device with profiles is written out once per profile, and each
		// profile's own `export` takes the device's place. One written
		// beside them would be read by nothing.
		if n.Export != "" {
			add("node %q: export and profiles are not written together; give each profile its own export", nodeID)
		}
		// Profiles are ways a device is written out. A node nobody owns is
		// a machine running services, and nothing is written out for it.
		if n.Owner == "" {
			add("node %q: profiles belong to a device, and this node has no owner", nodeID)
			continue
		}
		owner, ownerKnown := inv.Users[n.Owner]
		for _, name := range n.ProfileNames() {
			p := n.Profiles[name]
			subject := fmt.Sprintf("node %q: profile %q", nodeID, name)
			// The name ends a file name, so it cannot hold a path separator.
			if name == "" || strings.ContainsAny(name, "/\\ \t") {
				add("%s: a profile name ends a file name, so it cannot be empty or hold a slash or a space", subject)
			}
			if p.Export == inventory.ExportNone {
				add("%s: export none writes nothing; remove the profile instead", subject)
			} else {
				checkExport(subject, p.Export, servicesReached[nodeID])
			}
			if _, ok := manifests[p.Runs]; p.Runs != "" && !ok {
				add("%s: runs names service %q, which is not defined", subject, p.Runs)
			}
			// A profile chooses among the routes the device's credential
			// opens, as a credential chooses among its owner's.
			if !ownerKnown {
				continue
			}
			var opens []string
			for _, routeName := range owner.Access {
				if owner.OpensRoute(n.CredentialOr(), routeName) {
					opens = append(opens, routeName)
				}
			}
			for _, routeName := range p.Access {
				if !containsString(opens, routeName) {
					add("%s: access names route %q, which this device's credential does not open; it opens %s",
						subject, routeName, strings.Join(opens, ", "))
				}
			}
		}
	}
	for _, key := range userKeys {
		// A person's own `export` narrows the files they carry themselves,
		// which anyone may have: it is the credentials no device of theirs
		// names, not a property of having no devices at all.
		checkExport(fmt.Sprintf("user %q", key), inv.Users[key].Export, servicesReached[key])
	}

	// Rule 4 and 9: every hop names an existing instance and existing port
	// on it; no route names one instance twice.
	var routeNames []string
	for name := range inv.Routes {
		routeNames = append(routeNames, name)
	}
	sort.Strings(routeNames)
	for _, routeName := range routeNames {
		route := inv.Routes[routeName]
		seen := map[string]bool{}
		for _, raw := range route.Hops {
			hop, err := derive.ParseHop(raw)
			if err != nil {
				add("route %q: %s", routeName, err)
				continue
			}
			if seen[hop.Instance] {
				add("route %q names instance %q twice", routeName, hop.Instance)
			}
			seen[hop.Instance] = true

			ports, ok := portByInstance[hop.Instance]
			if !ok {
				add("route %q: hop %q names an instance that does not exist", routeName, raw)
				continue
			}
			if _, ok := ports[hop.Port]; !ok {
				add("route %q: hop %q: instance %q has no port %q", routeName, raw, hop.Instance, hop.Port)
			}
		}
	}

	// Rule 5: no port named "self".
	for _, id := range realIDs {
		if _, ok := realInstances[id].inst.Ports["self"]; ok {
			add("instance %q: port \"self\" is reserved", id)
		}
	}

	// Rule 23: an instance's runtime, when written, names one of the three
	// values. Nothing else reads it — no branch, no template — so a
	// misspelling is silent everywhere but here, and the error lists the
	// three rather than leaving a reader to guess which word was meant.
	for _, id := range realIDs {
		switch runtime := realInstances[id].inst.Runtime; runtime {
		case "", inventory.RuntimeHost, inventory.RuntimeDocker, inventory.RuntimePodman:
		default:
			add("instance %q: runtime %q is not %q, %q or %q",
				id, runtime, inventory.RuntimeHost, inventory.RuntimeDocker, inventory.RuntimePodman)
		}
	}

	// Rule 32: a containerised instance binds every interface of its
	// container and joins only container networks its node lists; a host
	// process joins none. A node's `containers` names no network twice, each
	// with a subnet that overlaps no other of the node's, and a gateway,
	// when written, inside it. A fixed address sits inside its network's
	// subnet, is not its gateway, and is held by one instance.
	for _, nodeID := range nodeIDsInOrder {
		n := nodeByID[nodeID]
		seen := map[string]bool{}
		var prefixes []netip.Prefix
		var names []string
		for _, c := range n.Containers {
			if c.Name == "" {
				add("node %q: a container network has no name", n.ID)
				continue
			}
			if seen[c.Name] {
				add("node %q: container network %q is listed twice", n.ID, c.Name)
			}
			seen[c.Name] = true
			prefix, err := netip.ParsePrefix(c.Subnet)
			if err != nil {
				add("node %q: container network %q: subnet %q is not a CIDR prefix", n.ID, c.Name, c.Subnet)
				continue
			}
			if c.Gateway != "" {
				if gw, err := netip.ParseAddr(c.Gateway); err != nil || !prefix.Contains(gw) {
					add("node %q: container network %q: gateway %q is not an address inside %s", n.ID, c.Name, c.Gateway, c.Subnet)
				}
			}
			for i, other := range prefixes {
				if other.Overlaps(prefix) {
					add("node %q: container networks %q and %q overlap: %s and %s", n.ID, names[i], c.Name, other, prefix)
				}
			}
			prefixes = append(prefixes, prefix)
			names = append(names, c.Name)
		}
	}
	held := map[string]string{}
	for _, id := range realIDs {
		ref := realInstances[id]
		inst := ref.inst
		if !inst.Containerised() {
			if len(inst.Containers) > 0 {
				add("instance %q runs as a %s process and names container networks %s: only a container joins one",
					id, inventory.RuntimeHost, strings.Join(inst.ContainerNames(), ", "))
			}
			continue
		}
		if inst.Bind != "" && inst.Bind != inventory.ContainerBind {
			add("instance %q runs in a container and binds %q: inside a container only %q is reachable, and what the host publishes is derived from the edges into it",
				id, inst.Bind, inventory.ContainerBind)
		}
		node := nodeByID[ref.nodeID]
		byName := map[string]inventory.ContainerNetwork{}
		for _, c := range node.Containers {
			byName[c.Name] = c
		}
		for _, name := range inst.ContainerNames() {
			c, ok := byName[name]
			if !ok {
				add("instance %q joins container network %q, which node %q does not list in `containers`",
					id, name, ref.nodeID)
				continue
			}
			address := inst.Containers[name]
			if address == "" {
				continue
			}
			a, err := netip.ParseAddr(address)
			if err != nil {
				add("instance %q: address %q on container network %q is not an IP address", id, address, name)
				continue
			}
			if prefix, err := netip.ParsePrefix(c.Subnet); err == nil && !prefix.Contains(a) {
				add("instance %q: address %s on container network %q is outside its subnet %s", id, address, name, c.Subnet)
			}
			if c.Gateway != "" && c.Gateway == address {
				add("instance %q: address %s on container network %q is the network's gateway", id, address, name)
			}
			key := ref.nodeID + "|" + name + "|" + a.String()
			if other, ok := held[key]; ok {
				add("instances %q and %q both hold %s on container network %q", other, id, address, name)
			}
			held[key] = id
		}
	}

	// Rule 35: a dial a service declares resolves for every instance of it
	// that does not write it.
	for _, issue := range derive.ServiceDialIssues(inv, manifests) {
		add("%s", issue)
	}

	// Rule 36: a node's id is its file's name, so the file a reader opens
	// for a node is the one that defines it. A node account names a uid.
	for _, id := range nodeIDsInOrder {
		n := nodeByID[id]
		if base := strings.TrimSuffix(filepath.Base(n.Path), ".yaml"); n.Path != "" && base != n.ID {
			add("node %q is written in %s: name the file %s.yaml", n.ID, n.Path, n.ID)
		}
		for name, a := range n.Accounts {
			if a.UID <= 0 {
				add("node %q: account %q has no uid", n.ID, name)
			}
		}
	}

	// Rule 34: every set's members are routes, and every @set named in an
	// access list is a set.
	setNames := make([]string, 0, len(inv.Sets))
	for name := range inv.Sets {
		setNames = append(setNames, name)
	}
	sort.Strings(setNames)
	for _, name := range setNames {
		for _, member := range inv.Sets[name] {
			if _, ok := inv.Routes[member]; !ok {
				add("set %q names route %q, which does not exist", name, member)
			}
		}
	}
	checkWritten := func(where string, list []string) {
		for _, entry := range list {
			if set, ok := strings.CutPrefix(entry, inventory.SetPrefix); ok {
				if _, defined := inv.Sets[set]; !defined {
					add("%s: access names set %q, which users.yaml does not define under sets", where, set)
				}
			}
		}
	}
	for _, key := range userKeys {
		u := inv.Users[key]
		checkWritten("user "+strconv.Quote(key), u.AccessWritten)
		for _, c := range u.CredentialNames() {
			checkWritten("user "+strconv.Quote(key)+" credential "+strconv.Quote(c), u.Credentials[c].AccessWritten)
		}
	}

	// Rule 33: a route's scope names a network or a node, not both, and its
	// entry is reachable there; a credential opening a route scoped to a
	// network reaches that network.
	networkNames := map[string]bool{}
	for _, n := range inv.Networks {
		networkNames[n] = true
	}
	if inv.Universal != "" {
		networkNames[inv.Universal] = true
	}
	for _, id := range nodeIDsInOrder {
		if networkNames[id] {
			add("node %q has the name of a network, so a route scope %q is ambiguous", id, id)
		}
		for _, c := range nodeByID[id].Containers {
			if networkNames[c.Name] || nodeByID[c.Name].ID != "" {
				add("node %q: container network %q has the name of a network or a node, so a route scope or a name table %q is ambiguous", id, c.Name, c.Name)
			}
		}
	}
	// containerScope is every route scoped to a container network of its
	// entry's node rather than to a network or a node.
	containerScope := map[string]bool{}
	routeKeys := make([]string, 0, len(inv.Routes))
	for name := range inv.Routes {
		routeKeys = append(routeKeys, name)
	}
	sort.Strings(routeKeys)
	for _, name := range routeKeys {
		r := inv.Routes[name]
		if r.Scope == "" || len(r.Hops) == 0 {
			continue
		}
		entry, err := derive.ParseHop(r.Hops[0])
		if err != nil {
			continue
		}
		ref, ok := realInstances[entry.Instance]
		if !ok {
			continue
		}
		entryNode := nodeByID[ref.nodeID]
		switch {
		case networkNames[r.Scope]:
			if _, on := entryNode.Networks[r.Scope]; !on {
				add("route %q is scoped to network %q, and its entry %s is on node %q, which has no address there",
					name, r.Scope, r.Hops[0], ref.nodeID)
			}
		case nodeByID[r.Scope].ID != "":
			if r.Scope != ref.nodeID {
				add("route %q is scoped to node %q, and its entry %s is on node %q",
					name, r.Scope, r.Hops[0], ref.nodeID)
			}
		case containsString(entryNode.ContainerNames(), r.Scope):
			containerScope[name] = true
			if _, joined := ref.inst.Containers[r.Scope]; !joined {
				add("route %q is scoped to container network %q, and its entry %s does not join it",
					name, r.Scope, r.Hops[0])
			}
		default:
			add("route %q is scoped to %q, which is neither a network, a node, nor a container network of node %q", name, r.Scope, ref.nodeID)
		}
	}
	// A credential is carried by a device or a person, and neither is on a
	// container network: what reaches one is a router the model does not
	// carry, so a route scoped to one is opened by nobody.
	for _, key := range userKeys {
		user := inv.Users[key]
		for _, credential := range user.CredentialNames() {
			for _, name := range routeKeys {
				if containerScope[name] && user.OpensRoute(credential, name) {
					add("user %q: credential %q opens route %q, scoped to a container network, which no credential reaches",
						key, credential, name)
				}
			}
		}
	}
	for _, key := range userKeys {
		user := inv.Users[key]
		for _, credential := range user.CredentialNames() {
			for _, name := range routeKeys {
				scope := inv.Routes[name].Scope
				if scope == "" || !networkNames[scope] || scope == inv.Universal || !user.OpensRoute(credential, name) {
					continue
				}
				reached := containsString(user.Credentials[credential].Reaches, scope)
				for _, id := range nodeIDsInOrder {
					n := nodeByID[id]
					if n.Owner != key || n.CredentialOr() != credential {
						continue
					}
					if _, on := n.Networks[scope]; on || containsString(n.Reaches, scope) {
						reached = true
					}
				}
				if !reached {
					add("user %q: credential %q opens route %q, scoped to network %q, and neither it nor a device carrying it reaches %q: add it to `reaches`",
						key, credential, name, scope, scope)
				}
			}
		}
	}

	// Rules 24 and 25: what an instance's `deploy` may say, and where it
	// may say it. A host process renders no deployment file, and neither
	// does an instance of a service that declares no deploy/ — in both
	// cases the mapping would be read by nothing. What it may not hold is
	// a port mapping or a secret: the first is derived from `ports` and
	// the resolved edges, the second stays in the configuration file
	// beside it, and a second spelling of either is the thing the second
	// file exists to remove.
	for _, id := range realIDs {
		inst := realInstances[id].inst
		if inst.Deploy == nil {
			continue
		}
		if !manifests[inst.Service].Deploys {
			add("instance %q writes deploy values, and service %q holds no %s/ directory to render them",
				id, inst.Service, confgen.DeployDir)
		}
		if !inst.Containerised() {
			add("instance %q writes deploy values and runs as a %s process, which renders no deployment file",
				id, inventory.RuntimeHost)
		}
		for _, key := range deployKeys(inst.Deploy) {
			switch key {
			case "port", "ports":
				add("instance %q: deploy %q: a port mapping is derived from ports and from the edges into it, and writing one here is the second truth the deployment file removes",
					id, key)
			case "secret", "secrets":
				add("instance %q: deploy %q: a deployment file carries no credential — it names the rendered configuration beside it",
					id, key)
			}
		}
	}

	// Rule 6: every route in an access list exists; every owner exists.
	for _, key := range userKeys {
		user := inv.Users[key]
		for _, routeName := range user.Access {
			if _, ok := inv.Routes[routeName]; !ok {
				add("user %q: access names route %q, which does not exist", key, routeName)
			}
		}
		// A credential chooses among the routes the person already holds.
		// One naming a route they do not have would grant access from the
		// wrong place: access is the person's, and narrowing is all a
		// credential does with it.
		for _, credential := range user.CredentialNames() {
			for _, routeName := range user.Credentials[credential].Access {
				if !containsString(user.Access, routeName) {
					add("user %q: credential %q names route %q, which is not one of their routes, which are %s",
						key, credential, routeName, strings.Join(user.Access, ", "))
				}
			}
		}
		// `devices: none` asserts this inventory holds no node file for this
		// person. A node file owned by them contradicts it, and the two say
		// opposite things about whether a missing device is deliberate.
		if inv.Users[key].Devices == inventory.DevicesNone {
			var owned []string
			for _, nodeID := range nodeIDsInOrder {
				if nodeByID[nodeID].Owner == key {
					owned = append(owned, nodeID)
				}
			}
			if len(owned) > 0 {
				add("user %q: devices: none says this inventory holds no node file for them, but %s is theirs", key, strings.Join(owned, ", "))
			}
		}
	}
	for _, nodeID := range nodeIDsInOrder {
		owner := nodeByID[nodeID].Owner
		if owner == "" {
			continue
		}
		if _, ok := inv.Users[owner]; !ok {
			add("node %q: owner %q is not a user", nodeID, owner)
		}
		// A device names one of its owner's credentials. Naming one they
		// do not keep would file its secret under a name nothing else
		// refers to, and the device would authenticate as somebody who
		// does not exist.
		n := nodeByID[nodeID]
		if owner == "" {
			continue
		}
		if _, ok := inv.Users[owner]; !ok {
			continue // already reported above.
		}
		credential := n.CredentialOr()
		if !containsString(inv.Users[owner].CredentialNames(), credential) {
			add("node %q: credential %q is not one of %q's credentials, which are %s",
				nodeID, credential, owner, strings.Join(inv.Users[owner].CredentialNames(), ", "))
		}
	}

	// Rule 7: an override's identifier matches an instance the owner's
	// access derives for that node, and it sets neither service nor role.
	for _, nodeID := range nodeIDsInOrder {
		for _, inst := range overridesByNode[nodeID] {
			matched := false
			for _, ci := range derivedByID[inventory.LocalName(inst.ID)] {
				if ci.Node == nodeID {
					matched = true
					break
				}
			}
			if !matched {
				add("node %q: instance %q overrides nothing this node derives (check the route name)", nodeID, inst.ID)
			}
		}
	}
	// An instance on an owned node that sets service or role is not a valid
	// override either — it is neither a real instance (those only exist on
	// unowned, service-hosting nodes by convention) nor a bare override.
	for _, nodeID := range nodeIDsInOrder {
		n := nodeByID[nodeID]
		if n.Owner == "" {
			continue
		}
		for _, inst := range n.Instances {
			if !isOverride(inst) {
				add("node %q: instance %q sets service or role; an override may only set ports and bind", nodeID, inst.ID)
			}
		}
	}

	// fansOut reports whether an instance's service declares that one of
	// its instances is the entrance for several routes. It is the only
	// thing that relaxes rule 8, and it is read from the service rather
	// than the instance so that a proxy does not become a fan-out by
	// accident of how many routes happen to name it.
	fansOut := func(instance string) bool {
		r, ok := realInstances[instance]
		if !ok {
			return false
		}
		m, ok := manifests[r.inst.Service]
		if !ok {
			return false
		}
		return m.FansOut()
	}

	// forwards reports whether an instance's service moves bytes through
	// without terminating them. Like fansOut, it is read from the service:
	// an instance does not become a relay by accident of where it sits in a
	// route.
	forwards := func(instance string) bool {
		r, ok := realInstances[instance]
		if !ok {
			return false
		}
		return manifests[r.inst.Service].Forwards
	}

	// Rule 21: a route does not end on an instance that forwards. Such a
	// route terminates nowhere: the last hop reads nothing it is given and
	// has nowhere to pass it, and the client granted it would be handed a
	// file with an address and no account, since the account belongs to the
	// hop that ends the chain and there is none.
	for _, routeName := range routeNames {
		hops := inv.Routes[routeName].Hops
		if len(hops) == 0 {
			continue
		}
		last, err := derive.ParseHop(hops[len(hops)-1])
		if err != nil {
			continue // rule 4 already reported this hop.
		}
		if forwards(last.Instance) {
			add("route %q ends on %q, which forwards: a relay terminates nothing, so the route needs a hop after it",
				routeName, last.Instance)
		}
	}

	// Rule 8: a non-terminal hop has the same successor in every route
	// through it, unless its service declares `downstreams: many`. A proxy
	// picks its upstream by the name the request arrived at, and every
	// route through it names exactly one, so the fan-out is written down
	// here and resolved before anything runs. Rule-based routing is the
	// other thing — an upstream chosen per request from a rule set this
	// inventory does not hold — and it stays unsupported, so the error
	// separates the two rather than sending a reader to look for a rule
	// set they did not write.
	type successor struct {
		next, route string
	}
	successors := map[string]successor{}
	// downstreamsOf is every hop that follows a fan-out instance, by route,
	// which rules 17 and 18 below then check the published names of.
	type downstream struct {
		route string
		entry string
		hop   derive.Hop
	}
	downstreamsOf := map[string][]downstream{}
	for _, routeName := range routeNames {
		hops := inv.Routes[routeName].Hops
		for i := 0; i+1 < len(hops); i++ {
			cur, err := derive.ParseHop(hops[i])
			if err != nil {
				continue // rule 4 already reported this hop.
			}
			if fansOut(cur.Instance) {
				next, err := derive.ParseHop(hops[i+1])
				if err != nil {
					continue // rule 4 again.
				}
				downstreamsOf[cur.Instance] = append(downstreamsOf[cur.Instance], downstream{route: routeName, entry: cur.Port, hop: next})
				continue
			}
			if prev, ok := successors[cur.Instance]; ok {
				if prev.next != hops[i+1] {
					add("instance %q has different successors in routes %q and %q — rule-based routing is not supported; a service that is one entrance for several routes declares downstreams: many",
						cur.Instance, prev.route, routeName)
				}
				continue
			}
			successors[cur.Instance] = successor{next: hops[i+1], route: routeName}
		}
	}

	// publishedOf is every port's published name, for rules 17 and 18.
	publishedOf := map[string]map[string]string{}
	for _, id := range realIDs {
		names := map[string]string{}
		for name, p := range realInstances[id].inst.Ports {
			names[name] = p.Published
		}
		publishedOf[id] = names
	}

	// dispatchesBy is how a fan-out instance tells its routes apart, which
	// decides which of the two rules below applies to it.
	dispatchesBy := func(instance string) string {
		r, ok := realInstances[instance]
		if !ok {
			return confgen.DispatchName
		}
		return manifests[r.inst.Service].DispatchesBy()
	}

	var fanOutIDs []string
	for id := range downstreamsOf {
		fanOutIDs = append(fanOutIDs, id)
	}
	sort.Strings(fanOutIDs)

	// Rule 17: every port a fan-out instance dispatching by name reaches
	// declares `published`. Without it the proxy has nothing to tell one
	// downstream from another, and it would render a site block with no name
	// to match on. An instance dispatching by port needs no such name: what
	// tells its routes apart is which of its own ports they arrived on.
	for _, id := range fanOutIDs {
		if dispatchesBy(id) != confgen.DispatchName {
			continue
		}
		for _, d := range downstreamsOf[id] {
			if publishedOf[d.hop.Instance][d.hop.Port] == "" {
				add("instance %q reaches %s:%s in route %q, which declares no published name to tell it apart from the other downstreams",
					id, d.hop.Instance, d.hop.Port, d.route)
			}
		}
	}

	// Rule 22: two routes entering the same port of an instance that
	// dispatches by port have the same successor. `downstreams: many` lifts
	// rule 8 for the instance as a whole, and this puts it back one level
	// down, where such a service actually decides: a relay has one next hop
	// per listening port, and two routes disagreeing about it would render
	// two endpoints on one port going to different places.
	for _, id := range fanOutIDs {
		if dispatchesBy(id) != confgen.DispatchPort {
			continue
		}
		type seen struct{ route, next string }
		byEntry := map[string]seen{}
		for _, d := range downstreamsOf[id] {
			next := d.hop.Instance + ":" + d.hop.Port
			prev, ok := byEntry[d.entry]
			if !ok {
				byEntry[d.entry] = seen{route: d.route, next: next}
				continue
			}
			if prev.next != next {
				add("instance %q dispatches by port, and its port %q has different successors in routes %q and %q: a port listens for one next hop",
					id, d.entry, prev.route, d.route)
			}
		}
	}

	// Rule 18: two ports declaring the same published name must not be
	// mistaken for each other. A name is a DNS name, and two services on one
	// machine answering to it on different ports — Shadowsocks on 38250/tcp
	// and Hysteria2 on 443/udp — is an ordinary deployment: a client dialing
	// the name also dials the number. It is broken in three cases. A name
	// resolves to one machine, so ports on two nodes cannot both be reached
	// at it. A reverse proxy tells its downstreams apart by name alone, so a
	// name shared by a port it fronts matches two site blocks. And two ports
	// on one number and transport cannot be told apart by anyone dialing it.
	// Only a fan-out that dispatches by name fronts anything in the sense
	// this rule means. A relay picks its next hop by the port a connection
	// arrived on and matches no name at all, so two ports behind one relay
	// sharing a published name is the ordinary case of one machine answering
	// to one name on two numbers.
	fronted := map[string]bool{}
	for id, ds := range downstreamsOf {
		if dispatchesBy(id) != confgen.DispatchName {
			continue
		}
		for _, d := range ds {
			fronted[d.hop.Instance+":"+d.hop.Port] = true
		}
	}
	type publishedAt struct {
		node, instance, port, endpoint string
		fronted                        bool
	}
	byName := map[string][]publishedAt{}
	for _, id := range realIDs {
		var portNames []string
		for name := range publishedOf[id] {
			portNames = append(portNames, name)
		}
		sort.Strings(portNames)
		for _, name := range portNames {
			if published := publishedOf[id][name]; published != "" {
				p := realInstances[id].inst.Ports[name]
				byName[published] = append(byName[published], publishedAt{
					node: realInstances[id].nodeID, instance: id, port: name,
					endpoint: fmt.Sprintf("%d/%s", p.Number, p.ProtocolOr()),
					fronted:  fronted[id+":"+name],
				})
			}
		}
	}
	var publishedNames []string
	for name := range byName {
		publishedNames = append(publishedNames, name)
	}
	sort.Strings(publishedNames)
	for _, name := range publishedNames {
		at := byName[name]
		for i := 0; i < len(at); i++ {
			for j := i + 1; j < len(at); j++ {
				a, b := at[i], at[j]
				switch {
				case a.node != b.node:
					add("published name %q is declared by %s:%s on %s and %s:%s on %s, and one name reaches one machine",
						name, a.instance, a.port, a.node, b.instance, b.port, b.node)
				case a.fronted || b.fronted:
					add("published name %q is declared by %s:%s and %s:%s, and a proxy in front of one cannot tell them apart",
						name, a.instance, a.port, b.instance, b.port)
				case a.endpoint == b.endpoint:
					add("published name %q is declared by %s:%s and %s:%s, both on %s",
						name, a.instance, a.port, b.instance, b.port, a.endpoint)
				}
			}
		}
	}

	// Rule 19: a target declaring it needs its upstream's shared secrets
	// reaches a port that hands some out. The declaration is the consumer's,
	// so nothing about the port it dials makes it true; asking a port that
	// hands out nothing renders an empty list, and a credential half built
	// from it authenticates nothing. The failure is worth naming here
	// because the rendered file looks complete.
	portSelf := map[string]map[string][]string{}
	for _, n := range inv.Nodes {
		if n.Broken != "" {
			continue
		}
		for _, inst := range n.Instances {
			ports := map[string][]string{}
			for name, p := range inst.Ports {
				ports[name] = p.Self
			}
			portSelf[inst.ID] = ports
		}
	}
	exportInstanceByID := map[string]derive.ExportInstance{}
	for _, ei := range model.ExportInstances {
		exportInstanceByID[ei.ID] = ei
	}
	type sharedNeed struct{ from, to, port string }
	var needs []sharedNeed
	seenNeed := map[sharedNeed]bool{}
	for _, e := range model.Edges {
		from := e.FromInstance
		if from == "" {
			from = e.From.Instance
		}
		var wants confgen.UpstreamDecls
		switch {
		case e.FromInstance != "":
			ei, ok := exportInstanceByID[e.FromInstance]
			if !ok {
				continue
			}
			wants = exports[confgen.ExportKey(ei.Service, ei.Export)].Upstream
		default:
			inst, ok := realInstances[e.From.Instance]
			if !ok {
				continue
			}
			wants = manifests[inst.inst.Service].Upstream
		}
		if !wants.Wants(confgen.UpstreamShared) {
			continue
		}
		// A declaration that says the hop may hand over nothing is not the
		// mistake this rule looks for. See confgen.UpstreamDecl.Optional.
		if wants[confgen.UpstreamShared].Optional {
			continue
		}
		// The port that hands the secrets out is the one the credential is
		// at, which is the hop behind a relay rather than the relay itself:
		// a forwarder holds nothing to hand over. See
		// docs/inventory.md#a-service-that-forwards.
		to := e.Terminal
		if to.Instance == "" {
			to = e.To
		}
		if len(portSelf[to.Instance][to.Port]) > 0 {
			continue
		}
		n := sharedNeed{from: from, to: to.Instance, port: to.Port}
		if seenNeed[n] {
			continue
		}
		seenNeed[n] = true
		needs = append(needs, n)
	}
	sort.Slice(needs, func(i, j int) bool {
		if needs[i].from != needs[j].from {
			return needs[i].from < needs[j].from
		}
		if needs[i].to != needs[j].to {
			return needs[i].to < needs[j].to
		}
		return needs[i].port < needs[j].port
	})
	for _, n := range needs {
		add("instance %q needs the shared secrets of %s:%s, which hands out none",
			n.from, n.to, n.port)
	}

	// Rule 12: a person carrying a credential can use it on the universal
	// network and on the networks that credential names in `reaches`. With
	// neither, a route it opens can be entered from nowhere.
	for _, key := range userKeys {
		user := inv.Users[key]
		carried := credentialsCarriedThemselves(inv, key, user)
		for _, credential := range user.CredentialNames() {
			reaches := user.Credentials[credential].Reaches
			if len(reaches) > 0 && !containsString(carried, credential) {
				add("user %q: credential %q declares reaches, but a device of theirs names it, and the device's own networks apply", key, credential)
			}
			for _, network := range reaches {
				if !containsString(inv.Networks, network) {
					add("user %q: credential %q reaches unknown network %q", key, credential, network)
				}
			}
		}
	}
	for _, key := range userKeys {
		user := inv.Users[key]
		carried := credentialsCarriedThemselves(inv, key, user)
		if len(carried) == 0 {
			continue
		}
		for _, routeName := range user.Access {
			opened := false
			for _, credential := range carried {
				if user.OpensRoute(credential, routeName) {
					opened = true
					break
				}
			}
			if !opened {
				continue
			}
			route, ok := inv.Routes[routeName]
			if !ok || len(route.Hops) == 0 {
				continue // rule 6 already reported the missing route.
			}
			hop, err := derive.ParseHop(route.Hops[0])
			if err != nil {
				continue
			}
			r, ok := realInstances[hop.Instance]
			if !ok {
				continue // rule 4 already reported the missing instance.
			}
			node := nodeByID[r.nodeID]
			tried := carriedNetworks(inv.Universal, user, carried, routeName)
			reachable := false
			for _, network := range tried {
				if _, ok := node.Networks[network]; ok {
					reachable = true
					break
				}
			}
			if !reachable {
				add("user %q: route %q enters %q, which has no address on a network reachable by their carried credential (tried: %s)", key, routeName, hop.Instance, strings.Join(tried, ", "))
			}
		}
	}

	// Rule 13: account names rendered for one port are distinct.
	type portKey struct{ instance, port string }
	seenPorts := map[portKey]bool{}
	for _, g := range model.Grants {
		seenPorts[portKey{g.Instance, g.Port}] = true
	}
	var portKeys []portKey
	for k := range seenPorts {
		portKeys = append(portKeys, k)
	}
	sort.Slice(portKeys, func(i, j int) bool {
		if portKeys[i].instance != portKeys[j].instance {
			return portKeys[i].instance < portKeys[j].instance
		}
		return portKeys[i].port < portKeys[j].port
	})
	for _, k := range portKeys {
		byName := map[string]int{}
		for _, p := range model.Principals(k.instance, k.port) {
			byName[p.Name]++
		}
		var names []string
		for n := range byName {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			if byName[n] > 1 {
				add("%s, port %s: account %q is rendered by more than one principal", k.instance, k.port, n)
			}
		}
	}

	// Rule 14: two instances on one node do not bind the same address and
	// port. Protocol is not modelled yet, so this checks address and port
	// only — a stricter check than the rule asks for, never a looser one.
	type bound struct {
		bind     string
		port     int
		protocol string
	}
	for _, nodeID := range nodeIDsInOrder {
		seen := map[bound][]string{}
		for _, inst := range nodeByID[nodeID].Instances {
			if isOverride(inst) {
				continue // a client's own local listeners, not reachable from outside.
			}
			// A container on a container network binds inside its own
			// network namespace, where nothing else listens: what it holds
			// on the node is the host mapping derived for it.
			var mappings map[string]derive.Mapping
			if inst.Containerised() && len(inst.Containers) > 0 && model != nil {
				mappings = model.Mappings(inv, inst.ID)
			}
			for portName, port := range inst.Ports {
				binds := []string{inst.Bind}
				if mappings != nil {
					binds = mappings[portName].Addresses
				}
				for _, bind := range binds {
					b := bound{bind: bind, port: port.Number, protocol: port.ProtocolOr()}
					seen[b] = append(seen[b], fmt.Sprintf("%s:%s", inst.ID, portName))
				}
			}
		}
		var bounds []bound
		for b := range seen {
			bounds = append(bounds, b)
		}
		sort.Slice(bounds, func(i, j int) bool {
			if bounds[i].bind != bounds[j].bind {
				return bounds[i].bind < bounds[j].bind
			}
			if bounds[i].port != bounds[j].port {
				return bounds[i].port < bounds[j].port
			}
			return bounds[i].protocol < bounds[j].protocol
		})
		for _, b := range bounds {
			names := seen[b]
			if len(names) > 1 {
				sort.Strings(names)
				add("node %q: %s bind the same address, port and protocol (%s:%d/%s)", nodeID, strings.Join(names, ", "), b.bind, b.port, b.protocol)
			}
		}
	}

	// Rule 16: no .previous file older than seven days. previous is keyed
	// by the secret path it is the previous value of, per
	// secretstore.PreviousModTimes.
	var previousPaths []string
	for path := range previous {
		previousPaths = append(previousPaths, path)
	}
	sort.Strings(previousPaths)
	for _, path := range previousPaths {
		age := time.Since(previous[path])
		if age > sevenDays {
			// Days, not a duration: "15014h0m0s" is a number a reader
			// has to divide before it means anything.
			add("%s.previous: %d days old, over the seven-day limit — finish the rotation or delete it", path, int(age.Hours()/24))
		}
	}

	fanOutProxy := func(instance string) bool {
		r, ok := realInstances[instance]
		return ok && manifests[r.inst.Service].FansOut() && manifests[r.inst.Service].DispatchesBy() == confgen.DispatchName
	}
	for _, msg := range networkIssues(inv, nodeByID, fanOutProxy) {
		add("%s", msg)
	}

	return issues
}

// sevenDays is rule 16's staleness limit for a .previous file.
const sevenDays = 7 * 24 * time.Hour

// networkIssues is rules 27 to 31: network ranges, hardware addresses,
// hosts, names and dials.
func networkIssues(inv *inventory.Root, nodeByID map[string]inventory.Node, fansOut func(string) bool) []string {
	var out []string
	add := func(format string, args ...any) { out = append(out, fmt.Sprintf(format, args...)) }

	if inv.HostsBroken != "" {
		add("hosts.yaml will not parse: %s", inv.HostsBroken)
	}

	// Rule 27: subnet and gateway.
	prefixes := map[string]netip.Prefix{}
	for _, name := range inv.Networks {
		n := inv.NetworkInfo[name]
		if n.Subnet == "" {
			if n.Gateway != "" {
				add("network %q has a gateway but no subnet", name)
			}
			continue
		}
		prefix, err := netip.ParsePrefix(n.Subnet)
		if err != nil {
			add("network %q: subnet %q is not a CIDR prefix", name, n.Subnet)
			continue
		}
		prefixes[name] = prefix
		if n.Gateway != "" {
			if gw, err := netip.ParseAddr(n.Gateway); err != nil || !prefix.Contains(gw) {
				add("network %q: gateway %q is not an address inside %s", name, n.Gateway, n.Subnet)
			}
		}
	}

	// Members of every network, nodes and hosts together, for 27 and 28.
	type member struct{ id, network, address, mac string }
	var members []member
	for _, id := range sortedKeys(nodeByID) {
		n := nodeByID[id]
		for network, addr := range n.Networks {
			members = append(members, member{"node " + strconv.Quote(id), network, addr, n.MACs[network]})
		}
	}
	hostIDs := make([]string, 0, len(inv.Hosts))
	for id := range inv.Hosts {
		hostIDs = append(hostIDs, id)
	}
	sort.Strings(hostIDs)
	for _, id := range hostIDs {
		h := inv.Hosts[id]
		// Rule 29.
		if !containsString(inv.Networks, h.Network) && h.Network != inv.Universal {
			add("host %q names network %q, which networks.yaml does not declare", id, h.Network)
		}
		if _, ok := nodeByID[id]; ok {
			add("host %q is also a node identifier", id)
		}
		if _, err := netip.ParseAddr(h.Address); err != nil {
			add("host %q: address %q is not an IP address", id, h.Address)
		}
		members = append(members, member{"host " + strconv.Quote(id), h.Network, h.Address, h.MAC})
	}
	sort.Slice(members, func(i, j int) bool {
		if members[i].network != members[j].network {
			return members[i].network < members[j].network
		}
		return members[i].id < members[j].id
	})
	addrHeld := map[[2]string]string{}
	macHeld := map[[2]string]string{}
	for _, m := range members {
		if prefix, ok := prefixes[m.network]; ok {
			if a, err := netip.ParseAddr(m.address); err == nil && !prefix.Contains(a) {
				add("%s: address %s on network %q is outside %s", m.id, m.address, m.network, prefix)
			}
		}
		// Rule 28.
		if prev, ok := addrHeld[[2]string{m.network, m.address}]; ok {
			add("on network %q, address %s is held by %s and %s", m.network, m.address, prev, m.id)
		} else {
			addrHeld[[2]string{m.network, m.address}] = m.id
		}
		if m.mac == "" {
			continue
		}
		if !validMAC(m.mac) {
			add("%s: mac %q on network %q is not six colon-separated octets", m.id, m.mac, m.network)
		}
		if prev, ok := macHeld[[2]string{m.network, m.mac}]; ok {
			add("on network %q, mac %s is held by %s and %s", m.network, m.mac, prev, m.id)
		} else {
			macHeld[[2]string{m.network, m.mac}] = m.id
		}
	}

	// Rule 30.
	_, conflicts := derive.Names(inv, fansOut)
	out = append(out, conflicts...)
	for _, id := range sortedKeys(nodeByID) {
		_, conflicts := derive.ContainerNames(inv, id, fansOut)
		for _, c := range conflicts {
			out = append(out, fmt.Sprintf("node %q: %s", id, c))
		}
	}

	// Rule 31.
	nodeOf := map[string]inventory.Node{}
	instOf := map[string]inventory.Instance{}
	portsOf := map[string]inventory.Ports{}
	for _, id := range sortedKeys(nodeByID) {
		for _, inst := range nodeByID[id].Instances {
			if inst.Service != "" {
				nodeOf[inst.ID] = nodeByID[id]
				instOf[inst.ID] = inst
				portsOf[inst.ID] = inst.Ports
			}
		}
	}
	for _, id := range sortedKeys(nodeByID) {
		for _, inst := range nodeByID[id].Instances {
			names := make([]string, 0, len(inst.Dials))
			for name := range inst.Dials {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				hop, err := derive.ParseHop(inst.Dials[name])
				if err != nil {
					add("instance %q dials %q as %q, which is not [<node>/]<instance>:<port>", inst.ID, name, inst.Dials[name])
					continue
				}
				to, ok := nodeOf[hop.Instance]
				switch {
				case !ok:
					add("instance %q dials %q at instance %q, which does not exist", inst.ID, name, hop.Instance)
					continue
				case hop.Instance == inst.ID:
					add("instance %q dials itself as %q", inst.ID, name)
					continue
				}
				if _, ok := portsOf[hop.Instance][hop.Port]; !ok {
					add("instance %q dials %q at %s, and %q has no port %q", inst.ID, name, inst.Dials[name], hop.Instance, hop.Port)
					continue
				}
				if _, _, err := derive.ResolveAddress(inv, inst, nodeByID[id], instOf[hop.Instance], to); err != nil {
					add("instance %q dials %q: %s", inst.ID, name, err)
				}
			}
		}
	}
	return out
}

func validMAC(mac string) bool {
	parts := strings.Split(mac, ":")
	if len(parts) != 6 {
		return false
	}
	for _, p := range parts {
		if _, err := strconv.ParseUint(p, 16, 8); err != nil || len(p) != 2 {
			return false
		}
	}
	return true
}

func sortedKeys(m map[string]inventory.Node) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

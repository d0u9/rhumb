package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/d0u9/rhumb/confgen"
	"github.com/d0u9/rhumb/derive"
	"github.com/d0u9/rhumb/inventory"
	"github.com/d0u9/rhumb/render"
	"github.com/d0u9/rhumb/secretstore"
	"github.com/d0u9/rhumb/target"
)

// RenderTarget runs the same rendering the export performs for one target:
// its templates and role defaults from the service directory, and its render
// context — node, instance, upstream, principals and own — from the
// inventory's derivation and the secrets root.
//
// It returns one artefact per file the service declares. A program reading
// two files is still one service, and both are rendered from one defaults
// file and one context, so an account table and the configuration naming it
// cannot disagree about what the instance is.
func (m Renderer) RenderTarget(instance string) ([]artefact, error) {
	t, err := m.findTarget(instance)
	if err != nil {
		return nil, err
	}

	// A target is either a deployment, rendered from its service, or a file
	// written out for a person, rendered from its export. The two live in
	// different directories and have different manifests; everything below
	// this point is the same for both.
	var defaults, dir string
	var files []confgen.File
	// What this target needs from its upstream is the consumer's own
	// declaration: a service and an export each say it for themselves.
	var wants confgen.UpstreamDecls
	var fansOut bool
	switch {
	case t.Export != "":
		export, ok := m.Data.Exports[confgen.ExportKey(t.Service, t.Export)]
		if !ok {
			return nil, fmt.Errorf("%s: export %q is not defined", instance, t.Export)
		}
		if export.Template == "" {
			return nil, fmt.Errorf("%s: export %q writes nothing", instance, t.Export)
		}
		// An export is one file by construction: it is one way of handing
		// one credential over, and a second file would be a second way.
		files = []confgen.File{{Template: export.Template, Output: export.Output}}
		defaults, wants = export.Defaults, export.Upstream
		dir = filepath.Join(m.RootPath, m.Data.ExportDirs[confgen.ExportKey(t.Service, t.Export)])
	default:
		manifest, ok := m.Data.Manifests[t.Service]
		if !ok {
			return nil, fmt.Errorf("%s: service %q is not defined", instance, t.Service)
		}
		files = manifest.Renders()
		if len(files) == 0 {
			return nil, fmt.Errorf("%s: service %q renders nothing", instance, t.Service)
		}
		defaults, wants = manifest.Defaults, manifest.Upstream
		fansOut = manifest.FansOut()
		dir = filepath.Join(m.RootPath, m.Data.ServiceDirs[t.Service])
	}

	defaultsPath := filepath.Join(dir, confgen.DefaultsFilename)
	defaultsBytes, err := os.ReadFile(defaultsPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("reading defaults %s: %w", defaultsPath, err)
	}

	instanceMap, nodeMap := m.instanceAndNode(instance, t.Node)

	var own map[string]any
	if m.SecretsDir != "" {
		own, err = secretstore.ReadSelf(m.SecretsDir, m.secretID(instance))
		if err != nil {
			return nil, err
		}
	}

	principals, err := m.principalsFor(instance)
	if err != nil {
		return nil, err
	}

	// A fan-out instance has one successor per route, so there is no single
	// upstream to resolve: picking one of them would be arbitrary, and the
	// template reads downstreams instead.
	var upstream map[string]any
	var upstreams []map[string]any
	downstreams := m.downstreamsFor(instance, fansOut)
	switch {
	case fansOut:
	case t.Export == "" && m.InstanceByID(instance) == nil:
		// A device profile's program: one process over every route it is
		// rendered with.
		upstreams, err = m.UpstreamsFor(instance, wants)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", instance, err)
		}
		if len(upstreams) == 0 {
			return nil, fmt.Errorf("%s: no route is left to render it with", instance)
		}
		if len(upstreams) == 1 {
			upstream = upstreams[0]
		}
	default:
		upstream, err = m.UpstreamFor(instance, wants)
		if err != nil {
			return nil, err
		}
	}

	links, err := m.linksFor(instance)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", instance, err)
	}

	dials, err := m.dialsFor(instance)
	if err != nil {
		return nil, err
	}

	var members []render.Member
	if inst := m.InstanceByID(instance); inst != nil {
		for _, id := range inst.Members {
			mb, err := m.memberFor(id)
			if err != nil {
				return nil, err
			}
			members = append(members, mb)
		}
	}

	out := make([]artefact, 0, len(files))
	for _, file := range files {
		templatePath := filepath.Join(dir, file.Template)
		templateBytes, err := os.ReadFile(templatePath)
		if err != nil {
			return nil, fmt.Errorf("reading template %s: %w", templatePath, err)
		}
		rendered, err := render.Render(render.Input{
			Target:         render.Target{Service: t.Service, Instance: inventory.LocalName(instance)},
			Template:       string(templateBytes),
			Defaults:       defaultsBytes,
			DefaultsKind:   defaults,
			Instance:       instanceMap,
			Node:           nodeMap,
			Upstream:       upstream,
			Upstreams:      upstreams,
			Downstreams:    downstreams,
			Published:      m.publishedFor(instance),
			PublishedNames: m.publishedNamesFor(instance),
			Dials:          dials,
			Links:          links,
			Members:        members,
			Names:          m.names(t.Node),
			Principals:     principals,
			Self:           own,
		})
		if err != nil {
			return nil, err
		}
		out = append(out, artefact{Output: file.Output, Bytes: rendered, Executable: file.Executable()})
	}
	return out, nil
}

// findTarget locates instance among the tree's targets.
func (m Renderer) findTarget(instance string) (targetRef, error) {
	for _, t := range target.List(m.Data.Inv, m.Data.Derived) {
		if t.Instance != instance {
			continue
		}
		if t.Broken != "" {
			return targetRef{}, fmt.Errorf("%s: %s", instance, t.Broken)
		}
		// An unmanaged user has no node, and their bundle is named for
		// them instead — see docs/export.md#what-is-written.
		node := t.Node
		if node == "" {
			node = t.User
		}
		return targetRef{Node: node, Service: t.Service, Export: t.Export}, nil
	}
	return targetRef{}, fmt.Errorf("%s: not found", instance)
}

type targetRef struct {
	Node    string
	Service string
	Export  string
}

// instanceAndNode builds the instance and node datasources for instance,
// whether it is a real, authored instance or one derive.Derive produced.
func (m Renderer) instanceAndNode(instance, nodeID string) (instanceMap, nodeMap map[string]any) {
	for _, n := range m.Data.Inv.Nodes {
		if n.Broken != "" {
			continue
		}
		for _, inst := range n.Instances {
			if inst.ID == instance && inst.Service != "" {
				return instanceValues(inst, n), nodeValues(n)
			}
		}
	}
	for _, ci := range m.Data.Derived.ExportInstances {
		if ci.ID != instance {
			continue
		}
		instanceMap = map[string]any{"id": ci.ID, "export": ci.Export, "bind": ci.Bind}
		if len(ci.Ports) > 0 {
			instanceMap["ports"] = ci.Ports.Numbers()
		}
		if ci.Profile != "" {
			instanceMap["profile"] = ci.Profile
		}
		if len(ci.Values) > 0 {
			instanceMap["values"] = ci.Values
		}
		if ci.Node != "" {
			for _, n := range m.Data.Inv.Nodes {
				if n.ID == ci.Node && n.Broken == "" {
					nodeMap = nodeValues(n)
				}
			}
		}
		return instanceMap, nodeMap
	}
	return nil, nil
}

func instanceValues(inst inventory.Instance, n inventory.Node) map[string]any {
	v := map[string]any{"id": inventory.LocalName(inst.ID), "service": inst.Service, "bind": inst.Bind}
	// The container networks it joins, in its node's preference order, each
	// with its range and the address fixed there, so a deploy template
	// writes the whole network definition from one list.
	if joined := n.JoinedContainers(inst); len(joined) > 0 {
		containers := make([]any, 0, len(joined))
		for _, c := range joined {
			containers = append(containers, containerValues(c, inst.Containers[c.Name]))
		}
		v["containers"] = containers
	}
	if len(inst.Ports) > 0 {
		// A template asks what an instance listens on, not how: the
		// transport belongs to the model, and a role that needs it reads
		// it from its own defaults.
		v["ports"] = inst.Ports.Numbers()
	}
	if len(inst.Values) > 0 {
		v["values"] = inst.Values
	}
	return v
}

func nodeValues(n inventory.Node) map[string]any {
	networks := map[string]any{}
	for name, addr := range n.Networks {
		networks[name] = addr
	}
	out := map[string]any{"id": n.ID, "networks": networks}
	if len(n.Containers) > 0 {
		containers := make([]any, 0, len(n.Containers))
		for _, c := range n.Containers {
			containers = append(containers, containerValues(c, ""))
		}
		out["containers"] = containers
	}
	if len(n.Accounts) > 0 {
		accounts := map[string]any{}
		for name, a := range n.Accounts {
			gid, group := a.GID, a.Group
			if gid == 0 {
				gid = a.UID
			}
			if group == "" {
				group = name
			}
			accounts[name] = map[string]any{"name": name, "uid": a.UID, "gid": gid, "group": group}
		}
		out["accounts"] = accounts
	}
	return out
}

// containerValues is one container network as a template reads it: its
// name and range, and address when the instance holds a fixed one there.
func containerValues(c inventory.ContainerNetwork, address string) map[string]any {
	v := map[string]any{"name": c.Name, "subnet": c.Subnet}
	if c.Gateway != "" {
		v["gateway"] = c.Gateway
	}
	if address != "" {
		v["address"] = address
	}
	return v
}

// principalsFor reads every per-principal port's accounts and secrets for
// instance.
func (m Renderer) principalsFor(instance string) (map[string][]render.Principal, error) {
	var ports inventory.Ports
	for _, n := range m.Data.Inv.Nodes {
		for _, inst := range n.Instances {
			if inst.ID == instance {
				ports = inst.Ports
			}
		}
	}
	if len(ports) == 0 {
		return nil, nil
	}

	// A role whose template cannot emit two accounts for one principal
	// declares rotation: disruptive — then a .previous value never becomes
	// a second account. See docs/inventory.md#rotation.
	rotatesInPlace := true
	// A service whose inbound side does not authenticate has no account
	// table and no credential under any of its ports: a grant on one of
	// them implies nothing. Grants still exist — they are who may reach
	// here, which is a separate fact from who holds a secret — so this
	// mirrors the same filter secretstore.ImpliedPaths applies, rather than
	// asking the secrets tree for files nothing ever generates. A web
	// service behind a reverse proxy is the case: the proxy holds a grant
	// on its port and no credential for it.
	//
	// Whether a port authenticates is the port's: a link's to port answers
	// from link.to, so a relay that forwards route traffic unread still
	// holds an account per link dialling it. See docs/links.md.
	var role confgen.Manifest
	known := false
	// A process's member is no target of its own, so its service is read
	// from the instance itself.
	service := ""
	if inst := m.InstanceByID(instance); inst != nil {
		service = inst.Service
	} else if t, err := m.findTarget(instance); err == nil {
		service = t.Service
	}
	if service != "" {
		role, known = m.Data.Manifests[service]
		rotatesInPlace = role.Rotation != confgen.RotationDisruptive
	}
	if !known {
		return nil, nil
	}

	out := map[string][]render.Principal{}
	authenticates := false
	for port := range ports {
		if !derive.PortAuthenticates(role, m.Data.Derived, instance, port) {
			continue
		}
		authenticates = true
		for _, p := range m.Data.Derived.Principals(instance, port) {
			path := secretstore.Path{Instance: instance, Port: port, Group: p.Group, Name: p.Slot}
			principal := render.Principal{Name: p.Name}
			// Only a person has a person's two halves. An instance
			// relaying through is its own principal and belongs to
			// nobody, so a template grouping by User skips it rather
			// than filing it under a person who does not exist.
			if p.Kind == derive.PrincipalUser {
				principal.User, principal.Credential = p.Group, p.Slot
			}
			if m.SecretsDir != "" {
				v, err := secretstore.ReadValue(m.SecretsDir, m.storedAt(path))
				if err != nil {
					return nil, err
				}
				principal.Secret = v
			}
			out[port] = append(out[port], principal)

			if m.SecretsDir != "" && rotatesInPlace {
				// A role rendering an account table emits both the current
				// and the previous value as two accounts, so the old
				// credential keeps working until the rotation finishes.
				if v, ok, err := secretstore.ReadPrevious(m.SecretsDir, m.storedAt(path)); err != nil {
					return nil, err
				} else if ok {
					previous := render.Principal{Name: p.Name, Secret: v}
					if p.Kind == derive.PrincipalUser {
						previous.User, previous.Credential = p.Group, p.Slot
					}
					out[port] = append(out[port], previous)
				}
			}
		}
	}
	if !authenticates {
		return nil, nil
	}
	return out, nil
}

// UpstreamFor resolves instance's own upstream, the one edge derive.Derive
// found leaving it, or nil for a terminal instance. wants is what the target
// being rendered declared it needs from that hop; anything it did not ask for
// is not read and does not reach its template.
func (m Renderer) UpstreamFor(instance string, wants confgen.UpstreamDecls) (map[string]any, error) {
	for i := range m.Data.Derived.Edges {
		e := &m.Data.Derived.Edges[i]
		if e.FromInstance == instance || e.From.Instance == instance {
			return m.upstreamVia(instance, e, wants)
		}
	}
	return nil, nil
}

// UpstreamsFor is every upstream of a derived instance that is one process
// over several routes, one per route it is rendered with, in route order.
// Each carries its route and the service it authenticates against.
func (m Renderer) UpstreamsFor(instance string, wants confgen.UpstreamDecls) ([]map[string]any, error) {
	routes := m.routesOf(instance)
	var out []map[string]any
	for _, route := range routes {
		for i := range m.Data.Derived.Edges {
			e := &m.Data.Derived.Edges[i]
			if e.FromInstance != instance || e.Route != route {
				continue
			}
			up, err := m.upstreamVia(instance, e, wants)
			if err != nil {
				return nil, fmt.Errorf("route %s: %w", route, err)
			}
			if up == nil {
				up = map[string]any{}
			}
			terminal := e.Terminal
			if terminal.Instance == "" {
				terminal = e.To
			}
			up["route"] = route
			if inst := m.InstanceByID(terminal.Instance); inst != nil {
				up["service"] = inst.Service
			}
			out = append(out, up)
		}
	}
	return out, nil
}

// upstreamVia resolves the upstream instance reads across edge.
func (m Renderer) upstreamVia(instance string, edge *derive.Edge, wants confgen.UpstreamDecls) (map[string]any, error) {

	// An instance whose own service forwards holds no credential at the hop
	// it dials: derive granted it none, and there is no file under it. It
	// reads an address and a port and moves bytes between them. See
	// docs/inventory.md#a-service-that-forwards.
	relaying := false
	if inst := m.InstanceByID(instance); inst != nil {
		relaying = m.Data.Manifests[inst.Service].Forwards
	}

	var group, slot string
	if e := edge; e.FromInstance != "" {
		// A derived client instance's principal is a device or an unmanaged
		// user — found the same way secretstore paths are.
		group, slot = principalFor(m, instance)
	} else {
		// An authored instance normally owns its upstream identity. A
		// principal binding makes it carry that user's default identity
		// instead; derive has already made the same choice for the grant.
		//
		// A principal naming nobody falls back to the instance's own
		// identity, which is the fallback derive takes too: validate
		// reports the name, and the two must agree on the path until it
		// is fixed, or the check report shows a credential missing from
		// somewhere it was never going to be written.
		group, slot = instance, inventory.DefaultCredential
		if inst := m.InstanceByID(instance); inst != nil && inst.Principal != "" {
			if _, ok := m.Data.Inv.Users[inst.Principal]; ok {
				group = inst.Principal
			}
		}
	}

	// Where the credential comes from. It is the hop this instance dials for
	// every ordinary edge, and the hop behind it when that one forwards: a
	// relay terminates nothing, so a client dials one machine and
	// authenticates against another. derive resolved which, and reading
	// Terminal here is the whole of the difference.
	creds := edge.Terminal
	if creds.Instance == "" {
		creds = edge.To
	}

	// A hop into a service whose inbound side does not authenticate has no
	// credential to read: a grant on one of its ports implies nothing, and
	// there is no file under it. Asking anyway is how a reverse proxy in
	// front of a web service used to fail — it dials a port that
	// authenticates nobody.
	var authenticates bool
	if to := m.InstanceByID(creds.Instance); to != nil && !relaying {
		authenticates = m.Data.Manifests[to.Service].Auth == confgen.AuthPerPrincipal
	}

	var secret string
	if m.SecretsDir != "" && slot != "" && authenticates {
		v, err := secretstore.ReadValue(m.SecretsDir, secretstore.Path{
			Instance: m.secretID(creds.Instance), Port: creds.Port, Group: group, Name: slot,
		})
		if err != nil {
			return nil, err
		}
		secret = v
	}

	// The account name the upstream's own table calls this principal. A
	// protocol whose client sends a user name as well as a secret —
	// Hysteria2's userpass, a share URI's userinfo — needs the name the
	// server will match, not the path segment the secret is filed under.
	account := slot
	for _, g := range m.Data.Derived.Grants {
		if g.Instance == creds.Instance && g.Port == creds.Port &&
			g.Principal.Group == group && g.Principal.Slot == slot {
			account = g.Principal.Name
			break
		}
	}

	out := map[string]any{"address": edge.Address, "port": edge.Port, "secret": secret, "account": account}
	// The name the hop's port answers to, when it has one. A client that
	// dials a service by its own name — ss.example.com for one program,
	// hy2.example.com for another on the same node — reads it here; the
	// address stays the node's, for a template that wants that instead.
	//
	// Only for an edge resolved on the universal network, which is where a
	// name is taken to resolve. An edge on loopback or a private network
	// was given the address that network needs, and a template writing
	// `or published address` must fall back to it rather than send a LAN
	// client out to a public name.
	published := ""
	if edge.Network != "" && edge.Network == m.Data.Inv.Universal {
		published = m.publishedAt(edge.To.Instance, edge.To.Port)
	}
	if published != "" {
		out["published"] = published
	}
	// A secret belongs to the instance it is filed under. It crosses to
	// another only because the program dialling says it needs it, so the
	// question asked here is what this target declared, not what the hop it
	// reaches happens to publish. A reverse proxy in front of a web service
	// declares nothing and is handed nothing.
	if wants.Wants(confgen.UpstreamShared) && m.SecretsDir != "" && !relaying {
		handed := destinationSelf(m, creds.Instance, creds.Port)
		own, err := secretstore.ReadSelf(m.SecretsDir, m.secretID(creds.Instance))
		if err != nil {
			return nil, err
		}
		// In the order the port writes them: the protocols that take more
		// than one take them as a sequence, and a different order
		// authenticates nothing.
		values := make([]string, 0, len(handed))
		for _, ref := range handed {
			v, ok := lookupSelf(own, inventory.ParseSelfRef(ref))
			if !ok {
				return nil, fmt.Errorf("%s: %s needs the shared secrets of %s:%s, and %q is missing",
					creds.Instance, instance, creds.Instance, creds.Port, ref)
			}
			values = append(values, v)
		}
		out["shared"] = values
	}
	// The hop's own values, for a target that declared it needs them. A
	// parameter the two ends have to agree on — a Hysteria2 obfuscation
	// mode, the range a client hops ports over — is written on the instance
	// that listens, and reaching it here is what keeps the client's file
	// from holding a second copy of it. Nothing is filtered: these are
	// configuration, and the secrets a client is given arrive as shared.
	if wants.Wants(confgen.UpstreamValues) {
		if to := m.InstanceByID(creds.Instance); to != nil && len(to.Values) > 0 {
			out["values"] = to.Values
		} else {
			out["values"] = map[string]any{}
		}
	}
	// A forwarded chain has two ends, and a client's file needs both: it
	// dials the relay's address and port, and everything else about the
	// connection — the name on the certificate it must ask for, the name
	// the protocol is published under — belongs to the hop that terminates
	// it. Only written when the two differ, so a template can tell a
	// forwarded route from an ordinary one by asking whether it is there.
	if creds.Instance != edge.To.Instance {
		exit := map[string]any{"instance": creds.Instance, "port": creds.Port}
		if to := m.InstanceByID(creds.Instance); to != nil {
			exit["number"] = to.Ports[creds.Port].Number
			if p := to.Ports[creds.Port].Published; p != "" {
				exit["published"] = p
			}
		}
		out["exit"] = exit
	}
	return out, nil
}

// destinationSelf is what instance's port hands to everything granted on
// it, in the order it writes them, or nil when it hands out nothing. See
// docs/inventory.md#a-secret-several-people-hold.
func destinationSelf(m Renderer, instance, port string) []string {
	for _, n := range m.Data.Inv.Nodes {
		if n.Broken != "" {
			continue
		}
		for _, inst := range n.Instances {
			if inst.ID == instance {
				return inst.Ports[port].Self
			}
		}
	}
	return nil
}

// lookupSelf reads one reference out of the nested self datasource. A
// reference never names a field: half a credential is not a thing to hand
// over, so a name with fields is not found here.
func lookupSelf(self map[string]any, ref inventory.SelfRef) (string, bool) {
	switch v := self[ref.Name].(type) {
	case string:
		return v, ref.Key == ""
	case map[string]any:
		if ref.Key == "" {
			return "", false
		}
		s, ok := v[ref.Key].(string)
		return s, ok
	}
	return "", false
}

// principalFor finds the principal kind and identifier a derived client
// instance grants as, on its own entry edge.
func principalFor(m Renderer, instance string) (group, slot string) {
	for _, ci := range m.Data.Derived.ExportInstances {
		if ci.ID != instance {
			continue
		}
		if ci.Node == "" {
			return ci.User, ci.Credential
		}
		for _, n := range m.Data.Inv.Nodes {
			if n.ID == ci.Node && n.Broken == "" {
				return n.Owner, ci.Credential
			}
		}
		return "", ""
	}
	return "", ""
}

// Renderer is everything rendering one target needs and nothing else: the
// loaded inventory, the root its templates sit under, and the secrets store.
// It is deliberately not a TUI model — the same rendering serves the inspect
// page and `rhumb export` on the command line, and only one of those has a
// cursor.
type Renderer struct {
	Data       Loaded
	RootPath   string
	SecretsDir string
	// SecretInstance, when set, maps an instance ID to the one its secrets
	// are stored under: a migration preview reads a renamed node's secrets
	// where they are before apply moves them.
	SecretInstance func(string) string
	// Routes, when it holds an instance, narrows the routes that
	// instance is rendered with: a profile's program over only some of the
	// routes it may take, this time. Unset renders every route.
	Routes map[string][]string
}

// routesOf is the routes a derived instance is rendered with: its own,
// narrowed by m.Routes.
func (m Renderer) routesOf(instance string) []string {
	for _, ci := range m.Data.Derived.ExportInstances {
		if ci.ID != instance {
			continue
		}
		only, narrowed := m.Routes[instance]
		if !narrowed {
			return ci.Routes
		}
		var out []string
		for _, r := range ci.Routes {
			if containsString(only, r) {
				out = append(out, r)
			}
		}
		return out
	}
	return nil
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// storedAt is where p is stored, after secretInstance.
func (m Renderer) storedAt(p secretstore.Path) secretstore.Path {
	p.Instance = m.secretID(p.Instance)
	return p
}

// secretID is the instance ID whose directory holds instance's secrets.
func (m Renderer) secretID(instance string) string {
	if m.SecretInstance == nil {
		return instance
	}
	return m.SecretInstance(instance)
}

// downstreamsFor is the hop that follows this instance in each route through
// it, resolved: what a reverse proxy renders one site block from. It is empty
// unless the service declared that it fans out, so a template that asks and
// was not meant to renders nothing rather than another instance's business.
//
// Ordered by route name, so a rendered file does not change because a route
// was added above another.
func (m Renderer) downstreamsFor(instance string, fansOut bool) []render.Downstream {
	if !fansOut {
		return nil
	}
	var out []render.Downstream
	for _, e := range m.Data.Derived.Edges {
		if e.From.Instance != instance {
			continue
		}
		d := render.Downstream{
			Route:     inventory.FlatID(e.Route),
			Instance:  inventory.LocalName(e.To.Instance),
			Port:      e.To.Port,
			Published: m.publishedAt(e.To.Instance, e.To.Port),
			Address:   e.Address,
			Number:    e.Port,
			Entry:     e.From.Port,
			Link:      e.Link,
		}
		if from := m.InstanceByID(instance); from != nil {
			d.EntryNumber = from.Ports[e.From.Port].Number
		}
		if to := m.InstanceByID(e.To.Instance); to != nil {
			port := to.Ports[e.To.Port]
			d.Title, d.Description = port.Title, port.Description
			proxy := map[string]any{}
			for k, v := range m.Data.Manifests[to.Service].Proxy {
				proxy[k] = v
			}
			for k, v := range port.Proxy {
				proxy[k] = v
			}
			d.Proxy = proxy
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Route < out[j].Route })
	return out
}

// publishedFor is this instance's own ports' published names. The service
// behind a proxy renders the same string the proxy matches on — Vaultwarden's
// DOMAIN is the case it exists for — so both sides read one value.
func (m Renderer) publishedFor(instance string) map[string]string {
	inst := m.InstanceByID(instance)
	if inst == nil {
		return nil
	}
	out := map[string]string{}
	for name, p := range inst.Ports {
		if p.Published != "" {
			out[name] = p.Published
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// publishedAt is one port's published name, or empty. Rule 17 has already
// reported a fan-out downstream that has none, so rendering an empty match
// here does not hide anything.
func (m Renderer) publishedAt(instance, port string) string {
	inst := m.InstanceByID(instance)
	if inst == nil {
		return ""
	}
	return inst.Ports[port].Published
}

// InstanceByID finds a real instance in the inventory.
func (m Renderer) InstanceByID(instance string) *inventory.Instance {
	for _, n := range m.Data.Inv.Nodes {
		for i := range n.Instances {
			if n.Instances[i].ID == instance {
				return &n.Instances[i]
			}
		}
	}
	return nil
}

// artefact is one file a render writes: the rendered bytes, the name they
// are written under, and whether the name says it is a script. A service, an
// export and a deployment all produce these, so what publishes them does not
// have to know which of the three it is holding.
type artefact struct {
	Output     string
	Bytes      []byte
	Executable bool
}

// deployArtefact is what a deployment's render returns. It is artefact: the
// two were one thing as soon as a service could write several files too.
type deployArtefact = artefact

// deployFor is what a containerised instance writes beside its
// configuration: the file a deployment tool reads, and whatever else has to
// be in place before that tool runs — the step that puts the rendered
// configuration where the runtime expects it being the case this exists for.
// It returns nothing at all for an instance that renders only its
// configuration — a host process, a file written out for a person, or an
// instance of a service holding no deploy/ — so a caller asks without
// checking first.
//
// Every file of one deployment is rendered from one view: the instance's
// own, plus the two things only the model can resolve — the host mapping of
// each port, and the downstreams of a service that fans out — and one
// merged deploy/defaults.yaml, so a script and the compose file beside it
// cannot disagree about what the instance deploys with. No secret reaches
// any of them: the credential is in the file beside them, and a deploy
// template names that file rather than repeating what is in it. See
// docs/export.md#a-second-file-what-deploys-it.
func (m Renderer) deployFor(instance string) ([]deployArtefact, error) {
	t, err := m.findTarget(instance)
	if err != nil {
		return nil, err
	}
	if t.Export != "" {
		return nil, nil
	}
	inst := m.InstanceByID(instance)
	if inst == nil || !inst.Containerised() {
		return nil, nil
	}
	deploy, ok := m.Data.Deploys[t.Service]
	if !ok {
		return nil, nil
	}
	if len(deploy.Files) == 0 {
		return nil, fmt.Errorf("%s: service %q deploys nothing: its deploy/confgen.yaml names no file", instance, t.Service)
	}

	dir := filepath.Join(m.RootPath, m.Data.DeployDirs[t.Service])
	defaultsPath := filepath.Join(dir, confgen.DefaultsFilename)
	defaultsBytes, err := os.ReadFile(defaultsPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("reading defaults %s: %w", defaultsPath, err)
	}

	// The instance as every other render sees it, plus what only a
	// deployment reads: what delivers the process, and the values that
	// start it.
	var node inventory.Node
	for _, n := range m.Data.Inv.Nodes {
		if n.Broken == "" && n.ID == t.Node {
			node = n
		}
	}
	instanceMap := instanceValues(*inst, node)
	instanceMap["runtime"] = inst.RuntimeOr()
	overlay := inst.Deploy
	if overlay == nil {
		// Non-nil and empty: this instance writes no `deploy`, so the
		// service's own deployment defaults stand alone. Nil would mean
		// the instance's `values` merge instead, which configure the
		// program rather than start it.
		overlay = map[string]any{}
	}
	instanceMap["deploy"] = overlay

	mapping := map[string]render.Mapping{}
	for port, hm := range m.Data.Derived.Mappings(m.Data.Inv, instance) {
		mapping[port] = render.Mapping{Addresses: hm.Addresses, Number: hm.Number}
	}

	var nodeMap map[string]any
	if node.ID != "" {
		nodeMap = nodeValues(node)
	}

	out := make([]deployArtefact, 0, len(deploy.Files))
	for _, file := range deploy.Files {
		templatePath := filepath.Join(dir, file.Template)
		templateBytes, err := os.ReadFile(templatePath)
		if err != nil {
			return nil, fmt.Errorf("reading template %s: %w", templatePath, err)
		}
		rendered, err := render.Render(render.Input{
			Target:         render.Target{Service: t.Service, Instance: inventory.LocalName(instance)},
			Template:       string(templateBytes),
			Defaults:       defaultsBytes,
			DefaultsKind:   deploy.Defaults,
			Instance:       instanceMap,
			Overlay:        overlay,
			Node:           nodeMap,
			Downstreams:    m.downstreamsFor(instance, m.Data.Manifests[t.Service].FansOut()),
			Published:      m.publishedFor(instance),
			PublishedNames: m.publishedNamesFor(instance),
			Names:          m.names(t.Node),
			Mapping:        mapping,
		})
		if err != nil {
			return nil, err
		}
		out = append(out, deployArtefact{Output: file.Output, Bytes: rendered, Executable: file.Executable()})
	}
	return out, nil
}

// dialsFor is this instance's `dials`, resolved as an edge would be. See
// docs/inventory.md#dialling-a-service-that-is-not-on-a-route.
func (m Renderer) dialsFor(instance string) (map[string]render.Downstream, error) {
	from, fromNode := m.realInstance(instance)
	if from == nil || len(from.Dials) == 0 {
		return nil, nil
	}
	out := map[string]render.Downstream{}
	for name, value := range from.Dials {
		hop, err := derive.ParseHop(value)
		if err != nil {
			return nil, fmt.Errorf("%s: dial %q: %w", instance, name, err)
		}
		to, toNode := m.realInstance(hop.Instance)
		if to == nil {
			return nil, fmt.Errorf("%s: dial %q: no instance %q", instance, name, hop.Instance)
		}
		addr, _, err := derive.ResolveAddress(m.Data.Inv, *from, fromNode, *to, toNode)
		if err != nil {
			return nil, fmt.Errorf("%s: dial %q: %w", instance, name, err)
		}
		out[name] = render.Downstream{
			Instance:  inventory.LocalName(hop.Instance),
			Port:      hop.Port,
			Published: to.Ports[hop.Port].Published,
			Address:   addr,
			Number:    to.Ports[hop.Port].Number,
		}
	}
	return out, nil
}

// realInstance finds a real instance and the node it runs on.
func (m Renderer) realInstance(instance string) (*inventory.Instance, inventory.Node) {
	for _, n := range m.Data.Inv.Nodes {
		for i := range n.Instances {
			if n.Instances[i].ID == instance && n.Instances[i].Service != "" {
				return &n.Instances[i], n
			}
		}
	}
	return nil, inventory.Node{}
}

// names is every network's name table and each container network's on
// node, for the names template function. Rule 33 keeps a container
// network's name from being a network's, so the two never share a key.
func (m Renderer) names(node string) map[string][]render.Name {
	fansOut := func(instance string) bool {
		inst := m.InstanceByID(instance)
		return inst != nil && m.Data.Manifests[inst.Service].FansOut() && m.Data.Manifests[inst.Service].DispatchesBy() == confgen.DispatchName
	}
	tables, _ := derive.Names(m.Data.Inv, fansOut)
	containers, _ := derive.ContainerNames(m.Data.Inv, node, fansOut)
	out := make(map[string][]render.Name, len(tables)+len(containers))
	for _, from := range []map[string][]derive.Name{tables, containers} {
		for network, list := range from {
			for _, n := range list {
				out[network] = append(out[network], render.Name{Name: n.Name, Address: n.Address})
			}
		}
	}
	return out
}

// publishedNamesFor is every name each of this instance's ports is
// published at.
func (m Renderer) publishedNamesFor(instance string) map[string][]string {
	inst := m.InstanceByID(instance)
	if inst == nil {
		return nil
	}
	out := map[string][]string{}
	for name, p := range inst.Ports {
		if len(p.Names) > 0 {
			out[name] = p.Names
		}
	}
	return out
}

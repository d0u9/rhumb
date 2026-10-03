// Package render turns one target into rendered bytes: a role's defaults,
// the render context docs/inventory.md#the-render-context pins,
// and the target itself, run through a template. It knows nothing of the
// TUI or the filesystem layout — Input carries everything it needs as
// already-decoded values and bytes.
//
// The rules are in docs/export.md#rendering.
package render

import (
	"bytes"
	"fmt"
	"text/template"

	"gopkg.in/yaml.v3"

	"github.com/d0u9/rhumb/confgen"
)

// Target names what is being rendered, for error messages and for the
// "target" template function.
type Target struct {
	Service  string
	Instance string
}

// String is service/role/instance, as docs/export.md#targets-and-selectors
// writes it.
func (t Target) String() string {
	return t.Service + "/" + t.Instance
}

// Principal is one account a per-principal port grants, with its secret —
// what docs/inventory.md#the-render-context's principals
// datasource holds per entry.
type Principal struct {
	Name   string
	Secret string
	// User is the person this credential belongs to, and Credential which
	// of theirs it is — the two halves Name is built from. A template
	// rendering something per person rather than per credential reads
	// User: a home directory belongs to whoever owns it, and two of their
	// credentials are two ways into one directory rather than two
	// directories. Both are empty for a principal that is not a person's,
	// such as an instance relaying through.
	User       string
	Credential string
}

// Grantee is one person holding grants on a port, with every account name
// their credentials produce there. It is the principals datasource grouped
// by holder: a service rendering something per person rather than per
// credential — a home directory, an entry in a name map — needs the group,
// and computing it in a template means building a set by hand.
//
// Principals that belong to nobody, such as an instance relaying through,
// are not grantees and are absent.
type Grantee struct {
	// User is the person, as users.yaml keys them.
	User string
	// Accounts is their account names on this port, in the order the
	// principals datasource holds them.
	Accounts []string
}

// Downstream is one hop that follows a fan-out instance: the far end of one
// route through it, resolved the same way an upstream is. A reverse proxy
// renders one site block per Downstream, matching on Published and
// forwarding to Address and Port.
type Downstream struct {
	// Route is the route this hop belongs to. Downstreams are ordered by
	// it, so a rendered file does not change because a route was added
	// above another.
	Route string
	// Instance and Port name the hop, for a template that wants to label
	// what it forwards to.
	Instance string
	Port     string
	// Published is the name the request arrived at: what tells this
	// downstream from the others sharing the entrance, for an instance that
	// dispatches by name.
	Published string
	// Entry and EntryNumber are the port of this instance that the route
	// came in on: its name, and the number it listens on. They are what
	// tells one downstream from another for an instance that dispatches by
	// port — a relay writes the pair as one endpoint, listening on Entry
	// and sending to Address.
	Entry       string
	EntryNumber int
	// Link names the link this hop's edge rides, when it rides one; Address
	// is then the target's as the link's far end dials it. See
	// docs/links.md#the-render-context.
	Link string
	// Address is the far end, chosen the way every other edge is: loopback
	// when the two ends share a node, otherwise the downstream's address on
	// the first network in preference order that the proxy reaches.
	Address string
	Number  int
	// Title and Description are the downstream port's own, for a page
	// listing what this proxy serves.
	Title       string
	Description string
	// Proxy is what the downstream asks of the proxy in front of it: its
	// service's hints, overridden by its port's.
	Proxy map[string]any
}

// Name is one entry of a network's name table.
type Name struct {
	Name    string
	Address string
}

// Mapping is where a container runtime publishes one of this instance's
// ports on the machine it runs on: the addresses it binds, and the number,
// which is the port's own on both sides. A deploy template reads one as
// `mapping "<port>"` and writes one published port per address. It is
// derived from the model and never written; see
// docs/export.md#a-second-file-what-deploys-it.
type Mapping struct {
	// Addresses is every address this port is published at, in the
	// inventory's network preference order. It is a list because a machine
	// on two networks serves both, and a port reached from its own node
	// and from another needs loopback beside the address that other
	// machine dials.
	Addresses []string
	Number    int
}

// Input is one target's render context, plus the template it renders. Every
// field but Template, Defaults and DefaultsKind corresponds to one of
// docs/inventory.md#the-render-context's datasources; node,
// instance, upstream, own and target are read-only template functions
// returning them.
type Input struct {
	Target Target

	// Template is the role's template text.
	Template string

	// Defaults is the role's defaults.yaml content. Nil or empty means no
	// defaults.
	Defaults []byte
	// DefaultsKind is confgen.DefaultsDocument or confgen.DefaultsElement,
	// and decides how Defaults and Instance combine. See
	// docs/export.md#two-kinds-of-defaults.
	DefaultsKind string

	// Instance is the render context's instance datasource: id, service,
	// role, ports, bind. It is also what Defaults merges with or hands to
	// the template, replacing what used to be a separate values file.
	Instance map[string]any
	// Node is the node this instance runs on: id, networks. Nil for an
	// unmanaged user's derived instance, who has no node.
	Node map[string]any
	// Upstream is the next hop, resolved: address, port, the account name
	// this instance connects as, and its secret. When this target's own
	// manifest declares it needs them, it also holds the secrets that hop's
	// port hands to everything granted on it, in that port's order, as
	// shared, and that hop's instance's own values as values. A value
	// crosses between instances because the program dialling says it needs
	// it, never because the one listening publishes it, so each of the two
	// is absent from every target that did not ask. Nil for a terminal
	// instance.
	Upstream map[string]any
	// Upstreams is every upstream of a program that is one process over
	// several routes — a device profile that runs it — each as Upstream
	// holds one, with its route and the service it authenticates against.
	// Upstream is set too when there is exactly one; with more, asking for
	// upstream is an error naming them, since the program takes one.
	Upstreams []map[string]any
	// Downstreams is the render context's downstreams datasource: for an
	// instance whose service declares downstreams: many, the hop that
	// follows it in each route through it, resolved. Empty for every other
	// instance, so a template that asks and was not meant to fan out
	// renders nothing rather than something wrong.
	Downstreams []Downstream
	// Published is this instance's own ports' published names, by port. A
	// service behind a proxy renders the same string the proxy matches on —
	// Vaultwarden's DOMAIN is the case it exists for — so the name has to
	// reach both, and a port without one is absent. It is its own field
	// rather than a key inside Instance's ports so that a template reads it
	// the way it reads an account table, with published "<port>".
	Published map[string]string
	// PublishedNames is every name each port is published at, for a proxy
	// answering to its own sites; read with publishedNames "<port>".
	PublishedNames map[string][]string
	// Dials is this instance's `dials`, resolved, by the caller's name for
	// each. A template reads one with dial "<name>".
	Dials map[string]Downstream
	// Names is every network's name table, by network, read with
	// names "<network>".
	Names map[string][]Name
	// Members is, for a process, each instance it runs, in ID order, with
	// what that instance would have been given on its own. Empty for every
	// other instance. See docs/inventory.md#processes.
	Members []Member
	// Links is every link this instance ends, by name, read with links or
	// link "<name>". Empty for an instance ending none.
	Links []Link
	// Principals is every port with auth: per-principal, each to the
	// accounts and secrets of everything holding a grant on it.
	Principals map[string][]Principal
	// Overlay is what lays over Defaults for a document render, when it is
	// not the instance's own values: a deploy template merges the
	// instance's `deploy` mapping instead, key by key, exactly as values
	// lays over the service's own defaults. Nil means values, which is
	// every other render — so an empty, non-nil map is how a deploy render
	// of an instance that writes no `deploy` says the defaults stand alone.
	Overlay map[string]any
	// Mapping is this instance's ports' host mappings, by port. It is what
	// a deploy template is rendered for, and it is absent from every other
	// render: a configuration file describes what the program listens on
	// inside its own namespace, which is what Instance already says.
	Mapping map[string]Mapping
	// Self is the instance's own secrets, mirroring the tree under
	// <instance>/self/: a string where the name is one file, a map where it
	// is a set, a record of fields, or both. It is what the secret template
	// function reads from.
	Self map[string]any
}

// Render runs Template over Input and returns the rendered bytes. Every
// error — a template that will not parse or execute, a missing secret — is
// wrapped with Input.Target so a failure among many targets names which one
// it was.
func Render(in Input) ([]byte, error) {
	out, err := render(in)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", in.Target, err)
	}
	return out, nil
}

func render(in Input) ([]byte, error) {
	defaults, err := decodeMapping(in.Defaults, "defaults")
	if err != nil {
		return nil, err
	}

	instance := in.Instance
	if instance == nil {
		instance = map[string]any{}
	}

	var root map[string]any
	switch in.DefaultsKind {
	case confgen.DefaultsDocument:
		// The instance's own values win over the whole document. It is
		// values that merge, not the instance map: an instance's other keys
		// are what rhumb itself needs — id, service, bind, ports — and are
		// reached through the instance function, so merging them would put
		// them in the document under names a service never declared, and
		// would leave the keys a service does declare unreachable, because
		// values is the only place a node file may write them.
		values, _ := instance["values"].(map[string]any)
		if in.Overlay != nil {
			values = in.Overlay
		}
		root = mergeInto(values, defaults)
	case confgen.DefaultsElement:
		// The defaults are one entry of a list, and it is for the template to
		// apply to each entry of whichever list that is — see the open
		// question in docs/export.md#open-questions. The instance
		// reaches the template unmerged, and defaults() hands over the raw
		// defaults.
		root = instance
	default:
		return nil, fmt.Errorf("unknown defaults kind %q", in.DefaultsKind)
	}

	tmpl, err := template.New(in.Target.String()).Funcs(funcs(defaults, in)).Parse(in.Template)
	if err != nil {
		return nil, fmt.Errorf("parsing template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, root); err != nil {
		return nil, fmt.Errorf("rendering: %w", err)
	}
	return buf.Bytes(), nil
}

// decodeMapping parses YAML that must be a mapping, or nothing when data is
// empty.
func decodeMapping(data []byte, what string) (map[string]any, error) {
	if len(data) == 0 {
		return map[string]any{}, nil
	}
	var m map[string]any
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", what, err)
	}
	if m == nil {
		return nil, fmt.Errorf("%s: not a mapping", what)
	}
	return m, nil
}

// DeployValues is the document a deployment template renders against: the
// service's deploy/defaults.yaml with the instance's `deploy` laid over it,
// the same merge Render performs for a document render. The export's
// manifest reads it so that it carries exactly what the templates saw.
func DeployValues(defaults []byte, overlay map[string]any) (map[string]any, error) {
	d, err := decodeMapping(defaults, "defaults")
	if err != nil {
		return nil, err
	}
	return mergeInto(overlay, d), nil
}

// Link is one link an instance ends, from that end's side. See
// docs/links.md#the-render-context.
type Link struct {
	Name string
	// End is "from" or "to": which end this instance is.
	End string
	// Peer is the other end.
	Peer LinkPeer
	// Carries is every mapping riding the link, the same list at both ends,
	// in key order.
	Carries []LinkMapping
}

// LinkPeer is a link's other end. For the from end it is the port dialled:
// Port, Number, Address and Published, the account this instance connects
// as and its secret, and Shared and Values as link.from declares. For the to
// end it is the instance and the account it connects as.
type LinkPeer struct {
	Instance  string
	Port      string
	Number    int
	Address   string
	Published string
	Account   string
	Secret    string
	Shared    []string
	Values    map[string]any
}

// LinkMapping is one entrance and target that route traffic crosses a link
// between. Key is stable, for a name a template generates.
type LinkMapping struct {
	Key      string
	Entrance LinkEndpoint
	Target   LinkEndpoint
	// Routes is every route using this mapping, flattened as Route is.
	Routes []string
}

// LinkEndpoint is one end of a LinkMapping. Address is set on the target
// only: the address the link's far end dials it at. Published is the
// entrance's name, for an entrance dispatching by name.
type LinkEndpoint struct {
	Instance  string
	Port      string
	Number    int
	Protocol  string
	Address   string
	Published string
}

// Member is one instance a process runs, as a template for the process reads
// it with members. Each field is what the instance's own render would carry.
type Member struct {
	Name        string
	Service     string
	Instance    map[string]any
	Upstream    map[string]any
	Downstreams []Downstream
	Links       []Link
	Principals  map[string][]Principal
	Published   map[string]string
	Dials       map[string]Downstream
	// Self is the member's own secrets, what `secret` reads for an instance
	// rendered on its own: a Shadowsocks port's PSK, a Hysteria2 obfs
	// password. The process's own secret reads the process, which has none.
	Self map[string]any
}

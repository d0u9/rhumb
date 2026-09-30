// Package inventory reads a generator root's inventory: nodes/*.yaml,
// users.yaml, routes.yaml and networks.yaml, parsed into one model. It only
// parses — no derivation and no validation. A file that will not parse is
// listed with its error rather than dropped, as confgen already does for a
// broken manifest.
//
// The model is docs/inventory.md.
package inventory

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// NodesDir is the generator root's subdirectory holding one file per node.
const NodesDir = "nodes"

// UsersFilename, RoutesFilename and NetworksFilename name the generator
// root's other inventory files.
const (
	UsersFilename    = "users.yaml"
	RoutesFilename   = "routes.yaml"
	NetworksFilename = "networks.yaml"
	HostsFilename    = "hosts.yaml"
)

// DevicesNone is the one value a user's `devices` key may take: this person
// has no node file in this inventory, and that is deliberate. It says only
// that, so it exists to tell a person with no devices apart from one whose
// device files were misplaced, which otherwise look identical. It decides
// nothing: a credential no device names is carried by the person whether or
// not this is written, and writing it beside a device file is a contradiction
// validate reports.
const DevicesNone = "none"

// DefaultCredential is the credential a person has when they declare none,
// and the one a device uses when it names none. A name like any other: it is
// written out in secret paths, in account names and in the picture, and it
// is simply the one you get without choosing.
const DefaultCredential = "default"

// ExportNone is the node.export value meaning: write nothing for this
// device, regardless of what the services it reaches offer.
const ExportNone = "none"

// Runtime values an instance's `runtime` may take: what delivers the
// process. RuntimeHost is the default and is not written. A containerised
// instance binds ContainerBind, joins a container network of its node, and is
// what a deployment file is rendered for. See
// docs/inventory.md#what-runs-the-process.
const (
	RuntimeHost   = "host"
	RuntimeDocker = "docker"
	RuntimePodman = "podman"
)

// Networks is a node's `networks` key: a mapping of network name to this
// node's address on it. It says where others can reach this node.
//
// An entry is an address, or {address, mac} for a network that hands
// addresses out by hardware address; the mac is kept in Node.MACs.
type Networks map[string]string

// UnmarshalYAML accepts both spellings of an entry.
func (ns *Networks) UnmarshalYAML(value *yaml.Node) error {
	entries, err := decodeAddresses(value)
	if err != nil {
		return err
	}
	out := make(Networks, len(entries))
	for name, e := range entries {
		out[name] = e.Address
	}
	*ns = out
	return nil
}

// address is one entry of a node's `networks` key in its mapping form.
type address struct {
	Address string `yaml:"address"`
	MAC     string `yaml:"mac"`
}

func decodeAddresses(value *yaml.Node) (map[string]address, error) {
	var raw map[string]yaml.Node
	if err := value.Decode(&raw); err != nil {
		return nil, err
	}
	out := make(map[string]address, len(raw))
	for name, node := range raw {
		if node.Kind == yaml.ScalarNode {
			out[name] = address{Address: node.Value}
			continue
		}
		var a address
		dec := node
		if err := dec.Decode(&a); err != nil {
			return nil, fmt.Errorf("network %q: want an address or {address, mac}: %w", name, err)
		}
		a.MAC = NormalizeMAC(a.MAC)
		out[name] = a
	}
	return out, nil
}

// NormalizeMAC writes a hardware address lower case with colons, so a value
// copied from a router's upper-case or dashed table is the same value.
func NormalizeMAC(mac string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(mac)), "-", ":")
}

// Network is one entry of networks.yaml: a name, and the range it covers
// when someone administers it.
type Network struct {
	Name    string `yaml:"name"`
	Subnet  string `yaml:"subnet"`
	Gateway string `yaml:"gateway"`
}

// Host is one entry of hosts.yaml: a machine this inventory deploys nothing
// to, but still names and reserves an address for. See
// docs/inventory.md#hosts.
type Host struct {
	Network string   `yaml:"network"`
	Address string   `yaml:"address"`
	MAC     string   `yaml:"mac"`
	Names   []string `yaml:"names"`
}

// Instance is one service running on a node, and one rendered configuration file.
type Instance struct {
	// Name is the instance's id as written: unique within its node, which
	// already says where it runs.
	Name string `yaml:"id"`
	// ID is the instance's key across the inventory, <node>/<name>. Set by
	// Load, not part of the YAML; hops and dials name an instance by it.
	ID      string `yaml:"-"`
	Service string `yaml:"service"`
	Role    string `yaml:"role"`
	Bind    string `yaml:"bind"`
	Ports   Ports  `yaml:"ports"`
	// Principal names the user whose credential this instance carries when
	// it dials its upstream. Empty means the instance carries its own. It
	// grants no access: validate requires the named user already to hold
	// every route the instance enters. Unwritten, derive.FillPrincipals
	// fills it when users.yaml grants those routes to one user alone.
	Principal string `yaml:"principal"`
	// PrincipalDerived is set when Principal was filled rather than written,
	// so a tool rewriting the file leaves it alone.
	PrincipalDerived bool `yaml:"-"`
	// Runtime says what delivers this process: RuntimeHost, RuntimeDocker
	// or RuntimePodman. Unwritten, Load fills it from the node's own
	// `runtime`, and from RuntimeHost when the node writes none either.
	Runtime string `yaml:"runtime"`
	// Containers is the container networks this instance joins, each one
	// of its node's `containers`, mapped to the fixed address it holds
	// there, or to "" when the runtime assigns one. Unwritten on a
	// containerised instance, Load fills the node's first; a host process
	// joins none. Two instances sharing a container network dial each other
	// by name, and an edge between them publishes nothing on the host. See
	// docs/inventory.md#container-networks.
	Containers map[string]string `yaml:"containers"`
	// Self says which of its service's own secrets this instance holds,
	// and the keys of each one that is a set. Unwritten means every name
	// the service declares, which is the ordinary case; written, it is the
	// whole list, so `self: {}` means none of them. A key a port hands out
	// is a key this instance has whether or not it is written here, so the
	// keys are worth writing only for one no port hands out. See
	// docs/inventory.md#a-services-own-secrets.
	Self map[string][]string `yaml:"self"`
	// Process names the running program this instance belongs to, for
	// instances that share one: a server listening on two ports from one
	// configuration file is two instances, because each port has its own
	// principals and its own grants, and one process. Empty means this
	// instance is a process of its own.
	Process string `yaml:"process"`
	// Values is this instance's own parameters — a Hysteria2 masquerade
	// target, a MicroBin public path, an nginx server block — whatever its
	// service and role need beyond where it runs and what it listens on.
	// rhumb does not read it; a template reads it by name. See
	// docs/inventory.md#an-instances-own-values.
	Values map[string]any `yaml:"values"`
	// Deploy is what starting this instance needs beyond the model: an
	// image, volumes, a restart policy. rhumb reads no key of it, exactly as
	// it reads no key of Values; the service's deploy template does. It is
	// a second mapping rather than a corner of Values because the two have
	// different readers — Values configures the program, Deploy starts it.
	// Ports are not in it, and neither are secrets: the mapping is derived,
	// and the credential stays in the configuration file beside it. See
	// docs/inventory.md#what-a-container-needs-beyond-the-model.
	Deploy map[string]any `yaml:"deploy"`
	// Dials names services this instance calls in passing, off any route:
	// the caller's own name for the dependency to a hop,
	// [<node>/]<instance>:<port>. The node defaults to this instance's own;
	// Load writes it in.
	// A dial is not an edge and grants nothing. See
	// docs/inventory.md#dialling-a-service-that-is-not-on-a-route.
	Dials map[string]string `yaml:"dials"`
	// DialsDerived names the dials resolved from the service's declaration
	// rather than written, so a tool rewriting the file leaves them alone.
	DialsDerived map[string]bool `yaml:"-"`

	// Path is the file this instance was written in, relative to the
	// generator root: its node file, or its own file in the node's instance
	// directory. Set by Load, not part of the YAML.
	Path string `yaml:"-"`
}

// RuntimeOr is what delivers this process, RuntimeHost when the instance
// says nothing.
func (i Instance) RuntimeOr() string {
	if i.Runtime == "" {
		return RuntimeHost
	}
	return i.Runtime
}

// ContainerNetwork is one entry of a node's `containers`: a network the
// node's container runtime holds, and the range it covers. The range is
// written rather than left to the runtime, because a runtime picks a
// different one on every machine that creates the network, and an address
// fixed inside it is only fixed if the range is.
type ContainerNetwork struct {
	Name    string `yaml:"name"`
	Subnet  string `yaml:"subnet"`
	Gateway string `yaml:"gateway"`
}

// ContainerNames is the names of this node's container networks, in its
// preference order.
func (n Node) ContainerNames() []string {
	out := make([]string, len(n.Containers))
	for i, c := range n.Containers {
		out[i] = c.Name
	}
	return out
}

// JoinedContainers is the container networks inst joins, in its node's
// preference order. A network the node does not list is left out; the
// validator reports it.
func (n Node) JoinedContainers(inst Instance) []ContainerNetwork {
	var out []ContainerNetwork
	for _, c := range n.Containers {
		if _, ok := inst.Containers[c.Name]; ok {
			out = append(out, c)
		}
	}
	return out
}

// SharedContainer is the first container network, in n's preference order,
// that both a and b join, or "" when they share none.
func (n Node) SharedContainer(a, b Instance) string {
	for _, c := range n.Containers {
		_, inA := a.Containers[c.Name]
		_, inB := b.Containers[c.Name]
		if inA && inB {
			return c.Name
		}
	}
	return ""
}

// ContainerNames is the names of the container networks this instance
// joins, sorted.
func (i Instance) ContainerNames() []string {
	out := make([]string, 0, len(i.Containers))
	for name := range i.Containers {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Containerised reports whether this instance's process runs in a
// container, which is every runtime but RuntimeHost. It is what decides
// whether `bind` is a host's interfaces or a container's, and so what the
// graph badges and what a deployment file is rendered for.
func (i Instance) Containerised() bool { return i.RuntimeOr() != RuntimeHost }

// IsUser reports whether a name is a user key, which is how a node group
// tells a person's devices from whoever hosts the machines.
func (r *Root) IsUser(name string) bool {
	_, ok := r.Users[name]
	return ok
}

// Protocol values a port may declare. TCP is the default, so a port that
// says nothing is a TCP port.
const (
	ProtocolTCP = "tcp"
	ProtocolUDP = "udp"
)

// Port is one listening port: its number, and the transport it listens on.
// Two ports collide only when they share an address, a number and a
// transport — a QUIC service on 443/udp and a web server on 443/tcp are
// both real and both correct.
type Port struct {
	Number   int    `yaml:"port"`
	Protocol string `yaml:"protocol"`
	// Self names the instance's own secrets everything granted on this port
	// also holds, in the order they are written — the order matters, since
	// the protocols taking more than one take them as a sequence. A `set`
	// secret is named <name>.<key>, anything else by name alone, and never
	// a field: a field is half a credential. See
	// docs/inventory.md#a-secret-several-people-hold.
	Self []string `yaml:"self"`
	// Published is the bare hostname this port answers to, for a port
	// reached from a browser. A reverse proxy in front matches its site
	// block on it, and the service behind renders the same string into its
	// own configuration — Vaultwarden's absolute URLs, cookie domain and
	// WebAuthn relying-party identifier are built from it, and a value that
	// disagrees with what the proxy serves breaks sign-in rather than the
	// page. It is a bare hostname and not a URL: the site block wants the
	// name alone, and a template needing a scheme writes one. See
	// docs/inventory.md#the-name-a-port-is-published-at.
	//
	// It is written as a string or a list; Published is the first name and
	// Names all of them, for a proxy answering to its own sites.
	Published string   `yaml:"-"`
	Names     []string `yaml:"-"`
	// Title and Description are what a page listing this port calls it and
	// says it is for — a portal card. Only a port a reverse proxy publishes
	// is listed.
	Title       string
	Description string
	// Proxy overrides, key by key, the proxy hints the service declares:
	// what a reverse proxy in front of this port must do for it.
	Proxy map[string]any
}

// portYAML is Port's mapping form as written.
type portYAML struct {
	Number      int            `yaml:"port"`
	Protocol    string         `yaml:"protocol"`
	Self        []string       `yaml:"self"`
	Published   yaml.Node      `yaml:"published"`
	Title       string         `yaml:"title"`
	Description string         `yaml:"description"`
	Proxy       map[string]any `yaml:"proxy"`
}

// SelfRef is one entry of Port.Self, split: the secret's name, and the key
// of it for a `set` secret.
type SelfRef struct {
	Name string
	Key  string
}

// ParseSelfRef splits "psk.users" into its name and key, and "tls_key" into
// a name alone.
func ParseSelfRef(ref string) SelfRef {
	if name, key, ok := strings.Cut(ref, "."); ok {
		return SelfRef{Name: name, Key: key}
	}
	return SelfRef{Name: ref}
}

// SelfNames is which of the service's own secrets this instance holds,
// given every name the service declares. An instance that says nothing
// holds them all; one that writes `self` holds what it names, plus whatever
// its ports hand out — naming a value on a port is saying the instance has
// it. The result keeps declared's order.
func (i Instance) SelfNames(declared []string) []string {
	if i.Self == nil {
		return declared
	}
	held := map[string]bool{}
	for name := range i.Self {
		held[name] = true
	}
	for _, p := range i.Ports {
		for _, ref := range p.Self {
			held[ParseSelfRef(ref).Name] = true
		}
	}
	out := make([]string, 0, len(held))
	for _, name := range declared {
		if held[name] {
			out = append(out, name)
		}
	}
	return out
}

// SelfKeys is the keys of each `set` secret this instance holds: what its
// ports hand out, plus whatever it declares itself. The result is sorted,
// so a caller walking it produces the same order every time.
func (i Instance) SelfKeys() map[string][]string {
	seen := map[string]map[string]bool{}
	note := func(name, key string) {
		if key == "" {
			return
		}
		if seen[name] == nil {
			seen[name] = map[string]bool{}
		}
		seen[name][key] = true
	}
	for _, p := range i.Ports {
		for _, ref := range p.Self {
			r := ParseSelfRef(ref)
			note(r.Name, r.Key)
		}
	}
	for name, keys := range i.Self {
		for _, key := range keys {
			note(name, key)
		}
	}
	out := make(map[string][]string, len(seen))
	for name, keys := range seen {
		list := make([]string, 0, len(keys))
		for key := range keys {
			list = append(list, key)
		}
		sort.Strings(list)
		out[name] = list
	}
	return out
}

// ProtocolOr is the port's transport, defaulting to TCP.
func (p Port) ProtocolOr() string {
	if p.Protocol == "" {
		return ProtocolTCP
	}
	return p.Protocol
}

// Ports is an instance's `ports` key: port name to port. A name is written
// against a bare number for the common case, and against a mapping when the
// transport has to be said:
//
//	ports:
//	  web: 8080
//	  main: {port: 443, protocol: udp}
type Ports map[string]Port

// UnmarshalYAML accepts both spellings above, so adding a protocol to one
// port does not rewrite the others.
func (ps *Ports) UnmarshalYAML(value *yaml.Node) error {
	var raw map[string]yaml.Node
	if err := value.Decode(&raw); err != nil {
		return err
	}
	out := make(Ports, len(raw))
	for name, node := range raw {
		var number int
		if err := node.Decode(&number); err == nil {
			out[name] = Port{Number: number}
			continue
		}
		var py portYAML
		if err := node.Decode(&py); err != nil {
			return fmt.Errorf("port %q: want a number or {port, protocol}: %w", name, err)
		}
		port := Port{Number: py.Number, Protocol: py.Protocol, Self: py.Self,
			Title: py.Title, Description: py.Description, Proxy: py.Proxy}
		switch py.Published.Kind {
		case 0:
		case yaml.ScalarNode:
			port.Names = []string{py.Published.Value}
		default:
			if err := py.Published.Decode(&port.Names); err != nil {
				return fmt.Errorf("port %q: published: want a name or a list of names: %w", name, err)
			}
		}
		if len(port.Names) > 0 {
			port.Published = port.Names[0]
		}
		switch port.ProtocolOr() {
		case ProtocolTCP, ProtocolUDP:
		default:
			return fmt.Errorf("port %q: protocol %q is not %s or %s", name, port.Protocol, ProtocolTCP, ProtocolUDP)
		}
		out[name] = port
	}
	*ps = out
	return nil
}

// PortsOf builds Ports from plain numbers, for a caller writing them in Go
// rather than parsing YAML — a test, or a fixture.
func PortsOf(numbers map[string]int) Ports {
	out := make(Ports, len(numbers))
	for name, n := range numbers {
		out[name] = Port{Number: n}
	}
	return out
}

// Numbers is the port numbers by name, for a caller that needs no transport
// — a template's `ports` datasource, a report of what an instance listens on.
func (ps Ports) Numbers() map[string]int {
	out := make(map[string]int, len(ps))
	for name, p := range ps {
		out[name] = p.Number
	}
	return out
}

// Node is one nodes/*.yaml file: a machine or a device.
type Node struct {
	ID    string `yaml:"id"`
	Owner string `yaml:"owner"`
	// Group is the directory this node's file sits in under nodes/, and is
	// not written in the file: nodes/dana/phone.yaml is grouped under
	// "dana", nodes/cloud/sea1.yaml under "cloud". A file
	// directly in nodes/ has none. A group naming a user in users.yaml is
	// that user's devices, and fills Owner for every node in it; a group
	// naming no user is whoever the machines belong to — a provider, a
	// household, a company — and rhumb reads nothing more into it.
	Group string `yaml:"-"`
	// Networks says where others can reach this node: network name to
	// address.
	Networks Networks `yaml:"networks"`
	// MACs is the hardware address written beside an address in
	// `networks`, by network. Read for the reservation export and rule 28.
	MACs map[string]string `yaml:"-"`
	// Reaches lists networks this node can open a connection on, without
	// being reachable on them — a phone or a laptop on the home LAN. Every
	// node reaches every network it has an address on, and every node
	// reaches Root.Universal implicitly, so Reaches is written only for the
	// remaining case. See
	// docs/inventory.md#reached-and-reaching.
	Reaches []string `yaml:"reaches"`
	// Containers is the container networks on this machine, in preference
	// order: the first is the one a containerised instance joins when it
	// names none, and two instances sharing several dial over the first of
	// them. Each is a scope inside the node's loopback. See
	// docs/inventory.md#container-networks.
	Containers []ContainerNetwork `yaml:"containers"`
	// Runtime is what delivers an instance on this node that writes no
	// `runtime` of its own. Empty means RuntimeHost.
	Runtime string `yaml:"runtime"`
	// Platform is the machine's GOOS/GOARCH, such as "linux/amd64": which
	// release of a program a bundle for this node carries, and which
	// service manager it registers with. Empty leaves it to whoever builds
	// the bundle. It is carried into the manifest and read nowhere else.
	Platform string `yaml:"platform"`
	// Download is when a bundle for this node downloads a release when
	// its builder does not say: "build", into the bundle, for a machine
	// that cannot reach the release, or "install", on the machine. Empty
	// leaves it to the builder's default. Carried into the manifest.
	Download string `yaml:"download"`
	// Accounts is this machine's POSIX accounts by name, for templates that
	// must write numeric owners: a file on a volume keeps the number, so the
	// number is written once, here. See
	// docs/inventory.md#a-nodes-accounts.
	Accounts map[string]Account `yaml:"accounts"`
	// Export narrows what is written for this device to one of the ways the
	// services it reaches offer, or ExportNone to write nothing at all.
	// Empty takes every way they offer. See
	// docs/inventory.md#which-export-a-person-receives.
	Export string `yaml:"export"`
	// Profiles are the uses this device is put to, by name — "singbox",
	// "browser" — each written out as its own set of files. A device with
	// none is written out once, as Export says; a device with profiles is
	// written out once per profile, and each profile's Export takes the
	// device's place. The two are not written together. See
	// docs/inventory.md#a-device-with-several-profiles.
	Profiles map[string]Profile `yaml:"profiles"`
	// Instances is what the node runs, written inline or read from the
	// directory the node names. decodeNode fills it; see instancesRef.
	Instances []Instance `yaml:"-"`
	// Credential names which of its owner's credentials this device uses.
	// Empty means DefaultCredential. Two devices naming the same one hold
	// the same secret — one password across a laptop and a phone is a thing
	// people do, and the model says it rather than inventing two values the
	// server would then have to carry.
	Credential string `yaml:"credential"`

	// Path is the file's path, relative to the generator root. Set by Load,
	// not part of the YAML.
	Path string `yaml:"-"`
	// Broken is the parse error if the file is not valid YAML or not a
	// mapping, and empty otherwise. Fields above are valid only when this is
	// empty.
	Broken string `yaml:"-"`
}

// Profile is one use a device is put to. All of a device's profiles carry
// the device's one credential: they are several files, not several accounts.
type Profile struct {
	// Export narrows this profile's files to one of the ways the services it
	// reaches offer, exactly as a device's own `export` does. Empty takes
	// every way they offer.
	Export string `yaml:"export"`
	// Values are handed to the template as the instance's own values, as an
	// authored override's are. An override pinning one of this profile's
	// files wins over them key by key.
	Values map[string]any `yaml:"values"`
	// Access narrows this profile to some of the routes the device's
	// credential opens. Empty takes every one of them.
	Access        []string `yaml:"access"`
	AccessWritten []string `yaml:"-"`
	// Runs names the service whose program runs this profile's file on the
	// device. Set, the export also writes a manifest, so a deployment tool
	// can install the file as that program's configuration. Empty, the file
	// is for a person, who pastes it wherever it goes.
	Runs string `yaml:"runs"`
}

// ProfileNames is n's profile names, sorted.
func (n Node) ProfileNames() []string {
	names := make([]string, 0, len(n.Profiles))
	for name := range n.Profiles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// CredentialOr is the credential this device uses, defaulting to
// DefaultCredential.
func (n Node) CredentialOr() string {
	if n.Credential != "" {
		return n.Credential
	}
	return DefaultCredential
}

// User is one entry of users.yaml: a logical identity, not a Linux account.
type User struct {
	// Username is the account name the services see. Empty means it is the
	// same as the user's own map key — see UsernameOr.
	Username string `yaml:"username"`
	// Devices is DevicesNone for a person this inventory has no node file
	// for, and empty otherwise. An assertion, not a switch. See DevicesNone.
	Devices string `yaml:"devices"`
	// Export is the `export` for the files this person carries themselves:
	// the credentials no device of theirs names have no node to carry an
	// `export`, so it is written here instead and means the same thing.
	// Empty takes every way the services they reach offer.
	Export string `yaml:"export"`
	// Credentials are this person's credentials, by name. A person who
	// declares none has one called DefaultCredential.
	//
	// How many credentials someone keeps is theirs to decide, not the
	// service's: one password across every device is as legitimate as one
	// per device, and the server carries whatever number results. A device
	// names which one it uses; two naming the same one share a value.
	Credentials map[string]Credential `yaml:"credentials"`
	// Access is the routes granted, with every @set expanded by Load.
	// AccessWritten is the list as written, for a tool rewriting the file.
	Access        []string `yaml:"access"`
	AccessWritten []string `yaml:"-"`
}

// Credential is one of a person's credentials: what it is for, and which of
// their routes it opens.
type Credential struct {
	// Note says where this credential is used — free text rhumb never reads.
	Note string `yaml:"note"`
	// Reaches names additional networks where the person can use a credential
	// without a modeled device. The universal network remains implicit.
	Reaches []string `yaml:"reaches"`
	// Access narrows this credential to some of the person's routes. Empty
	// takes every route they are granted, which is what a credential means
	// without one. It may name only routes the person holds: access is
	// granted to the person, and a credential chooses among what they
	// already have rather than reaching past it. See
	// docs/inventory.md#a-credential-may-open-fewer-routes.
	Access        []string `yaml:"access"`
	AccessWritten []string `yaml:"-"`
}

// CredentialNames returns this person's credential names in a stable order,
// or DefaultCredential alone when they declare none.
func (u User) CredentialNames() []string {
	if len(u.Credentials) == 0 {
		return []string{DefaultCredential}
	}
	out := make([]string, 0, len(u.Credentials))
	for name := range u.Credentials {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// AccessFor returns the routes one of this person's credentials opens: the
// credential's own list when it narrows, and the person's whole access
// otherwise. A name this person does not keep gets nothing, so a caller need
// not check first — validation reports the name, and a grant on nothing is
// the safe reading of it in the meantime.
func (u User) AccessFor(credential string) []string {
	c, ok := u.Credentials[credential]
	switch {
	case !ok && len(u.Credentials) == 0 && credential == DefaultCredential:
		// The credential a person has when they declare none.
	case !ok:
		return nil
	case len(c.Access) > 0:
		return c.Access
	}
	return u.Access
}

// OpensRoute reports whether one of this person's credentials opens a route.
func (u User) OpensRoute(credential, route string) bool {
	for _, name := range u.AccessFor(credential) {
		if name == route {
			return true
		}
	}
	return false
}

// Account is the name a service's own table calls one of this person's
// credentials: the username and the credential, always both, so that the
// table reads the same whether someone keeps one credential or five and
// adding a second never renames the first.
func (u User) Account(key, credential string) string {
	return u.UsernameOr(key) + "-" + credential
}

// UsernameOr returns u.Username, or key — the user's own map key — when
// Username is empty.
func (u User) UsernameOr(key string) string {
	if u.Username != "" {
		return u.Username
	}
	return key
}

// Route is one entry of routes.yaml: an ordered list of hops.
type Route struct {
	Hops []string `yaml:"hops"`
	// Scope is the group the route is written under in routes.yaml: a
	// network name or a node id, the place a client must be to use it.
	// Empty is a route written at the top level, reachable wherever its
	// entry is. The route's key is <scope>/<name> when Scope is set. Set by
	// Load, not part of the YAML. See docs/inventory.md#route-scopes.
	Scope string `yaml:"-"`
}

// RouteScope splits a route key into its scope and its name within the
// scope. A top-level route has no scope.
func RouteScope(key string) (scope, name string) {
	if i := strings.LastIndex(key, QualifiedSep); i >= 0 {
		return key[:i], key[i+1:]
	}
	return "", key
}

// Root is a generator root's parsed inventory.
type Root struct {
	// Nodes is every nodes/*.yaml file, sorted by file name, whether or not
	// it parses cleanly.
	Nodes []Node

	// Users is users.yaml's `users` map, keyed by user identifier. Empty and
	// UsersBroken set if the file exists but will not parse; empty and
	// UsersBroken empty if the file does not exist.
	Users       map[string]User
	UsersBroken string
	// Sets is users.yaml's `sets`: named lists of routes an access list
	// names as @<set>. Load expands them; see
	// docs/inventory.md#named-sets.
	Sets map[string][]string

	// Routes is routes.yaml's `routes` map, keyed by route name.
	Routes       map[string]Route
	RoutesBroken string

	// Networks is networks.yaml's preference-ordered list.
	Networks []string
	// Universal is networks.yaml's `universal` key: the one network every
	// node can reach without saying so. Empty means nothing is implicit.
	Universal      string
	NetworksBroken string
	// NetworkInfo is each network's subnet and gateway, by name.
	NetworkInfo map[string]Network

	// Hosts is hosts.yaml's `hosts` map, keyed by host identifier.
	Hosts       map[string]Host
	HostsBroken string
}

// Load parses a generator root's inventory. A missing nodes/ directory or a
// missing top-level file is an empty result for that part, not an error —
// whether the inventory is complete is validate's job, not this one's. The
// root itself not existing is an error.
func Load(root string) (*Root, error) {
	if _, err := os.Stat(root); err != nil {
		return nil, fmt.Errorf("inventory: reading root: %w", err)
	}

	rt := &Root{}

	nodes, err := loadNodes(root)
	if err != nil {
		return nil, err
	}
	rt.Nodes = nodes

	users, sets, brokenUsers, err := loadUsers(root)
	if err != nil {
		return nil, err
	}
	rt.Users, rt.Sets, rt.UsersBroken = users, sets, brokenUsers
	expandSets(rt)

	// A group naming a user is that user's devices, so the owner is the
	// directory and does not have to be repeated in every file in it. An
	// `owner` written anyway wins, for a device kept somewhere else.
	for i, n := range rt.Nodes {
		if n.Owner != "" || n.Group == "" {
			continue
		}
		if _, ok := rt.Users[n.Group]; ok {
			rt.Nodes[i].Owner = n.Group
		}
	}

	routes, brokenRoutes, err := loadRoutes(root)
	if err != nil {
		return nil, err
	}
	rt.Routes, rt.RoutesBroken = routes, brokenRoutes

	networks, universal, brokenNetworks, err := loadNetworks(root)
	if err != nil {
		return nil, err
	}
	rt.Universal, rt.NetworksBroken = universal, brokenNetworks
	rt.NetworkInfo = map[string]Network{}
	for _, n := range networks {
		rt.Networks = append(rt.Networks, n.Name)
		rt.NetworkInfo[n.Name] = n
	}

	hosts, brokenHosts, err := loadHosts(root)
	if err != nil {
		return nil, err
	}
	rt.Hosts, rt.HostsBroken = hosts, brokenHosts

	return rt, nil
}

func loadNodes(root string) ([]Node, error) {
	dir := filepath.Join(root, NodesDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("inventory: reading %s: %w", dir, err)
	}

	var nodes []Node
	rootInstanceDirs := map[string]bool{}
	read := func(path, group string) {
		relPath, err := filepath.Rel(root, path)
		if err != nil {
			relPath = path
		}

		node := Node{Path: relPath, Group: group}
		data, err := os.ReadFile(path)
		if err != nil {
			node.Broken = err.Error()
			nodes = append(nodes, node)
			return
		}
		instanceDir, err := decodeNode(data, &node)
		node.Path = relPath
		for i := range node.Instances {
			node.Instances[i].Path = relPath
		}
		if group == "" && instanceDir != "" {
			rootInstanceDirs[instanceDir] = true
		}
		if err == nil && instanceDir != "" {
			var instances []Instance
			instances, err = loadInstanceDir(root, filepath.Join(filepath.Dir(path), instanceDir))
			if err == nil {
				node.Instances = instances
			}
		}
		if err != nil {
			// decodeStrict may have partially populated node before failing;
			// start clean so a broken node carries no half-parsed fields.
			node = Node{Path: relPath, Group: group, Broken: fmt.Sprintf("%s: %s", path, err)}
		}
		node.Group = group
		qualifyInstances(&node)
		nodes = append(nodes, node)
	}
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".yaml" {
			read(filepath.Join(dir, entry.Name()), "")
		}
	}
	for _, entry := range entries {
		if !entry.IsDir() || rootInstanceDirs[entry.Name()] {
			continue
		}
		// One level of grouping: nodes/<group>/<node>.yaml. A second
		// level is not read, so a directory is a group and never a path.
		group := entry.Name()
		inner, err := os.ReadDir(filepath.Join(dir, group))
		if err != nil {
			return nil, fmt.Errorf("inventory: reading %s: %w", filepath.Join(dir, group), err)
		}
		for _, sub := range inner {
			if sub.IsDir() || filepath.Ext(sub.Name()) != ".yaml" {
				continue
			}
			read(filepath.Join(dir, group, sub.Name()), group)
		}
	}

	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Path < nodes[j].Path })
	return nodes, nil
}

// instancesRef is the mapping form of a node's `instances` key: the name of
// a directory beside the node file holding instances in YAML files.
type instancesRef struct {
	Directory string `yaml:"directory"`
}

// nodeFile and nodeFileWithDirectory are a node file in each form of its
// `instances` key. They are named because a strict decode error names the type.
type nodeFile struct {
	Node      `yaml:",inline"`
	Instances []Instance `yaml:"instances"`
}

type nodeFileWithDirectory struct {
	Node      `yaml:",inline"`
	Instances instancesRef `yaml:"instances"`
}

// decodeNode decodes a node file whose `instances` key is either the inline
// list or an instancesRef, and returns the directory the latter names. Both
// forms are decoded strictly from the original bytes, so an error's line
// number is the file's own. The directory is returned even when decoding
// fails, so a broken node's instance directory is not mistaken for a group.
func decodeNode(data []byte, node *Node) (string, error) {
	var peek struct {
		Instances yaml.Node `yaml:"instances"`
		Networks  yaml.Node `yaml:"networks"`
	}
	if err := yaml.Unmarshal(data, &peek); err != nil {
		return "", err
	}
	defer func() {
		if peek.Networks.Kind != yaml.MappingNode {
			return
		}
		if entries, err := decodeAddresses(&peek.Networks); err == nil {
			for name, e := range entries {
				if e.MAC == "" {
					continue
				}
				if node.MACs == nil {
					node.MACs = map[string]string{}
				}
				node.MACs[name] = e.MAC
			}
		}
	}()
	if peek.Instances.Kind != yaml.MappingNode {
		var doc nodeFile
		if err := decodeStrict(data, &doc); err != nil {
			return "", err
		}
		*node = doc.Node
		node.Instances = doc.Instances
		return "", nil
	}
	var ref instancesRef
	_ = peek.Instances.Decode(&ref)
	dirName := ref.Directory
	if dirName == "" || dirName == "." || dirName == ".." || filepath.Base(dirName) != dirName {
		return "", fmt.Errorf("instances.directory must name a directory beside the node file")
	}
	var doc nodeFileWithDirectory
	if err := decodeStrict(data, &doc); err != nil {
		return dirName, err
	}
	*node = doc.Node
	return dirName, nil
}

func loadInstanceDir(root, dir string) ([]Instance, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading instance directory %s: %w", dir, err)
	}
	var instances []Instance
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) != ".yaml" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if entry.IsDir() {
			return nil, fmt.Errorf("instance file %s is a directory", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading instance file %s: %w", path, err)
		}
		relPath, err := filepath.Rel(root, path)
		if err != nil {
			relPath = path
		}
		var doc yaml.Node
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return nil, fmt.Errorf("instance file %s: %w", path, err)
		}
		if len(doc.Content) == 0 {
			return nil, fmt.Errorf("instance file %s: empty document", path)
		}
		switch doc.Content[0].Kind {
		case yaml.MappingNode:
			var instance Instance
			if err := decodeStrict(data, &instance); err != nil {
				return nil, fmt.Errorf("instance file %s: %w", path, err)
			}
			instance.Path = relPath
			instances = append(instances, instance)
		case yaml.SequenceNode:
			var group []Instance
			if err := decodeStrict(data, &group); err != nil {
				return nil, fmt.Errorf("instance file %s: %w", path, err)
			}
			for i := range group {
				group[i].Path = relPath
			}
			instances = append(instances, group...)
		default:
			return nil, fmt.Errorf("instance file %s: expected an instance mapping or list", path)
		}
	}
	return instances, nil
}

func loadUsers(root string) (map[string]User, map[string][]string, string, error) {
	path := filepath.Join(root, UsersFilename)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, "", nil
		}
		return nil, nil, "", fmt.Errorf("inventory: reading %s: %w", path, err)
	}

	var doc struct {
		Sets  map[string][]string `yaml:"sets"`
		Users map[string]User     `yaml:"users"`
	}
	if err := decodeStrict(data, &doc); err != nil {
		return nil, nil, fmt.Sprintf("%s: %s", path, err), nil
	}
	return doc.Users, doc.Sets, "", nil
}

func loadRoutes(root string) (map[string]Route, string, error) {
	path := filepath.Join(root, RoutesFilename)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "", nil
		}
		return nil, "", fmt.Errorf("inventory: reading %s: %w", path, err)
	}

	var doc struct {
		Routes map[string]yaml.Node `yaml:"routes"`
	}
	if err := decodeStrict(data, &doc); err != nil {
		return nil, fmt.Sprintf("%s: %s", path, err), nil
	}
	// A key is a route when its value holds `hops`, and a scope otherwise:
	// a mapping of route names to routes, each keyed <scope>/<name>.
	routes := map[string]Route{}
	decode := func(node *yaml.Node, v any) error {
		raw, err := yaml.Marshal(node)
		if err != nil {
			return err
		}
		return decodeStrict(raw, v)
	}
	for key, node := range doc.Routes {
		if node.Kind != yaml.MappingNode {
			return nil, fmt.Sprintf("%s: routes.%s: want a route or a scope of routes", path, key), nil
		}
		if hops, _ := mappingValue(&node, "hops"); hops != nil {
			var r Route
			if err := decode(&node, &r); err != nil {
				return nil, fmt.Sprintf("%s: routes.%s: %s", path, key, err), nil
			}
			routes[key] = r
			continue
		}
		var group map[string]Route
		if err := decode(&node, &group); err != nil {
			return nil, fmt.Sprintf("%s: routes.%s: %s", path, key, err), nil
		}
		for name, r := range group {
			r.Scope = key
			routes[key+QualifiedSep+name] = r
		}
	}
	return routes, "", nil
}

func loadHosts(root string) (map[string]Host, string, error) {
	path := filepath.Join(root, HostsFilename)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "", nil
		}
		return nil, "", fmt.Errorf("inventory: reading %s: %w", path, err)
	}
	var doc struct {
		Hosts map[string]Host `yaml:"hosts"`
	}
	if err := decodeStrict(data, &doc); err != nil {
		return nil, fmt.Sprintf("%s: %s", path, err), nil
	}
	for id, h := range doc.Hosts {
		h.MAC = NormalizeMAC(h.MAC)
		doc.Hosts[id] = h
	}
	return doc.Hosts, "", nil
}

func loadNetworks(root string) (networks []Network, universal string, broken string, err error) {
	path := filepath.Join(root, NetworksFilename)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "", "", nil
		}
		return nil, "", "", fmt.Errorf("inventory: reading %s: %w", path, err)
	}

	var doc struct {
		Networks  []Network `yaml:"networks"`
		Universal string    `yaml:"universal"`
	}
	if err := decodeStrict(data, &doc); err != nil {
		return nil, "", fmt.Sprintf("%s: %s", path, err), nil
	}
	return doc.Networks, doc.Universal, "", nil
}

// decodeStrict decodes data into v, rejecting unknown fields and a document
// that is not a mapping.
func decodeStrict(data []byte, v any) error {
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	return dec.Decode(v)
}

// QualifiedSep separates a node from an instance name in an instance's ID.
const QualifiedSep = "/"

// LocalName is the part of an instance ID after its node: what the instance
// file wrote, and what names its container, its export directory and its
// hostname on a container network. A derived instance's ID has no node part
// and is returned whole.
func LocalName(id string) string {
	if i := strings.LastIndex(id, QualifiedSep); i >= 0 {
		return id[i+1:]
	}
	return id
}

// FlatID is an instance ID with its separator replaced, for a place that
// needs the whole ID as one path segment or account name.
func FlatID(id string) string {
	return strings.ReplaceAll(id, QualifiedSep, "-")
}

// ContainerBind is the only bind a containerised instance has: inside the
// container every interface is the container's own, and what reaches it from
// the host is the mapping, not the bind.
const ContainerBind = "0.0.0.0"

// qualifyInstances sets each instance's ID to <node>/<name>, fills what an
// instance takes from its node — runtime, container network, bind — and
// writes the node into every dial that leaves it out.
func qualifyInstances(node *Node) {
	if node.Broken != "" {
		return
	}
	for i := range node.Instances {
		inst := &node.Instances[i]
		inst.ID = node.ID + QualifiedSep + inst.Name
		// What runs the process, which container network it joins and
		// what it binds follow from the node unless the instance says
		// otherwise. Inside a container the only bind that is reachable at
		// all is every interface: who gets in is the host mapping's to say.
		if inst.Runtime == "" {
			inst.Runtime = node.Runtime
		}
		if inst.Containerised() {
			if len(inst.Containers) == 0 && len(node.Containers) > 0 {
				inst.Containers = map[string]string{node.Containers[0].Name: ""}
			}
			if inst.Bind == "" {
				inst.Bind = ContainerBind
			}
		}
		if len(inst.Dials) == 0 {
			continue
		}
		dials := make(map[string]string, len(inst.Dials))
		for name, raw := range inst.Dials {
			if !strings.Contains(raw, QualifiedSep) {
				raw = node.ID + QualifiedSep + raw
			}
			dials[name] = raw
		}
		inst.Dials = dials
	}
}

// mappingValue is the value under key in a mapping node, or nil.
func mappingValue(node *yaml.Node, key string) (*yaml.Node, bool) {
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1], true
		}
	}
	return nil, false
}

// SetPrefix marks an access entry naming a set rather than a route.
const SetPrefix = "@"

// ExpandAccess is list with every @set replaced by its routes, in order,
// each route once. An unknown set is kept as written, for validate to name.
func ExpandAccess(list []string, sets map[string][]string) []string {
	if list == nil {
		return nil
	}
	out := make([]string, 0, len(list))
	seen := map[string]bool{}
	add := func(s string) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, entry := range list {
		name, isSet := strings.CutPrefix(entry, SetPrefix)
		members, ok := sets[name]
		if !isSet || !ok {
			add(entry)
			continue
		}
		for _, route := range members {
			add(route)
		}
	}
	return out
}

// expandSets fills every access list from its written form.
func expandSets(rt *Root) {
	for key, u := range rt.Users {
		u.AccessWritten = u.Access
		u.Access = ExpandAccess(u.Access, rt.Sets)
		for name, c := range u.Credentials {
			c.AccessWritten = c.Access
			c.Access = ExpandAccess(c.Access, rt.Sets)
			u.Credentials[name] = c
		}
		rt.Users[key] = u
	}
	for i := range rt.Nodes {
		for name, p := range rt.Nodes[i].Profiles {
			p.AccessWritten = p.Access
			p.Access = ExpandAccess(p.Access, rt.Sets)
			rt.Nodes[i].Profiles[name] = p
		}
	}
}

// Account is one POSIX account on a node: its uid, and its primary group's
// gid and name. An unwritten group is the account's own name, and an
// unwritten gid its uid.
type Account struct {
	UID   int    `yaml:"uid"`
	GID   int    `yaml:"gid"`
	Group string `yaml:"group"`
}

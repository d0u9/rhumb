package inventory

import (
	"fmt"
	"net/netip"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Interface is a stable inventory identity, not an operating-system name.
type Interface struct {
	MAC       string                      `yaml:"mac,omitempty"`
	Addresses map[string]InterfaceAddress `yaml:"addresses,omitempty"`
}

type InterfaceAddress struct {
	Network         string   `yaml:"network"`
	IP              string   `yaml:"ip"`
	DHCPReservation bool     `yaml:"dhcp_reservation,omitempty"`
	Names           []string `yaml:"names,omitempty"`
}

type AddressRef struct {
	Interface string `yaml:"interface"`
	Address   string `yaml:"address"`
}

// NameRef selects a local interface address or an explicit connection endpoint.
type NameRef struct {
	Interface string `yaml:"interface,omitempty"`
	Address   string `yaml:"address,omitempty"`
	Endpoint  string `yaml:"endpoint,omitempty"`
}

// NameTarget returns the DNS target and scope, without looking up external DNS.
func (h Host) NameTarget(ref NameRef) (network, target string, external bool, err error) {
	if ref.Endpoint != "" {
		if ref.Interface != "" || ref.Address != "" {
			return "", "", false, fmt.Errorf("choose endpoint or interface/address, not both")
		}
		target, err = h.EndpointAddress(ref.Endpoint)
		if err != nil {
			return "", "", false, err
		}
		if from := h.Endpoints[ref.Endpoint].From; from != nil {
			address, _ := h.ReferencedAddress(*from)
			if len(address.Names) > 0 {
				return ref.Endpoint, address.Names[0], false, nil
			}
		}
		_, ipErr := netip.ParseAddr(target)
		return ref.Endpoint, target, ipErr != nil, nil
	}
	address, err := h.ReferencedAddress(AddressRef{Interface: ref.Interface, Address: ref.Address})
	if err != nil {
		return "", "", false, err
	}
	if len(address.Names) == 0 {
		return "", "", false, fmt.Errorf("referenced address has no names")
	}
	return address.Network, address.Names[0], false, nil
}

type Endpoint struct {
	From    *AddressRef `yaml:"from,omitempty"`
	Address string      `yaml:"address,omitempty"`
}

// EndpointAddress resolves only local, explicitly named addresses; never DNS.
func (h Host) EndpointAddress(network string) (string, error) {
	e, ok := h.Endpoints[network]
	if !ok {
		return "", fmt.Errorf("endpoint %q does not exist", network)
	}
	if (e.From == nil) == (e.Address == "") {
		return "", fmt.Errorf("endpoint %q needs exactly one of from and address", network)
	}
	if e.From == nil {
		if !validEndpointLiteral(e.Address) {
			return "", fmt.Errorf("endpoint %q needs an unscoped IP or bare domain", network)
		}
		return e.Address, nil
	}
	a, err := h.ReferencedAddress(*e.From)
	if err != nil {
		return "", fmt.Errorf("endpoint %q: %w", network, err)
	}
	if a.Network != network {
		return "", fmt.Errorf("endpoint %q references address on network %q", network, a.Network)
	}
	if _, err := netip.ParseAddr(a.IP); err != nil {
		return "", fmt.Errorf("endpoint %q references an invalid IP", network)
	}
	return a.IP, nil
}

// ReferencedAddress resolves a reference inside this host, never another device.
func (h Host) ReferencedAddress(ref AddressRef) (InterfaceAddress, error) {
	i, ok := h.Interfaces[ref.Interface]
	if !ok {
		return InterfaceAddress{}, fmt.Errorf("interface %q does not exist", ref.Interface)
	}
	a, ok := i.Addresses[ref.Address]
	if !ok {
		return InterfaceAddress{}, fmt.Errorf("address %q on interface %q does not exist", ref.Address, ref.Interface)
	}
	return a, nil
}

// HostSource is an inventory-relative location suitable for diagnostics and edits.
func HostSource(host string) string { return HostsFilename + ": hosts." + host }

func AddressSource(host, iface, address string) string {
	return HostSource(host) + ".interfaces." + iface + ".addresses." + address + ".ip"
}

// ResolveHosts refreshes computed node values after loading or a planned edit.
// Problems stay inspectable; a host with any invalid endpoint exposes none.
func (r *Root) ResolveHosts() {
	r.HostProblems = nil
	bound := map[string]string{}
	for _, id := range hostKeys(r.Hosts) {
		h := r.Hosts[id]

		for _, network := range hostKeys(h.Endpoints) {
			if _, err := h.EndpointAddress(network); err != nil {
				r.HostProblems = append(r.HostProblems, HostSource(id)+": "+err.Error())
			}
		}
	}
	for i := range r.Nodes {
		n := &r.Nodes[i]
		if n.Host == "" {
			continue
		}
		n.Networks, n.Reaches = nil, nil
		h, ok := r.Hosts[n.Host]
		if !ok {
			r.HostProblems = append(r.HostProblems, fmt.Sprintf("%s: host %q does not exist", n.Path, n.Host))
			continue
		}
		if prior, ok := bound[n.Host]; ok {
			r.HostProblems = append(r.HostProblems, fmt.Sprintf("host %q is bound by nodes %q and %q", n.Host, prior, n.ID))
		}
		bound[n.Host] = n.ID
		networks := Networks{}
		valid := true
		for _, network := range hostKeys(h.Endpoints) {
			address, err := h.EndpointAddress(network)
			if err != nil {
				valid = false
				continue
			}
			networks[network] = address
		}
		if valid {
			n.Networks = networks
			n.Reaches = append([]string(nil), h.Reaches...)
		}
	}
	sort.Strings(r.HostProblems)
}

// NetworkMember is an authored address owner, never an endpoint reference.
type NetworkMember struct {
	Host, Interface, AddressID, Network, Address, MAC, Source string
	Names                                                     []string
	Reservation                                               bool
}

func hostKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// NetworkMembers lists each authored interface address once.
func (r *Root) NetworkMembers() []NetworkMember {
	var out []NetworkMember
	for _, id := range hostKeys(r.Hosts) {
		h := r.Hosts[id]
		for _, iface := range hostKeys(h.Interfaces) {
			i := h.Interfaces[iface]
			for _, aid := range hostKeys(i.Addresses) {
				a := i.Addresses[aid]
				out = append(out, NetworkMember{Host: id, Interface: iface, AddressID: aid, Network: a.Network, Address: a.IP, MAC: i.MAC, Names: a.Names, Reservation: a.DHCPReservation, Source: AddressSource(id, iface, aid)})
			}
		}
	}
	return out
}

func validEndpointLiteral(value string) bool {
	if ip, err := netip.ParseAddr(value); err == nil {
		return ip.Zone() == ""
	}
	return ValidDNSName(value)
}

// ValidDNSName accepts an ASCII DNS host name, with an optional final dot.
func ValidDNSName(value string) bool {
	if _, err := netip.ParseAddr(value); err == nil {
		return false
	}
	name := strings.TrimSuffix(value, ".")
	if len(name) == 0 || len(name) > 253 {
		return false
	}
	for _, label := range strings.Split(name, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}

// YAML null bypasses custom unmarshalling; check nested record mappings first.
func checkHostMappings(data []byte) error {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return err
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("hosts document must be a mapping")
	}
	root := doc.Content[0]
	// Resolve merges before checking values: explicit keys and earlier merge
	// sources shadow later sources, just as the YAML decoder does.
	merging := map[*yaml.Node]bool{}
	var entries func(*yaml.Node) (map[string]*yaml.Node, error)
	entries = func(node *yaml.Node) (map[string]*yaml.Node, error) {
		if node.Kind == yaml.AliasNode && node.Alias != nil {
			node = node.Alias
		}
		if node.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("host records must be mappings (line %d)", node.Line)
		}
		if merging[node] {
			return nil, fmt.Errorf("cyclic host mapping at line %d", node.Line)
		}
		merging[node] = true
		defer delete(merging, node)
		out := map[string]*yaml.Node{}
		var merges []*yaml.Node
		for i := 0; i+1 < len(node.Content); i += 2 {
			key, value := node.Content[i], node.Content[i+1]
			if key.Value == "<<" && key.Tag == "!!merge" {
				if value.Kind == yaml.AliasNode && value.Alias != nil {
					value = value.Alias
				}
				if value.Kind == yaml.SequenceNode {
					merges = append(merges, value.Content...)
				} else {
					merges = append(merges, value)
				}
			} else {
				out[key.Value] = value
			}
		}
		for _, merge := range merges {
			inherited, err := entries(merge)
			if err != nil {
				return nil, err
			}
			for key, value := range inherited {
				if _, exists := out[key]; !exists {
					out[key] = value
				}
			}
		}
		return out, nil
	}
	active := map[*yaml.Node]bool{}
	var walk func(*yaml.Node, int) error
	walk = func(node *yaml.Node, level int) error {
		if node.Kind == yaml.AliasNode && node.Alias != nil {
			node = node.Alias
		}
		if active[node] {
			return fmt.Errorf("cyclic host mapping at line %d", node.Line)
		}
		active[node] = true
		defer delete(active, node)
		if level == 10 {
			if node.Kind != yaml.SequenceNode {
				return fmt.Errorf("host names references must be a sequence (line %d)", node.Line)
			}
			for _, ref := range node.Content {
				if err := walk(ref, 11); err != nil {
					return err
				}
			}
			return nil
		}
		fields, err := entries(node)
		if err != nil {
			return err
		}
		for _, key := range hostKeys(fields) {
			value := fields[key]
			if level == -1 {
				if key == "hosts" {
					if err := walk(value, 0); err != nil {
						return err
					}
				}
				continue
			}
			if level == 0 || level == 2 || level == 4 || level == 6 || level == 9 {
				if err := walk(value, level+1); err != nil {
					return err
				}
				continue
			}
			next := -1
			if level == 1 && key == "names" {
				next = 9
			}
			if level == 1 && key == "interfaces" {
				next = 2
			}
			if level == 1 && key == "endpoints" {
				next = 6
			}
			if level == 3 && key == "addresses" {
				next = 4
			}
			if level == 7 && key == "from" {
				next = 8
			}
			if next >= 0 {
				if err := walk(value, next); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return walk(root, -1)
}

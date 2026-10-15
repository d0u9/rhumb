package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/d0u9/rhumb/inventory"
)

// HostChange names an authored field, never a computed node address.
// Kind is ip, mac, endpoint (literal), or target (interface/address reference).
type HostChange struct {
	Kind      string `json:"kind"`
	Interface string `json:"interface,omitempty"`
	Address   string `json:"address,omitempty"`
	Network   string `json:"network,omitempty"`
	From      string `json:"from"`
	To        string `json:"to"`
}

func parseHostChanges(raw string) ([]HostChange, error) {
	if raw == "" {
		return nil, nil
	}
	var changes []HostChange
	d := json.NewDecoder(strings.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&changes); err != nil {
		return nil, fmt.Errorf("host edits: %w", err)
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("host edits must contain one JSON array")
	}
	seen := map[string]bool{}
	for _, c := range changes {
		key := c.Kind + "/" + c.Interface + "/" + c.Address + "/" + c.Network
		if seen[key] || c.To == "" || c.From == "" {
			return nil, fmt.Errorf("duplicate or empty host edit %q", key)
		}
		seen[key] = true
		switch c.Kind {
		case "ip":
			if c.Interface == "" || c.Address == "" || c.Network != "" {
				return nil, fmt.Errorf("ip edit needs interface and address")
			}
		case "mac":
			if c.Interface == "" || c.Address != "" || c.Network != "" {
				return nil, fmt.Errorf("mac edit needs only interface")
			}
		case "endpoint", "target":
			if c.Network == "" || c.Interface != "" || c.Address != "" {
				return nil, fmt.Errorf("endpoint edit needs only network")
			}
		default:
			return nil, fmt.Errorf("unknown host edit kind %q", c.Kind)
		}
	}
	return changes, nil
}

func cloneHosts(hosts map[string]inventory.Host) map[string]inventory.Host {
	out := maps.Clone(hosts)
	if out == nil {
		return nil
	}
	for id, h := range out {
		h.Names = maps.Clone(h.Names)
		for name, refs := range h.Names {
			h.Names[name] = slices.Clone(refs)
		}
		h.Reaches = slices.Clone(h.Reaches)
		h.Endpoints = maps.Clone(h.Endpoints)
		for network, e := range h.Endpoints {
			if e.From != nil {
				ref := *e.From
				e.From = &ref
				h.Endpoints[network] = e
			}
		}
		h.Interfaces = maps.Clone(h.Interfaces)
		for name, iface := range h.Interfaces {
			iface.Addresses = maps.Clone(iface.Addresses)
			for key, a := range iface.Addresses {
				a.Names = slices.Clone(a.Names)
				iface.Addresses[key] = a
			}
			h.Interfaces[name] = iface
		}
		out[id] = h
	}
	return out
}

// HostChangeValue is shared by planning, retry checks and the TUI.
func HostChangeValue(h inventory.Host, c HostChange) (string, error) {
	switch c.Kind {
	case "ip":
		i, ok := h.Interfaces[c.Interface]
		if !ok {
			break
		}
		a, ok := i.Addresses[c.Address]
		if !ok {
			break
		}
		return a.IP, nil
	case "mac":
		i, ok := h.Interfaces[c.Interface]
		if !ok {
			break
		}
		if i.MAC == "" {
			return "(none)", nil
		}
		return i.MAC, nil
	case "endpoint", "target":
		e, ok := h.Endpoints[c.Network]
		if !ok {
			break
		}
		if c.Kind == "endpoint" && e.From == nil {
			return e.Address, nil
		}
		if c.Kind == "target" && e.From != nil {
			return e.From.Interface + "/" + e.From.Address, nil
		}
	}
	return "", fmt.Errorf("host edit %s has no matching authored field", c.Kind)
}

func applyHostChanges(inv *inventory.Root, index int, changes []HostChange) error {
	n := inv.Nodes[index]
	if len(changes) == 0 {
		return nil
	}
	if n.Host == "" {
		return fmt.Errorf("host edits require a host reference")
	}
	h := inv.Hosts[n.Host]
	for _, c := range changes {
		value, err := HostChangeValue(h, c)
		if err != nil {
			return err
		}
		if value != c.From {
			return fmt.Errorf("stale host edit %s: expected %q, got %q", c.Kind, c.From, value)
		}
		switch c.Kind {
		case "ip":
			i := h.Interfaces[c.Interface]
			a := i.Addresses[c.Address]
			a.IP = c.To
			i.Addresses[c.Address] = a
			h.Interfaces[c.Interface] = i
		case "mac":
			i := h.Interfaces[c.Interface]
			if c.To == "(none)" {
				i.MAC = ""
			} else {
				i.MAC = inventory.NormalizeMAC(c.To)
			}
			h.Interfaces[c.Interface] = i
		case "endpoint":
			e := h.Endpoints[c.Network]
			e.Address = c.To
			h.Endpoints[c.Network] = e
		case "target":
			i, a, ok := strings.Cut(c.To, "/")
			if !ok || i == "" || a == "" || strings.Contains(a, "/") {
				return fmt.Errorf("endpoint target must be interface/address")
			}
			e := h.Endpoints[c.Network]
			e.From = &inventory.AddressRef{Interface: i, Address: a}
			h.Endpoints[c.Network] = e
		}
	}
	inv.Hosts[n.Host] = h
	return nil
}

func migrationNodeNetworks(inv *inventory.Root, n inventory.Node) map[string]bool {
	out := map[string]bool{}
	for name := range n.Networks {
		out[name] = true
	}
	if n.Host != "" {
		h := inv.Hosts[n.Host]
		for _, i := range h.Interfaces {
			for _, a := range i.Addresses {
				out[a.Network] = true
			}
		}
		for _, name := range h.Reaches {
			out[name] = true
		}
	}
	return out
}

// hostChangeTarget names what a host edit changes: interface/address for an
// IP, the interface for a MAC, the network scope for an endpoint.
func hostChangeTarget(c HostChange) string {
	var parts []string
	for _, part := range []string{c.Interface, c.Address, c.Network} {
		if part != "" {
			parts = append(parts, part)
		}
	}
	return strings.Join(parts, "/")
}

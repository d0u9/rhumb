package validate

import (
	"fmt"
	"net/netip"
	"sort"
	"strings"

	"github.com/d0u9/rhumb/inventory"
)

// hostIssues checks authored host facts without refreshing computed node values.
func hostIssues(inv *inventory.Root) []string {
	var out []string
	add := func(source, message string, args ...any) {
		out = append(out, source+": "+fmt.Sprintf(message, args...))
	}
	networks := map[string]bool{}
	for _, n := range inv.Networks {
		networks[n] = true
	}
	bound := map[string]string{}
	for _, n := range inv.Nodes {
		if n.Broken != "" || n.Host == "" {
			continue
		}
		_, ok := inv.Hosts[n.Host]
		if !ok {
			add(n.Path, "host %q does not exist", n.Host)
		}
		if prior, ok := bound[n.Host]; ok {
			add(inventory.HostSource(n.Host), "bound by nodes %q and %q", prior, n.ID)
		}
		bound[n.Host] = n.ID
	}
	reserved := map[string]map[string]bool{}
	for id, h := range inv.Hosts {
		source := inventory.HostSource(id)
		if !hostLocalID(id) {
			add(source, "invalid host identifier %q", id)
		}
		for _, n := range h.Reaches {
			if !networks[n] {
				add(source+".reaches", "unknown network %q", n)
			}
		}
		for network := range h.Endpoints {
			if !networks[network] {
				add(source+".endpoints", "unknown network %q", network)
			}
			if _, err := h.EndpointAddress(network); err != nil {
				add(source+".endpoints", "%s", err)
			}
		}
		for name, iface := range h.Interfaces {
			loc := source + ".interfaces." + name
			if !hostLocalID(name) {
				add(loc, "invalid interface identifier %q", name)
			}
			mac := inventory.NormalizeMAC(iface.MAC)
			if mac != "" && !validMAC(mac) {
				add(loc+".mac", "invalid MAC %q", iface.MAC)
			}
			for aid, address := range iface.Addresses {
				at := loc + ".addresses." + aid
				if !hostLocalID(aid) {
					add(at, "invalid address identifier %q", aid)
				}
				if !networks[address.Network] {
					add(at, "unknown network %q", address.Network)
				}
				ip, err := netip.ParseAddr(address.IP)
				if err != nil || ip.Zone() != "" {
					add(at+".ip", "expected an unscoped IP literal")
				}
				if !address.DHCPReservation {
					continue
				}
				if mac == "" || !validMAC(mac) || err != nil || !ip.Is4() || ip.Zone() != "" {
					add(at+".dhcp_reservation", "requires a valid MAC and IPv4 address")
					continue
				}
				key := address.Network + "\x00" + mac
				if reserved[key] == nil {
					reserved[key] = map[string]bool{}
				}
				reserved[key][ip.String()] = true
			}
		}
	}
	for key, ips := range reserved {
		if len(ips) > 1 {
			network, mac, _ := strings.Cut(key, "\x00")
			add(inventory.HostsFilename, "MAC %q reserves multiple IPv4 addresses on network %q", mac, network)
		}
	}
	sort.Strings(out)
	return out
}

func hostLocalID(s string) bool {
	if s == "" || strings.TrimSpace(s) != s {
		return false
	}
	for i, c := range s {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' {
			continue
		}
		if i > 0 && (c == '-' || c == '_') {
			continue
		}
		return false
	}
	return true
}

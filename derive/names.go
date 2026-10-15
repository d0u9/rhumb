package derive

import (
	"fmt"
	"maps"
	"net/netip"
	"slices"
	"sort"
	"strings"

	"github.com/d0u9/rhumb/inventory"
)

// MaxCNAMEHops bounds each resolution, including service and device aliases.
const MaxCNAMEHops = 8

func canonicalName(name string) string { return strings.ToLower(strings.TrimSuffix(name, ".")) }

func addressName(name, address, source string) (Name, bool) {
	ip, err := netip.ParseAddr(address)
	if err != nil || ip.Zone() != "" {
		return Name{}, false
	}
	kind := "A"
	if !ip.Unmap().Is4() {
		kind = "AAAA"
	}
	return Name{Name: name, Type: kind, Value: ip.Unmap().String(), Source: source}, true
}

func (t *nameTables) addDeviceNames(inv *inventory.Root) {
	for _, id := range slices.Sorted(maps.Keys(inv.Hosts)) {
		h := inv.Hosts[id]
		for _, name := range slices.Sorted(maps.Keys(h.Names)) {
			source := inventory.HostSource(id) + ".names." + name
			refs := h.Names[name]
			if len(refs) == 0 {
				t.conflicts = append(t.conflicts, source+": needs at least one address reference")
			}
			networks := map[string]bool{}
			for _, ref := range refs {
				network, target, external, err := h.NameTarget(ref)
				if err != nil {
					t.conflicts = append(t.conflicts, source+": "+err.Error())
					continue
				}
				if networks[network] {
					t.conflicts = append(t.conflicts, fmt.Sprintf("%s: multiple references on network %q", source, network))
					continue
				}
				networks[network] = true
				record, ip := addressName(name, target, source)
				if !ip {
					record = Name{Name: name, Type: "CNAME", Value: target, Source: source, External: external}
				}
				t.add(network, record)
			}
		}
	}
}

func (t *nameTables) addAliases(input map[string]string) {
	aliases := map[string]string{}
	sources := map[string]string{}
	for _, name := range slices.Sorted(maps.Keys(input)) {
		target := canonicalName(input[name])
		key := canonicalName(name)
		source := inventory.NamesFilename + ": names." + name
		if !inventory.ValidDNSName(key) || !inventory.ValidDNSName(target) {
			t.conflicts = append(t.conflicts, source+": alias and target must be DNS names")
			continue
		}
		if prior, ok := sources[key]; ok {
			t.conflicts = append(t.conflicts, source+": duplicate DNS name also defined at "+prior)
			continue
		}
		aliases[key], sources[key] = target, source
	}
	added := map[string]bool{}
	found := map[string]bool{}
	keys := slices.Sorted(maps.Keys(aliases))
	// Each pass makes aliases whose targets are now present visible to the next pass.
	for range len(aliases) {
		changed := false
		for _, network := range slices.Sorted(maps.Keys(t.byKey)) {
			for _, name := range keys {
				key := network + "\x00" + name
				if added[key] {
					continue
				}
				_, targetExists := t.byKey[network][aliases[name]]
				_, collides := t.byKey[network][name]
				if !targetExists && !collides {
					continue
				}
				t.add(network, Name{Name: name, Type: "CNAME", Value: aliases[name], Source: sources[name]})
				added[key], found[name], changed = true, true, true
			}
		}
		if !changed {
			break
		}
	}
	for _, name := range keys {
		if found[name] {
			continue
		}
		seen := map[string]bool{}
		at := name
		for aliases[at] != "" && !seen[at] {
			seen[at] = true
			at = aliases[at]
		}
		if seen[at] {
			t.conflicts = append(t.conflicts, sources[name]+": CNAME cycle")
		} else {
			t.conflicts = append(t.conflicts, sources[name]+": target "+aliases[name]+" is absent from every network")
		}
	}
}

// FlattenNames resolves within one table without DNS lookups. Invalid input
// returns no records, so a hosts-only backend cannot publish a partial table.
func FlattenNames(records []Name) ([]Name, []string) { return resolveNames(records, true) }

func resolveNames(records []Name, flatten bool) ([]Name, []string) {
	table := map[string]Name{}
	var issues []string
	for _, n := range records {
		n.Name = canonicalName(n.Name)
		if !inventory.ValidDNSName(n.Name) {
			issues = append(issues, fmt.Sprintf("%s: invalid DNS name %q", n.Source, n.Name))
		}
		if n.Type == "CNAME" && !inventory.ValidDNSName(n.Value) {
			issues = append(issues, fmt.Sprintf("%s: invalid CNAME target %q", n.Source, n.Value))
		}
		if prev, exists := table[n.Name]; exists {
			issues = append(issues, fmt.Sprintf("duplicate DNS name %q (%s and %s)", n.Name, prev.Source, n.Source))
		}
		table[n.Name] = n
	}
	var out []Name
	for _, name := range slices.Sorted(maps.Keys(table)) {
		record := table[name]
		at := record
		seen := map[string]bool{}
		hops := 0
		for at.Type == "CNAME" {
			if seen[at.Name] {
				issues = append(issues, fmt.Sprintf("%s: CNAME cycle resolving %q", record.Source, name))
				break
			}
			seen[at.Name] = true
			if hops == MaxCNAMEHops {
				issues = append(issues, fmt.Sprintf("%s: %q exceeds %d CNAME hops", record.Source, name, MaxCNAMEHops))
				break
			}
			hops++
			target := canonicalName(at.Value)
			next, ok := table[target]
			if !ok {
				if at.External {
					if !flatten {
						break
					}
					issues = append(issues, fmt.Sprintf("%s: cannot flatten external CNAME target %q without a DNS lookup", at.Source, target))
					break
				}
				issues = append(issues, fmt.Sprintf("%s: CNAME %q target %q is absent from this network", at.Source, at.Name, target))
				break
			}
			at = next
		}
		if at.Type != "CNAME" {
			ip, err := netip.ParseAddr(at.Value)
			if err != nil || ip.Zone() != "" || (at.Type != "A" && at.Type != "AAAA") || (at.Type == "A") != ip.Is4() {
				issues = append(issues, fmt.Sprintf("%s: invalid %s record %q", at.Source, at.Type, at.Name))
				continue
			}
			record.Type, record.Value, record.External = at.Type, at.Value, false
			out = append(out, record)
		}
	}
	if len(issues) > 0 {
		sort.Strings(issues)
		return nil, issues
	}
	return out, nil
}

package derive

import (
	"sort"

	"github.com/d0u9/rhumb/confgen"
	"github.com/d0u9/rhumb/inventory"
)

// FillPrincipals writes a principal into every instance that dials an
// upstream, writes none, and has exactly one user whose default credential
// opens every route it enters, marking it derived. users.yaml already says
// who may use those routes; when it names one user, the instance can only be
// carrying that user's credential. With none or several, the instance keeps
// dialling as itself unless it writes a principal.
func FillPrincipals(inv *inventory.Root, manifests map[string]confgen.Manifest) {
	entered := map[string][]string{}
	for name, route := range inv.Routes {
		if len(route.Hops) < 2 {
			continue
		}
		seen := map[string]bool{}
		for _, raw := range route.Hops[:len(route.Hops)-1] {
			hop, err := ParseHop(raw)
			if err != nil || seen[hop.Instance] {
				continue
			}
			seen[hop.Instance] = true
			entered[hop.Instance] = append(entered[hop.Instance], name)
		}
	}
	users := make([]string, 0, len(inv.Users))
	for key := range inv.Users {
		users = append(users, key)
	}
	sort.Strings(users)

	for ni := range inv.Nodes {
		n := &inv.Nodes[ni]
		if n.Broken != "" {
			continue
		}
		for ii := range n.Instances {
			inst := &n.Instances[ii]
			routes := entered[inst.ID]
			if inst.Principal != "" || len(manifests[inst.Service].Upstream) == 0 || len(routes) == 0 {
				continue
			}
			var found []string
			for _, key := range users {
				opens := true
				for _, route := range routes {
					if !inv.Users[key].OpensRoute(inventory.DefaultCredential, route) {
						opens = false
						break
					}
				}
				if opens {
					found = append(found, key)
				}
			}
			if len(found) == 1 {
				inst.Principal = found[0]
				inst.PrincipalDerived = true
			}
		}
	}
}

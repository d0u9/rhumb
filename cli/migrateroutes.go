package cli

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/d0u9/rhumb/inventory"
)

type routeChange struct{ From, To string }

func parseRouteChanges(encoded string) ([]routeChange, error) {
	if encoded == "" {
		return nil, nil
	}
	var specs []string
	if err := json.Unmarshal([]byte(encoded), &specs); err != nil {
		return nil, fmt.Errorf("--route values: %w", err)
	}
	var changes []routeChange
	fromSeen, toSeen := map[string]bool{}, map[string]bool{}
	for _, spec := range specs {
		fields := map[string]string{}
		for _, part := range strings.Split(spec, ",") {
			key, value, ok := strings.Cut(part, "=")
			if !ok || value == "" || (key != "from" && key != "to") || fields[key] != "" {
				return nil, fmt.Errorf("--route %q: want from=<old>,to=<new>", spec)
			}
			fields[key] = value
		}
		if fields["from"] == "" || fields["to"] == "" || strings.ContainsAny(fields["to"], ":\\") || strings.Count(fields["to"], inventory.QualifiedSep) > 1 {
			return nil, fmt.Errorf("--route %q: want from=<old>,to=<new> with a route name, or <scope>/<name> to move it", spec)
		}
		// A plain new name keeps the route in the scope it is in.
		if !strings.Contains(fields["to"], inventory.QualifiedSep) {
			if scope, _ := inventory.RouteScope(fields["from"]); scope != "" {
				fields["to"] = scope + inventory.QualifiedSep + fields["to"]
			}
		}
		if fromSeen[fields["from"]] || toSeen[fields["to"]] {
			return nil, fmt.Errorf("--route %q: route given more than once", spec)
		}
		fromSeen[fields["from"]], toSeen[fields["to"]] = true, true
		if fields["from"] != fields["to"] {
			changes = append(changes, routeChange{fields["from"], fields["to"]})
		}
	}
	return changes, nil
}

func renameMigrationRoutes(inv *inventory.Root, changes []routeChange) (map[string][]string, error) {
	refs := map[string][]string{}
	if len(changes) == 0 {
		return refs, nil
	}
	byName := map[string]string{}
	for _, change := range changes {
		if _, ok := inv.Routes[change.From]; !ok {
			return nil, fmt.Errorf("route %q does not exist", change.From)
		}
		if _, ok := inv.Routes[change.To]; ok {
			return nil, fmt.Errorf("route %q already exists", change.To)
		}
		byName[change.From] = change.To
		refs[change.From] = append(refs[change.From], "routes.yaml: routes."+change.From)
	}
	copyOf := cloneRoutes(inv.Routes)
	for from, to := range byName {
		route := copyOf[from]
		route.Scope, _ = inventory.RouteScope(to)
		delete(copyOf, from)
		copyOf[to] = route
	}
	inv.Routes = copyOf
	renameIn := func(list []string, where string) {
		for i, name := range list {
			if to := byName[name]; to != "" {
				list[i] = to
				if where != "" {
					refs[name] = append(refs[name], where)
				}
			}
		}
	}
	if len(inv.Sets) > 0 {
		sets := make(map[string][]string, len(inv.Sets))
		for name, members := range inv.Sets {
			members = append([]string(nil), members...)
			renameIn(members, "users.yaml: sets."+name)
			sets[name] = members
		}
		inv.Sets = sets
	}
	inv.Users = cloneUsersForRoutes(inv.Users)
	for _, user := range inv.Users {
		renameIn(user.AccessWritten, "")
		for _, credential := range user.Credentials {
			renameIn(credential.AccessWritten, "")
		}
	}
	for key, user := range inv.Users {
		for i, name := range user.Access {
			if to := byName[name]; to != "" {
				user.Access[i] = to
				refs[name] = append(refs[name], "users.yaml: users."+key+".access")
			}
		}
		for credentialName, credential := range user.Credentials {
			for i, name := range credential.Access {
				if to := byName[name]; to != "" {
					credential.Access[i] = to
					refs[name] = append(refs[name], "users.yaml: users."+key+".credentials."+credentialName+".access")
				}
			}
			user.Credentials[credentialName] = credential
		}
		inv.Users[key] = user
	}
	for i := range inv.Nodes {
		if len(inv.Nodes[i].Profiles) == 0 {
			continue
		}
		profiles := map[string]inventory.Profile{}
		for name, profile := range inv.Nodes[i].Profiles {
			profile.Access = append([]string(nil), profile.Access...)
			profile.AccessWritten = append([]string(nil), profile.AccessWritten...)
			renameIn(profile.AccessWritten, "")
			for j, route := range profile.Access {
				if to := byName[route]; to != "" {
					profile.Access[j] = to
					refs[route] = append(refs[route], inv.Nodes[i].Path+": profiles."+name+".access")
				}
			}
			profiles[name] = profile
		}
		inv.Nodes[i].Profiles = profiles
	}
	for from := range refs {
		sort.Strings(refs[from])
	}
	return refs, nil
}

func cloneUsersForRoutes(users map[string]inventory.User) map[string]inventory.User {
	copyOf := make(map[string]inventory.User, len(users))
	for key, user := range users {
		user.Access = append([]string(nil), user.Access...)
		user.AccessWritten = append([]string(nil), user.AccessWritten...)
		credentials := make(map[string]inventory.Credential, len(user.Credentials))
		for name, credential := range user.Credentials {
			credential.Access = append([]string(nil), credential.Access...)
			credential.AccessWritten = append([]string(nil), credential.AccessWritten...)
			credentials[name] = credential
		}
		user.Credentials = credentials
		copyOf[key] = user
	}
	return copyOf
}

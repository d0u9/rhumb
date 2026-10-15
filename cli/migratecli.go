package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/d0u9/rhumb/derive"
	"github.com/d0u9/rhumb/engine"
	"github.com/d0u9/rhumb/inventory"
	"github.com/d0u9/rhumb/validate"
)

// Migrate builds the report for a plan the Migrate tab encodes as
// flags; "apply" writes it through the same validated plan. It is not a
// command-line action: the TUI is the only interface.
func Migrate(in io.Reader, out io.Writer, args []string, flags map[string]string, s Settings) error {
	if flags["apply"] == "true" {
		return migrateApplyAction(in, out, args, flags, s)
	}
	if flags["yes"] == "true" {
		return fmt.Errorf("confirmation given without apply")
	}
	return MigrateSnapshot(in, out, args, flags, s, nil)
}

// MigrateSnapshot lets the TUI inspect the same validated before/after
// inventory used by the report. Returning true stops before report rendering.
func MigrateSnapshot(in io.Reader, out io.Writer, args []string, flags map[string]string, s Settings, snapshot func(before, after engine.Loaded) bool) error {
	if len(args) != 1 || args[0] != "node" {
		return fmt.Errorf("usage: rhumb migrate node [--node from=<old>,to=<new> --network ... --instance ... --route ... --published ...]")
	}
	root := s.Root
	if root == "" {
		return fmt.Errorf("no generator root given")
	}
	if flags["node"] == "" {
		return fmt.Errorf("migration plan names no source node")
	}
	oldID, newID, err := parseNodeChange(flags["node"])
	if err != nil {
		return err
	}
	networkChanges, err := parseNetworkChanges(flags["network"])
	if err != nil {
		return err
	}
	instanceChanges, err := parseInstanceChanges(flags["instance"])
	if err != nil {
		return err
	}
	routeChanges, err := parseRouteChanges(flags["route"])
	if err != nil {
		return err
	}
	publishedChanges, err := parsePublishedChanges(flags["published"])
	if err != nil {
		return err
	}
	hostChanges, err := parseHostChanges(flags["host-edit"])
	if err != nil {
		return err
	}
	if oldID == newID && len(networkChanges) == 0 && len(instanceChanges) == 0 && len(routeChanges) == 0 && len(publishedChanges) == 0 && len(hostChanges) == 0 {
		return fmt.Errorf("give a new node ID, network name, or address")
	}

	l, err := engine.Load(root)
	if err != nil {
		return err
	}
	if broken := BrokenFiles(l.Inv); len(broken) != 0 {
		return fmt.Errorf("current inventory is broken:\n  %s", strings.Join(broken, "\n  "))
	}
	if issues := validate.Validate(l.Inv, l.Manifests, l.Exports, l.Derived, nil); len(issues) != 0 {
		return fmt.Errorf("current inventory has %d validation problems; run rhumb check first: %s", len(issues), issues[0].Message)
	}

	// Copy the containers that can change. No change to this preview can
	// reach the loaded snapshot, which is the source for the before/after diff.
	inv := *l.Inv
	inv.Nodes = append([]inventory.Node(nil), l.Inv.Nodes...)
	inv.Hosts = cloneHosts(l.Inv.Hosts)
	index := -1
	for i := range inv.Nodes {
		if inv.Nodes[i].ID == oldID {
			index = i
		}
		if newID != oldID && inv.Nodes[i].ID == newID {
			return fmt.Errorf("node %q already exists", newID)
		}
	}
	if index < 0 {
		return fmt.Errorf("node %q not found", oldID)
	}
	before := inv.Nodes[index]
	// Validate the whole set against the original names before any rename.
	// This rejects ambiguous chains and swaps such as A -> B, B -> C.
	for _, change := range networkChanges {
		if !migrationNodeNetworks(&inv, before)[change.From] {
			return fmt.Errorf("node %q has no address on network %q", oldID, change.From)
		}
		if change.From != change.To {
			for _, name := range l.Inv.Networks {
				if name == change.To {
					return fmt.Errorf("network %q already exists", change.To)
				}
			}
		}
	}
	if err := applyHostChanges(&inv, index, hostChanges); err != nil {
		return err
	}
	renamedRefs := map[string][]string{}
	for _, change := range networkChanges {
		if change.From != change.To {
			renamedRefs[change.From], err = renameInventoryNetwork(&inv, change.From, change.To)
			if err != nil {
				return err
			}
		}
	}
	inv.Nodes[index].ID = newID
	// A node's file is named after it, so a renamed node moves to a new file.
	if oldPath := inv.Nodes[index].Path; oldID != newID && oldPath != "" {
		newPath := filepath.Join(filepath.Dir(oldPath), newID+".yaml")
		inv.Nodes[index].Path = newPath
		instances := append([]inventory.Instance(nil), inv.Nodes[index].Instances...)
		for i := range instances {
			if instances[i].Path == oldPath {
				instances[i].Path = newPath
			}
		}
		inv.Nodes[index].Instances = instances
	}
	if err := renameMigrationNode(&inv, index, oldID, newID); err != nil {
		return err
	}
	publishedRefs, err := renameMigrationPublished(&inv, index, publishedChanges)
	if err != nil {
		return err
	}
	instanceRefs, err := renameMigrationInstances(&inv, index, instanceChanges)
	if err != nil {
		return err
	}
	routeRefs, err := renameMigrationRoutes(&inv, routeChanges)
	if err != nil {
		return err
	}

	inv.ResolveHosts()
	after, err := derive.Derive(&inv, l.Manifests)
	if err != nil {
		return fmt.Errorf("target inventory cannot derive routes: %w", err)
	}
	if issues := validate.Validate(&inv, l.Manifests, l.Exports, after, nil); len(issues) != 0 {
		return fmt.Errorf("target inventory has %d validation problems: %s", len(issues), issues[0].Message)
	}
	newLoaded := l
	newLoaded.Inv, newLoaded.Derived = &inv, after
	if snapshot != nil && snapshot(l, newLoaded) {
		return nil
	}

	if _, err := parseMigrationScenario(flags["scenario"]); err != nil {
		return err
	}
	rep := &migrationReport{OldID: oldID, NewID: newID, Root: root, Inventory: before.Path, Apply: flags["apply"] == "true"}
	if oldID != newID {
		rep.Edits = append(rep.Edits, migrationChangeRow{Kind: "Node ID", Before: oldID, After: newID})
	}
	for _, c := range hostChanges {
		rep.Edits = append(rep.Edits, migrationChangeRow{Kind: "Host " + c.Kind + " " + hostChangeTarget(c), Before: c.From, After: c.To, Refs: []string{inventory.HostSource(before.Host)}})
	}
	for _, change := range networkChanges {
		if change.From != change.To {
			rep.Edits = append(rep.Edits, migrationChangeRow{Kind: "Network name", Before: change.From, After: change.To, Refs: renamedRefs[change.From]})
		}

	}
	renames := map[string]string{}
	for _, change := range instanceChanges {
		renames[change.From] = change.To
		rep.Edits = append(rep.Edits, migrationChangeRow{Kind: "Instance ID", Before: change.From, After: change.To, Refs: instanceRefs[change.From]})
	}
	for _, change := range routeChanges {
		rep.Edits = append(rep.Edits, migrationChangeRow{Kind: "Route name", Before: change.From, After: change.To, Refs: routeRefs[change.From]})
		rep.NodeMatches = append(rep.NodeMatches, migrationTextMatch{Path: "routes.yaml", Kind: "route key", Term: change.From,
			Text: "opaque instance values, deploy settings and service templates may use this route name as a key"})
	}
	for _, change := range publishedChanges {
		rep.Edits = append(rep.Edits, migrationChangeRow{Kind: "Published name " + change.Instance + ":" + change.Port, Before: publishedRefs[change.key()], After: change.To})
	}

	// The edge key denotes the same route connection across a node rename.
	// A derived client ID starts with its node ID, so normalize that prefix.
	oldEdges := map[string]derive.Edge{}
	for _, edge := range l.Derived.Edges {
		oldEdges[edgeKey(edge, oldID, newID)] = edge
	}
	newEdges := map[string]derive.Edge{}
	for _, edge := range after.Edges {
		newEdges[edgeKey(edge, newID, newID)] = edge
	}
	keys := map[string]bool{}
	for key := range oldEdges {
		keys[key] = true
	}
	for key := range newEdges {
		keys[key] = true
	}
	for _, key := range sortedKeys(keys) {
		old, was := oldEdges[key]
		new, is := newEdges[key]
		if !was || !is || old.Address != new.Address || old.Network != new.Network || old.Port != new.Port {
			rep.Edges = append(rep.Edges, migrationEdgeChange{Key: key, Before: edgeAddress(old, was), After: edgeAddress(new, is)})
		}
	}
	rep.Work = compareMigrationTargets(rep, l, newLoaded, root, s.Secrets, oldID, newID)
	compareMigrationSecrets(rep, l, newLoaded)
	migrationDNSReview(rep, l, newLoaded, oldID, newID, oldID != newID || len(networkChanges) != 0 || len(hostChanges) != 0, instanceChanges)
	if err := reportServiceReferences(rep, root, oldID, before, networkChanges, instanceChanges, routeChanges, hostChanges); err != nil {
		return err
	}
	rep.Procedure = buildMigrationProcedure(l, newLoaded, oldID, newID, root, s.Secrets, flags, renames)
	_, err = out.Write(rep.markdown())
	return err
}

func MigrationReport(flags map[string]string, s Settings) ([]byte, error) {
	var report bytes.Buffer
	if err := Migrate(nil, &report, []string{"node"}, flags, s); err != nil {
		return nil, err
	}
	return report.Bytes(), nil
}

func edgeKey(edge derive.Edge, sourceID, targetID string) string {
	from := edge.FromInstance
	if from == "" {
		from = edge.From.Instance + ":" + edge.From.Port
	} else if sourceID != targetID && strings.HasPrefix(from, sourceID+"-") {
		from = targetID + strings.TrimPrefix(from, sourceID)
	}
	return edge.Route + " " + from + " -> " + edge.To.Instance + ":" + edge.To.Port
}

func edgeAddress(edge derive.Edge, exists bool) string {
	if !exists {
		return "(absent)"
	}
	if edge.Link != "" {
		// Adding a link changes an edge without changing its route, so
		// which link and who now dials is part of what changed.
		return fmt.Sprintf("%s:%d [%s] via %s, dialled by %s", edge.Address, edge.Port, edge.Network, edge.Link, edge.Dialer)
	}
	return fmt.Sprintf("%s:%d [%s]", edge.Address, edge.Port, edge.Network)
}

type NetworkChange struct {
	From string
	To   string
}

func parseNodeChange(spec string) (string, string, error) {
	fields := map[string]string{}
	for _, part := range strings.Split(spec, ",") {
		key, value, ok := strings.Cut(part, "=")
		if !ok || value == "" || (key != "from" && key != "to") || fields[key] != "" {
			return "", "", fmt.Errorf("--node: want from=<old>,to=<new>")
		}
		fields[key] = value
	}
	if fields["from"] == "" || fields["to"] == "" {
		return "", "", fmt.Errorf("--node: want from=<old>,to=<new>")
	}
	return fields["from"], fields["to"], nil
}

// parseNetworkChanges keeps each repeated --network occurrence together.
// The named fields avoid positional pairing between several networks.
func parseNetworkChanges(encoded string) ([]NetworkChange, error) {
	if encoded == "" {
		return nil, nil
	}
	var specs []string
	if err := json.Unmarshal([]byte(encoded), &specs); err != nil {
		return nil, fmt.Errorf("--network values: %w", err)
	}
	changes := make([]NetworkChange, 0, len(specs))
	fromSeen, toSeen := map[string]bool{}, map[string]bool{}
	for _, spec := range specs {
		fields := map[string]string{}
		for _, part := range strings.Split(spec, ",") {
			key, value, ok := strings.Cut(part, "=")
			if !ok || value == "" || (key != "from" && key != "to") || fields[key] != "" {
				return nil, fmt.Errorf("--network %q: want from=<old>,to=<new>", spec)
			}
			fields[key] = value
		}
		change := NetworkChange{From: fields["from"], To: fields["to"]}
		if change.From == "" || change.To == "" {
			return nil, fmt.Errorf("--network %q: give from and to", spec)
		}
		if fromSeen[change.From] || toSeen[change.To] {
			return nil, fmt.Errorf("--network %q: network given more than once", spec)
		}
		fromSeen[change.From], toSeen[change.To] = true, true
		changes = append(changes, change)
	}
	return changes, nil
}

// renameInventoryNetwork changes every schema-defined reference in the
// in-memory snapshot. It does not infer meaning from opaque values or text.
func renameInventoryNetwork(inv *inventory.Root, oldName, newName string) ([]string, error) {
	found := false
	for _, name := range inv.Networks {
		if name == newName {
			return nil, fmt.Errorf("network %q already exists", newName)
		}
		if name == oldName {
			found = true
		}
	}
	if !found {
		return nil, fmt.Errorf("network %q is not in networks.yaml", oldName)
	}
	inv.Networks = append([]string(nil), inv.Networks...)
	refs := []string{"networks.yaml: networks"}
	for i, name := range inv.Networks {
		if name == oldName {
			inv.Networks[i] = newName
		}
	}
	if info, ok := inv.NetworkInfo[oldName]; ok {
		infos := make(map[string]inventory.Network, len(inv.NetworkInfo))
		for name, value := range inv.NetworkInfo {
			infos[name] = value
		}
		delete(infos, oldName)
		info.Name = newName
		infos[newName] = info
		inv.NetworkInfo = infos
	}
	if inv.Universal == oldName {
		inv.Universal = newName
		refs = append(refs, "networks.yaml: universal")
	}
	if len(inv.Hosts) > 0 {
		inv.Hosts = cloneHosts(inv.Hosts)
		for key, h := range inv.Hosts {
			if e, ok := h.Endpoints[oldName]; ok {
				delete(h.Endpoints, oldName)
				h.Endpoints[newName] = e
				refs = append(refs, inventory.HostSource(key)+".endpoints."+oldName)
			}
			for name, targets := range h.Names {
				for i, ref := range targets {
					if ref.Endpoint == oldName {
						targets[i].Endpoint = newName
						refs = append(refs, inventory.HostSource(key)+".names."+name)
					}
				}
				h.Names[name] = targets
			}
			for name, iface := range h.Interfaces {
				for aid, a := range iface.Addresses {
					if a.Network == oldName {
						a.Network = newName
						iface.Addresses[aid] = a
						refs = append(refs, inventory.AddressSource(key, name, aid))
					}
				}
				h.Interfaces[name] = iface
			}
			for i, network := range h.Reaches {
				if network == oldName {
					h.Reaches[i] = newName
					refs = append(refs, inventory.HostSource(key)+".reaches")
				}
			}
			inv.Hosts[key] = h
		}
	}

	inv.ResolveHosts()
	if len(inv.Users) > 0 {
		users := make(map[string]inventory.User, len(inv.Users))
		for key, user := range inv.Users {
			users[key] = user
		}
		inv.Users = users
		for key, user := range inv.Users {
			credentials := make(map[string]inventory.Credential, len(user.Credentials))
			for name, value := range user.Credentials {
				credentials[name] = value
			}
			for credentialName, credential := range user.Credentials {
				for i, name := range credential.Reaches {
					if name == oldName {
						credential.Reaches = append([]string(nil), credential.Reaches...)
						credential.Reaches[i] = newName
						credentials[credentialName] = credential
						refs = append(refs, "users.yaml: users."+key+".credentials."+credentialName+".reaches")
						break
					}
				}
			}
			user.Credentials = credentials
			inv.Users[key] = user
		}
	}
	sort.Strings(refs)
	return refs, nil
}

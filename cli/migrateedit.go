package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/d0u9/rhumb/engine"

	"github.com/d0u9/rhumb/inventory"
	"gopkg.in/yaml.v3"
)

type migrationEdit struct {
	path     string // relative to the generator root
	original []byte
	replaced []byte
	mode     os.FileMode
	// moveTo, when set, is where the replaced file is written instead of
	// path, which is then removed: a renamed node's file.
	moveTo string
}

type migrationYAML struct {
	root  string
	files map[string]*migrationYAMLFile
}

type migrationYAMLFile struct {
	doc      yaml.Node
	original []byte
	mode     os.FileMode
	changed  bool
}

func (m *migrationYAML) file(path string) (*migrationYAMLFile, error) {
	if file := m.files[path]; file != nil {
		return file, nil
	}
	if path == "" || filepath.IsAbs(path) || filepath.Clean(path) != path || path == ".." || len(path) >= 3 && path[:3] == "../" {
		return nil, fmt.Errorf("unsafe inventory path %q", path)
	}
	abs := filepath.Join(m.root, path)
	parent := filepath.Dir(path)
	for parent != "." {
		info, err := os.Lstat(filepath.Join(m.root, parent))
		if err != nil {
			return nil, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("%s has a non-directory or symlink ancestor", path)
		}
		parent = filepath.Dir(parent)
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, err
	}
	file := &migrationYAMLFile{original: data, mode: info.Mode().Perm()}
	if err := yaml.Unmarshal(data, &file.doc); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	m.files[path] = file
	return file, nil
}

func migrationMap(node *yaml.Node, key string) (*yaml.Node, *yaml.Node) {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil, nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i], node.Content[i+1]
		}
	}
	return nil, nil
}

func migrationRoot(doc *yaml.Node) *yaml.Node {
	if doc != nil && len(doc.Content) == 1 {
		return doc.Content[0]
	}
	return nil
}

func migrationScalar(node *yaml.Node, old, next, where string) (bool, error) {
	if node == nil || node.Kind != yaml.ScalarNode || node.Value != old {
		return false, fmt.Errorf("stale migration field %s: expected %q", where, old)
	}
	if old == next {
		return false, nil
	}
	node.Value = next
	return true, nil
}

func migrationSetField(mapping *yaml.Node, key, old, next, where string) (bool, error) {
	if old == next {
		return false, nil
	}
	_, value := migrationMap(mapping, key)
	return migrationScalar(value, old, next, where)
}

func migrationSetSequence(node *yaml.Node, old, next []string, where string) (bool, error) {
	if len(old) != len(next) {
		return false, fmt.Errorf("migration cannot change sequence length at %s", where)
	}
	changed := false
	for i := range old {
		if old[i] == next[i] {
			continue
		}
		if node == nil || node.Kind != yaml.SequenceNode || i >= len(node.Content) {
			return false, fmt.Errorf("missing migration sequence at %s", where)
		}
		one, err := migrationScalar(node.Content[i], old[i], next[i], where)
		if err != nil {
			return false, err
		}
		changed = changed || one
	}
	return changed, nil
}

func migrationInstanceNode(doc *yaml.Node, id string) *yaml.Node {
	root := migrationRoot(doc)
	if root == nil {
		return nil
	}
	if root.Kind == yaml.MappingNode {
		if _, idNode := migrationMap(root, "id"); idNode != nil && idNode.Value == id {
			return root
		}
		_, root = migrationMap(root, "instances")
	}
	if root != nil && root.Kind == yaml.SequenceNode {
		for _, candidate := range root.Content {
			if _, idNode := migrationMap(candidate, "id"); idNode != nil && idNode.Value == id {
				return candidate
			}
		}
	}
	return nil
}

func (m *migrationYAML) patchInstances(old, next []inventory.Instance) error {
	if len(old) != len(next) {
		return fmt.Errorf("migration changed instance count")
	}
	for i, source := range old {
		target := next[i]
		if inventory.LocalName(source.ID) == inventory.LocalName(target.ID) && migrationPublishedEqual(source.Ports, target.Ports) && reflect.DeepEqual(source.Dials, target.Dials) {
			continue
		}
		file, err := m.file(source.Path)
		if err != nil {
			return err
		}
		instance := migrationInstanceNode(&file.doc, inventory.LocalName(source.ID))
		if instance == nil {
			return fmt.Errorf("%s: instance %q was not found", source.Path, inventory.LocalName(source.ID))
		}
		changed, err := migrationSetField(instance, "id", inventory.LocalName(source.ID), inventory.LocalName(target.ID), source.Path+": id")
		if err != nil {
			return err
		}
		file.changed = file.changed || changed
		_, dials := migrationMap(instance, "dials")
		for name, sourceDial := range source.Dials {
			if source.DialsDerived[name] {
				continue
			}
			// A dial to its own node is usually written without the node;
			// keep whichever form the file has.
			from := migrationDialText(sourceDial, source.ID)
			to := migrationDialText(target.Dials[name], target.ID)
			if _, value := migrationMap(dials, name); value != nil && value.Value == sourceDial {
				from, to = sourceDial, target.Dials[name]
			}
			changed, err = migrationSetField(dials, name, from, to, source.Path+": "+inventory.LocalName(source.ID)+".dials."+name)
			if err != nil {
				return err
			}
			file.changed = file.changed || changed
		}
		_, ports := migrationMap(instance, "ports")
		for portName, sourcePort := range source.Ports {
			targetPort := target.Ports[portName]
			if sourcePort.Published == targetPort.Published {
				continue
			}
			_, port := migrationMap(ports, portName)
			// published is a name or a list; the first entry is Published.
			_, published := migrationMap(port, "published")
			where := source.Path + ": ports." + portName + ".published"
			if published != nil && published.Kind == yaml.SequenceNode && len(published.Content) > 0 {
				published, where = published.Content[0], where+"[0]"
			}
			changed, err = migrationScalar(published, sourcePort.Published, targetPort.Published, where)
			if err != nil {
				return err
			}
			file.changed = file.changed || changed
		}
	}
	return nil
}

func migrationPublishedEqual(old, next inventory.Ports) bool {
	for name, port := range old {
		if port.Published != next[name].Published {
			return false
		}
	}
	return true
}

func (m *migrationYAML) patchNodes(old, next *inventory.Root, networks []NetworkChange) error {
	if len(old.Nodes) != len(next.Nodes) {
		return fmt.Errorf("migration changed node count")
	}
	for i, source := range old.Nodes {
		target := next.Nodes[i]
		file, err := m.file(source.Path)
		if err != nil {
			return err
		}
		mapping := migrationRoot(&file.doc)
		changed, err := migrationSetField(mapping, "id", source.ID, target.ID, source.Path+": id")
		if err != nil {
			return err
		}
		file.changed = file.changed || changed
		_, profiles := migrationMap(mapping, "profiles")
		for name, profile := range source.Profiles {
			_, profileNode := migrationMap(profiles, name)
			_, access := migrationMap(profileNode, "access")
			changed, err = migrationSetSequence(access, profile.AccessWritten, target.Profiles[name].AccessWritten, source.Path+": profiles."+name+".access")
			if err != nil {
				return err
			}
			file.changed = file.changed || changed
		}
		if err := m.patchInstances(source.Instances, target.Instances); err != nil {
			return err
		}
	}
	return nil
}

func (m *migrationYAML) patchNetworks(old, next *inventory.Root) error {
	if len(old.Networks) != len(next.Networks) {
		return fmt.Errorf("migration changed network count")
	}
	if reflect.DeepEqual(old.Networks, next.Networks) && old.Universal == next.Universal {
		return nil
	}
	file, err := m.file(inventory.NetworksFilename)
	if err != nil {
		return err
	}
	mapping := migrationRoot(&file.doc)
	_, sequence := migrationMap(mapping, "networks")
	for i := range old.Networks {
		if old.Networks[i] == next.Networks[i] {
			continue
		}
		if sequence == nil || sequence.Kind != yaml.SequenceNode || i >= len(sequence.Content) {
			return fmt.Errorf("missing migration sequence at networks.yaml: networks")
		}
		// Each entry is {name, subnet, gateway}; only the name changes.
		_, name := migrationMap(sequence.Content[i], "name")
		changed, err := migrationScalar(name, old.Networks[i], next.Networks[i], "networks.yaml: networks["+strconv.Itoa(i)+"].name")
		if err != nil {
			return err
		}
		file.changed = file.changed || changed
	}
	changed, err := migrationSetField(mapping, "universal", old.Universal, next.Universal, "networks.yaml: universal")
	if err != nil {
		return err
	}
	file.changed = file.changed || changed
	return nil
}

func (m *migrationYAML) patchHosts(old, next *inventory.Root) error {
	if reflect.DeepEqual(old.Hosts, next.Hosts) {
		return nil
	}
	if len(old.Hosts) != len(next.Hosts) {
		return fmt.Errorf("migration cannot create or remove hosts")
	}
	file, err := m.file(inventory.HostsFilename)
	if err != nil {
		return err
	}
	root := migrationRoot(&file.doc)
	// Root merges can own hosts too; materialize before locating their source.
	if err := detachHostAliases(root); err != nil {
		return err
	}
	_, hosts := migrationMap(root, "hosts")
	if hosts == nil {
		hosts = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "hosts"}, hosts)
	}
	if hosts.Kind != yaml.MappingNode {
		return fmt.Errorf("hosts must be a mapping")
	}
	for _, key := range sortedKeys(next.Hosts) {
		h := next.Hosts[key]
		prior, existed := old.Hosts[key]
		if existed && reflect.DeepEqual(prior, h) {
			continue
		}
		var desired yaml.Node
		if err := desired.Encode(h); err != nil {
			return err
		}
		_, current := migrationMap(hosts, key)
		if !existed || current == nil {
			return fmt.Errorf("host %q has no authored source", key)
		}
		var before yaml.Node
		if err := before.Encode(prior); err != nil {
			return err
		}
		if err := patchHostYAML(current, &before, &desired); err != nil {
			return fmt.Errorf("host %s: %w", key, err)
		}
		file.changed = true
	}
	if len(next.Hosts) < len(old.Hosts) {
		return fmt.Errorf("migration cannot remove hosts")
	}
	return nil
}

// Alias expansion preserves each host's current values during scoped edits.
func detachHostAliases(node *yaml.Node) error {
	if node.Kind == yaml.AliasNode {
		expanded, err := cloneHostYAML(node, map[*yaml.Node]bool{})
		if err != nil {
			return err
		}
		*node = *expanded
	}
	for _, child := range node.Content {
		if err := detachHostAliases(child); err != nil {
			return err
		}
	}
	if node.Kind == yaml.MappingNode {
		return flattenHostMerges(node)
	}
	return nil
}

// Materialize effective keys so removing a field cannot reveal its merged value.
func flattenHostMerges(node *yaml.Node) error {
	held := map[string]bool{}
	var content []*yaml.Node
	var merges []*yaml.Node
	for i := 0; i+1 < len(node.Content); i += 2 {
		key, value := node.Content[i], node.Content[i+1]
		if key.Tag == "!!merge" {
			merges = append(merges, value)
			for _, comment := range []string{key.HeadComment, key.LineComment, key.FootComment, value.HeadComment, value.LineComment, value.FootComment} {
				if comment != "" {
					if node.FootComment != "" {
						node.FootComment += "\n"
					}
					node.FootComment += comment
				}
			}
		} else {
			held[key.Value] = true
			content = append(content, key, value)
		}
	}
	if len(merges) == 0 {
		return nil
	}
	for _, merge := range merges {
		sources := []*yaml.Node{merge}
		if merge.Kind == yaml.SequenceNode {
			sources = merge.Content
		}
		for _, source := range sources {
			if source.Kind != yaml.MappingNode {
				return fmt.Errorf("host YAML merge requires mappings")
			}
			for i := 0; i+1 < len(source.Content); i += 2 {
				key, value := source.Content[i], source.Content[i+1]
				if !held[key.Value] {
					held[key.Value] = true
					content = append(content, key, value)
				}
			}
		}
	}
	node.Content = content
	return nil
}

func cloneHostYAML(node *yaml.Node, active map[*yaml.Node]bool) (*yaml.Node, error) {
	if node == nil || active[node] {
		return nil, fmt.Errorf("invalid or cyclic host YAML alias")
	}
	active[node] = true
	defer delete(active, node)
	if node.Kind == yaml.AliasNode {
		copy, err := cloneHostYAML(node.Alias, active)
		if err != nil {
			return nil, err
		}
		for _, comment := range []string{node.HeadComment, node.LineComment, node.FootComment} {
			if comment == "" {
				continue
			}
			// The YAML emitter drops line comments on expanded block mappings.
			if copy.HeadComment != "" {
				copy.HeadComment += "\n"
			}
			copy.HeadComment += comment
		}
		return copy, nil
	}
	copy := *node
	copy.Anchor, copy.Alias = "", nil
	copy.Content = nil
	for _, child := range node.Content {
		next, err := cloneHostYAML(child, active)
		if err != nil {
			return nil, err
		}
		copy.Content = append(copy.Content, next)
	}
	return &copy, nil
}

func removeMigrationKey(mapping *yaml.Node, key string) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content = append(mapping.Content[:i], mapping.Content[i+2:]...)
			return
		}
	}
}

// Patch only changed typed host fields; retain comments and untouched YAML.
func patchHostYAML(current, before, after *yaml.Node) error {
	if reflect.DeepEqual(before, after) {
		return nil
	}
	if current == nil {
		return fmt.Errorf("missing authored field")
	}
	if before.Kind == yaml.MappingNode && after.Kind == yaml.MappingNode && current.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(before.Content); i += 2 {
			key := before.Content[i].Value
			_, next := migrationMap(after, key)
			if next == nil {
				removeMigrationKey(current, key)
			}
		}
		for i := 0; i+1 < len(after.Content); i += 2 {
			key := after.Content[i].Value
			_, old := migrationMap(before, key)
			_, at := migrationMap(current, key)
			if old == nil && at == nil {
				current.Content = append(current.Content, after.Content[i], after.Content[i+1])
				continue
			}
			if old == nil {
				old = at
			}
			if err := patchHostYAML(at, old, after.Content[i+1]); err != nil {
				return err
			}
		}
		return nil
	}
	if current.Kind == yaml.ScalarNode && after.Kind == yaml.ScalarNode {
		current.Value, current.Tag = after.Value, after.Tag
		return nil
	}
	head, line, foot := current.HeadComment, current.LineComment, current.FootComment
	*current = *after
	current.HeadComment, current.LineComment, current.FootComment = head, line, foot
	return nil
}

func (m *migrationYAML) patchRoutes(old, next *inventory.Root, changes []routeChange) error {
	if reflect.DeepEqual(old.Routes, next.Routes) || len(old.Routes) == 0 && len(next.Routes) == 0 {
		return nil
	}
	file, err := m.file(inventory.RoutesFilename)
	if err != nil {
		return err
	}
	_, routes := migrationMap(migrationRoot(&file.doc), "routes")
	renamed := map[string]string{}
	for _, change := range changes {
		renamed[change.From] = change.To
	}
	// A renamed node takes the routes scoped to it along.
	nodeRename := migrationNodeRename(old, next)
	newNameOf := func(oldName string) string {
		if to := renamed[oldName]; to != "" {
			return to
		}
		if scope, local := inventory.RouteScope(oldName); scope != "" && nodeRename[scope] != "" {
			return nodeRename[scope] + inventory.QualifiedSep + local
		}
		return oldName
	}
	// A scope every route of which moves to one new scope, not already
	// written, is the group key renamed in place, so the file keeps its order.
	moves := map[string]map[string]bool{}
	for oldName := range old.Routes {
		from, _ := inventory.RouteScope(oldName)
		to, _ := inventory.RouteScope(newNameOf(oldName))
		if moves[from] == nil {
			moves[from] = map[string]bool{}
		}
		moves[from][to] = true
	}
	for from, tos := range moves {
		if from == "" || len(tos) != 1 {
			continue
		}
		for to := range tos {
			if to == from || to == "" {
				continue
			}
			if key, _ := migrationMap(routes, to); key != nil {
				continue
			}
			key, _ := migrationMap(routes, from)
			changed, err := migrationScalar(key, from, to, "routes.yaml: scope key")
			if err != nil {
				return err
			}
			file.changed = file.changed || changed
		}
	}
	for oldName, source := range old.Routes {
		newName := newNameOf(oldName)
		target, ok := next.Routes[newName]
		if !ok {
			return fmt.Errorf("route %q missing from target", newName)
		}
		oldScope, oldLocal := inventory.RouteScope(oldName)
		newScope, newLocal := inventory.RouteScope(newName)
		// Where the route is written now: under its old scope's key, or the
		// new one when the whole scope was renamed above.
		parent := routes
		if oldScope != "" {
			_, parent = migrationMap(routes, oldScope)
			if parent == nil {
				_, parent = migrationMap(routes, newScope)
			}
		}
		key, route := migrationMap(parent, oldLocal)
		if key == nil {
			return fmt.Errorf("routes.yaml: route %q was not found", oldName)
		}
		changed, err := migrationScalar(key, oldLocal, newLocal, "routes.yaml: route key")
		if err != nil {
			return err
		}
		file.changed = file.changed || changed
		_, hops := migrationMap(route, "hops")
		changed, err = migrationSetSequence(hops, source.Hops, target.Hops, "routes.yaml: "+oldName+".hops")
		if err != nil {
			return err
		}
		file.changed = file.changed || changed
		if newScope == oldScope || parent == migrationScopeNode(routes, newScope) {
			continue
		}
		// Moved to another scope: take the pair out and write it there.
		for i := 0; i+1 < len(parent.Content); i += 2 {
			if parent.Content[i] == key {
				parent.Content = append(parent.Content[:i], parent.Content[i+2:]...)
				break
			}
		}
		dest := migrationScopeNode(routes, newScope)
		if dest == nil {
			dest = &yaml.Node{Kind: yaml.MappingNode}
			routes.Content = append(routes.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: newScope}, dest)
		}
		dest.Content = append(dest.Content, key, route)
		file.changed = true
	}
	// A scope left with no routes is removed.
	for i := 0; i+1 < len(routes.Content); {
		v := routes.Content[i+1]
		if v.Kind == yaml.MappingNode && len(v.Content) == 0 {
			routes.Content = append(routes.Content[:i], routes.Content[i+2:]...)
			file.changed = true
			continue
		}
		i += 2
	}
	return nil
}

// migrationScopeNode is the mapping routes are written in for a scope: the
// routes mapping itself for the top level.
func migrationScopeNode(routes *yaml.Node, scope string) *yaml.Node {
	if scope == "" {
		return routes
	}
	_, v := migrationMap(routes, scope)
	return v
}

// migrationNodeRename is the one node id present before and not after,
// mapped to the one present after and not before, when there is exactly one
// of each.
func migrationNodeRename(old, next *inventory.Root) map[string]string {
	ids := func(r *inventory.Root) map[string]bool {
		out := map[string]bool{}
		for _, n := range r.Nodes {
			out[n.ID] = true
		}
		return out
	}
	before, after := ids(old), ids(next)
	var gone, added []string
	for id := range before {
		if !after[id] {
			gone = append(gone, id)
		}
	}
	for id := range after {
		if !before[id] {
			added = append(added, id)
		}
	}
	if len(gone) == 1 && len(added) == 1 {
		return map[string]string{gone[0]: added[0]}
	}
	return nil
}

func (m *migrationYAML) patchUsers(old, next *inventory.Root) error {
	if len(old.Users) == 0 {
		return nil
	}
	file, err := m.file(inventory.UsersFilename)
	if err != nil {
		return err
	}
	_, sets := migrationMap(migrationRoot(&file.doc), "sets")
	for name, members := range old.Sets {
		_, list := migrationMap(sets, name)
		changed, err := migrationSetSequence(list, members, next.Sets[name], "users.yaml: sets."+name)
		if err != nil {
			return err
		}
		file.changed = file.changed || changed
	}
	_, users := migrationMap(migrationRoot(&file.doc), "users")
	for name, source := range old.Users {
		_, user := migrationMap(users, name)
		_, access := migrationMap(user, "access")
		changed, err := migrationSetSequence(access, source.AccessWritten, next.Users[name].AccessWritten, "users.yaml: "+name+".access")
		if err != nil {
			return err
		}
		file.changed = file.changed || changed
		_, credentials := migrationMap(user, "credentials")
		for credentialName, credential := range source.Credentials {
			_, one := migrationMap(credentials, credentialName)
			_, access = migrationMap(one, "access")
			changed, err = migrationSetSequence(access, credential.AccessWritten, next.Users[name].Credentials[credentialName].AccessWritten, "users.yaml: "+name+"."+credentialName+".access")
			if err != nil {
				return err
			}
			file.changed = file.changed || changed
			_, reaches := migrationMap(one, "reaches")
			changed, err = migrationSetSequence(reaches, credential.Reaches, next.Users[name].Credentials[credentialName].Reaches, "users.yaml: "+name+"."+credentialName+".reaches")
			if err != nil {
				return err
			}
			file.changed = file.changed || changed
		}
	}
	return nil
}

func buildMigrationEdits(root string, before, after engine.Loaded, networks []NetworkChange, routes []routeChange) ([]migrationEdit, error) {
	m := migrationYAML{root: root, files: map[string]*migrationYAMLFile{}}
	if err := m.patchNodes(before.Inv, after.Inv, networks); err != nil {
		return nil, err
	}
	if err := m.patchNetworks(before.Inv, after.Inv); err != nil {
		return nil, err
	}
	if err := m.patchHosts(before.Inv, after.Inv); err != nil {
		return nil, err
	}
	if err := m.patchRoutes(before.Inv, after.Inv, routes); err != nil {
		return nil, err
	}
	if err := m.patchUsers(before.Inv, after.Inv); err != nil {
		return nil, err
	}
	var edits []migrationEdit
	for path, file := range m.files {
		if !file.changed {
			continue
		}
		data, err := encodeMigrationYAML(file.original, &file.doc)
		if err != nil {
			return nil, fmt.Errorf("encoding %s: %w", path, err)
		}
		if bytes.Equal(data, file.original) {
			continue
		}
		edits = append(edits, migrationEdit{path: path, original: file.original, replaced: data, mode: file.mode})
	}
	// A node whose file is named after it moves with a rename.
	afterPath := map[string]string{}
	for i, n := range before.Inv.Nodes {
		if i < len(after.Inv.Nodes) && after.Inv.Nodes[i].Path != n.Path {
			afterPath[n.Path] = after.Inv.Nodes[i].Path
		}
	}
	for from, to := range afterPath {
		found := false
		for i := range edits {
			if edits[i].path == from {
				edits[i].moveTo, found = to, true
			}
		}
		if !found {
			file, err := m.file(from)
			if err != nil {
				return nil, err
			}
			edits = append(edits, migrationEdit{path: from, original: file.original, replaced: file.original, mode: file.mode, moveTo: to})
		}
	}
	sort.Slice(edits, func(i, j int) bool { return edits[i].path < edits[j].path })
	return edits, nil
}

// migrationDialText is a dial as its instance file writes it: without the
// node when the target is on the instance's own node.
func migrationDialText(dial, instanceID string) string {
	node, _, _ := strings.Cut(instanceID, inventory.QualifiedSep)
	return strings.TrimPrefix(dial, node+inventory.QualifiedSep)
}

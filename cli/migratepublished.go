package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/d0u9/rhumb/inventory"
)

type publishedChange struct{ Instance, Port, To string }

func (c publishedChange) key() string { return c.Instance + ":" + c.Port }

func parsePublishedChanges(encoded string) ([]publishedChange, error) {
	if encoded == "" {
		return nil, nil
	}
	var specs []string
	if err := json.Unmarshal([]byte(encoded), &specs); err != nil {
		return nil, fmt.Errorf("--published values: %w", err)
	}
	var changes []publishedChange
	seen := map[string]bool{}
	for _, spec := range specs {
		fields := map[string]string{}
		for _, part := range strings.Split(spec, ",") {
			key, value, ok := strings.Cut(part, "=")
			if !ok || value == "" || (key != "instance" && key != "port" && key != "to") || fields[key] != "" {
				return nil, fmt.Errorf("--published %q: want instance=<id>,port=<name>,to=<hostname>", spec)
			}
			fields[key] = value
		}
		change := publishedChange{fields["instance"], fields["port"], fields["to"]}
		if change.Instance == "" || change.Port == "" || change.To == "" || strings.ContainsAny(change.To, "/:\\") {
			return nil, fmt.Errorf("--published %q: want instance=<id>,port=<name>,to=<bare hostname>", spec)
		}
		if seen[change.key()] {
			return nil, fmt.Errorf("--published %q: port given more than once", spec)
		}
		seen[change.key()] = true
		changes = append(changes, change)
	}
	return changes, nil
}

// renameMigrationPublished changes only a port's typed published field in the
// preview snapshot. DNS is external and is never changed here.
func renameMigrationPublished(inv *inventory.Root, nodeIndex int, changes []publishedChange) (map[string]string, error) {
	oldNames := map[string]string{}
	if len(changes) == 0 {
		return oldNames, nil
	}
	node := &inv.Nodes[nodeIndex]
	node.Instances = append([]inventory.Instance(nil), node.Instances...)
	for _, change := range changes {
		found := false
		for i, inst := range node.Instances {
			if inventory.LocalName(inst.ID) != change.Instance || inst.Service == "" {
				continue
			}
			port, ok := inst.Ports[change.Port]
			if !ok || port.Published == "" {
				return nil, fmt.Errorf("instance %q has no published name on port %q", change.Instance, change.Port)
			}
			oldNames[change.key()] = port.Published
			ports := make(inventory.Ports, len(inst.Ports))
			for name, value := range inst.Ports {
				ports[name] = value
			}
			// Published is Names[0]; a change renames that first name only.
			port.Published = change.To
			port.Names = append([]string(nil), port.Names...)
			port.Names[0] = change.To
			ports[change.Port] = port
			node.Instances[i].Ports = ports
			found = true
			break
		}
		if !found {
			return nil, fmt.Errorf("node %q has no authored instance %q", node.ID, change.Instance)
		}
	}
	return oldNames, nil
}

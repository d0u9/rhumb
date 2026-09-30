package cli

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
)

// migrationReport is everything one validated plan knows. The analysis fills
// it; markdown renders it. Keeping the two apart lets the report be reordered
// into changes, attention items and a procedure without re-running anything.
type migrationReport struct {
	OldID, NewID, Root, Inventory string
	Apply                         bool

	Edits          []migrationChangeRow
	Edges          []migrationEdgeChange
	Targets        []migrationTargetChange
	Secrets        []string
	DNS            []migrationDNSItem
	ServiceMatches []migrationTextMatch
	NodeMatches    []migrationTextMatch
	InstanceIDs    []string
	Work           []migrationTargetWork
	Procedure      migrationProcedure
}

// migrationChangeRow is one requested inventory change and the typed references
// it rewrites.
type migrationChangeRow struct {
	Kind, Before, After string
	Refs                []string
}

type migrationEdgeChange struct{ Key, Before, After string }

// migrationTargetChange is one export target whose rendered output differs,
// or whose comparison could not be made (Unknown names why).
type migrationTargetChange struct {
	Owner, Label string
	Files        []migrationFileChange
	Unknown      string
}

type migrationFileChange struct {
	// Action is add, remove, update or move.
	Action   string
	Old, New string
	// Same is set for a move whose bytes and mode are unchanged.
	Same bool
}

// migrationTextMatch is a literal old fact found in an authored file.
type migrationTextMatch struct {
	Path       string
	Line       int
	Kind, Term string
	Text       string
}

type migrationDNSItem struct {
	Name, OldName, Service, Port string
	HostBefore, HostAfter        string
	IngressBefore, IngressAfter  string
	NoIngress                    bool
	Addresses                    []migrationDNSAddress
}

type migrationDNSAddress struct{ Role, Node, Before, After string }

func (r *migrationReport) markdown() []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "# Node migration: `%s` → `%s`\n\n", r.OldID, r.NewID)
	if r.Apply {
		b.WriteString("> Preview before apply. Nothing is written until you confirm. External DNS was not checked.\n\n")
	} else {
		b.WriteString("> Proposed plan. No inventory, secret or DNS change has been made. External DNS was not checked.\n\n")
	}
	fmt.Fprintf(&b, "- Generator root: `%s`\n- Inventory file: `%s`\n", r.Root, r.Inventory)
	attention := r.attention()
	fmt.Fprintf(&b, "- Needs attention: %d item(s)\n", len(attention))
	// Links name the heading text, URL-encoded, which is how Obsidian resolves
	// them; a GitHub-style slug such as #1-changes does not jump there.
	b.WriteString("\n## Contents\n\n1. [Changes](#1.%20Changes)\n2. [Needs attention](#2.%20Needs%20attention)\n3. [Procedure](#3.%20Procedure)\n")

	b.WriteString("\n## 1. Changes\n")
	r.writeChanges(&b)

	b.WriteString("\n## 2. Needs attention\n\n")
	if len(attention) == 0 {
		b.WriteString("Nothing found.\n")
	}
	for i, item := range attention {
		fmt.Fprintf(&b, "### 2.%d %s\n\n%s\n", i+1, item.title, item.body)
	}

	b.WriteString("\n## 3. Procedure\n")
	r.writeProcedure(&b, attention)
	return b.Bytes()
}

func (r *migrationReport) writeChanges(b *bytes.Buffer) {
	b.WriteString("\n### Inventory\n\n")
	if len(r.Edits) == 0 {
		b.WriteString("No inventory change.\n")
	} else {
		b.WriteString("| Change | Before | After |\n|---|---|---|\n")
		for _, edit := range r.Edits {
			fmt.Fprintf(b, "| %s | %s | %s |\n", edit.Kind, mdCode(edit.Before), mdCode(edit.After))
		}
		for _, edit := range r.Edits {
			if len(edit.Refs) == 0 {
				continue
			}
			fmt.Fprintf(b, "\n%s %s → %s rewrites %d typed reference(s):\n\n", edit.Kind, mdCode(edit.Before), mdCode(edit.After), len(edit.Refs))
			for _, ref := range edit.Refs {
				fmt.Fprintf(b, "- %s\n", mdCode(ref))
			}
		}
	}

	b.WriteString("\n### Route edges\n\n")
	if len(r.Edges) == 0 {
		b.WriteString("No route edge changes its address, network or port.\n")
	} else {
		b.WriteString("| Edge | Before | After |\n|---|---|---|\n")
		for _, edge := range r.Edges {
			fmt.Fprintf(b, "| %s | %s | %s |\n", mdCode(edge.Key), mdCode(edge.Before), mdCode(edge.After))
		}
	}

	b.WriteString("\n### Rendered output\n\n")
	if len(r.Targets) == 0 {
		b.WriteString("Every target renders identically.\n")
	} else {
		b.WriteString("Grouped by the machine each file is installed on or handed to.\n")
		owner := ""
		for _, target := range r.Targets {
			if target.Owner != owner {
				owner = target.Owner
				fmt.Fprintf(b, "\n#### %s\n\n| Instance | File | Change |\n|---|---|---|\n", mdCode(owner))
			}
			if target.Unknown != "" {
				fmt.Fprintf(b, "| %s | — | **not compared**: %s |\n", mdCode(target.Label), mdCell(target.Unknown))
				continue
			}
			// A renamed node moves every file; list only those whose content
			// changes and count the rest.
			moved := 0
			for _, file := range target.Files {
				if file.Action == "move" && file.Same {
					moved++
					continue
				}
				fmt.Fprintf(b, "| %s | %s | %s |\n", mdCode(target.Label), mdCode(migrationFileName(file)), file.describe())
			}
			if moved != 0 {
				fmt.Fprintf(b, "| %s | %d other file(s) | bundle path moves; content identical |\n", mdCode(target.Label), moved)
			}
		}
	}

	b.WriteString("\n### Secret paths\n\n")
	if len(r.Secrets) == 0 {
		b.WriteString("Unchanged.\n")
	} else {
		for _, line := range r.Secrets {
			fmt.Fprintf(b, "- %s\n", line)
		}
	}
}

func (f migrationFileChange) describe() string {
	switch f.Action {
	case "add":
		return "new file"
	case "remove":
		return "no longer rendered"
	case "update":
		return "content changed"
	}
	if f.Same {
		return "bundle path moves; content identical"
	}
	return "bundle path moves; **content changed**"
}

// migrationFileName drops the bundle's node/service/instance prefix, which
// the table already shows.
func migrationFileName(f migrationFileChange) string {
	path := f.New
	if path == "" {
		path = f.Old
	}
	return exportOutputName(path)
}

type migrationAttentionItem struct {
	anchor, title, body string
}

// attention lists the items the operator must decide. The procedure refers
// to them by number, so the order here is the order they are resolved in.
func (r *migrationReport) attention() []migrationAttentionItem {
	var items []migrationAttentionItem
	var unknown []string
	for _, target := range r.Targets {
		if target.Unknown != "" {
			unknown = append(unknown, fmt.Sprintf("- %s on %s: %s", mdCode(target.Label), mdCode(target.Owner), mdCell(target.Unknown)))
		}
	}
	if len(unknown) != 0 {
		items = append(items, migrationAttentionItem{"render", "Render comparison unavailable",
			"These targets could not be rendered, so the plan cannot say whether they change. Treat each as changed: export, inspect and redeploy it.\n\nWhen an instance is renamed, its secrets reach the new ID only when the plan is applied, so before that `no own secret named` or a missing file under the new ID is expected. Export the target again after applying; only an error that remains then needs fixing.\n\n" + strings.Join(unknown, "\n") + "\n"})
	}
	if r.OldID != r.NewID && strings.Contains(r.Inventory, r.OldID) {
		items = append(items, migrationAttentionItem{"filename", "Inventory filename keeps the old node ID",
			fmt.Sprintf("Apply changes the `id` inside %s but keeps the filename. Rename the file by hand afterwards if the name matters to you; nothing reads it as the ID.\n", mdCode(r.Inventory))})
	}
	if len(r.Secrets) != 0 {
		items = append(items, migrationAttentionItem{"secrets", "Secret paths change",
			"Apply copies each secret to its new path and verifies it; the old files stay for rollback. Until you retire them, `rhumb check` reports the old paths as orphaned and exits non-zero. Investigate every other problem it reports.\n\n" + strings.Join(prefixLines(r.Secrets, "- "), "\n") + "\n"})
	}
	if len(r.InstanceIDs) != 0 {
		items = append(items, migrationAttentionItem{"instances", "Instance IDs still contain an old name",
			"These IDs are not renamed by this plan. Container names, data directories and secret paths are derived from them, so renaming one later is a second migration. Rename them in this plan if they should change.\n\n" + strings.Join(prefixLines(r.InstanceIDs, "- "), "\n") + "\n"})
	}
	if len(r.DNS) != 0 {
		items = append(items, migrationAttentionItem{"dns", "DNS records to inspect", r.dnsMarkdown()})
	}
	if len(r.ServiceMatches) != 0 {
		items = append(items, migrationAttentionItem{"service-text", "Old names in service files",
			"Service templates and settings are never rewritten automatically. Decide for each line whether it must change; a comment may simply record history.\n\n" + migrationMatchTable(r.ServiceMatches)})
	}
	if len(r.NodeMatches) != 0 {
		items = append(items, migrationAttentionItem{"node-text", "Old names in node files",
			"Typed fields in this list are already covered by the inventory changes above. Check the opaque `values`, `deploy` settings and comments.\n\n" + migrationMatchTable(r.NodeMatches)})
	}
	return items
}

func (r *migrationReport) dnsMarkdown() string {
	var b strings.Builder
	b.WriteString("The inventory has no DNS zone, so every item is a check, not a known change. A backend's private address is a candidate only; the answer normally points at the ingress.\n")
	for _, item := range r.DNS {
		fmt.Fprintf(&b, "\n**%s** — %s on port %s\n\n", mdCode(item.Name), mdCode(item.Service), mdCode(item.Port))
		if item.OldName != "" && item.OldName != item.Name {
			fmt.Fprintf(&b, "- Published name: %s → %s\n", mdCode(item.OldName), mdCode(item.Name))
		}
		fmt.Fprintf(&b, "- Hosted on: %s → %s\n", mdCode(item.HostBefore), mdCode(item.HostAfter))
		if item.NoIngress {
			b.WriteString("- Ingress: no route enters this port; DNS target unknown\n")
		} else {
			fmt.Fprintf(&b, "- Ingress: %s → %s\n", item.IngressBefore, item.IngressAfter)
		}
		for _, address := range item.Addresses {
			fmt.Fprintf(&b, "- %s %s: %s → %s\n", address.Role, mdCode(address.Node), address.Before, address.After)
		}
	}
	return b.String()
}

func migrationMatchTable(matches []migrationTextMatch) string {
	var b strings.Builder
	b.WriteString("| File | Kind | Found | Line |\n|---|---|---|---|\n")
	for _, match := range matches {
		fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", mdCode(fmt.Sprintf("%s:%d", match.Path, match.Line)), match.Kind, mdCode(match.Term), mdCode(match.Text))
	}
	return b.String()
}

func prefixLines(lines []string, prefix string) []string {
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = prefix + line
	}
	return out
}

// mdCode renders a value as inline code that survives a table cell.
func mdCode(value string) string {
	if value == "" {
		return "—"
	}
	value = strings.ReplaceAll(value, "|", `\|`)
	fence := "`"
	for strings.Contains(value, fence) {
		fence += "`"
	}
	if strings.HasPrefix(value, "`") || strings.HasSuffix(value, "`") {
		return fence + " " + value + " " + fence
	}
	return fence + value + fence
}

// mdCell keeps prose on one table line.
func mdCell(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "\n", " "), "|", `\|`)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

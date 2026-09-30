package cli

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func renameScalars(n *yaml.Node, from, to string) {
	if n.Kind == yaml.ScalarNode {
		n.Value = strings.ReplaceAll(n.Value, from, to)
	}
	for _, c := range n.Content {
		renameScalars(c, from, to)
	}
}

// TestEncodeMigrationYAML_RenameKeepsLayout: a rename changes the renamed
// text and nothing else — comments, blank lines, indentation, flow lists and
// quoting stay as written.
func TestEncodeMigrationYAML_RenameKeepsLayout(t *testing.T) {
	original := `# routes
routes:
  sea:
    hops: [network-4/ss:users, far/ss:users]

  # 中文注释 network-4
  network-4:
    samba:
      hops: ["network-4/samba:smb"]
users:
  dana: {access: ['network-4/samba']}
`
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(original), &doc); err != nil {
		t.Fatal(err)
	}
	renameScalars(&doc, "network-4", "network-8")
	got, err := encodeMigrationYAML([]byte(original), &doc)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.ReplaceAll(original, "network-4/", "network-8/")
	want = strings.Replace(want, "  network-4:", "  network-8:", 1)
	if string(got) != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

// TestEncodeMigrationYAML_ShapeChangeUsesTwoSpaces: an edit that moves or
// removes an entry is re-encoded at the house indent.
func TestEncodeMigrationYAML_ShapeChangeUsesTwoSpaces(t *testing.T) {
	original := "a:\n  b: 1\n  c: 2\n"
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(original), &doc); err != nil {
		t.Fatal(err)
	}
	m := doc.Content[0].Content[1]
	m.Content = m.Content[:2]
	got, err := encodeMigrationYAML([]byte(original), &doc)
	if err != nil || string(got) != "a:\n  b: 1\n" {
		t.Fatalf("got %q, %v", got, err)
	}
}

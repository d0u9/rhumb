package cli

import (
	"bytes"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// encodeMigrationYAML writes doc back over original. When the edit left the
// document's shape alone and only changed scalars — a rename, which is what
// most migrations are — each changed scalar is replaced where it stands, so
// indentation, blank lines, flow lists and quoting stay as they were written.
// Anything else is re-encoded at two spaces, which is the house indent.
func encodeMigrationYAML(original []byte, doc *yaml.Node) ([]byte, error) {
	var pristine yaml.Node
	if err := yaml.Unmarshal(original, &pristine); err == nil {
		var changes []scalarChange
		if sameYAMLShape(&pristine, doc, &changes) {
			if out, ok := patchYAMLScalars(original, changes); ok {
				return out, nil
			}
		}
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

type scalarChange struct {
	line, column int
	from, to     string
}

// sameYAMLShape reports whether a and b differ only in scalar values,
// collecting each one that does at a's position.
func sameYAMLShape(a, b *yaml.Node, changes *[]scalarChange) bool {
	if a.Kind != b.Kind || len(a.Content) != len(b.Content) {
		return false
	}
	if a.Kind == yaml.ScalarNode && a.Value != b.Value {
		*changes = append(*changes, scalarChange{line: a.Line, column: a.Column, from: a.Value, to: b.Value})
	}
	for i := range a.Content {
		if !sameYAMLShape(a.Content[i], b.Content[i], changes) {
			return false
		}
	}
	return true
}

// yamlIndicators are the characters that can make a plain scalar need
// quoting. A replacement introducing one its original did not have is not
// patched in place, since the original's quoting may not cover it.
const yamlIndicators = ":#,[]{}&*!|>'\"%@`"

func patchYAMLScalars(original []byte, changes []scalarChange) ([]byte, bool) {
	lines := strings.SplitAfter(string(original), "\n")
	// Right to left within a line, so earlier columns stay valid.
	for i := len(changes) - 1; i >= 0; i-- {
		for j := 0; j < i; j++ {
			if changes[j].line > changes[j+1].line || changes[j].line == changes[j+1].line && changes[j].column > changes[j+1].column {
				changes[j], changes[j+1] = changes[j+1], changes[j]
			}
		}
	}
	for i := len(changes) - 1; i >= 0; i-- {
		c := changes[i]
		if c.line < 1 || c.line > len(lines) || strings.ContainsAny(c.from, "\n\\") || strings.ContainsAny(c.to, "\n\\") {
			return nil, false
		}
		for _, r := range c.to {
			if strings.ContainsRune(yamlIndicators, r) && !strings.ContainsRune(c.from, r) {
				return nil, false
			}
		}
		line := lines[c.line-1]
		off := 0
		for n := 1; n < c.column; n++ {
			if off >= len(line) {
				return nil, false
			}
			_, size := utf8.DecodeRuneInString(line[off:])
			off += size
		}
		rest := line[off:]
		if rest != "" && (rest[0] == '"' || rest[0] == '\'') {
			if strings.ContainsRune(c.from, rune(rest[0])) || strings.ContainsRune(c.to, rune(rest[0])) {
				return nil, false
			}
			off++
			rest = rest[1:]
		}
		if !strings.HasPrefix(rest, c.from) {
			return nil, false
		}
		lines[c.line-1] = line[:off] + c.to + rest[len(c.from):]
	}
	return []byte(strings.Join(lines, "")), true
}

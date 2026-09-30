package cli

import (
	"fmt"
	"strings"
)

// plural is count followed by noun, pluralised in English.
func plural(count int, noun string) string {
	if count == 1 {
		return "1 " + noun
	}
	if strings.HasSuffix(noun, "ty") {
		return fmt.Sprintf("%d %sies", count, strings.TrimSuffix(noun, "y"))
	}
	return fmt.Sprintf("%d %ss", count, noun)
}

func columns(rows [][]string) []string {
	widths := map[int]int{}
	for _, r := range rows {
		for i, cell := range r {
			if i < len(r)-1 && len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		var b strings.Builder
		for i, cell := range r {
			if i > 0 {
				b.WriteString("  ")
			}
			if i < len(r)-1 {
				fmt.Fprintf(&b, "%-*s", widths[i], cell)
				continue
			}
			b.WriteString(cell)
		}
		out = append(out, strings.TrimRight(b.String(), " "))
	}
	return out
}

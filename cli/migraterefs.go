package cli

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/d0u9/rhumb/inventory"
)

// reportServiceReferences finds literal old facts in authored service and
// node files. These are review candidates: the same string may be a comment,
// a historical example, or a hostname which intentionally stays unchanged.
func reportServiceReferences(rep *migrationReport, root, oldID string, oldNode inventory.Node, changes []NetworkChange, instances []instanceChange, routes []routeChange) error {
	terms := map[string]bool{oldID: true}
	for _, change := range changes {
		if change.From != change.To {
			terms[change.From] = true
		}
		if change.Address != "" && change.Address != oldNode.Networks[change.From] {
			terms[oldNode.Networks[change.From]] = true
		}
	}
	delete(terms, "")
	matches, err := scanMigrationText(root, "services", terms)
	if err != nil {
		return fmt.Errorf("scanning service references: %w", err)
	}
	rep.ServiceMatches = matches
	// Node files also hold opaque instance values and deploy settings.
	ids := map[string]bool{}
	for term := range terms {
		ids[term] = true
	}
	for _, change := range instances {
		ids[change.From] = true
	}
	for _, change := range routes {
		ids[change.From] = true
	}
	matches, err = scanMigrationText(root, "nodes", ids)
	if err != nil {
		return fmt.Errorf("scanning node references: %w", err)
	}
	for _, match := range matches {
		if !migrationTypedLine(match.Text, changes) {
			rep.NodeMatches = append(rep.NodeMatches, match)
		}
	}
	renamed := map[string]bool{}
	for _, change := range instances {
		renamed[change.From] = true
	}
	for _, inst := range oldNode.Instances {
		if renamed[inst.ID] {
			continue
		}
		for _, term := range sortedKeys(terms) {
			if strings.Contains(inst.ID, term) {
				rep.InstanceIDs = append(rep.InstanceIDs, fmt.Sprintf("%s contains %s (%s)", mdCode(inst.ID), mdCode(term), mdCode(inst.Path)))
			}
		}
	}
	sort.Strings(rep.InstanceIDs)
	return nil
}

func scanMigrationText(root, subtree string, terms map[string]bool) ([]migrationTextMatch, error) {
	var matches []migrationTextMatch
	err := filepath.WalkDir(filepath.Join(root, subtree), func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.IndexByte(string(data), 0) >= 0 {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		scanner := bufio.NewScanner(strings.NewReader(string(data)))
		for line := 1; scanner.Scan(); line++ {
			text := strings.TrimSpace(scanner.Text())
			kind := "配置/模板内容"
			if strings.HasPrefix(text, "#") {
				kind = "注释"
			}
			for _, term := range sortedKeys(terms) {
				if strings.Contains(text, term) {
					matches = append(matches, migrationTextMatch{Path: filepath.ToSlash(rel), Line: line, Kind: kind, Term: term, Text: text})
				}
			}
		}
		return scanner.Err()
	})
	if os.IsNotExist(err) {
		err = nil
	}
	if err != nil {
		return nil, err
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].Path != matches[j].Path {
			return matches[i].Path < matches[j].Path
		}
		return matches[i].Line < matches[j].Line
	})
	return matches, nil
}

// migrationTypedLine reports a node-file line whose match is a typed field
// the plan already rewrites or reports elsewhere: an ID (instance IDs have
// their own attention item), the instance directory, a renamed network's
// key, or reaches.
func migrationTypedLine(text string, changes []NetworkChange) bool {
	key, _, ok := strings.Cut(strings.TrimPrefix(text, "- "), ":")
	if !ok || strings.HasPrefix(text, "#") {
		return false
	}
	switch strings.TrimSpace(key) {
	case "id", "directory", "reaches":
		return true
	}
	for _, change := range changes {
		if strings.TrimSpace(key) == change.From {
			return true
		}
	}
	return false
}

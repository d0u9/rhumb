package cli

import (
	"bytes"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/d0u9/rhumb/engine"

	"github.com/d0u9/rhumb/inventory"
	"github.com/d0u9/rhumb/secretstore"
	"github.com/d0u9/rhumb/target"
)

// compareMigrationTargets uses the export renderer on each side. A failed
// render is reported as unknown, never mistaken for an unchanged target.
type migrationTargetWork struct {
	Target  target.Target
	Present bool
	Unknown bool
	Files   []engine.File
}

func compareMigrationTargets(rep *migrationReport, before, after engine.Loaded, root, secrets, oldID, newID string) []migrationTargetWork {
	oldTargets := map[string]target.Target{}
	newTargets := map[string]target.Target{}
	for _, t := range target.List(before.Inv, before.Derived) {
		oldTargets[migrationTargetKey(t, oldID, newID)] = t
	}
	for _, t := range target.List(after.Inv, after.Derived) {
		newTargets[migrationTargetKey(t, newID, newID)] = t
	}
	keys := make([]string, 0, len(oldTargets)+len(newTargets))
	seen := map[string]bool{}
	for key := range oldTargets {
		keys = append(keys, key)
		seen[key] = true
	}
	for key := range newTargets {
		if !seen[key] {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	oldRenderer := engine.Renderer{Data: before, RootPath: root, SecretsDir: secrets}
	// Until apply copies them, a renamed node's secrets are still under its
	// old name; the preview reads them there.
	newRenderer := engine.Renderer{Data: after, RootPath: root, SecretsDir: secrets, SecretInstance: func(id string) string {
		if oldID != newID && strings.HasPrefix(id, newID+inventory.QualifiedSep) {
			return oldID + strings.TrimPrefix(id, newID)
		}
		return id
	}}
	secretProblem := missingMigrationSecret(before, after, secrets)
	workDetails := []migrationTargetWork{}
	for _, key := range keys {
		old, hadOld := oldTargets[key]
		newTarget, hasNew := newTargets[key]
		item := newTarget
		if !hasNew {
			item = old
		}
		owner := item.Node
		if owner == "" {
			owner = item.User
		}
		change := migrationTargetChange{Owner: owner, Label: item.Service + "/" + inventory.LocalName(item.Instance)}
		if secretProblem != "" {
			change.Unknown = secretProblem
			rep.Targets = append(rep.Targets, change)
			workDetails = append(workDetails, migrationTargetWork{Target: item, Present: hasNew, Unknown: true})
			continue
		}
		var oldFiles, newFiles []engine.File
		var oldErr, newErr error
		if hadOld {
			oldFiles, oldErr = oldRenderer.RenderAll([]string{old.Instance})
		}
		if hasNew {
			newFiles, newErr = newRenderer.RenderAll([]string{newTarget.Instance})
		}
		if oldErr != nil || newErr != nil {
			change.Unknown = fmt.Sprintf("before: %s; after: %s", renderState(oldErr, hadOld), renderState(newErr, hasNew))
			rep.Targets = append(rep.Targets, change)
			workDetails = append(workDetails, migrationTargetWork{Target: item, Present: hasNew, Unknown: true})
			continue
		}
		if !hadOld || !hasNew {
			files, action := newFiles, "add"
			if !hasNew {
				files, action = oldFiles, "remove"
			}
			for _, file := range files {
				if action == "add" {
					change.Files = append(change.Files, migrationFileChange{Action: action, New: file.Path})
				} else {
					change.Files = append(change.Files, migrationFileChange{Action: action, Old: file.Path})
				}
			}
			rep.Targets = append(rep.Targets, change)
			workDetails = append(workDetails, migrationTargetWork{Target: item, Present: hasNew, Files: files})
			continue
		}
		change.Files = compareExportFileChanges(oldFiles, newFiles)
		if len(change.Files) != 0 {
			rep.Targets = append(rep.Targets, change)
			workDetails = append(workDetails, migrationTargetWork{Target: item, Present: true, Files: newFiles})
		}
	}
	sort.SliceStable(rep.Targets, func(i, j int) bool { return rep.Targets[i].Owner < rep.Targets[j].Owner })
	return workDetails
}

func missingMigrationSecret(before, after engine.Loaded, secrets string) string {
	paths := append(secretstore.ImpliedPaths(before.Inv, before.Manifests, before.Derived), secretstore.ImpliedPaths(after.Inv, after.Manifests, after.Derived)...)
	if len(paths) == 0 {
		return ""
	}
	if secrets == "" {
		return "no secrets root given"
	}
	return ""
}

func migrationTargetKey(t target.Target, oldID, newID string) string {
	owner := t.Node
	if owner == "" {
		owner = t.User
	}
	if owner == oldID {
		owner = newID
	}
	instance := t.Instance
	if oldID != newID && strings.HasPrefix(instance, oldID+inventory.QualifiedSep) {
		instance = newID + strings.TrimPrefix(instance, oldID)
	} else if oldID != newID && strings.HasPrefix(instance, oldID+"-") {
		instance = newID + strings.TrimPrefix(instance, oldID)
	}
	return owner + "/" + t.Service + "/" + t.Export + "/" + instance
}

func renderState(err error, exists bool) string {
	if !exists {
		return "absent"
	}
	if err != nil {
		return err.Error()
	}
	return "rendered"
}

// compareExportFiles describes each changed file in one line.
func compareExportFiles(oldFiles, newFiles []engine.File) []string {
	var lines []string
	for _, change := range compareExportFileChanges(oldFiles, newFiles) {
		switch change.Action {
		case "add":
			lines = append(lines, "add "+change.New)
		case "remove":
			lines = append(lines, "remove "+change.Old)
		case "update":
			lines = append(lines, "update "+change.New+" (content or mode changed)")
		default:
			state := "content or mode also changed"
			if change.Same {
				state = "content unchanged"
			}
			lines = append(lines, "export path "+change.Old+" -> "+change.New+" ("+state+")")
		}
	}
	return lines
}

// compareExportFileChanges pairs files by their name inside the target, so a
// moved bundle path is one change rather than a removal and an addition.
func compareExportFileChanges(oldFiles, newFiles []engine.File) []migrationFileChange {
	oldByName := map[string]engine.File{}
	newByName := map[string]engine.File{}
	names := map[string]bool{}
	for _, file := range oldFiles {
		oldByName[exportOutputName(file.Path)] = file
		names[exportOutputName(file.Path)] = true
	}
	for _, file := range newFiles {
		newByName[exportOutputName(file.Path)] = file
		names[exportOutputName(file.Path)] = true
	}
	var changes []migrationFileChange
	for _, key := range sortedKeys(names) {
		old, hadOld := oldByName[key]
		newFile, hasNew := newByName[key]
		switch {
		case !hadOld:
			changes = append(changes, migrationFileChange{Action: "add", New: newFile.Path})
		case !hasNew:
			changes = append(changes, migrationFileChange{Action: "remove", Old: old.Path})
		default:
			same := bytes.Equal(old.Bytes, newFile.Bytes) && old.Mode() == newFile.Mode()
			if old.Path != newFile.Path {
				changes = append(changes, migrationFileChange{Action: "move", Old: old.Path, New: newFile.Path, Same: same})
			} else if !same {
				changes = append(changes, migrationFileChange{Action: "update", Old: old.Path, New: newFile.Path})
			}
		}
	}
	return changes
}

func exportOutputName(path string) string {
	parts := strings.Split(filepath.ToSlash(path), "/")
	if len(parts) <= 3 {
		return path
	}
	return strings.Join(parts[3:], "/")
}

func compareMigrationSecrets(rep *migrationReport, before, after engine.Loaded) {
	oldPaths := secretstore.ImpliedPaths(before.Inv, before.Manifests, before.Derived)
	newPaths := secretstore.ImpliedPaths(after.Inv, after.Manifests, after.Derived)
	oldSet, newSet := map[string]bool{}, map[string]bool{}
	for _, path := range oldPaths {
		oldSet[path.String()] = true
	}
	for _, path := range newPaths {
		newSet[path.String()] = true
	}
	var changes []string
	for path := range oldSet {
		if !newSet[path] {
			changes = append(changes, "removed: "+mdCode(path))
		}
	}
	for path := range newSet {
		if !oldSet[path] {
			changes = append(changes, "added: "+mdCode(path))
		}
	}
	sort.Strings(changes)
	rep.Secrets = changes
}

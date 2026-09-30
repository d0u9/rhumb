package cli

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/d0u9/rhumb/engine"

	"github.com/d0u9/rhumb/inventory"
	"github.com/d0u9/rhumb/secretstore"
)

// A rename expands the secret store first. Old files remain valid for rollback.
type migrationSecretCopy struct {
	from   string
	to     string
	digest [32]byte
}

func planMigrationSecretCopies(before, after engine.Loaded, flags map[string]string, root string) ([]migrationSecretCopy, error) {
	oldPaths := secretstore.ImpliedPaths(before.Inv, before.Manifests, before.Derived)
	newPaths := secretstore.ImpliedPaths(after.Inv, after.Manifests, after.Derived)
	oldSet, newSet := map[secretstore.Path]bool{}, map[secretstore.Path]bool{}
	for _, path := range oldPaths {
		oldSet[path] = true
	}
	for _, path := range newPaths {
		newSet[path] = true
	}
	changes, err := parseInstanceChanges(flags["instance"])
	if err != nil {
		return nil, err
	}
	nodeFrom, nodeTo, _ := parseNodeChange(flags["node"])
	renames := map[string]string{}
	for _, change := range changes {
		renames[change.From] = change.To
	}
	var copies []migrationSecretCopy
	paired := map[secretstore.Path]bool{}
	for _, old := range oldPaths {
		if newSet[old] {
			continue
		}
		// A secret sits under <node>/<name>: renaming the node moves every
		// one of its instances, and renaming an instance moves that one.
		oldNode, oldName, _ := strings.Cut(old.Instance, inventory.QualifiedSep)
		newID := ""
		if oldNode == nodeFrom && nodeFrom != "" {
			name := oldName
			if to := renames[oldName]; to != "" {
				name = to
			}
			newID = nodeTo + inventory.QualifiedSep + name
		}
		if newID == "" || newID == old.Instance {
			return nil, fmt.Errorf("apply blocked: no explicit instance rename maps secret %s", old)
		}
		next := old
		next.Instance = newID
		if !newSet[next] || paired[next] {
			return nil, fmt.Errorf("apply blocked: no unique destination for secret %s", old)
		}
		paired[next] = true
		if root == "" {
			return nil, fmt.Errorf("apply blocked: no secrets root given")
		}
		from, to := old.String(), next.String()
		data, err := readMigrationSecret(root, from)
		if err != nil {
			return nil, fmt.Errorf("reading old secret %s: %w", from, err)
		}
		if err := checkMigrationSecretDestination(root, to, data); err != nil {
			return nil, err
		}
		copies = append(copies, migrationSecretCopy{from: from, to: to, digest: sha256.Sum256(data)})
	}
	for _, next := range newPaths {
		if !oldSet[next] && !paired[next] {
			return nil, fmt.Errorf("apply blocked: no old secret maps to %s", next)
		}
	}
	sort.Slice(copies, func(i, j int) bool { return copies[i].to < copies[j].to })
	return copies, nil
}

func sameMigrationSecretCopies(a, b []migrationSecretCopy) bool { return slices.Equal(a, b) }

func readMigrationSecret(root, rel string) ([]byte, error) {
	if !filepath.IsLocal(rel) {
		return nil, fmt.Errorf("unsafe secret path %q", rel)
	}
	path := filepath.Join(root, rel)
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", rel)
	}
	return os.ReadFile(path)
}

func checkMigrationSecretDestination(root, rel string, source []byte) error {
	if !filepath.IsLocal(rel) {
		return fmt.Errorf("unsafe secret path %q", rel)
	}
	path := filepath.Join(root, rel)
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("secret destination %s is not a regular file", rel)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(data, source) {
		return fmt.Errorf("secret destination %s already exists with different content", rel)
	}
	return nil
}

func applyMigrationSecretCopies(root string, copies []migrationSecretCopy) error {
	for _, copy := range copies {
		data, err := readMigrationSecret(root, copy.from)
		if err != nil {
			return fmt.Errorf("reading old secret %s: %w", copy.from, err)
		}
		if sha256.Sum256(data) != copy.digest {
			return fmt.Errorf("migration plan is stale: secret %s changed", copy.from)
		}
		if err := checkMigrationSecretDestination(root, copy.to, data); err != nil {
			return err
		}
		path := filepath.Join(root, copy.to)
		if _, err := os.Lstat(path); err == nil {
			continue // Already copied and verified by checkMigrationSecretDestination.
		} else if !os.IsNotExist(err) {
			return err
		}
		if err := makeMigrationSecretParents(root, filepath.Dir(copy.to)); err != nil {
			return err
		}
		if err := writeMigrationFile(path, data, 0o600, true); err != nil {
			return fmt.Errorf("copying secret %s: %w", copy.to, err)
		}
		readback, err := readMigrationSecret(root, copy.to)
		if err != nil || sha256.Sum256(readback) != copy.digest {
			return fmt.Errorf("verifying copied secret %s: %v", copy.to, err)
		}
	}
	return nil
}

func makeMigrationSecretParents(root, rel string) error {
	if !filepath.IsLocal(rel) {
		return fmt.Errorf("unsafe secret directory %q", rel)
	}
	current := root
	for _, part := range strings.Split(rel, string(os.PathSeparator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			if err := os.Mkdir(current, 0o700); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("secret directory %s is not a real directory", current)
		}
	}
	return nil
}

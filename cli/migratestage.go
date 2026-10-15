package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"

	"github.com/d0u9/rhumb/engine"
	"github.com/d0u9/rhumb/inventory"
	"github.com/d0u9/rhumb/validate"
)

func migrationInventoryEqual(actual, expected *inventory.Root) bool {
	return reflect.DeepEqual(actual.Nodes, expected.Nodes) &&
		migrationUserReferencesEqual(actual.Users, expected.Users) &&
		reflect.DeepEqual(actual.Routes, expected.Routes) &&
		reflect.DeepEqual(actual.Links, expected.Links) &&
		reflect.DeepEqual(actual.Networks, expected.Networks) &&
		reflect.DeepEqual(actual.NetworkInfo, expected.NetworkInfo) &&
		reflect.DeepEqual(actual.Names, expected.Names) &&
		reflect.DeepEqual(actual.Hosts, expected.Hosts) && actual.Universal == expected.Universal
}

// Load replacement YAML before any real writes, not only after publication.
func validateStagedMigration(root string, edits []migrationEdit, expected engine.Loaded) error {
	temp, err := os.MkdirTemp("", "rhumb-migration-check-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)
	paths, err := migrationInputDigests(root)
	if err != nil {
		return err
	}
	for path := range paths {
		data, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			return err
		}
		dest := filepath.Join(temp, path)
		if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(dest, data, 0o600); err != nil {
			return err
		}
	}
	for _, edit := range edits {
		path := edit.path
		if edit.moveTo != "" {
			path = edit.moveTo
			if err := os.Remove(filepath.Join(temp, edit.path)); err != nil {
				return err
			}
		}
		dest := filepath.Join(temp, path)
		if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(dest, edit.replaced, 0o600); err != nil {
			return err
		}
	}
	loaded, err := engine.Load(temp)
	if err != nil {
		return fmt.Errorf("staged migration: %w", err)
	}
	if broken := BrokenFiles(loaded.Inv); len(broken) != 0 {
		return fmt.Errorf("staged migration has broken inventory: %s", broken[0])
	}
	if issues := validate.Validate(loaded.Inv, loaded.Manifests, loaded.Exports, loaded.Derived, nil); len(issues) != 0 {
		return fmt.Errorf("staged migration is invalid: %s", issues[0].Message)
	}
	if !migrationInventoryEqual(loaded.Inv, expected.Inv) {
		return fmt.Errorf("staged inventory differs from the validated plan")
	}
	return nil
}

package cli

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/d0u9/rhumb/engine"
	"github.com/d0u9/rhumb/inventory"
	"github.com/d0u9/rhumb/validate"
)

func migrateApplyAction(in io.Reader, out io.Writer, args []string, flags map[string]string, s Settings) error {
	if len(args) != 1 || args[0] != "node" || flags["node"] == "" {
		return fmt.Errorf("apply needs a migration plan with a source node")
	}
	root := s.Root
	if root == "" {
		return fmt.Errorf("no generator root given")
	}
	already, err := migrationAlreadyApplied(root, flags)
	if err != nil {
		return err
	}
	if already {
		fmt.Fprintln(out, "Requested migration target state is already present; no files changed.")
		return nil
	}
	inputs, err := migrationInputDigests(root)
	if err != nil {
		return err
	}
	var before, after engine.Loaded
	var report bytes.Buffer
	err = MigrateSnapshot(nil, &report, args, flags, s, func(old, next engine.Loaded) bool {
		before, after = old, next
		return false
	})
	if err != nil {
		return err
	}
	if err := checkMigrationInputDigests(root, inputs); err != nil {
		return err
	}
	networks, err := parseNetworkChanges(flags["network"])
	if err != nil {
		return err
	}
	routes, err := parseRouteChanges(flags["route"])
	if err != nil {
		return err
	}
	edits, err := buildMigrationEdits(root, before, after, networks, routes)
	if err != nil {
		return err
	}
	secretCopies, err := planMigrationSecretCopies(before, after, flags, s.Secrets)
	if err != nil {
		return err
	}
	if _, err := out.Write(report.Bytes()); err != nil {
		return err
	}
	if len(edits) == 0 && len(secretCopies) == 0 {
		fmt.Fprintln(out, "No local inventory edits are needed.")
		return nil
	}
	if len(secretCopies) != 0 {
		fmt.Fprintln(out, "Secret copies to create (old files stay for rollback):")
		for _, copy := range secretCopies {
			fmt.Fprintf(out, "  %s -> %s\n", copy.from, copy.to)
		}
	}
	fmt.Fprintln(out, "Local inventory files to replace:")
	for _, edit := range edits {
		fmt.Fprintln(out, "  "+edit.path)
	}
	if flags["yes"] != "true" {
		if in == nil {
			in = strings.NewReader("")
		}
		approved, err := Confirm(in, out, "Apply these local inventory edits? [y/N] ")
		if err != nil {
			return err
		}
		if !approved {
			fmt.Fprintln(out, "Migration cancelled; no files changed.")
			return nil
		}
	}
	if err := checkMigrationInputDigests(root, inputs); err != nil {
		return err
	}
	// Rebuild after approval, before the first write. The preview and each
	// replacement must still describe the same inventory and service files.
	var rebuiltBefore, rebuiltAfter engine.Loaded
	var rebuilt bytes.Buffer
	err = MigrateSnapshot(nil, &rebuilt, args, flags, s, func(old, next engine.Loaded) bool {
		rebuiltBefore, rebuiltAfter = old, next
		return false
	})
	if err != nil {
		return err
	}
	if !bytes.Equal(report.Bytes(), rebuilt.Bytes()) {
		return fmt.Errorf("migration plan is stale: preview changed before apply")
	}
	rebuiltEdits, err := buildMigrationEdits(root, rebuiltBefore, rebuiltAfter, networks, routes)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(edits, rebuiltEdits) {
		return fmt.Errorf("migration plan is stale: replacement files changed before apply")
	}
	if err := checkMigrationInputDigests(root, inputs); err != nil {
		return err
	}
	verifiedCopies, err := planMigrationSecretCopies(rebuiltBefore, rebuiltAfter, flags, s.Secrets)
	if err != nil {
		return err
	}
	if !sameMigrationSecretCopies(secretCopies, verifiedCopies) {
		return fmt.Errorf("migration plan is stale: secret copies changed before apply")
	}
	if err := applyMigrationSecretCopies(s.Secrets, verifiedCopies); err != nil {
		return err
	}
	backupDir, err := applyMigrationEdits(root, edits, rebuiltAfter, os.Rename)
	if err != nil {
		if len(secretCopies) != 0 {
			return fmt.Errorf("%w; verified secret copies remain for retry, old secrets remain untouched", err)
		}
		if backupDir != "" && !strings.Contains(err.Error(), backupDir) {
			return fmt.Errorf("%w; backup directory: %s", err, backupDir)
		}
		return err
	}
	if len(secretCopies) != 0 {
		fmt.Fprintf(out, "Verified %d new secret path(s); old secrets retained for rollback.\n", len(secretCopies))
	}
	fmt.Fprintf(out, "Applied %d local inventory file(s). Exact originals: %s\n", len(edits), backupDir)
	fmt.Fprintln(out, "Run rhumb check; retained old secret paths will appear as orphaned until retired after cutover. Investigate every other problem. Then export and install affected targets, and verify DNS and routes from the relevant networks.")
	return nil
}

func migrationInputDigests(root string) (map[string][32]byte, error) {
	paths := map[string]bool{}
	for _, name := range []string{inventory.UsersFilename, inventory.RoutesFilename, inventory.NetworksFilename} {
		paths[name] = true
	}
	for _, subtree := range []string{"nodes", "services"} {
		err := filepath.WalkDir(filepath.Join(root, subtree), func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			if !entry.Type().IsRegular() {
				return fmt.Errorf("migration input %s is not a regular file", path)
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			paths[rel] = true
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}
	digests := map[string][32]byte{}
	for path := range paths {
		data, err := os.ReadFile(filepath.Join(root, path))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		digests[path] = sha256.Sum256(data)
	}
	return digests, nil
}

func checkMigrationInputDigests(root string, expected map[string][32]byte) error {
	current, err := migrationInputDigests(root)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(expected, current) {
		return fmt.Errorf("migration plan is stale: inventory or service files changed before apply")
	}
	return nil
}

func applyMigrationEdits(root string, edits []migrationEdit, expected engine.Loaded, replace func(string, string) error) (string, error) {
	if len(edits) == 0 {
		return "", nil
	}
	for _, edit := range edits {
		if err := checkMigrationEdit(root, edit); err != nil {
			return "", err
		}
	}
	backupDir, err := os.MkdirTemp(root, ".rhumb-migration-backup-")
	if err != nil {
		return "", err
	}
	staged := map[string]string{}
	defer func() {
		for _, path := range staged {
			_ = os.Remove(path)
		}
	}()
	for _, edit := range edits {
		backup := filepath.Join(backupDir, edit.path)
		if err := os.MkdirAll(filepath.Dir(backup), 0o700); err != nil {
			return backupDir, fmt.Errorf("creating backup for %s: %w", edit.path, err)
		}
		if err := writeMigrationFile(backup, edit.original, edit.mode, true); err != nil {
			return backupDir, fmt.Errorf("backing up %s: %w", edit.path, err)
		}
		original := filepath.Join(root, edit.path)
		temp, err := os.CreateTemp(filepath.Dir(original), ".rhumb-migration-*.tmp")
		if err != nil {
			return backupDir, err
		}
		staged[edit.path] = temp.Name()
		if err := temp.Chmod(edit.mode); err != nil {
			_ = temp.Close()
			return backupDir, err
		}
		if _, err := temp.Write(edit.replaced); err != nil {
			_ = temp.Close()
			return backupDir, err
		}
		if err := temp.Sync(); err != nil {
			_ = temp.Close()
			return backupDir, err
		}
		if err := temp.Close(); err != nil {
			return backupDir, err
		}
	}
	var applied []migrationEdit
	rollback := func(cause error) (string, error) {
		var failures []string
		for i := len(applied) - 1; i >= 0; i-- {
			edit := applied[i]
			backup := filepath.Join(backupDir, edit.path)
			if edit.moveTo != "" {
				_ = os.Remove(filepath.Join(root, edit.moveTo))
			}
			if err := os.Rename(backup, filepath.Join(root, edit.path)); err != nil {
				failures = append(failures, edit.path+": "+err.Error())
			}
		}
		if len(failures) != 0 {
			return backupDir, fmt.Errorf("%w; restore failed for %s; backups remain at %s", cause, strings.Join(failures, "; "), backupDir)
		}
		return backupDir, fmt.Errorf("%w; changed inventory files were restored from %s", cause, backupDir)
	}
	for _, edit := range edits {
		if err := checkMigrationEdit(root, edit); err != nil {
			return rollback(err)
		}
		dest := edit.path
		if edit.moveTo != "" {
			dest = edit.moveTo
			if _, err := os.Lstat(filepath.Join(root, dest)); err == nil {
				return rollback(fmt.Errorf("moving %s: %s already exists", edit.path, dest))
			}
		}
		if err := replace(staged[edit.path], filepath.Join(root, dest)); err != nil {
			return rollback(fmt.Errorf("replacing %s: %w", edit.path, err))
		}
		if edit.moveTo != "" {
			if err := os.Remove(filepath.Join(root, edit.path)); err != nil {
				applied = append(applied, edit)
				return rollback(fmt.Errorf("removing %s after moving it: %w", edit.path, err))
			}
		}
		delete(staged, edit.path)
		applied = append(applied, edit)
	}
	actual, err := engine.Load(root)
	if err != nil {
		return rollback(fmt.Errorf("loading applied inventory: %w", err))
	}
	if broken := BrokenFiles(actual.Inv); len(broken) != 0 {
		return rollback(fmt.Errorf("applied inventory is broken: %s", strings.Join(broken, "; ")))
	}
	if issues := validate.Validate(actual.Inv, actual.Manifests, actual.Exports, actual.Derived, nil); len(issues) != 0 {
		return rollback(fmt.Errorf("applied inventory has %d validation problems: %s", len(issues), issues[0].Message))
	}
	if !reflect.DeepEqual(actual.Inv.Nodes, expected.Inv.Nodes) ||
		!migrationUserReferencesEqual(actual.Inv.Users, expected.Inv.Users) ||
		!reflect.DeepEqual(actual.Inv.Routes, expected.Inv.Routes) ||
		!reflect.DeepEqual(actual.Inv.Networks, expected.Inv.Networks) ||
		actual.Inv.Universal != expected.Inv.Universal {
		return rollback(fmt.Errorf("applied inventory differs from the validated plan"))
	}
	return backupDir, nil
}

func migrationUserReferencesEqual(actual, expected map[string]inventory.User) bool {
	if len(actual) != len(expected) {
		return false
	}
	for name, desired := range expected {
		user, ok := actual[name]
		if !ok || !slices.Equal(user.Access, desired.Access) || len(user.Credentials) != len(desired.Credentials) {
			return false
		}
		for credentialName, desiredCredential := range desired.Credentials {
			credential, ok := user.Credentials[credentialName]
			if !ok || !slices.Equal(credential.Access, desiredCredential.Access) || !slices.Equal(credential.Reaches, desiredCredential.Reaches) {
				return false
			}
		}
	}
	return true
}

func checkMigrationEdit(root string, edit migrationEdit) error {
	path := filepath.Join(root, edit.path)
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != edit.mode {
		return fmt.Errorf("migration plan is stale at %s: type or permissions changed", edit.path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(data, edit.original) {
		return fmt.Errorf("migration plan is stale at %s: file changed", edit.path)
	}
	return nil
}

func writeMigrationFile(path string, data []byte, mode os.FileMode, exclusive bool) error {
	flags := os.O_WRONLY | os.O_CREATE
	if exclusive {
		flags |= os.O_EXCL
	}
	file, err := os.OpenFile(path, flags, mode)
	if err != nil {
		return err
	}
	if err := file.Chmod(mode); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func migrationAlreadyApplied(root string, flags map[string]string) (bool, error) {
	oldID, newID, err := parseNodeChange(flags["node"])
	if err != nil {
		return false, err
	}
	networks, err := parseNetworkChanges(flags["network"])
	if err != nil {
		return false, err
	}
	instances, err := parseInstanceChanges(flags["instance"])
	if err != nil {
		return false, err
	}
	routes, err := parseRouteChanges(flags["route"])
	if err != nil {
		return false, err
	}
	published, err := parsePublishedChanges(flags["published"])
	if err != nil {
		return false, err
	}
	if oldID == newID && len(networks)+len(instances)+len(routes)+len(published) == 0 {
		return false, nil
	}
	l, err := engine.Load(root)
	if err != nil {
		return false, err
	}
	if broken := BrokenFiles(l.Inv); len(broken) != 0 {
		return false, fmt.Errorf("current inventory is broken: %s", strings.Join(broken, "; "))
	}
	if issues := validate.Validate(l.Inv, l.Manifests, l.Exports, l.Derived, nil); len(issues) != 0 {
		return false, fmt.Errorf("current inventory has %d validation problems: %s", len(issues), issues[0].Message)
	}
	var oldPresent bool
	var node *inventory.Node
	for i := range l.Inv.Nodes {
		if l.Inv.Nodes[i].ID == oldID {
			oldPresent = true
		}
		if l.Inv.Nodes[i].ID == newID {
			node = &l.Inv.Nodes[i]
		}
	}
	if node == nil || oldID != newID && oldPresent {
		return false, nil
	}
	for _, change := range networks {
		name := change.To
		if name == "" {
			name = change.From
		}
		address, ok := node.Networks[name]
		if !ok || change.Address != "" && change.Address != address {
			return false, nil
		}
		if change.From != name {
			for _, current := range l.Inv.Networks {
				if current == change.From {
					return false, nil
				}
			}
		}
	}
	instanceNames := map[string]string{}
	for _, change := range instances {
		instanceNames[change.From] = change.To
		for _, inst := range node.Instances {
			if inventory.LocalName(inst.ID) == change.From {
				return false, nil
			}
		}
		if !migrationNodeHasInstance(node, change.To) {
			return false, nil
		}
	}
	for _, change := range routes {
		if _, old := l.Inv.Routes[change.From]; old {
			return false, nil
		}
		if _, present := l.Inv.Routes[change.To]; !present {
			return false, nil
		}
	}
	for _, change := range published {
		instanceID := change.Instance
		if renamed := instanceNames[instanceID]; renamed != "" {
			instanceID = renamed
		}
		found := false
		for _, inst := range node.Instances {
			if inventory.LocalName(inst.ID) == instanceID && inst.Ports[change.Port].Published == change.To {
				found = true
			}
		}
		if !found {
			return false, nil
		}
	}
	return true, nil
}

func migrationNodeHasInstance(node *inventory.Node, id string) bool {
	for _, inst := range node.Instances {
		if inventory.LocalName(inst.ID) == id {
			return true
		}
	}
	return false
}

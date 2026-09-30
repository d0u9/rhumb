package cli

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/d0u9/rhumb/engine"

	"github.com/d0u9/rhumb/inventory"
)

func TestMigrationApplyWritesTypedFieldsAndIsIdempotent(t *testing.T) {
	root, secrets := buildExportableRoot(t)
	nodePath := filepath.Join(root, "nodes", "srv.yaml")
	oldNode, err := os.ReadFile(nodePath)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, nodePath, "# preserve this note\n"+string(oldNode))
	oldNode, err = os.ReadFile(nodePath)
	if err != nil {
		t.Fatal(err)
	}
	flags := map[string]string{
		"apply": "true", "yes": "true", "node": "from=srv,to=srv08",
		"network": `["from=internet,to=wan,address=203.0.113.8"]`,
		"route":   `["from=sea,to=sea08"]`,
	}
	var out bytes.Buffer
	if err := Migrate(nil, &out, []string{"node"}, flags, Settings{Root: root, Secrets: secrets}); err != nil {
		t.Fatalf("apply: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "Applied 4 local inventory file(s)") {
		t.Fatalf("output:\n%s", out.String())
	}
	inv, err := inventory.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Nodes) != 1 || inv.Nodes[0].ID != "srv08" || inv.Nodes[0].Networks["wan"] != "203.0.113.8" || inv.Routes["sea08"].Hops[0] != "srv08/u-node-group-09:main" {
		t.Fatalf("applied inventory: %+v", inv)
	}
	if _, old := inv.Routes["sea"]; old {
		t.Fatal("old route survived apply")
	}
	if len(inv.Users["friend-a"].Access) != 1 || inv.Users["friend-a"].Access[0] != "sea08" {
		t.Fatalf("user access: %+v", inv.Users["friend-a"])
	}
	// The node's file is renamed with it.
	if _, err := os.Stat(nodePath); !os.IsNotExist(err) {
		t.Fatalf("old node file still present: %v", err)
	}
	newNode, err := os.ReadFile(filepath.Join(root, "nodes", "srv08.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(newNode, []byte("# preserve this note")) {
		t.Fatal("node comment was lost")
	}
	backups, err := filepath.Glob(filepath.Join(root, ".rhumb-migration-backup-*", "nodes", "srv.yaml"))
	if err != nil || len(backups) != 1 {
		t.Fatalf("backups = %v, %v", backups, err)
	}
	backup, err := os.ReadFile(backups[0])
	if err != nil || !bytes.Equal(backup, oldNode) {
		t.Fatal("backup does not contain exact original bytes")
	}
	var again bytes.Buffer
	if err := Migrate(nil, &again, []string{"node"}, flags, Settings{Root: root, Secrets: secrets}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(again.String(), "already present") {
		t.Fatalf("second apply: %s", again.String())
	}
	unchanged, err := os.ReadFile(filepath.Join(root, "nodes", "srv08.yaml"))
	if err != nil || !bytes.Equal(newNode, unchanged) {
		t.Fatal("second apply changed node file")
	}
}

func TestMigrationApplyCopiesRenamedSecretsAndKeepsOriginals(t *testing.T) {
	root, secrets := buildExportableRoot(t)
	path := filepath.Join(root, "nodes", "srv.yaml")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	oldSecret := filepath.Join(secrets, "srv", "u-node-group-09", "main", "friend-a", "default")
	newSecret := filepath.Join(secrets, "srv08", "u-node-group-0908", "main", "friend-a", "default")
	oldValue, err := os.ReadFile(oldSecret)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err = Migrate(nil, &out, []string{"node"}, map[string]string{
		"apply": "true", "yes": "true", "node": "from=srv,to=srv08",
		"instance": `["from=u-node-group-09,to=u-node-group-0908"]`,
	}, Settings{Root: root, Secrets: secrets})
	if err != nil {
		t.Fatalf("err = %v\n%s", err, out.String())
	}
	after, err := os.ReadFile(filepath.Join(filepath.Dir(path), "srv08.yaml"))
	if err != nil || bytes.Equal(before, after) {
		t.Fatal("apply did not update inventory")
	}
	for _, path := range []string{oldSecret, newSecret} {
		data, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(data, oldValue) {
			t.Fatalf("secret %s changed: %v", path, err)
		}
	}
	info, err := os.Stat(newSecret)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("new secret mode: %v, %v", info, err)
	}
	if !strings.Contains(out.String(), "old secrets retained for rollback") {
		t.Fatalf("missing rollback note: %s", out.String())
	}
}

func TestMigrationApplyRejectsConflictingSecretDestination(t *testing.T) {
	root, secrets := buildExportableRoot(t)
	path := filepath.Join(root, "nodes", "srv.yaml")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(secrets, "srv08", "u-node-group-0908", "main", "friend-a", "default"), "different")
	var out bytes.Buffer
	err = Migrate(nil, &out, []string{"node"}, map[string]string{
		"apply": "true", "yes": "true", "node": "from=srv,to=srv08",
		"instance": `["from=u-node-group-09,to=u-node-group-0908"]`,
	}, Settings{Root: root, Secrets: secrets})
	if err == nil || !strings.Contains(err.Error(), "different content") {
		t.Fatalf("err = %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("conflict changed inventory")
	}
}

func TestMigrationSecretCopyRetriesAfterPartialFailure(t *testing.T) {
	root := t.TempDir()
	first, second := []byte("first secret\n"), []byte("second secret\n")
	writeFile(t, filepath.Join(root, "old-one", "self", "key"), string(first))
	writeFile(t, filepath.Join(root, "old-two", "self", "key"), string(second))
	writeFile(t, filepath.Join(root, "new-two"), "blocks directory")
	copies := []migrationSecretCopy{
		{from: "old-one/self/key", to: "new-one/self/key", digest: sha256.Sum256(first)},
		{from: "old-two/self/key", to: "new-two/self/key", digest: sha256.Sum256(second)},
	}
	if err := applyMigrationSecretCopies(root, copies); err == nil {
		t.Fatal("expected second copy to fail")
	}
	newFirst, err := os.ReadFile(filepath.Join(root, "new-one", "self", "key"))
	if err != nil || !bytes.Equal(newFirst, first) {
		t.Fatal("first verified copy was lost")
	}
	if err := os.Remove(filepath.Join(root, "new-two")); err != nil {
		t.Fatal(err)
	}
	if err := applyMigrationSecretCopies(root, copies); err != nil {
		t.Fatalf("retry: %v", err)
	}
	for _, pair := range []struct {
		name string
		want []byte
	}{{"old-one/self/key", first}, {"new-one/self/key", first}, {"old-two/self/key", second}, {"new-two/self/key", second}} {
		data, err := os.ReadFile(filepath.Join(root, pair.name))
		if err != nil || !bytes.Equal(data, pair.want) {
			t.Fatalf("wrong bytes in %s: %v", pair.name, err)
		}
	}
}

func TestMigrationApplyDeclinedDoesNotWrite(t *testing.T) {
	root, secrets := buildExportableRoot(t)
	path := filepath.Join(root, "nodes", "srv.yaml")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err = Migrate(strings.NewReader("n\n"), &out, []string{"node"}, map[string]string{
		"apply": "true", "node": "from=srv,to=srv08",
	}, Settings{Root: root, Secrets: secrets})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Migration cancelled") {
		t.Fatalf("output: %s", out.String())
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("declined apply changed inventory")
	}
	backups, err := filepath.Glob(filepath.Join(root, ".rhumb-migration-backup-*"))
	if err != nil || len(backups) != 0 {
		t.Fatalf("declined apply created backups: %v, %v", backups, err)
	}
}

func TestMigrationApplyUpdatesSeparateInstanceFileAndPublishedName(t *testing.T) {
	root, secrets := buildExportableRoot(t)
	writeFile(t, filepath.Join(root, "nodes", "srv.yaml"), `
id: srv
networks:
  internet: 203.0.113.10
instances:
  directory: srv.instances
`)
	instancePath := filepath.Join(root, "nodes", "srv.instances", "one.yaml")
	writeFile(t, instancePath, `# keep instance note
id: u-node-group-09
service: hysteria2
ports:
  main: {port: 443, published: old.example.test}
`)
	var out bytes.Buffer
	err := Migrate(nil, &out, []string{"node"}, map[string]string{
		"apply": "true", "yes": "true", "node": "from=srv,to=srv08",
		"published": `["instance=u-node-group-09,port=main,to=new.example.test"]`,
	}, Settings{Root: root, Secrets: secrets})
	if err != nil {
		t.Fatalf("apply: %v\n%s", err, out.String())
	}
	data, err := os.ReadFile(instancePath)
	if err != nil || !bytes.Contains(data, []byte("# keep instance note")) || !bytes.Contains(data, []byte("new.example.test")) {
		t.Fatalf("instance file: %s, %v", data, err)
	}
	if bytes.Contains(data, []byte("old.example.test")) {
		t.Fatal("old published name survived")
	}
}

func TestMigrationApplyRenamesNetworkAcrossNodesAndCredentials(t *testing.T) {
	root, secrets := buildExportableRoot(t)
	path := filepath.Join(root, "nodes", "srv.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, strings.Replace(string(data), "  internet: 203.0.113.10", "  home: {address: 10.0.1.4, mac: 02:00:00:00:00:04}\n  internet: 203.0.113.10", 1))
	writeFile(t, filepath.Join(root, "nodes", "guest.yaml"), `id: guest
reaches: [home]
`)
	writeFile(t, filepath.Join(root, "networks.yaml"), "networks:\n  - name: home\n    subnet: 10.0.1.0/24\n    gateway: 10.0.1.1\n  - name: internet\nuniversal: internet\n")
	writeFile(t, filepath.Join(root, "hosts.yaml"), "hosts:\n  printer:\n    network: home\n    address: 10.0.1.9\n    names: [printer.home.test]\n")
	writeFile(t, filepath.Join(root, "users.yaml"), `users:
  friend-a:
    username: yak
    devices: none
    access: [sea]
    credentials:
      default:
        reaches: [home]
`)
	var out bytes.Buffer
	err = Migrate(nil, &out, []string{"node"}, map[string]string{
		"apply": "true", "yes": "true", "node": "from=srv,to=srv08",
		"network": `["from=home,to=network-8,address=10.0.1.8"]`,
	}, Settings{Root: root, Secrets: secrets})
	if err != nil {
		t.Fatalf("apply: %v\n%s", err, out.String())
	}
	inv, err := inventory.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if inv.Nodes[0].ID == "" {
		t.Fatal("missing node")
	}
	if inv.NetworkInfo["network-8"].Subnet != "10.0.1.0/24" || inv.Hosts["printer"].Network != "network-8" {
		t.Fatalf("network info or hosts: %+v %+v", inv.NetworkInfo, inv.Hosts)
	}
	for _, node := range inv.Nodes {
		if node.ID == "srv08" && (node.Networks["network-8"] != "10.0.1.8" || node.MACs["network-8"] != "02:00:00:00:00:04") {
			t.Fatalf("node address: %+v %+v", node.Networks, node.MACs)
		}
	}
	if inv.Networks[0] != "network-8" || inv.Users["friend-a"].Credentials["default"].Reaches[0] != "network-8" {
		t.Fatalf("network references: %+v", inv)
	}
	var found bool
	for _, node := range inv.Nodes {
		if node.ID == "guest" && len(node.Reaches) == 1 && node.Reaches[0] == "network-8" {
			found = true
		}
	}
	if !found {
		t.Fatalf("other node reach was not updated: %+v", inv.Nodes)
	}
}

type migrationConfirmReader struct {
	change func() error
	done   bool
}

func (r *migrationConfirmReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	r.done = true
	if err := r.change(); err != nil {
		return 0, err
	}
	return copy(p, "y\n"), nil
}

func TestMigrationApplyRejectsStaleInputAfterConfirmation(t *testing.T) {
	root, secrets := buildExportableRoot(t)
	path := filepath.Join(root, "nodes", "srv.yaml")
	reader := &migrationConfirmReader{change: func() error {
		file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			return err
		}
		if _, err := file.WriteString("# external edit\n"); err != nil {
			_ = file.Close()
			return err
		}
		return file.Close()
	}}
	var out bytes.Buffer
	err := Migrate(reader, &out, []string{"node"}, map[string]string{
		"apply": "true", "node": "from=srv,to=srv08",
	}, Settings{Root: root, Secrets: secrets})
	if err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("err = %v\n%s", err, out.String())
	}
	data, err := os.ReadFile(path)
	if err != nil || !bytes.Contains(data, []byte("# external edit")) || bytes.Contains(data, []byte("srv08")) {
		t.Fatal("stale apply wrote over external edit")
	}
}

func TestMigrationApplyRestoresAfterSecondReplacementFails(t *testing.T) {
	root, secrets := buildExportableRoot(t)
	flags := map[string]string{"node": "from=srv,to=srv08", "network": `["from=internet,to=wan,address=203.0.113.8"]`}
	var before, after engine.Loaded
	err := MigrateSnapshot(nil, io.Discard, []string{"node"}, flags, Settings{Root: root, Secrets: secrets}, func(old, next engine.Loaded) bool {
		before, after = old, next
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	networks, err := parseNetworkChanges(flags["network"])
	if err != nil {
		t.Fatal(err)
	}
	edits, err := buildMigrationEdits(root, before, after, networks, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(edits) < 2 {
		t.Fatalf("need two files, got %d", len(edits))
	}
	calls := 0
	backupDir, err := applyMigrationEdits(root, edits, after, func(from, to string) error {
		calls++
		if calls == 2 {
			return errors.New("injected replacement failure")
		}
		return os.Rename(from, to)
	})
	if err == nil || !strings.Contains(err.Error(), "injected replacement failure") || backupDir == "" {
		t.Fatalf("backup %q, err %v", backupDir, err)
	}
	for _, edit := range edits {
		got, readErr := os.ReadFile(filepath.Join(root, edit.path))
		if readErr != nil || !bytes.Equal(got, edit.original) {
			t.Errorf("%s was not restored: %v", edit.path, readErr)
		}
	}
	if calls != 2 {
		t.Fatal(fmt.Sprintf("replacement calls = %d", calls))
	}
}

func TestMigrationApplyRestoresAfterPostWriteMismatch(t *testing.T) {
	root, secrets := buildExportableRoot(t)
	flags := map[string]string{"node": "from=srv,to=srv08"}
	var before, after engine.Loaded
	err := MigrateSnapshot(nil, io.Discard, []string{"node"}, flags, Settings{Root: root, Secrets: secrets}, func(old, next engine.Loaded) bool {
		before, after = old, next
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	edits, err := buildMigrationEdits(root, before, after, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	incorrect := after
	copyOf := *after.Inv
	copyOf.Nodes = append([]inventory.Node(nil), after.Inv.Nodes...)
	copyOf.Nodes[0].ID = "unexpected"
	incorrect.Inv = &copyOf
	_, err = applyMigrationEdits(root, edits, incorrect, os.Rename)
	if err == nil || !strings.Contains(err.Error(), "differs from the validated plan") {
		t.Fatalf("error = %v", err)
	}
	for _, edit := range edits {
		got, readErr := os.ReadFile(filepath.Join(root, edit.path))
		if readErr != nil || !bytes.Equal(got, edit.original) {
			t.Errorf("%s was not restored: %v", edit.path, readErr)
		}
	}
}

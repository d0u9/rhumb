package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/d0u9/rhumb/engine"

	"github.com/d0u9/rhumb/inventory"
	"github.com/d0u9/rhumb/secretstore"
	"gopkg.in/yaml.v3"
)

func TestMigrateNodePreviewShowsChangedEdgeWithoutWriting(t *testing.T) {
	root, secrets := buildExportableRoot(t)
	path := filepath.Join(root, "nodes", "srv.yaml")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err = Migrate(nil, &out, []string{"node"}, map[string]string{
		"node": "from=srv,to=srv08", "network": `["from=internet,address=203.0.113.8"]`,
	}, Settings{Root: root, Secrets: secrets})
	if err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"| Node ID | `srv` | `srv08` |", "| Address on internet | `203.0.113.10` | `203.0.113.8` |", "### Route edges\n\n| Edge |", "#### `srv08`", "| `hysteria2/u-node-group-09` | `config.yaml` | bundle path moves; **content changed** |", "#### `friend-a`", "| `hysteria2/yak-default-sea-hysteria2-link` | `share.txt` | content changed |", "rhumb export instance:'yak-default-sea-hysteria2-link'", "### Secret paths\n\n- added: `srv08/u-node-group-09/main/friend-a/default`\n- removed: `srv/u-node-group-09/main/friend-a/default`", "No inventory, secret or DNS change has been made"} {
		if !strings.Contains(got, want) {
			t.Errorf("preview missing %q:\n%s", want, got)
		}
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("preview changed the node file")
	}
}

func TestMigrationReportMarksTemplateAndDeployForReview(t *testing.T) {
	root, secrets := buildExportableRoot(t)
	path := filepath.Join(root, "services", "hysteria2", "deploy", "templates", "install.sh.tmpl")
	writeFile(t, path, "# previous node srv\necho srv\n")
	var out bytes.Buffer
	err := Migrate(nil, &out, []string{"node"}, map[string]string{"node": "from=srv,to=srv08"}, Settings{Root: root, Secrets: secrets})
	if err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"Old names in service files", "| `services/hysteria2/deploy/templates/install.sh.tmpl:1` | 注释 | `srv` | `# previous node srv` |", "| `services/hysteria2/deploy/templates/install.sh.tmpl:2` | 配置/模板内容 | `srv` | `echo srv` |"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in report:\n%s", want, got)
		}
	}
}

func TestMigrationCutoverListsSameMachineStopAndStartInstances(t *testing.T) {
	root, secrets := buildExportableRoot(t)
	var out bytes.Buffer
	err := Migrate(nil, &out, []string{"node"}, map[string]string{
		"node":     "from=srv,to=srv08",
		"instance": `["from=u-node-group-09,to=u-node-group-0908"]`,
	}, Settings{Root: root, Secrets: secrets})
	if err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		"### Phase 1 — Take services offline",
		"`hysteria2/u-node-group-09`",
		"### Phase 5 — Start services",
		"`hysteria2/u-node-group-0908`",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("cutover missing %q:\n%s", want, got)
		}
	}
	procedure := got[strings.Index(got, "## 3. Procedure"):]
	if strings.Contains(procedure[:strings.Index(procedure, "### Phase 2")], "hysteria2-link/") {
		t.Fatal("export handoff was treated as a runtime service")
	}
	if strings.Index(procedure, "`hysteria2/u-node-group-09`") > strings.Index(procedure, "`hysteria2/u-node-group-0908`") {
		t.Fatal("startup listed before shutdown")
	}
}

func TestCompareExportFilesDistinguishesNestedOutputs(t *testing.T) {
	oldFiles := []engine.File{{Path: "srv/service/instance/a/config.yaml", Bytes: []byte("old")}, {Path: "srv/service/instance/b/config.yaml", Bytes: []byte("same")}}
	newFiles := []engine.File{{Path: "srv/service/instance/a/config.yaml", Bytes: []byte("new")}, {Path: "srv/service/instance/b/config.yaml", Bytes: []byte("same")}}
	changes := compareExportFiles(oldFiles, newFiles)
	if len(changes) != 1 || changes[0] != "update srv/service/instance/a/config.yaml (content or mode changed)" {
		t.Fatalf("changes = %v", changes)
	}
}

func TestCompareExportFilesExplainsPathOnlyChange(t *testing.T) {
	changes := compareExportFiles(
		[]engine.File{{Path: "node-4/samba/samba-network-4-01/smbpasswd", Bytes: []byte("same")}},
		[]engine.File{{Path: "node-8/samba/samba-network-4-01/smbpasswd", Bytes: []byte("same")}},
	)
	if len(changes) != 1 || changes[0] != "export path node-4/samba/samba-network-4-01/smbpasswd -> node-8/samba/samba-network-4-01/smbpasswd (content unchanged)" {
		t.Fatalf("changes = %v", changes)
	}
}

func TestReportServiceReferencesFlagsUnchangedInstanceID(t *testing.T) {
	root := t.TempDir()
	rep := &migrationReport{}
	err := reportServiceReferences(rep, root, "node-4-server-linux-01", inventory.Node{
		Path:      "nodes/home/server.yaml",
		Instances: []inventory.Instance{{ID: "samba-network-4-01", Path: "nodes/home/server.yaml"}},
	}, []NetworkChange{{From: "network-4", To: "network-8"}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.InstanceIDs) != 1 || !strings.Contains(rep.InstanceIDs[0], "`samba-network-4-01` contains `network-4`") {
		t.Fatalf("instance IDs = %v", rep.InstanceIDs)
	}
}

func TestMigrateInstanceRenameRejectsCollision(t *testing.T) {
	inv := &inventory.Root{Nodes: []inventory.Node{{ID: "srv", Instances: []inventory.Instance{
		{ID: "old", Service: "samba"}, {ID: "taken", Service: "samba"},
	}}}}
	_, err := renameMigrationInstances(inv, 0, []instanceChange{{From: "old", To: "taken"}})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("error = %v", err)
	}
}

func TestMigratePublishedNamePreviewDoesNotWriteInventory(t *testing.T) {
	root, secrets := buildExportableRoot(t)
	path := filepath.Join(root, "nodes", "srv.yaml")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, strings.Replace(string(before), "main: 443", "main: {port: 443, published: old.example.test}", 1))
	var out bytes.Buffer
	err = Migrate(nil, &out, []string{"node"}, map[string]string{
		"node": "from=srv,to=srv", "published": `["instance=u-node-group-09,port=main,to=new.example.test"]`,
	}, Settings{Root: root, Secrets: secrets})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "| Published name u-node-group-09:main | `old.example.test` | `new.example.test` |") || !strings.Contains(out.String(), "DNS records to inspect") || !strings.Contains(out.String(), "- Published name: `old.example.test` → `new.example.test`") {
		t.Fatalf("preview = %s", out.String())
	}
	after, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(after), "old.example.test") || strings.Contains(string(after), "new.example.test") {
		t.Fatalf("preview changed inventory: %s, %v", after, err)
	}
}

func TestMigrationAddressChangeReviewsUnchangedPublishedName(t *testing.T) {
	root, secrets := buildExportableRoot(t)
	path := filepath.Join(root, "nodes", "srv.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, strings.Replace(string(data), "main: 443", "main: {port: 443, published: service.example.test}", 1))
	var out bytes.Buffer
	err = Migrate(nil, &out, []string{"node"}, map[string]string{
		"node": "from=srv,to=srv08", "network": `["from=internet,address=203.0.113.8"]`,
	}, Settings{Root: root, Secrets: secrets})
	if err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		"DNS records to inspect",
		"**`service.example.test`** — `hysteria2/u-node-group-09` on port `main`",
		"- Ingress: route `sea` via `srv` → route `sea` via `srv08`",
		"`internet 203.0.113.10` → `internet 203.0.113.8`",
		"- [ ] `dig +short service.example.test`",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
}

func TestRouteRenameUpdatesTypedReferencesWithoutMutatingSource(t *testing.T) {
	old := &inventory.Root{
		Routes: map[string]inventory.Route{"network-4-rss": {Hops: []string{"caddy:http", "rss:web"}}},
		Users:  map[string]inventory.User{"dana": {Access: []string{"network-4-rss"}, Credentials: map[string]inventory.Credential{"default": {Access: []string{"network-4-rss"}}}}},
		Nodes:  []inventory.Node{{ID: "laptop", Path: "nodes/laptop.yaml", Profiles: map[string]inventory.Profile{"browser": {Access: []string{"network-4-rss"}}}}},
	}
	copyOf := *old
	copyOf.Nodes = append([]inventory.Node(nil), old.Nodes...)
	refs, err := renameMigrationRoutes(&copyOf, []routeChange{{From: "network-4-rss", To: "network-8-rss"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(refs["network-4-rss"]) != 4 || copyOf.Users["dana"].Access[0] != "network-8-rss" || copyOf.Users["dana"].Credentials["default"].Access[0] != "network-8-rss" || copyOf.Nodes[0].Profiles["browser"].Access[0] != "network-8-rss" {
		t.Fatalf("renamed snapshot = %+v, refs = %v", copyOf, refs)
	}
	if old.Users["dana"].Access[0] != "network-4-rss" || old.Nodes[0].Profiles["browser"].Access[0] != "network-4-rss" {
		t.Fatal("source inventory was mutated")
	}
}

func TestExplicitMigrationFlagsNeedNode(t *testing.T) {
	root, secrets := buildExportableRoot(t)
	err := Migrate(strings.NewReader(""), &bytes.Buffer{}, []string{"node"}, map[string]string{
		"instance": `["from=u-node-group-09,to=u-node-group-0908"]`,
	}, Settings{Root: root, Secrets: secrets})
	if err == nil || !strings.Contains(err.Error(), "names no source node") {
		t.Fatalf("error = %v", err)
	}
}

func TestMigratePreviewMarksMissingSecretsUncompared(t *testing.T) {
	root, secrets := buildExportableRoot(t)
	l, err := engine.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	paths := secretstore.ImpliedPaths(l.Inv, l.Manifests, l.Derived)
	if len(paths) == 0 {
		t.Fatal("fixture has no implied secrets")
	}
	if err := os.Remove(filepath.Join(secrets, paths[0].String())); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err = Migrate(nil, &out, []string{"node"}, map[string]string{
		"node": "from=srv,to=srv08", "network": `["from=internet,address=203.0.113.8"]`,
	}, Settings{Root: root, Secrets: secrets})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "**not compared**") || !strings.Contains(out.String(), "Render comparison unavailable") {
		t.Fatalf("preview = %s", out.String())
	}
}

func TestMigratePreviewListsLiteralServiceNodeReferences(t *testing.T) {
	root, secrets := buildExportableRoot(t)
	writeFile(t, filepath.Join(root, "services", "hysteria2", "defaults.yaml"), "listen: :443\nlabel: srv\n")
	writeFile(t, filepath.Join(root, "services", "hysteria2", "templates", "server.yaml.tmpl"), "listen: {{ .listen }}\n# srv 203.0.113.10\n")
	var out bytes.Buffer
	err := Migrate(nil, &out, []string{"node"}, map[string]string{
		"node": "from=srv,to=srv08", "network": `["from=internet,address=203.0.113.8"]`,
	}, Settings{Root: root, Secrets: secrets})
	if err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"Old names in service files", "| `services/hysteria2/defaults.yaml:2` | 配置/模板内容 | `srv` |", "| `services/hysteria2/templates/server.yaml.tmpl:2` | 注释 | `203.0.113.10` |"} {
		if !strings.Contains(got, want) {
			t.Errorf("preview missing %q:\n%s", want, got)
		}
	}
}

func TestMigrateNodePreviewRejectsUnknownNetwork(t *testing.T) {
	root, secrets := buildExportableRoot(t)
	err := Migrate(nil, &bytes.Buffer{}, []string{"node"}, map[string]string{
		"node": "from=srv,to=srv08", "network": `["from=home,address=10.0.1.8"]`,
	}, Settings{Root: root, Secrets: secrets})
	if err == nil || !strings.Contains(err.Error(), `no address on network "home"`) {
		t.Fatalf("error = %v", err)
	}
}

func TestMigrateNodePreviewRequiresNamedDirection(t *testing.T) {
	root, secrets := buildExportableRoot(t)
	if err := Migrate(nil, &bytes.Buffer{}, []string{"node", "srv", "srv08"}, nil, Settings{Root: root, Secrets: secrets}); err == nil || !strings.Contains(err.Error(), "--node") {
		t.Fatalf("old positional form error = %v", err)
	}
	if err := Migrate(nil, &bytes.Buffer{}, []string{"node"}, map[string]string{"node": "from=srv,to=srv08", "network": `["address=203.0.113.8"]`}, Settings{Root: root, Secrets: secrets}); err == nil || !strings.Contains(err.Error(), "give from") {
		t.Fatalf("missing network error = %v", err)
	}
}

func TestMigrateNodePreviewAllowsAddressOnlyChange(t *testing.T) {
	root, secrets := buildExportableRoot(t)
	var out bytes.Buffer
	err := Migrate(nil, &out, []string{"node"}, map[string]string{
		"node": "from=srv,to=srv", "network": `["from=internet,address=203.0.113.8"]`,
	}, Settings{Root: root, Secrets: secrets})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "| Address on internet | `203.0.113.10` | `203.0.113.8` |") {
		t.Fatalf("preview = %s", out.String())
	}
}

func TestMigrateNodePreviewRenamesNetworkAndAddress(t *testing.T) {
	root, secrets := buildExportableRoot(t)
	var out bytes.Buffer
	err := Migrate(nil, &out, []string{"node"}, map[string]string{
		"node": "from=srv,to=srv08", "network": `["from=internet,to=wan,address=203.0.113.8"]`,
	}, Settings{Root: root, Secrets: secrets})
	if err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"| Network name | `internet` | `wan` |", "networks.yaml: networks", "networks.yaml: universal", "nodes/srv.yaml: networks.internet", "| Address on wan | `203.0.113.10` | `203.0.113.8` |"} {
		if !strings.Contains(got, want) {
			t.Errorf("preview missing %q:\n%s", want, got)
		}
	}
}

func TestMigrateNodePreviewHandlesMultipleNetworks(t *testing.T) {
	root, secrets := buildExportableRoot(t)
	path := filepath.Join(root, "nodes", "srv.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, strings.Replace(string(data), "  internet: 203.0.113.10", "  network-4: 10.0.1.4\n  tailnet: 10.0.2.4\n  internet: 203.0.113.10", 1))
	writeFile(t, filepath.Join(root, "networks.yaml"), "networks: [{name: network-4}, {name: tailnet}, {name: internet}]\nuniversal: internet\n")
	var out bytes.Buffer
	err = Migrate(nil, &out, []string{"node"}, map[string]string{
		"node":    "from=srv,to=srv08",
		"network": `["from=network-4,to=network-8,address=10.0.1.8","from=tailnet,address=10.0.2.8"]`,
	}, Settings{Root: root, Secrets: secrets})
	if err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"| Network name | `network-4` | `network-8` |", "| Address on network-8 | `10.0.1.4` | `10.0.1.8` |", "| Address on tailnet | `10.0.2.4` | `10.0.2.8` |"} {
		if !strings.Contains(got, want) {
			t.Errorf("preview missing %q:\n%s", want, got)
		}
	}
}

func TestParseNetworkChangesRejectsDuplicateDestination(t *testing.T) {
	_, err := parseNetworkChanges(`["from=network-4,to=network-8","from=tailnet,to=network-8"]`)
	if err == nil || !strings.Contains(err.Error(), "more than once") {
		t.Fatalf("error = %v", err)
	}
}

func TestParseNodeChangeRequiresNamedEndpoints(t *testing.T) {
	for _, spec := range []string{"srv,srv08", "from=srv", "to=srv08", "from=srv,to=srv08,to=other"} {
		if _, _, err := parseNodeChange(spec); err == nil {
			t.Errorf("parseNodeChange(%q) accepted an incomplete or ambiguous node change", spec)
		}
	}
	from, to, err := parseNodeChange("from=srv,to=srv08")
	if err != nil || from != "srv" || to != "srv08" {
		t.Fatalf("parseNodeChange = %q, %q, %v", from, to, err)
	}
}

func TestRenameInventoryNetworkUpdatesAllTypedReferencesWithoutMutatingSource(t *testing.T) {
	old := &inventory.Root{
		Networks: []string{"network-4", "internet"},
		Nodes: []inventory.Node{{ID: "server", Path: "nodes/home/server.yaml", Networks: inventory.Networks{"network-4": "10.0.1.4"}},
			{ID: "phone", Path: "nodes/alice/phone.yaml", Reaches: []string{"network-4"}}},
		Users: map[string]inventory.User{"alice": {Credentials: map[string]inventory.Credential{
			"first": {Reaches: []string{"network-4"}}, "second": {Reaches: []string{"network-4"}},
		}}},
	}
	copyOf := *old
	copyOf.Nodes = append([]inventory.Node(nil), old.Nodes...)
	refs, err := renameInventoryNetwork(&copyOf, "network-4", "network-8")
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 5 || copyOf.Networks[0] != "network-8" || copyOf.Nodes[0].Networks["network-8"] != "10.0.1.4" || copyOf.Nodes[1].Reaches[0] != "network-8" || copyOf.Users["alice"].Credentials["first"].Reaches[0] != "network-8" || copyOf.Users["alice"].Credentials["second"].Reaches[0] != "network-8" {
		t.Fatalf("renamed snapshot = %+v, refs = %v", copyOf, refs)
	}
	if old.Networks[0] != "network-4" || old.Nodes[0].Networks["network-4"] != "10.0.1.4" || old.Nodes[1].Reaches[0] != "network-4" || old.Users["alice"].Credentials["first"].Reaches[0] != "network-4" {
		t.Fatal("source inventory was mutated")
	}
}

func TestMigrateInstanceRenameRewritesDialsOnEveryNode(t *testing.T) {
	inv := &inventory.Root{Nodes: []inventory.Node{
		{ID: "srv", Instances: []inventory.Instance{
			{ID: "srv/rss-old", Service: "freshrss", Path: "nodes/srv.yaml"},
			{ID: "srv/digest", Service: "ai-digest", Path: "nodes/srv.yaml", Dials: map[string]string{"rss": "srv/rss-old:http", "llm": "srv/llm:api"}},
		}},
		{ID: "other", Instances: []inventory.Instance{
			{ID: "other/reader", Service: "reader", Path: "nodes/other.yaml", Dials: map[string]string{"feed": "srv/rss-old:http"}},
		}},
	}}
	original := inv.Nodes[1].Instances[0].Dials
	refs, err := renameMigrationInstances(inv, 0, []instanceChange{{From: "rss-old", To: "rss-new"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := inv.Nodes[0].Instances[1].Dials; got["rss"] != "srv/rss-new:http" || got["llm"] != "srv/llm:api" {
		t.Fatalf("digest dials = %v", got)
	}
	if got := inv.Nodes[1].Instances[0].Dials["feed"]; got != "srv/rss-new:http" {
		t.Fatalf("reader dial = %q", got)
	}
	if original["feed"] != "srv/rss-old:http" {
		t.Fatal("rename changed the source inventory's dials")
	}
	if !strings.Contains(strings.Join(refs["rss-old"], "\n"), "nodes/other.yaml: reader.dials.feed") {
		t.Fatalf("refs = %v", refs["rss-old"])
	}
}

func TestMigrationEditsRewriteDialValue(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "nodes", "srv.yaml"), `id: srv
instances:
  - id: digest
    service: ai-digest
    dials:
      rss: rss-old:http # the reader
`)
	m := migrationYAML{root: root, files: map[string]*migrationYAMLFile{}}
	source := []inventory.Instance{{ID: "srv/digest", Path: "nodes/srv.yaml", Dials: map[string]string{"rss": "srv/rss-old:http"}}}
	target := []inventory.Instance{{ID: "srv/digest", Path: "nodes/srv.yaml", Dials: map[string]string{"rss": "srv/rss-new:http"}}}
	if err := m.patchInstances(source, target); err != nil {
		t.Fatal(err)
	}
	file := m.files["nodes/srv.yaml"]
	data, err := yaml.Marshal(&file.doc)
	if err != nil || !file.changed || !strings.Contains(string(data), "rss: rss-new:http # the reader") {
		t.Fatalf("changed %v, err %v:\n%s", file.changed, err, data)
	}
}

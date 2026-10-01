package inventory

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoad_ParsesNodesUsersRoutesAndNetworks(t *testing.T) {
	root := t.TempDir()

	writeFile(t, filepath.Join(root, NodesDir, "u-node-group-09-01.yaml"), `
id: u-node-group-09-01

networks:
  internet: 203.0.113.10

instances:
  - id: ss-sea01
    service: shadowsocks-rust
    role: server
    ports:
      main: 38250
      alt: 49217
`)
	writeFile(t, filepath.Join(root, NodesDir, "macbook.yaml"), `
id: macbook
owner: dana
reaches: [home]
`)
	writeFile(t, filepath.Join(root, UsersFilename), `
users:
  dana:
    access: [sea]
  friend-a:
    username: yak
    devices: none
    export: link
    access: [sea]
`)
	writeFile(t, filepath.Join(root, RoutesFilename), `
routes:
  sea:
    hops: [u-node-group-09-01/ss-sea01:main]
`)
	writeFile(t, filepath.Join(root, NetworksFilename), `
networks: [{name: home}, {name: internet}]
universal: internet
`)

	got, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(got.Nodes) != 2 {
		t.Fatalf("Nodes = %d, want 2: %+v", len(got.Nodes), got.Nodes)
	}
	// Sorted by path/file name: macbook.yaml before u-node-group-09-01.yaml.
	macbook, sea := got.Nodes[0], got.Nodes[1]

	if macbook.ID != "macbook" || macbook.Owner != "dana" {
		t.Fatalf("macbook = %+v", macbook)
	}
	if macbook.Broken != "" {
		t.Fatalf("macbook.Broken = %q, want empty", macbook.Broken)
	}
	if len(macbook.Networks) != 0 {
		t.Fatalf("macbook.Networks = %+v, want none (it has no address anywhere)", macbook.Networks)
	}
	if len(macbook.Reaches) != 1 || macbook.Reaches[0] != "home" {
		t.Fatalf("macbook.Reaches = %+v, want [home]", macbook.Reaches)
	}

	if sea.ID != "u-node-group-09-01" {
		t.Fatalf("sea.ID = %q", sea.ID)
	}
	if sea.Networks["internet"] != "203.0.113.10" {
		t.Fatalf(`sea.Networks["internet"] = %q`, sea.Networks["internet"])
	}
	if len(sea.Instances) != 1 {
		t.Fatalf("sea.Instances = %+v, want 1", sea.Instances)
	}
	inst := sea.Instances[0]
	if inst.Name != "ss-sea01" || inst.ID != "u-node-group-09-01/ss-sea01" || inst.Service != "shadowsocks-rust" || inst.Role != "server" {
		t.Fatalf("inst = %+v", inst)
	}
	if inst.Ports["main"].Number != 38250 || inst.Ports["alt"].Number != 49217 {
		t.Fatalf("inst.Ports = %+v", inst.Ports)
	}

	if got.UsersBroken != "" {
		t.Fatalf("UsersBroken = %q, want empty", got.UsersBroken)
	}
	if len(got.Users) != 2 {
		t.Fatalf("Users = %d, want 2: %+v", len(got.Users), got.Users)
	}
	if got.Users["friend-a"].Devices != DevicesNone {
		t.Fatalf("friend-a.Devices = %q, want %q", got.Users["friend-a"].Devices, DevicesNone)
	}
	if got.Users["dana"].Devices != "" {
		t.Fatalf("dana.Devices = %q, want empty (managed is the default)", got.Users["dana"].Devices)
	}
	if got.Users["dana"].UsernameOr("dana") != "dana" {
		t.Fatalf(`dana.UsernameOr("dana") = %q, want "dana" (falls back to the map key)`, got.Users["dana"].UsernameOr("dana"))
	}
	if got.Users["friend-a"].UsernameOr("friend-a") != "yak" {
		t.Fatalf(`friend-a.UsernameOr("friend-a") = %q, want "yak"`, got.Users["friend-a"].UsernameOr("friend-a"))
	}
	if got.Users["friend-a"].Export != "link" {
		t.Fatalf("friend-a.Export = %q, want %q", got.Users["friend-a"].Export, "link")
	}

	if got.RoutesBroken != "" {
		t.Fatalf("RoutesBroken = %q, want empty", got.RoutesBroken)
	}
	if len(got.Routes) != 1 || len(got.Routes["sea"].Hops) != 1 {
		t.Fatalf("Routes = %+v", got.Routes)
	}
	if got.Routes["sea"].Hops[0] != "u-node-group-09-01/ss-sea01:main" {
		t.Fatalf("Hops[0] = %q", got.Routes["sea"].Hops[0])
	}

	if got.NetworksBroken != "" {
		t.Fatalf("NetworksBroken = %q, want empty", got.NetworksBroken)
	}
	if len(got.Networks) != 2 || got.Networks[0] != "home" || got.Networks[1] != "internet" {
		t.Fatalf("Networks = %+v", got.Networks)
	}
	if got.Universal != "internet" {
		t.Fatalf("Universal = %q, want %q", got.Universal, "internet")
	}
}

// TestLoad_InstanceValuesAreOpaque covers an instance's own values: rhumb
// parses them as an arbitrary mapping and does not interpret their shape —
// nested structure and a list both come through unchanged, for a template to
// read by name. See docs/inventory.md#an-instances-own-values.
func TestLoad_InstanceValuesAreOpaque(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, NodesDir, "srv.yaml"), `
id: srv
networks:
  internet: 203.0.113.10
instances:
  - id: hy2-sea01
    service: hysteria2
    role: server
    ports:
      main: 443
    values:
      masquerade:
        type: proxy
        proxy:
          url: https://example.org/
          rewriteHost: true
      tags: [prod, sea]
`)

	got, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.Nodes) != 1 || len(got.Nodes[0].Instances) != 1 {
		t.Fatalf("got = %+v", got)
	}
	values := got.Nodes[0].Instances[0].Values
	masquerade, ok := values["masquerade"].(map[string]any)
	if !ok {
		t.Fatalf("values[masquerade] = %#v, want a nested mapping", values["masquerade"])
	}
	proxy, ok := masquerade["proxy"].(map[string]any)
	if !ok || proxy["url"] != "https://example.org/" {
		t.Fatalf("values[masquerade][proxy] = %#v", masquerade["proxy"])
	}
	tags, ok := values["tags"].([]any)
	if !ok || len(tags) != 2 || tags[0] != "prod" {
		t.Fatalf("values[tags] = %#v, want [prod sea]", values["tags"])
	}
}

func TestLoad_InstancesFromDirectory(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, NodesDir, "cloud", "srv.yaml"), "id: srv\ninstances:\n  directory: srv.instances\n")
	writeFile(t, filepath.Join(root, NodesDir, "cloud", "srv.instances", "b.yaml"), "id: second\nservice: two\n")
	writeFile(t, filepath.Join(root, NodesDir, "cloud", "srv.instances", "a.yaml"), "id: first\nservice: one\n")
	writeFile(t, filepath.Join(root, NodesDir, "cloud", "srv.instances", "c.yaml"), "- id: third\n  service: shared\n- id: fourth\n  service: shared\n")
	writeFile(t, filepath.Join(root, NodesDir, "cloud", "srv.instances", "README.md"), "ignored\n")
	writeFile(t, filepath.Join(root, NodesDir, "local.yaml"), "id: local\ninstances:\n  directory: parts\n")
	writeFile(t, filepath.Join(root, NodesDir, "parts", "local.yaml"), "id: local-instance\nservice: local\n")

	got, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Nodes) != 2 || got.Nodes[0].Broken != "" || got.Nodes[1].Broken != "" {
		t.Fatalf("Nodes = %+v", got.Nodes)
	}
	instances := got.Nodes[0].Instances
	if len(instances) != 4 || instances[0].Name != "first" || instances[1].Name != "second" || instances[2].Name != "third" || instances[3].ID != "srv/fourth" {
		t.Fatalf("Instances = %+v, want file-name order", instances)
	}
	if instances[2].Path != filepath.Join(NodesDir, "cloud", "srv.instances", "c.yaml") || instances[3].Path != instances[2].Path {
		t.Fatalf("list entries must retain their source path: %+v", instances)
	}
	if got.Nodes[1].ID != "local" || len(got.Nodes[1].Instances) != 1 || got.Nodes[1].Instances[0].ID != "local/local-instance" {
		t.Fatalf("root-level node = %+v", got.Nodes[1])
	}
}

func TestLoad_BrokenInstanceDirectoryMarksNodeBroken(t *testing.T) {
	for _, tc := range []struct {
		name, directory, filename, content, want string
	}{
		{"missing", "missing", "", "", "missing"},
		{"bad YAML", "srv.instances", "bad.yaml", "id: [oops\n", "bad.yaml"},
		{"unknown field", "srv.instances", "bad.yaml", "id: bad\ntypo: yes\n", "bad.yaml"},
		{"bad list entry", "srv.instances", "bad.yaml", "- id: good\n  service: one\n- id: bad\n  typo: yes\n", "bad.yaml"},
		{"parent path", "../outside", "", "", "instances.directory"},
		{"unknown reference field", "srv.instances\n  typo: yes", "", "", "instances"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, filepath.Join(root, NodesDir, "srv.yaml"), "id: srv\ninstances:\n  directory: "+tc.directory+"\n")
			if tc.filename != "" {
				writeFile(t, filepath.Join(root, NodesDir, "srv.instances", tc.filename), tc.content)
			}
			got, err := Load(root)
			if err != nil {
				t.Fatal(err)
			}
			if len(got.Nodes) != 1 || !strings.Contains(got.Nodes[0].Broken, tc.want) || len(got.Nodes[0].Instances) != 0 {
				t.Fatalf("Nodes = %+v, want broken node mentioning %q", got.Nodes, tc.want)
			}
		})
	}
}

func TestLoad_MissingInventoryFilesAreEmptyNotErrors(t *testing.T) {
	root := t.TempDir()

	got, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.Nodes) != 0 || got.Users != nil || got.Routes != nil || got.Networks != nil {
		t.Fatalf("got = %+v, want an empty inventory", got)
	}
	if got.UsersBroken != "" || got.RoutesBroken != "" || got.NetworksBroken != "" {
		t.Fatalf("got = %+v, want no Broken set for files that simply don't exist", got)
	}
}

func TestLoad_MissingRootIsAnError(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "does-not-exist")); err == nil {
		t.Fatal("Load: want an error for a missing root")
	}
}

func TestLoad_BrokenNodeIsListedWithParseError(t *testing.T) {
	root := t.TempDir()

	writeFile(t, filepath.Join(root, NodesDir, "good.yaml"), "id: good\nreaches: [home]\n")
	// Invalid YAML.
	writeFile(t, filepath.Join(root, NodesDir, "bad-syntax.yaml"), "id: [unterminated\n")
	// Unknown key.
	writeFile(t, filepath.Join(root, NodesDir, "bad-key.yaml"), "id: bad-key\ntypo_field: oops\n")
	// A network entry that is neither a mapping nor a list.
	writeFile(t, filepath.Join(root, NodesDir, "bad-networks.yaml"), "id: bad-networks\nnetworks: home\n")

	got, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.Nodes) != 4 {
		t.Fatalf("Nodes = %d, want 4 (broken nodes still listed): %+v", len(got.Nodes), got.Nodes)
	}

	byPath := map[string]Node{}
	for _, n := range got.Nodes {
		byPath[filepath.Base(n.Path)] = n
	}

	if byPath["good.yaml"].Broken != "" {
		t.Fatalf("good.Broken = %q, want empty", byPath["good.yaml"].Broken)
	}
	if byPath["bad-syntax.yaml"].Broken == "" {
		t.Fatal("bad-syntax.Broken = empty, want parse error")
	}
	if byPath["bad-key.yaml"].Broken == "" {
		t.Fatal("bad-key.Broken = empty, want unknown-field error")
	}
	if byPath["bad-networks.yaml"].Broken == "" {
		t.Fatal("bad-networks.Broken = empty, want a networks shape error")
	}
}

func TestLoad_BrokenTopLevelFileIsReported(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, UsersFilename), "users: [not, a, mapping]\n")
	writeFile(t, filepath.Join(root, RoutesFilename), "routes:\n  sea:\n    hops: [u-node-group-09-01/ss-sea01:main]\n    typo: oops\n")
	writeFile(t, filepath.Join(root, NetworksFilename), "networks: {not: a-list}\n")

	got, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got.UsersBroken == "" {
		t.Fatal("UsersBroken = empty, want an error")
	}
	if got.RoutesBroken == "" {
		t.Fatal("RoutesBroken = empty, want an error")
	}
	if got.NetworksBroken == "" {
		t.Fatal("NetworksBroken = empty, want an error")
	}
}

// TestInstanceRuntime pins the default and what counts as containerised:
// an instance saying nothing is a host process, and every other runtime
// draws the boundary that changes what `bind` means.
func TestInstanceRuntime(t *testing.T) {
	for _, tc := range []struct {
		runtime       string
		want          string
		containerised bool
	}{
		{"", RuntimeHost, false},
		{RuntimeHost, RuntimeHost, false},
		{RuntimeDocker, RuntimeDocker, true},
		{RuntimePodman, RuntimePodman, true},
	} {
		inst := Instance{ID: "x", Runtime: tc.runtime}
		if got := inst.RuntimeOr(); got != tc.want {
			t.Errorf("Instance{Runtime: %q}.RuntimeOr() = %q, want %q", tc.runtime, got, tc.want)
		}
		if got := inst.Containerised(); got != tc.containerised {
			t.Errorf("Instance{Runtime: %q}.Containerised() = %v, want %v", tc.runtime, got, tc.containerised)
		}
	}
}

func TestLoad_InstancePathIsItsOwnFile(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, NodesDir, "cloud", "srv.yaml"), "id: srv\ninstances:\n  directory: srv.instances\n")
	writeFile(t, filepath.Join(root, NodesDir, "cloud", "srv.instances", "a.yaml"), "id: a\nservice: one\n")
	writeFile(t, filepath.Join(root, NodesDir, "cloud", "inline.yaml"), "id: inline\ninstances:\n  - id: b\n    service: two\n")

	got, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"a": filepath.Join(NodesDir, "cloud", "srv.instances", "a.yaml"),
		"b": filepath.Join(NodesDir, "cloud", "inline.yaml"),
	}
	for _, n := range got.Nodes {
		if n.Broken != "" {
			t.Fatalf("node %s broken: %s", n.Path, n.Broken)
		}
		for _, inst := range n.Instances {
			if inst.Path != want[inst.Name] {
				t.Errorf("instance %s Path = %q, want %q", inst.ID, inst.Path, want[inst.ID])
			}
		}
	}
}

// A node file error must carry the line it is on in that file, in both forms
// of `instances`.
func TestLoad_NodeErrorKeepsFileLineNumber(t *testing.T) {
	for _, instances := range []string{"instances:\n  directory: srv.instances\n", "instances:\n  - id: a\n    service: one\n"} {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, NodesDir, "srv.yaml"), "id: srv\n\n\n# comment\n"+instances+"\n\ntypo: yes\n")
		if strings.Contains(instances, "directory") {
			writeFile(t, filepath.Join(root, NodesDir, "srv.instances", "a.yaml"), "id: a\nservice: one\n")
		}
		got, err := Load(root)
		if err != nil {
			t.Fatal(err)
		}
		wantLine := strings.Count("id: srv\n\n\n# comment\n"+instances+"\n\n", "\n") + 1
		want := fmt.Sprintf("line %d", wantLine)
		if len(got.Nodes) != 1 || !strings.Contains(got.Nodes[0].Broken, want) {
			t.Fatalf("Nodes = %+v, want broken at %s", got.Nodes, want)
		}
	}
}

// Only a directory a root-level node names is an instance directory; any
// other directory, whatever its name, is a group.
func TestLoad_UnreferencedInstancesSuffixIsAGroup(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, NodesDir, "odd.instances", "n.yaml"), "id: n\n")

	got, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Nodes) != 1 || got.Nodes[0].ID != "n" || got.Nodes[0].Group != "odd.instances" {
		t.Fatalf("Nodes = %+v, want node n in group odd.instances", got.Nodes)
	}
}

// TestQualifyInstances_FillsWhatAContainerTakesFromItsNode: an instance that
// writes no runtime takes its node's, a containerised one joins the node's
// first container network and binds every interface, and a host process
// joins none.
func TestQualifyInstances_FillsWhatAContainerTakesFromItsNode(t *testing.T) {
	node := Node{
		ID: "home", Runtime: RuntimeDocker,
		Containers: []ContainerNetwork{{Name: "apps", Subnet: "172.29.0.0/24"}, {Name: "other", Subnet: "172.29.1.0/24"}},
		Instances: []Instance{
			{Name: "caddy"},
			{Name: "dns", Containers: map[string]string{"other": "172.29.1.53"}},
			{Name: "ssh", Runtime: RuntimeHost},
		},
	}
	qualifyInstances(&node)
	caddy, dns, ssh := node.Instances[0], node.Instances[1], node.Instances[2]
	if _, ok := caddy.Containers["apps"]; caddy.Runtime != RuntimeDocker || !ok || len(caddy.Containers) != 1 || caddy.Bind != ContainerBind {
		t.Errorf("caddy = runtime %q containers %v bind %q, want docker, apps, %s", caddy.Runtime, caddy.Containers, caddy.Bind, ContainerBind)
	}
	if len(dns.Containers) != 1 || dns.Containers["other"] != "172.29.1.53" {
		t.Errorf("dns containers = %v, want the one it wrote", dns.Containers)
	}
	if ssh.Runtime != RuntimeHost || len(ssh.Containers) != 0 || ssh.Bind != "" {
		t.Errorf("ssh = runtime %q containers %v bind %q, want a host process with neither", ssh.Runtime, ssh.Containers, ssh.Bind)
	}
}

// TestNode_SharedContainerFollowsTheNodesOrder: two instances on two common
// container networks dial over the one the node lists first, whatever order
// either instance wrote them in.
func TestNode_SharedContainerFollowsTheNodesOrder(t *testing.T) {
	node := Node{Containers: []ContainerNetwork{{Name: "apps"}, {Name: "tailnet"}, {Name: "media"}}}
	proxy := Instance{Containers: map[string]string{"tailnet": "172.29.250.10", "apps": ""}}
	app := Instance{Containers: map[string]string{"apps": "", "tailnet": ""}}
	media := Instance{Containers: map[string]string{"media": ""}}
	if got := node.SharedContainer(proxy, app); got != "apps" {
		t.Errorf("SharedContainer(proxy, app) = %q, want apps", got)
	}
	if got := node.SharedContainer(proxy, media); got != "" {
		t.Errorf("SharedContainer(proxy, media) = %q, want none", got)
	}
	joined := node.JoinedContainers(proxy)
	if len(joined) != 2 || joined[0].Name != "apps" || joined[1].Name != "tailnet" {
		t.Errorf("JoinedContainers(proxy) = %v, want apps then tailnet", joined)
	}
}

// TestLoad_ContainerWithoutAddressIsJoined: a container network written with
// no value is joined, with the address left to the runtime.
func TestLoad_ContainerWithoutAddressIsJoined(t *testing.T) {
	var inst Instance
	if err := decodeStrict([]byte("id: caddy\ncontainers:\n  apps:\n  tailnet: 172.29.250.10\n"), &inst); err != nil {
		t.Fatal(err)
	}
	if addr, ok := inst.Containers["apps"]; !ok || addr != "" {
		t.Errorf("apps = %q, %v; want joined with no address", addr, ok)
	}
	if inst.Containers["tailnet"] != "172.29.250.10" {
		t.Errorf("tailnet = %q, want 172.29.250.10", inst.Containers["tailnet"])
	}
}

// TestLoadRoutes_ScopedRoutesAreKeyedByScope: a key holding hops is a route;
// any other key is a scope of routes, each keyed <scope>/<name>.
func TestLoadRoutes_ScopedRoutesAreKeyedByScope(t *testing.T) {
	dir := t.TempDir()
	data := "routes:\n  ss:\n    hops: [a/ss:main]\n  home:\n    samba:\n      hops: [b/samba:smb]\n"
	if err := os.WriteFile(filepath.Join(dir, RoutesFilename), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	routes, broken, err := loadRoutes(dir)
	if err != nil || broken != "" {
		t.Fatalf("loadRoutes: %v %s", err, broken)
	}
	if r, ok := routes["ss"]; !ok || r.Scope != "" {
		t.Errorf("ss = %+v, want a top-level route", r)
	}
	if r, ok := routes["home/samba"]; !ok || r.Scope != "home" || r.Hops[0] != "b/samba:smb" {
		t.Errorf("home/samba = %+v, want scope home", r)
	}
}

func TestLoad_Links(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, LinksFilename), `links:
  home-nce:
    from: home/agent-home
    to: nce/relay-nce:agents
`)

	got, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.LinksBroken != "" {
		t.Fatalf("LinksBroken = %q, want empty", got.LinksBroken)
	}
	want := Link{From: "home/agent-home", To: "nce/relay-nce:agents"}
	if len(got.Links) != 1 || got.Links["home-nce"] != want {
		t.Fatalf("Links = %+v, want home-nce: %+v", got.Links, want)
	}
}

// A missing links.yaml is an inventory with no links, which is every
// inventory written before links existed.
func TestLoad_MissingLinksIsEmpty(t *testing.T) {
	got, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Links != nil || got.LinksBroken != "" {
		t.Fatalf("Links = %+v, LinksBroken = %q, want neither", got.Links, got.LinksBroken)
	}
}

func TestLoad_BrokenLinksIsReported(t *testing.T) {
	for name, content := range map[string]string{
		"unknown key":   "links:\n  a:\n    from: n/x\n    to: m/y:p\n    via: r\n",
		"not a mapping": "links: [a, b]\n",
		"bad syntax":    "links: {unterminated\n",
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, filepath.Join(root, LinksFilename), content)
			got, err := Load(root)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if got.LinksBroken == "" || got.Links != nil {
				t.Fatalf("Links = %+v, LinksBroken = %q, want only an error", got.Links, got.LinksBroken)
			}
		})
	}
}

// The reverse-exit fixture is what links.md proposes; its links.yaml is the
// file this step reads.
func TestLoad_ReverseExitFixtureLinks(t *testing.T) {
	got, err := Load(filepath.Join("..", "docs", "fixtures", "reverse-exit", "conf"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.LinksBroken != "" {
		t.Fatalf("LinksBroken = %q", got.LinksBroken)
	}
	if l := got.Links["home-nce"]; l.From != "home/agent-home" || l.To != "nce/relay-nce:agents" {
		t.Fatalf("Links = %+v", got.Links)
	}
}

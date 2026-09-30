package target

import (
	"sort"
	"testing"

	"github.com/d0u9/rhumb/confgen"
	"github.com/d0u9/rhumb/derive"
	"github.com/d0u9/rhumb/inventory"
)

// fixture is a small inventory: one server node, one managed client node,
// one unmanaged user, and a two-hop route so route: has something to pin
// beyond an entry hop.
func fixture(t *testing.T) (*inventory.Root, *derive.Model) {
	t.Helper()
	inv := &inventory.Root{
		Nodes: []inventory.Node{
			{
				ID:       "srv",
				Networks: inventory.Networks{"internet": "203.0.113.10"},
				Instances: []inventory.Instance{
					{ID: "ss-srv", Service: "ssserver", Ports: inventory.PortsOf(map[string]int{"main": 38250})},
				},
			},
			{
				ID:       "relay",
				Networks: inventory.Networks{"internet": "203.0.113.20"},
				Instances: []inventory.Instance{
					{ID: "ss-relay", Service: "ssserver", Ports: inventory.PortsOf(map[string]int{"main": 40000})},
				},
			},
			{ID: "laptop", Owner: "dana"},
			{ID: "broken.yaml", Broken: "yaml: line 3: did not find expected key"},
		},
		Users: map[string]inventory.User{
			"dana": {Access: []string{"sea"}},
			"yak":  {Devices: inventory.DevicesNone, Access: []string{"sea"}},
		},
		Routes: map[string]inventory.Route{
			"sea":   {Hops: []string{"ss-srv:main"}},
			"chain": {Hops: []string{"ss-relay:main", "ss-srv:main"}},
		},
		Networks:  []string{"internet"},
		Universal: "internet",
	}
	manifests := map[string]confgen.Manifest{
		"ssserver": {Auth: confgen.AuthPerPrincipal, Exports: []string{"ss-json"}, Template: "t"},
		"ss-json":  {Auth: confgen.AuthNone, Template: "t"},
	}
	model, err := derive.Derive(inv, manifests)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	return inv, model
}

func instanceNames(ts []Target) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.Instance
	}
	sort.Strings(out)
	return out
}

func TestList_RealAndDerivedInstances(t *testing.T) {
	inv, model := fixture(t)
	targets := List(inv, model)

	var clean []Target
	for _, tg := range targets {
		if tg.Broken == "" {
			clean = append(clean, tg)
		}
	}
	got := instanceNames(clean)
	want := []string{"laptop-sea-ssserver-ss-json", "ss-relay", "ss-srv", "yak-default-sea-ssserver-ss-json"}
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("List instances = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("List instances = %v, want %v", got, want)
		}
	}
}

func TestList_BrokenNodeContributesOneTarget(t *testing.T) {
	inv, model := fixture(t)
	targets := List(inv, model)

	var found *Target
	for i := range targets {
		if targets[i].Node == "broken.yaml" {
			found = &targets[i]
		}
	}
	if found == nil {
		t.Fatal("no target for the broken node")
	}
	if found.Broken == "" {
		t.Fatal("Broken = empty, want the parse error")
	}
	if found.Instance != "" {
		t.Fatalf("Instance = %q, want empty for a broken node", found.Instance)
	}
}

func TestList_FieldsOnRealAndDerivedTargets(t *testing.T) {
	inv, model := fixture(t)
	targets := List(inv, model)

	byInstance := map[string]Target{}
	for _, tg := range targets {
		byInstance[tg.Instance] = tg
	}

	srv := byInstance["ss-srv"]
	if srv.Node != "srv" || srv.Service != "ssserver" {
		t.Fatalf("ss-srv = %+v", srv)
	}
	if len(srv.Routes) != 2 { // both sea (entry) and chain (a later hop).
		t.Fatalf("ss-srv.Routes = %v, want 2 routes", srv.Routes)
	}

	client := byInstance["laptop-sea-ssserver-ss-json"]
	if client.Node != "laptop" || client.User != "dana" || client.Export != "ss-json" {
		t.Fatalf("laptop-sea-ssserver-ss-json = %+v", client)
	}
	if len(client.Routes) != 1 || client.Routes[0] != "sea" {
		t.Fatalf("laptop-sea-ssserver-ss-json.Routes = %v, want [sea]", client.Routes)
	}

	unmanaged := byInstance["yak-default-sea-ssserver-ss-json"]
	if unmanaged.Node != "" || unmanaged.User != "yak" {
		t.Fatalf("yak-default-sea-ssserver-ss-json = %+v, want no node and User yak", unmanaged)
	}
}

func TestGroupByNode(t *testing.T) {
	inv, model := fixture(t)
	targets := List(inv, model)
	groups := GroupByNode(targets)

	byNode := map[string]int{}
	for _, g := range groups {
		byNode[g.Node] = len(g.Targets)
	}
	if byNode["srv"] != 1 || byNode["relay"] != 1 || byNode["laptop"] != 1 {
		t.Fatalf("byNode = %v", byNode)
	}
	// yak has no node, so its target groups under its user key.
	if byNode["yak"] != 1 {
		t.Fatalf("byNode[yak] = %d, want 1", byNode["yak"])
	}
}

func TestMatch_ByEachField(t *testing.T) {
	inv, model := fixture(t)
	targets := List(inv, model)

	cases := []struct {
		selector string
		want     []string
	}{
		{"node:srv", []string{"ss-srv"}},
		{"user:dana", []string{"laptop-sea-ssserver-ss-json"}},
		{"service:ssserver", []string{"ss-relay", "ss-srv"}},
		{"route:chain", []string{"ss-relay", "ss-srv"}},
		{"instance:ss-srv", []string{"ss-srv"}},
		{"ss-srv", []string{"ss-srv"}}, // bare word is an instance term.
		{"node:*rv", []string{"ss-srv"}},
	}
	for _, c := range cases {
		matched, err := Match(c.selector, targets)
		if err != nil {
			t.Fatalf("Match(%q): %v", c.selector, err)
		}
		got := instanceNames(matched)
		want := append([]string(nil), c.want...)
		sort.Strings(want)
		if len(got) != len(want) {
			t.Fatalf("Match(%q) = %v, want %v", c.selector, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("Match(%q) = %v, want %v", c.selector, got, want)
			}
		}
	}
}

func TestMatch_AllTermsMustMatch(t *testing.T) {
	inv, model := fixture(t)
	targets := List(inv, model)
	// node:srv holds only a shadowsocks-rust instance, so combined with an
	// unrelated service the AND of both terms matches nothing — were this an
	// OR, node:srv alone would still produce ss-srv.
	if _, err := Match("node:srv service:hysteria2", targets); err == nil {
		t.Fatal("Match: want an error, terms are ANDed and none of node:srv holds a hysteria2 instance")
	}
}

func TestMatch_NothingIsAnError(t *testing.T) {
	inv, model := fixture(t)
	targets := List(inv, model)
	if _, err := Match("instance:nonesuch", targets); err == nil {
		t.Fatal("Match: want an error for a selector matching nothing")
	}
}

// TestMatch_DocumentedExamples pins the selector examples export.md prints,
// against nodes and a route named as that page's worked example does — so a
// change to selector syntax that leaves the doc's prose but breaks its
// examples is caught here rather than by a reader trying them by hand.
func TestMatch_DocumentedExamples(t *testing.T) {
	inv := &inventory.Root{
		Nodes: []inventory.Node{
			{
				ID:       "u-node-group-09-01",
				Networks: inventory.Networks{"internet": "203.0.113.10"},
				Instances: []inventory.Instance{
					{ID: "ss-sea01", Service: "ssserver", Ports: inventory.PortsOf(map[string]int{"main": 38250})},
					{ID: "hy2-sea01", Service: "hysteria2", Ports: inventory.PortsOf(map[string]int{"main": 443})},
				},
			},
			{ID: "macbook", Owner: "dana"},
		},
		Users: map[string]inventory.User{
			"dana": {Access: []string{"jp", "sea"}},
		},
		Routes: map[string]inventory.Route{
			"jp":  {Hops: []string{"hy2-sea01:main"}},
			"sea": {Hops: []string{"ss-sea01:main"}},
		},
		Networks:  []string{"internet"},
		Universal: "internet",
	}
	manifests := map[string]confgen.Manifest{
		"ssserver":  {Auth: confgen.AuthPerPrincipal, Exports: []string{"ss-json"}, Template: "t"},
		"ss-json":   {Auth: confgen.AuthNone, Template: "t"},
		"hysteria2": {Auth: confgen.AuthPerPrincipal, Exports: []string{"sing-box"}, Template: "t"},
		"sing-box":  {Auth: confgen.AuthNone, Template: "t"},
	}
	model, err := derive.Derive(inv, manifests)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	targets := List(inv, model)

	cases := []struct {
		selector string
		want     []string
	}{
		{"node:u-node-group-09-01", []string{"ss-sea01", "hy2-sea01"}},
		{"node:macbook", []string{"macbook-jp-hysteria2-sing-box", "macbook-sea-ssserver-ss-json"}},
		{"user:dana", []string{"macbook-jp-hysteria2-sing-box", "macbook-sea-ssserver-ss-json"}},
		{"service:hysteria2", []string{"hy2-sea01"}},
		{"route:jp", []string{"hy2-sea01", "macbook-jp-hysteria2-sing-box"}},
		{"instance:ss-sea01", []string{"ss-sea01"}},
		{"node:u-node-group-09-*", []string{"ss-sea01", "hy2-sea01"}},
	}
	for _, c := range cases {
		matched, err := Match(c.selector, targets)
		if err != nil {
			t.Fatalf("Match(%q): %v", c.selector, err)
		}
		got := instanceNames(matched)
		want := append([]string(nil), c.want...)
		sort.Strings(want)
		if len(got) != len(want) {
			t.Fatalf("Match(%q) = %v, want %v", c.selector, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("Match(%q) = %v, want %v", c.selector, got, want)
			}
		}
	}
}

func TestMatch_UnknownFieldIsAnError(t *testing.T) {
	inv, model := fixture(t)
	targets := List(inv, model)
	if _, err := Match("bogus:x", targets); err == nil {
		t.Fatal("Match: want an error for an unknown field")
	}
}

func TestMatch_ProfileNarrowsToOneUseOfADevice(t *testing.T) {
	inv, _ := fixture(t)
	inv.Nodes[2].Profiles = map[string]inventory.Profile{"singbox": {}, "browser": {}}
	model, err := derive.Derive(inv, map[string]confgen.Manifest{
		"ssserver": {Auth: confgen.AuthPerPrincipal, Exports: []string{"ss-json"}, Template: "t"},
	})
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	matched, err := Match("node:laptop profile:singbox", List(inv, model))
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if got := instanceNames(matched); len(got) != 1 || got[0] != "laptop-sea-ssserver-ss-json-singbox" || matched[0].Profile != "singbox" {
		t.Fatalf("Match = %v, want the singbox file alone", got)
	}
}

func TestRoutes_NarrowsAProfilesProgram(t *testing.T) {
	matched := []Target{
		{Instance: "mac-singbox", Profile: "singbox", Routes: []string{"sea-ss", "sea-hy2"}},
		{Instance: "mac-sea-hy2-hysteria2-link-phone", Profile: "phone", Export: "link", Routes: []string{"sea-hy2"}},
	}
	got, err := Routes("node:mac route:sea-hy2", matched)
	if err != nil || len(got) != 1 || len(got["mac-singbox"]) != 1 || got["mac-singbox"][0] != "sea-hy2" {
		t.Fatalf("Routes = %v, %v", got, err)
	}
	if got, _ := Routes("node:mac", matched); got != nil {
		t.Fatalf("no route term narrowed %v", got)
	}
}

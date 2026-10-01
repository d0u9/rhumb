package derive

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/d0u9/rhumb/confgen"
	"github.com/d0u9/rhumb/inventory"
)

// fixture loads docs/fixtures/reverse-exit, or one of its variants.
func fixture(t *testing.T, variant string) (*inventory.Root, map[string]confgen.Manifest) {
	t.Helper()
	root := filepath.Join("..", "docs", "fixtures", "reverse-exit", "conf")
	if variant != "" {
		root = filepath.Join("..", "docs", "fixtures", "reverse-exit", "variants", variant, "conf")
	}
	inv, err := inventory.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	cg, err := confgen.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	manifests := map[string]confgen.Manifest{}
	for _, s := range cg.Services {
		if s.Broken != "" {
			t.Fatalf("%s: %s", s.Name, s.Broken)
		}
		manifests[s.Name] = s.Manifest
	}
	return inv, manifests
}

func edgeOf(t *testing.T, m *Model, route, from string) Edge {
	t.Helper()
	for _, e := range m.Edges {
		if e.Route == route && e.From.Instance == from {
			return e
		}
	}
	t.Fatalf("no edge %s from %s in %+v", route, from, m.Edges)
	return Edge{}
}

func TestDerive_LinkReverseExit(t *testing.T) {
	inv, manifests := fixture(t, "")
	m, err := Derive(inv, manifests)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Links) != 1 {
		t.Fatalf("Links = %+v", m.Links)
	}
	l := m.Links[0]
	if l.Name != "home-nce" || l.From != "home/agent-home" || l.To != (Hop{"nce/relay-nce", "agents"}) ||
		l.Address != "nce.example.net" || l.Network != "internet" || l.Port != 40000 {
		t.Fatalf("Link = %+v", l)
	}

	e := edgeOf(t, m, "home-exit", "nce/relay-nce")
	if e.Link != "home-nce" || e.Dialer != "home/agent-home" || e.Address != "127.0.0.1" || e.Network != "" || e.Container != "" {
		t.Fatalf("Edge = %+v", e)
	}
	if e.DialerOr() != "home/agent-home" {
		t.Fatalf("DialerOr = %q", e.DialerOr())
	}

	var linkGrant bool
	for _, g := range m.Grants {
		if g.Instance == "nce/relay-nce" && g.Port == "agents" {
			linkGrant = g.Principal == Principal{Kind: PrincipalInstance, ID: "home/agent-home",
				Name: "home-agent-home", Group: "home-agent-home", Slot: inventory.DefaultCredential}
		}
	}
	if !linkGrant {
		t.Fatalf("Grants = %+v, want agent-home's own on relay-nce:agents", m.Grants)
	}

	if len(m.LinkMappings) != 1 {
		t.Fatalf("LinkMappings = %+v", m.LinkMappings)
	}
	mp := m.LinkMappings[0]
	if mp.Link != "home-nce" || mp.Key != "relay-nce.home--ss-home.users" ||
		mp.Entrance != (Hop{"nce/relay-nce", "home"}) || mp.Target != (Hop{"home/ss-home", "users"}) ||
		mp.Address != "127.0.0.1" || strings.Join(mp.Routes, ",") != "home-exit" {
		t.Fatalf("LinkMapping = %+v", mp)
	}
}

func TestDerive_LinkFarEndSharesAContainerNetwork(t *testing.T) {
	inv, manifests := fixture(t, "shared-container")
	m, err := Derive(inv, manifests)
	if err != nil {
		t.Fatal(err)
	}
	e := edgeOf(t, m, "home-exit", "nce/relay-nce")
	if e.Address != "ss-home" || e.Container != "apps" {
		t.Fatalf("Edge = %+v", e)
	}
}

func TestDerive_LinkErrors(t *testing.T) {
	for variant, want := range map[string]string{
		"no-shared-network": "loopback inside it is the container itself",
		"second-agent":      "home-nce, home-nce-2",
		"no-links":          "",
	} {
		t.Run(variant, func(t *testing.T) {
			inv, manifests := fixture(t, variant)
			_, err := Derive(inv, manifests)
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("err = %v, want one containing %q", err, want)
			}
		})
	}
}

// Two routes sharing a stretch share its mapping, and a far end that is the
// target itself dials nothing on its node.
func TestDerive_LinkMappingsDeduplicateAndFarEndMayBeTarget(t *testing.T) {
	inv, manifests := fixture(t, "")
	inv.Routes["home-exit-2"] = inventory.Route{Hops: []string{"nce/relay-nce:home", "home/ss-home:users"}}
	m, err := Derive(inv, manifests)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.LinkMappings) != 1 || strings.Join(m.LinkMappings[0].Routes, ",") != "home-exit,home-exit-2" {
		t.Fatalf("LinkMappings = %+v", m.LinkMappings)
	}

	inv, manifests = fixture(t, "")
	inv.Links["home-nce"] = inventory.Link{From: "home/ss-home", To: "nce/relay-nce:agents"}
	m, err = Derive(inv, manifests)
	if err != nil {
		t.Fatal(err)
	}
	if e := edgeOf(t, m, "home-exit", "nce/relay-nce"); e.Dialer != "home/ss-home" || e.Address != "127.0.0.1" {
		t.Fatalf("Edge = %+v", e)
	}
}

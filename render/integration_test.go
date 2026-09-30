package render_test

// This is the milestone 6 checkpoint docs/apps/conf/inventory.md asks for:
// "at the end of this milestone one target renders end to end." It wires
// inventory, derive and secretstore together by hand — the way a future
// export command will — and renders one real target through render.Render.

import (
	"strings"
	"testing"

	"github.com/d0u9/rhumb/confgen"
	"github.com/d0u9/rhumb/derive"
	"github.com/d0u9/rhumb/inventory"
	"github.com/d0u9/rhumb/render"
	"github.com/d0u9/rhumb/secretstore"
)

func TestEndToEnd_OneTargetRenders(t *testing.T) {
	inv := &inventory.Root{
		Nodes: []inventory.Node{
			{
				ID:       "srv",
				Networks: inventory.Networks{"internet": "203.0.113.10"},
				Instances: []inventory.Instance{
					{ID: "ss-srv", Service: "ssserver", Ports: inventory.PortsOf(map[string]int{"main": 38250})},
				},
			},
			{ID: "laptop", Owner: "dana"},
		},
		Users:     map[string]inventory.User{"dana": {Access: []string{"sea"}}},
		Routes:    map[string]inventory.Route{"sea": {Hops: []string{"ss-srv:main"}}},
		Networks:  []string{"internet"},
		Universal: "internet",
	}
	manifests := map[string]confgen.Manifest{
		"ssserver": {
			Secret:   confgen.Secret{Kind: "base64", Bytes: 32},
			Auth:     confgen.AuthPerPrincipal,
			Exports:  []string{"ss-json"},
			Template: "t",
		},
		"ss-json": {Auth: confgen.AuthNone, Template: "t"},
	}

	model, err := derive.Derive(inv, manifests)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}

	secretsRoot := t.TempDir()
	implied := secretstore.ImpliedPaths(inv, manifests, model)
	if err := secretstore.Generate(secretsRoot, implied, inv, manifests); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	// Render the server side, ss-srv/main: one principal, dana's laptop.
	target := render.Target{Service: "ssserver", Instance: "ss-srv"}
	srvNode := inv.Nodes[0]
	srvInst := srvNode.Instances[0]

	var principals []render.Principal
	for _, p := range model.Principals("ss-srv", "main") {
		value, err := secretstore.ReadValue(secretsRoot, secretstore.Path{
			Instance: "ss-srv", Port: "main", Group: p.Group, Name: p.Slot,
		})
		if err != nil {
			t.Fatalf("ReadValue: %v", err)
		}
		principals = append(principals, render.Principal{Name: p.Name, Secret: value})
	}

	out, err := render.Render(render.Input{
		Target:       target,
		Template:     `{{ (node).id }}/{{ (instance).id }}: {{ range principals "main" }}{{ .Name }} {{ end }}`,
		DefaultsKind: confgen.DefaultsDocument,
		Instance:     map[string]any{"id": srvInst.ID, "service": srvInst.Service},
		Node:         map[string]any{"id": srvNode.ID},
		Principals:   map[string][]render.Principal{"main": principals},
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.HasPrefix(string(out), "srv/ss-srv: dana-default") {
		t.Fatalf("out = %q, want it to start with srv/ss-srv: dana-default", out)
	}
	if len(principals) != 1 || principals[0].Secret == "" {
		t.Fatalf("principals = %+v, want one principal with a non-empty secret", principals)
	}

	// Render the derived client side too: laptop-sea-ssserver-ss-json, whose upstream is
	// ss-srv:main, resolved to srv's internet address.
	var upstream map[string]any
	for _, e := range model.Edges {
		if e.FromInstance == "laptop-sea-ssserver-ss-json" {
			upstreamSecret, err := secretstore.ReadValue(secretsRoot, secretstore.Path{
				Instance: "ss-srv", Port: "main", Group: "dana", Name: "default",
			})
			if err != nil {
				t.Fatalf("ReadValue: %v", err)
			}
			upstream = map[string]any{"address": e.Address, "port": e.Port, "secret": upstreamSecret}
		}
	}
	if upstream == nil {
		t.Fatal("no edge found from laptop-sea-ssserver-ss-json")
	}

	clientOut, err := render.Render(render.Input{
		Target:       render.Target{Service: "ss-json", Instance: "laptop-sea-ssserver-ss-json"},
		Template:     `{{ (upstream).address }}:{{ (upstream).port }}`,
		DefaultsKind: confgen.DefaultsDocument,
		Upstream:     upstream,
	})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if string(clientOut) != "203.0.113.10:38250" {
		t.Fatalf("clientOut = %q, want 203.0.113.10:38250", clientOut)
	}
}

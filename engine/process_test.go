package engine

import (
	"path/filepath"
	"testing"

	"github.com/d0u9/rhumb/target"
	"github.com/d0u9/rhumb/validate"
)

func TestProcess_OneFileForTwoInstances(t *testing.T) {
	root := filepath.Join("..", "docs", "fixtures", "reverse-exit", "variants", "one-process", "conf")
	l, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if issues := validate.Validate(l.Inv, l.Manifests, l.Exports, l.Derived, nil); len(issues) != 0 {
		t.Fatalf("issues = %v", issues)
	}

	// The process is the target; its members are not.
	var found bool
	for _, tg := range target.List(l.Inv, l.Derived) {
		switch tg.Instance {
		case "nce/relay-nce", "nce/tunnel-nce":
			t.Fatalf("member %s listed as a target", tg.Instance)
		case "nce/xray-nce":
			found = true
			if len(tg.Routes) != 2 || tg.Routes[0] != "home-exit" || tg.Routes[1] != "sea-exit" {
				t.Fatalf("process routes = %v", tg.Routes)
			}
		}
	}
	if !found {
		t.Fatal("process not listed")
	}

	m := Renderer{Data: l, RootPath: root}
	out, err := m.RenderTarget("nce/xray-nce")
	if err != nil {
		t.Fatal(err)
	}
	want := "relay-nce reverse-relay home-nce:to home>127.0.0.1:8388\ntunnel-nce tunnel-client nce-sea:from sea>127.0.0.1:8388\n\n"
	if len(out) != 1 || string(out[0].Bytes) != want {
		t.Fatalf("rendered = %q, want %q", out[0].Bytes, want)
	}

	// Its ports publish as its members' do.
	mp := l.Derived.Mappings(l.Inv, "nce/xray-nce")
	for _, port := range []string{"agents", "home", "sea"} {
		if len(mp[port].Addresses) != 1 {
			t.Fatalf("mapping %s = %+v", port, mp[port])
		}
	}
}

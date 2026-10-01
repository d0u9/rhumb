package engine

import (
	"path/filepath"
	"reflect"
	"testing"
)

// The reverse-exit fixture's expected/contexts, less the secrets, which need
// a secrets root.
func TestLinksFor_ReverseExit(t *testing.T) {
	l, err := Load(filepath.Join("..", "docs", "fixtures", "reverse-exit", "conf"))
	if err != nil {
		t.Fatal(err)
	}
	m := Renderer{Data: l}

	agent, err := m.linksFor("home/agent-home")
	if err != nil {
		t.Fatal(err)
	}
	if len(agent) != 1 {
		t.Fatalf("links = %+v", agent)
	}
	a := agent[0]
	if a.Name != "home-nce" || a.End != "from" || a.Peer.Instance != "relay-nce" || a.Peer.Port != "agents" ||
		a.Peer.Number != 40000 || a.Peer.Address != "nce.example.net" || a.Peer.Account != "home-agent-home" || a.Peer.Values == nil {
		t.Fatalf("agent link = %+v", a)
	}
	if len(a.Carries) != 1 {
		t.Fatalf("carries = %+v", a.Carries)
	}
	c := a.Carries[0]
	if c.Key != "relay-nce.home--ss-home.users" || c.Entrance.Number != 40001 || c.Entrance.Protocol != "tcp" ||
		c.Target.Instance != "ss-home" || c.Target.Address != "127.0.0.1" || c.Target.Number != 8388 || len(c.Routes) != 1 || c.Routes[0] != "home-exit" {
		t.Fatalf("carries[0] = %+v", c)
	}

	relay, err := m.linksFor("nce/relay-nce")
	if err != nil {
		t.Fatal(err)
	}
	if len(relay) != 1 || relay[0].End != "to" || relay[0].Peer.Instance != "agent-home" || relay[0].Peer.Account != "home-agent-home" ||
		len(relay[0].Carries) != 1 || !reflect.DeepEqual(relay[0].Carries[0], c) {
		t.Fatalf("relay links = %+v", relay)
	}

	ds := m.downstreamsFor("nce/relay-nce", true)
	if len(ds) != 1 || ds[0].Link != "home-nce" || ds[0].Address != "127.0.0.1" {
		t.Fatalf("downstreams = %+v", ds)
	}

	if none, _ := m.linksFor("home/ss-home"); none != nil {
		t.Fatalf("ss-home links = %+v, want none", none)
	}
}

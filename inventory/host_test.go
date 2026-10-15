package inventory

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoadHostStrictShapes(t *testing.T) {
	for _, body := range []string{
		"null",
		"network: ''\n    interfaces: {}",
		"names: []\n    endpoints: {}",
		"interfaces:\n      lan:\n        unknown: true",
		"interfaces:\n      lan:\n        addresses:\n          a:\n            network: home\n            ip: 10.0.0.10\n            unknown: true",
		"endpoints:\n      home:\n        from:\n          interface: lan\n          address: a\n          unknown: true",
	} {
		t.Run(body, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, filepath.Join(root, HostsFilename), "hosts:\n  host-a:\n    "+body+"\n")
			inv, err := Load(root)
			if err != nil {
				t.Fatal(err)
			}
			if inv.HostsBroken == "" {
				t.Fatal("invalid host accepted")
			}
		})
	}
	for _, body := range []string{"networks: {}", "reaches: []"} {
		t.Run(body, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, filepath.Join(root, NodesDir, "node-1.yaml"), "id: node-1\nhost: host-a\n"+body+"\n")
			inv, err := Load(root)
			if err != nil {
				t.Fatal(err)
			}
			if len(inv.Nodes) != 1 || inv.Nodes[0].Broken == "" {
				t.Fatal("mixed node accepted")
			}
		})
	}
}

func TestHostRejectsNullRecordsAndInvalidEndpointLiterals(t *testing.T) {
	for _, hosts := range []string{
		"hosts: {host-a: null}",
		"hosts: {host-a: {interfaces: {wired: null}}}",
		"hosts: {host-a: {interfaces: {wired: {addresses: {main: null}}}}}",
		"hosts: {host-a: {endpoints: {home: null}}}",
		"hosts: {host-a: {endpoints: {home: {from: null}}}}",
		"hosts: {host-a: {<<: {interfaces: {wired: null}}}}",
	} {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, HostsFilename), hosts+"\n")
		inv, err := Load(root)
		if err != nil || inv.HostsBroken == "" {
			t.Fatalf("null record accepted: %s (%v)", hosts, err)
		}
	}
	for _, address := range []string{"alice@host-a.example.net", "host-a.example.net?x", "host-a.example.net\r", "https://host-a.example.net", "host-a.example.net:443", "fe80::1%lan"} {
		h := Host{Endpoints: map[string]Endpoint{"home": {Address: address}}}
		if _, err := h.EndpointAddress("home"); err == nil {
			t.Errorf("invalid endpoint accepted: %q", address)
		}
	}
}

func TestHostAliasesAndOldShapesRejected(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, HostsFilename), `hosts:
  host-a:
    endpoints: &targets
      home: {address: host-a.home.test}
  host-b:
    endpoints: *targets
`)
	inv, err := Load(root)
	if err != nil || inv.HostsBroken != "" {
		t.Fatalf("aliases: %v", err)
	}
	if !reflect.DeepEqual(inv.Hosts["host-a"].Endpoints, inv.Hosts["host-b"].Endpoints) {
		t.Fatal("alias lost endpoints")
	}
	for _, body := range []string{"network: home\n    address: 10.0.0.10", "names: []", "mac: '02:00:00:00:00:11'"} {
		writeFile(t, filepath.Join(root, HostsFilename), "hosts:\n  host-a:\n    "+body+"\n")
		inv, err = Load(root)
		if err != nil || inv.HostsBroken == "" {
			t.Fatal("removed host fields accepted")
		}
	}
}

func TestLoadHostInterfacesAndResolution(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, HostsFilename), `hosts:
  host-a:
    interfaces:
      wired:
        mac: "02-00-00-00-00-11"
        addresses:
          primary: {network: home, ip: 10.0.0.10}
          secondary: {network: home, ip: 10.0.0.12}
      wifi:
        mac: "02:00:00:00:00:12"
        addresses:
          primary: {network: home, ip: 10.0.0.11}
    endpoints:
      home: {from: {interface: wired, address: secondary}}
      internet: {address: host-a.example.net}
    reaches: [backend]
  host-b:
    endpoints:
      internet: {address: host-b.example.net}
`)
	writeFile(t, filepath.Join(root, NodesDir, "node-1.yaml"), "id: node-1\nhost: host-a\n")
	inv, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if inv.HostsBroken != "" || len(inv.HostProblems) != 0 {
		t.Fatalf("host errors: %s %v", inv.HostsBroken, inv.HostProblems)
	}
	if got := inv.Hosts["host-a"].Interfaces["wired"].MAC; got != "02:00:00:00:00:11" {
		t.Fatalf("normalized MAC = %q", got)
	}
	if got := inv.Nodes[0].Networks; !reflect.DeepEqual(got, Networks{"home": "10.0.0.12", "internet": "host-a.example.net"}) {
		t.Fatalf("effective addresses = %v", got)
	}
	if !reflect.DeepEqual(inv.Nodes[0].Reaches, []string{"backend"}) {
		t.Fatalf("reaches = %v", inv.Nodes[0].Reaches)
	}
	if len(inv.NetworkMembers()) != 3 {
		t.Fatalf("address owners = %+v", inv.NetworkMembers())
	}
	inv.Nodes = append(inv.Nodes, Node{ID: "node-2", Host: "host-a"})
	inv.ResolveHosts()
	if len(inv.HostProblems) != 1 {
		t.Fatalf("duplicate bindings: %v", inv.HostProblems)
	}
	h := inv.Hosts["host-a"]
	h.Endpoints["home"] = Endpoint{From: &AddressRef{Interface: "wired", Address: "missing"}}
	inv.Hosts["host-a"] = h
	inv.ResolveHosts()
	for _, n := range inv.Nodes {
		if len(n.Networks) != 0 || len(n.Reaches) != 0 {
			t.Fatalf("broken host exposes stale values: %+v", n)
		}
	}
}

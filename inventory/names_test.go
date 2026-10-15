package inventory

import (
	"path/filepath"
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestLoadNamesOptionalAndStrict(t *testing.T) {
	for _, tc := range []struct {
		name, content string
		broken        bool
	}{
		{name: "missing"},
		{name: "valid", content: "names: {service.example.com: host-a.dev.example.com}\n"},
		{name: "malformed", content: "names: [", broken: true},
		{name: "unknown field", content: "aliases: {}\n", broken: true},
		{name: "sequence", content: "names: [service.example.com]\n", broken: true},
		{name: "nested target", content: "names: {service.example.com: {target: host-a.dev.example.com}}\n", broken: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if tc.content != "" {
				writeFile(t, filepath.Join(root, NamesFilename), tc.content)
			}
			got, err := Load(root)
			if err != nil {
				t.Fatal(err)
			}
			if (got.NamesBroken != "") != tc.broken {
				t.Fatalf("NamesBroken = %q, want broken %v", got.NamesBroken, tc.broken)
			}
			if tc.name == "valid" && got.Names["service.example.com"] != "host-a.dev.example.com" {
				t.Fatalf("Names = %#v", got.Names)
			}
			if tc.name == "missing" && len(got.Names) != 0 {
				t.Fatalf("Names = %#v", got.Names)
			}
		})
	}
}

func TestLoadHostNamesRejectMalformedReferences(t *testing.T) {
	for _, refs := range []string{"[null]", "[{interface: en, address: home-v4, unknown: true}]", "{interface: en, address: home-v4}", "[en]"} {
		t.Run(refs, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, filepath.Join(root, HostsFilename), "hosts:\n  host-a:\n    names:\n      host-a.dev.example.com: "+refs+"\n")
			got, err := Load(root)
			if err != nil {
				t.Fatal(err)
			}
			if got.HostsBroken == "" {
				t.Fatal("expected malformed host references to set HostsBroken")
			}
		})
	}
}

func TestLoadHostNamesTwoNetworksRoundTrip(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, HostsFilename), `hosts:
  host-a:
    names:
      host-a.dev.example.com:
        - {interface: en, address: home-v4}
        - {interface: en2, address: lab-v4}
    interfaces:
      en:
        addresses:
          home-v4: {network: home, ip: 10.0.0.10, names: [host-a-en.dev.example.com]}
      en2:
        addresses:
          lab-v4: {network: lab, ip: 10.1.0.10, names: [host-a-en2.dev.example.com]}
`)
	got, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if got.HostsBroken != "" {
		t.Fatal(got.HostsBroken)
	}
	want := []NameRef{{Interface: "en", Address: "home-v4"}, {Interface: "en2", Address: "lab-v4"}}
	if !reflect.DeepEqual(got.Hosts["host-a"].Names["host-a.dev.example.com"], want) {
		t.Fatalf("Names = %#v", got.Hosts["host-a"].Names)
	}
	data, err := yaml.Marshal(got.Hosts["host-a"])
	if err != nil {
		t.Fatal(err)
	}
	var roundtrip Host
	if err := yaml.Unmarshal(data, &roundtrip); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(roundtrip, got.Hosts["host-a"]) {
		t.Fatalf("roundtrip = %#v", roundtrip)
	}
}

func TestLoadEndpointNameReference(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, HostsFilename), "hosts:\n  host-a:\n    names:\n      host-a.example.test: [{endpoint: internet}]\n    endpoints:\n      internet: {address: origin.example.net}\n")
	inv, err := Load(root)
	if err != nil || inv.HostsBroken != "" {
		t.Fatal(err, inv.HostsBroken)
	}
	ref := inv.Hosts["host-a"].Names["host-a.example.test"][0]
	if ref.Endpoint != "internet" || ref.Interface != "" || ref.Address != "" {
		t.Fatal(ref)
	}
}

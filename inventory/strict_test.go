package inventory

import (
	"errors"
	"io"
	"path/filepath"
	"testing"
)

func TestHostMappingChecksResolveRootMerges(t *testing.T) {
	for _, tc := range []struct {
		name, data string
		broken     bool
	}{
		{"merged null host", "<<: {hosts: {host-a: null}}\n", true},
		{"merged null interface", "<<: {hosts: {host-a: {interfaces: {en: null}}}}\n", true},
		{"merged null address", "<<: {hosts: {host-a: {interfaces: {en: {addresses: {main: null}}}}}}\n", true},
		{"explicit override", "<<: {hosts: {host-a: null}}\nhosts: {host-a: {}}\n", false},
		{"earlier merge wins", "<<: [{hosts: {host-a: {}}}, {hosts: {host-a: null}}]\n", false},
		{"later merge loses", "<<: [{hosts: {host-a: null}}, {hosts: {host-a: {}}}]\n", true},
		{"nested explicit override", "hosts:\n  host-a:\n    <<: {interfaces: {en: null}}\n    interfaces: {en: {}}\n", false},
		{"root cycle", "&root {<<: *root}\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, filepath.Join(root, HostsFilename), tc.data)
			inv, err := Load(root)
			if err != nil {
				t.Fatal(err)
			}
			if (inv.HostsBroken != "") != tc.broken {
				t.Fatalf("HostsBroken=%q, want broken=%t", inv.HostsBroken, tc.broken)
			}
			if !tc.broken {
				if _, ok := inv.Hosts["host-a"]; !ok {
					t.Fatal("effective merged host missing")
				}
			}
		})
	}
}

func TestStrictInventoryRequiresOneMappingDocument(t *testing.T) {
	for _, tc := range []struct{ name, data string }{
		{"second mapping", "names: {}\n---\nnames: {alias.example.test: missing.example.test}\n"},
		{"second null", "names: {}\n---\nnull\n"},
		{"trailing empty document", "names: {}\n---\n"},
		{"null", "null\n"},
		{"sequence", "[]\n"},
		{"scalar", "example\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeFile(t, filepath.Join(root, NamesFilename), tc.data)
			inv, err := Load(root)
			if err != nil {
				t.Fatal(err)
			}
			if inv.NamesBroken == "" {
				t.Fatal("invalid names document accepted")
			}
		})
	}
	root := t.TempDir()
	writeFile(t, filepath.Join(root, HostsFilename), "hosts: {}\n---\nhosts: {host-a: null}\n")
	inv, err := Load(root)
	if err != nil || inv.HostsBroken == "" {
		t.Fatalf("multiple host documents accepted: %v %#v", err, inv)
	}
	var doc map[string]any
	if err := decodeStrict([]byte("# empty\n"), &doc); !errors.Is(err, io.EOF) {
		t.Fatalf("empty document error=%v", err)
	}
	if err := decodeStrict([]byte("names: {}\n# trailing comment\n"), &doc); err != nil {
		t.Fatal(err)
	}
}

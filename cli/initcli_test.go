package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/d0u9/rhumb/confgen"
	"github.com/d0u9/rhumb/derive"
	"github.com/d0u9/rhumb/inventory"
	"github.com/d0u9/rhumb/secretstore"
	"github.com/d0u9/rhumb/validate"
)

// TestInit_ScaffoldLoadsAndValidatesClean covers what the scaffold is for: a
// root that is a valid, empty inventory from the moment it is written, so a
// reader adds a machine to something that works rather than to something
// that first has to be made to work.
func TestInit_ScaffoldLoadsAndValidatesClean(t *testing.T) {
	root := filepath.Join(t.TempDir(), "confgen")
	secrets := filepath.Join(t.TempDir(), "secrets")

	var out bytes.Buffer
	flags := map[string]string{"secrets": secrets, "gitignore": "true"}
	if err := Init(nil, &out, []string{root}, flags, Settings{}); err != nil {
		t.Fatalf("Init: %v", err)
	}

	inv, err := inventory.Load(root)
	if err != nil {
		t.Fatalf("inventory.Load: %v", err)
	}
	if inv.NetworksBroken != "" || inv.UsersBroken != "" || inv.RoutesBroken != "" {
		t.Fatalf("broken: networks %q users %q routes %q", inv.NetworksBroken, inv.UsersBroken, inv.RoutesBroken)
	}
	confRoot, err := confgen.Load(root)
	if err != nil {
		t.Fatalf("confgen.Load: %v", err)
	}
	manifests := map[string]confgen.Manifest{}
	for _, svc := range confRoot.Services {
		manifests[svc.Name] = svc.Manifest
	}
	exportDefs := map[string]confgen.Export{}
	for _, def := range confRoot.Exports {
		exportDefs[def.Name] = def.Export
	}
	model, err := derive.Derive(inv, manifests)
	if err != nil {
		t.Fatalf("derive.Derive: %v", err)
	}
	for _, issue := range validate.Validate(inv, manifests, exportDefs, model, nil) {
		t.Errorf("validate: %s", issue.Message)
	}

	// The two files written into the secrets root are not credentials, so
	// the store is in step rather than holding two orphans.
	res, err := secretstore.Sync(secrets, secretstore.ImpliedPaths(inv, manifests, model))
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(res.Missing) != 0 || len(res.Orphaned) != 0 {
		t.Fatalf("Sync = %+v, want an empty store in step", res)
	}
	for _, name := range []string{"README.md", ".gitignore"} {
		if _, err := os.Stat(filepath.Join(secrets, name)); err != nil {
			t.Errorf("secrets root is missing %s: %v", name, err)
		}
	}
}

// TestInit_NeverOverwrites covers the rule that makes it safe to run twice,
// and safe to run on a root someone has already started by hand.
func TestInit_NeverOverwrites(t *testing.T) {
	root := filepath.Join(t.TempDir(), "confgen")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	mine := filepath.Join(root, inventory.UsersFilename)
	if err := os.WriteFile(mine, []byte("users: {dana: {access: []}}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := Init(nil, &out, []string{root}, map[string]string{}, Settings{}); err != nil {
		t.Fatalf("Init: %v", err)
	}

	kept, err := os.ReadFile(mine)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(kept), "dana") {
		t.Fatalf("users.yaml = %q, want the file that was already there", kept)
	}
	if !strings.Contains(out.String(), "kept   "+mine) {
		t.Fatalf("output = %q, want it to say what it left alone", out.String())
	}
}

// TestInit_NoRootIsAnErrorNamingTheKey covers the one way it can be called
// with nothing to write to.
func TestInit_NoRootIsAnErrorNamingTheKey(t *testing.T) {
	err := Init(nil, &bytes.Buffer{}, nil, map[string]string{}, Settings{})
	if err == nil || !strings.Contains(err.Error(), "generator root") {
		t.Fatalf("err = %v, want it to name the generator root", err)
	}
}

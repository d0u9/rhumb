package validate

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/d0u9/rhumb/confgen"
	"github.com/d0u9/rhumb/derive"
	"github.com/d0u9/rhumb/inventory"
)

// TestExampleInventoryLoadsDerivesAndValidates is
// docs/apps/conf/inventory.md milestone 11: a complete inventory under
// examples/conf/, loaded by the test that loads every example — until this
// exists, nothing has checked that the configuration the docs describe can
// actually be written. It runs the whole pipeline confgen, inventory and
// derive feed validate from, over the real example tree, and requires the
// result to be entirely clean: no broken file, no derive error, no issue.
func TestExampleInventoryLoadsDerivesAndValidates(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("no caller information")
	}
	root := filepath.Join(filepath.Dir(file), "..", "examples", "conf")
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("examples/conf: %v", err)
	}

	inv, err := inventory.Load(root)
	if err != nil {
		t.Fatalf("inventory.Load: %v", err)
	}
	for _, n := range inv.Nodes {
		if n.Broken != "" {
			t.Errorf("node %s: %s", n.Path, n.Broken)
		}
	}
	if inv.UsersBroken != "" {
		t.Errorf("users.yaml: %s", inv.UsersBroken)
	}
	if inv.RoutesBroken != "" {
		t.Errorf("routes.yaml: %s", inv.RoutesBroken)
	}
	if inv.NetworksBroken != "" {
		t.Errorf("networks.yaml: %s", inv.NetworksBroken)
	}
	if len(inv.Nodes) == 0 {
		t.Fatal("no nodes found")
	}

	confRoot, err := confgen.Load(root)
	if err != nil {
		t.Fatalf("confgen.Load: %v", err)
	}
	exportDefs := map[string]confgen.Export{}
	for _, def := range confRoot.Exports {
		if def.Broken != "" {
			t.Errorf("export %s: %s", def.Name, def.Broken)
			continue
		}
		exportDefs[def.Name] = def.Export
	}
	manifests := map[string]confgen.Manifest{}
	for _, svc := range confRoot.Services {
		if svc.Broken != "" {
			t.Errorf("service %s: %s", svc.Name, svc.Broken)
			continue
		}
		manifests[svc.Name] = svc.Manifest
	}
	if len(manifests) == 0 {
		t.Fatal("no services found")
	}

	model, err := derive.Derive(inv, manifests)
	if err != nil {
		t.Fatalf("derive.Derive: %v", err)
	}
	if len(model.ExportInstances) == 0 {
		t.Fatal("nothing derived — the example's routes and access grant nothing")
	}

	issues := Validate(inv, manifests, exportDefs, model, nil)
	for _, issue := range issues {
		t.Errorf("validate: %s", issue.Message)
	}
}

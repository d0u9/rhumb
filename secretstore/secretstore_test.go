package secretstore

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/d0u9/rhumb/confgen"
	"github.com/d0u9/rhumb/derive"
	"github.com/d0u9/rhumb/inventory"
)

func testInventory() (*inventory.Root, map[string]confgen.Manifest) {
	inv := &inventory.Root{
		Nodes: []inventory.Node{
			{
				ID:       "srv",
				Networks: inventory.Networks{"internet": "203.0.113.10"},
				Instances: []inventory.Instance{
					{ID: "srv/ss-srv", Service: "ssserver", Ports: inventory.PortsOf(map[string]int{"main": 38250})},
				},
			},
			{ID: "laptop", Owner: "dana"},
		},
		Users: map[string]inventory.User{
			"dana": {Access: []string{"sea"}},
		},
		Routes:    map[string]inventory.Route{"sea": {Hops: []string{"srv/ss-srv:main"}}},
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
	return inv, manifests
}

func mustDerive(t *testing.T, inv *inventory.Root, manifests map[string]confgen.Manifest) *derive.Model {
	t.Helper()
	m, err := derive.Derive(inv, manifests)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	return m
}

func TestImpliedPaths_OnePerPrincipalGrant(t *testing.T) {
	inv, manifests := testInventory()
	model := mustDerive(t, inv, manifests)

	paths := ImpliedPaths(inv, manifests, model)
	if len(paths) != 1 {
		t.Fatalf("ImpliedPaths = %+v, want 1", paths)
	}
	p := paths[0]
	if p.Instance != "srv/ss-srv" || p.Port != "main" || p.Group != "dana" || p.Name != "default" {
		t.Fatalf("path = %+v, want ss-srv/main/dana/default", p)
	}
	if p.String() != filepath.Join("srv", "ss-srv", "main", "dana", "default") {
		t.Fatalf("String() = %q", p.String())
	}
}

func TestSync_MissingAndGenerate(t *testing.T) {
	inv, manifests := testInventory()
	model := mustDerive(t, inv, manifests)
	implied := ImpliedPaths(inv, manifests, model)

	root := t.TempDir()
	res, err := Sync(root, implied)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(res.Missing) != 1 || len(res.Orphaned) != 0 {
		t.Fatalf("Sync = %+v, want one missing, none orphaned", res)
	}

	if err := Generate(root, res.Missing, inv, manifests); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	full := filepath.Join(root, "srv", "ss-srv", "main", "dana", "default")
	data, err := os.ReadFile(full)
	if err != nil {
		t.Fatalf("reading generated secret: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("generated secret is empty")
	}

	// Generate refuses to overwrite.
	if err := Generate(root, res.Missing, inv, manifests); err == nil {
		t.Fatal("Generate: want an error the second time, the file already exists")
	}

	// A second Sync now finds nothing missing and nothing orphaned.
	res2, err := Sync(root, implied)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(res2.Missing) != 0 || len(res2.Orphaned) != 0 {
		t.Fatalf("Sync = %+v, want none missing, none orphaned", res2)
	}
}

// TestSync_RenameProducesTheHint is the case that matters most: renaming a
// node produces one new implied path and leaves one orphaned file, and sync
// must say so rather than silently generating a new random value on one
// side of what was really one credential.
func TestSync_RenameProducesTheHint(t *testing.T) {
	inv, manifests := testInventory()
	model := mustDerive(t, inv, manifests)
	implied := ImpliedPaths(inv, manifests, model)

	root := t.TempDir()
	if err := Generate(root, implied, inv, manifests); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	oldValue, err := os.ReadFile(filepath.Join(root, "srv", "ss-srv", "main", "dana", "default"))
	if err != nil {
		t.Fatal(err)
	}

	// Rename the credential in the inventory: default -> work. The old
	// secret file is still on disk under the old name.
	user := inv.Users["dana"]
	user.Credentials = map[string]inventory.Credential{"work": {Note: "the office laptop"}}
	inv.Users["dana"] = user
	inv.Nodes[1].Credential = "work"
	model2 := mustDerive(t, inv, manifests)
	implied2 := ImpliedPaths(inv, manifests, model2)

	res, err := Sync(root, implied2)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(res.Missing) != 1 || len(res.Orphaned) != 1 {
		t.Fatalf("Sync = %+v, want exactly one missing and one orphaned", res)
	}
	if !res.RenameHint {
		t.Fatal("RenameHint = false, want true when counts match")
	}
	if res.Missing[0].Name != "work" || res.Orphaned[0].Name != "default" {
		t.Fatalf("Sync = %+v, want missing work and orphaned default", res)
	}

	// mv performs exactly that move, and a value carries over untouched.
	if err := Mv(root, res.Orphaned[0].String(), res.Missing[0].String()); err != nil {
		t.Fatalf("Mv: %v", err)
	}
	res3, err := Sync(root, implied2)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(res3.Missing) != 0 || len(res3.Orphaned) != 0 {
		t.Fatalf("Sync after Mv = %+v, want none missing, none orphaned", res3)
	}
	newValue, err := os.ReadFile(filepath.Join(root, "srv", "ss-srv", "main", "dana", "work"))
	if err != nil {
		t.Fatal(err)
	}
	if string(newValue) != string(oldValue) {
		t.Fatal("Mv changed the secret's value")
	}
}

// TestImpliedPaths_Own covers a role's own list: each name is implied once
// per instance of that role, regardless of how many ports or grants the
// instance has.
func TestImpliedPaths_Own(t *testing.T) {
	inv, manifests := testInventory()
	role := manifests["ssserver"]
	role.Self = confgen.SelfDecls{"psk": {}}
	manifests["ssserver"] = role
	model := mustDerive(t, inv, manifests)

	paths := ImpliedPaths(inv, manifests, model)
	var own *Path
	for i := range paths {
		if paths[i].Port == SelfPort {
			own = &paths[i]
		}
	}
	if own == nil {
		t.Fatalf("ImpliedPaths = %+v, want an own/psk path", paths)
	}
	if own.Instance != "srv/ss-srv" || own.Name != "psk" || own.Group != "" {
		t.Fatalf("own path = %+v, want ss-srv/self/psk", own)
	}
	if own.String() != filepath.Join("srv", "ss-srv", "self", "psk") {
		t.Fatalf("String() = %q", own.String())
	}
}

// TestSync_OwnGeneratedAndNotOrphaned pins the two directions a listed own
// secret must get right: Generate can create the file Sync reports missing,
// and once it exists Sync does not report it orphaned.
func TestSync_OwnGeneratedAndNotOrphaned(t *testing.T) {
	inv, manifests := testInventory()
	role := manifests["ssserver"]
	role.Self = confgen.SelfDecls{"psk": {}}
	manifests["ssserver"] = role
	model := mustDerive(t, inv, manifests)
	implied := ImpliedPaths(inv, manifests, model)

	root := t.TempDir()
	res, err := Sync(root, implied)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	foundMissing := false
	for _, p := range res.Missing {
		if p.Port == SelfPort {
			foundMissing = true
		}
	}
	if !foundMissing {
		t.Fatalf("Sync.Missing = %+v, want ss-srv/self/psk among them", res.Missing)
	}

	if err := Generate(root, res.Missing, inv, manifests); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	res2, err := Sync(root, implied)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(res2.Missing) != 0 || len(res2.Orphaned) != 0 {
		t.Fatalf("Sync after Generate = %+v, want none missing, none orphaned", res2)
	}
}

// TestSync_OwnDroppedFromListIsOrphaned covers a name leaving a role's own
// list. The file stays on disk — Sync never deletes — and is reported, which
// is the whole point of the list being the single place own secrets are
// named: without it, a credential nothing generates any more is
// indistinguishable from one nothing ever generated.
func TestSync_OwnDroppedFromListIsOrphaned(t *testing.T) {
	inv, manifests := testInventory()
	role := manifests["ssserver"]
	role.Self = confgen.SelfDecls{"psk": {}}
	manifests["ssserver"] = role
	model := mustDerive(t, inv, manifests)
	implied := ImpliedPaths(inv, manifests, model)

	root := t.TempDir()
	if err := Generate(root, implied, inv, manifests); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	role.Self = nil
	manifests["ssserver"] = role
	model2 := mustDerive(t, inv, manifests)
	implied2 := ImpliedPaths(inv, manifests, model2)

	res, err := Sync(root, implied2)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(res.Orphaned) != 1 || res.Orphaned[0].String() != filepath.Join("srv", "ss-srv", "self", "psk") {
		t.Fatalf("Sync.Orphaned = %+v, want ss-srv/self/psk", res.Orphaned)
	}
	if _, err := os.Stat(filepath.Join(root, "srv", "ss-srv", "self", "psk")); err != nil {
		t.Fatalf("Sync deleted the orphaned file: %v", err)
	}
}

// TestSync_UnlistedOwnPathIsOrphaned covers an own/ file no role's own list
// accounts for. A template may still read it, and Sync will never regenerate
// it, so reporting it is what tells a reader either to list the name or to
// remove the file.
func TestSync_UnlistedOwnPathIsOrphaned(t *testing.T) {
	inv, manifests := testInventory()
	model := mustDerive(t, inv, manifests)
	implied := ImpliedPaths(inv, manifests, model)

	root := t.TempDir()
	if err := Generate(root, implied, inv, manifests); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	ownPath := filepath.Join(root, "srv", "ss-srv", SelfPort, "auth_password")
	if err := os.MkdirAll(filepath.Dir(ownPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ownPath, []byte("hunter2"), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := Sync(root, implied)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(res.Orphaned) != 1 || res.Orphaned[0].String() != filepath.Join("srv", "ss-srv", "self", "auth_password") {
		t.Fatalf("Sync.Orphaned = %+v, want ss-srv/self/auth_password", res.Orphaned)
	}
}

func TestSync_PreviousFilesAreNeverOrphaned(t *testing.T) {
	inv, manifests := testInventory()
	model := mustDerive(t, inv, manifests)
	implied := ImpliedPaths(inv, manifests, model)

	root := t.TempDir()
	if err := Generate(root, implied, inv, manifests); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	prev := filepath.Join(root, "srv", "ss-srv", "main", "dana", "default"+PreviousSuffix)
	if err := os.WriteFile(prev, []byte("old-value"), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := Sync(root, implied)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(res.Orphaned) != 0 {
		t.Fatalf("Sync = %+v, want a .previous file never reported orphaned", res)
	}
}

func TestReadPrevious(t *testing.T) {
	inv, manifests := testInventory()
	model := mustDerive(t, inv, manifests)
	implied := ImpliedPaths(inv, manifests, model)
	p := implied[0]

	root := t.TempDir()
	if err := Generate(root, implied, inv, manifests); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if _, ok, err := ReadPrevious(root, p); err != nil || ok {
		t.Fatalf("ReadPrevious with no .previous file = %v, %v, want false, nil", ok, err)
	}

	if err := os.WriteFile(filepath.Join(root, p.String())+PreviousSuffix, []byte("old-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	value, ok, err := ReadPrevious(root, p)
	if err != nil {
		t.Fatalf("ReadPrevious: %v", err)
	}
	if !ok || value != "old-value" {
		t.Fatalf("ReadPrevious = %q, %v, want %q, true", value, ok, "old-value")
	}
}

func TestPreviousModTimes(t *testing.T) {
	inv, manifests := testInventory()
	model := mustDerive(t, inv, manifests)
	implied := ImpliedPaths(inv, manifests, model)
	p := implied[0]

	root := t.TempDir()
	if err := Generate(root, implied, inv, manifests); err != nil {
		t.Fatalf("Generate: %v", err)
	}

	previousPath := filepath.Join(root, p.String()) + PreviousSuffix
	if err := os.WriteFile(previousPath, []byte("old-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().Add(-9 * 24 * time.Hour)
	if err := os.Chtimes(previousPath, stamp, stamp); err != nil {
		t.Fatal(err)
	}

	got, err := PreviousModTimes(root)
	if err != nil {
		t.Fatalf("PreviousModTimes: %v", err)
	}
	mtime, ok := got[p.String()]
	if !ok {
		t.Fatalf("PreviousModTimes = %v, want an entry for %s", got, p)
	}
	if !mtime.Equal(stamp) {
		t.Fatalf("PreviousModTimes[%s] = %v, want %v", p, mtime, stamp)
	}
}

// TestSync_NonCredentialFilesAreNotOrphans covers the two things under a
// secrets root that are never credentials: a file beside the instance
// directories, and a dotfile anywhere. Reporting them would be noise nobody
// can act on, and `rhumb init` writes both.
func TestSync_NonCredentialFilesAreNotOrphans(t *testing.T) {
	inv, manifests := testInventory()
	model := mustDerive(t, inv, manifests)
	implied := ImpliedPaths(inv, manifests, model)

	root := t.TempDir()
	if err := Generate(root, implied, inv, manifests); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	write := func(parts ...string) {
		t.Helper()
		p := filepath.Join(append([]string{root}, parts...)...)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("README.md")
	write(".gitignore")
	write("srv", "ss-srv", ".DS_Store")

	res, err := Sync(root, implied)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(res.Orphaned) != 0 {
		t.Fatalf("Orphaned = %+v, want none of them reported", res.Orphaned)
	}

	// A stray file deeper in is still a credential nothing implies.
	write("srv", "ss-srv", "main", "dana", "stray")
	res, err = Sync(root, implied)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(res.Orphaned) != 1 || res.Orphaned[0].Name != "stray" {
		t.Fatalf("Orphaned = %+v, want the stray credential reported", res.Orphaned)
	}
}

// TestImpliedPaths_InstanceNarrowsItsOwnSecrets covers the difference between
// a service's list and what one instance of it holds: a paste bin with no
// admin interface should not be handed the two passwords guarding one.
func TestImpliedPaths_InstanceNarrowsItsOwnSecrets(t *testing.T) {
	inv, manifests := testInventory()
	ss := manifests["ssserver"]
	ss.Self = confgen.SelfDecls{"psk": {}, "admin_password": {}}
	manifests["ssserver"] = ss

	// Unwritten: every name the service declares.
	all := selfNames(ImpliedPaths(inv, manifests, mustDerive(t, inv, manifests)))
	if len(all) != 2 {
		t.Fatalf("self paths = %v, want both of the service's own secrets", all)
	}

	// Narrowed to one.
	inv.Nodes[0].Instances[0].Self = map[string][]string{"psk": nil}
	one := selfNames(ImpliedPaths(inv, manifests, mustDerive(t, inv, manifests)))
	if len(one) != 1 || one[0] != "psk" {
		t.Fatalf("self paths = %v, want only the narrowed name", one)
	}

	// Written empty: none of them. That is a different statement from
	// saying nothing, so it is not read as "all of them".
	inv.Nodes[0].Instances[0].Self = map[string][]string{}
	none := selfNames(ImpliedPaths(inv, manifests, mustDerive(t, inv, manifests)))
	if len(none) != 0 {
		t.Fatalf("self paths = %v, want none", none)
	}
}

func selfNames(paths []Path) []string {
	var out []string
	for _, p := range paths {
		if p.Port == SelfPort {
			out = append(out, p.Name)
		}
	}
	sort.Strings(out)
	return out
}

// TestImpliedPaths_SetAndFields covers a name that is a family of values, a
// name made of several fields, and one that is both: the tree is a name, a
// key and a field deep, and nothing deeper.
func TestImpliedPaths_SetAndFields(t *testing.T) {
	inv, manifests := testInventory()
	ss := manifests["ssserver"]
	ss.Self = confgen.SelfDecls{
		"psk":     {Set: true},
		"tls":     {Fields: map[string]confgen.Secret{"cert": {}, "key": {}}},
		"account": {Set: true, Fields: map[string]confgen.Secret{"uuid": {}, "password": {}}},
	}
	manifests["ssserver"] = ss
	inst := &inv.Nodes[0].Instances[0]
	port := inst.Ports["main"]
	port.Self = []string{"psk.users"}
	inst.Ports["main"] = port
	inst.Self = map[string][]string{"psk": {"users", "relays"}, "account": {"main"}, "tls": nil}

	var got []string
	for _, p := range ImpliedPaths(inv, manifests, mustDerive(t, inv, manifests)) {
		if p.Port == SelfPort {
			got = append(got, filepath.ToSlash(p.String()))
		}
	}
	want := []string{
		"srv/ss-srv/self/account/main/password",
		"srv/ss-srv/self/account/main/uuid",
		"srv/ss-srv/self/psk/relays",
		"srv/ss-srv/self/psk/users",
		"srv/ss-srv/self/tls/cert",
		"srv/ss-srv/self/tls/key",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("self paths = %v, want %v", got, want)
	}
}

// TestGenerate_OpaqueIsNeverGenerated pins the one shape rhumb refuses to
// invent: a private key or a certificate is nothing a random string can
// stand in for, so its path stays missing until someone writes it.
func TestGenerate_OpaqueIsNeverGenerated(t *testing.T) {
	inv, manifests := testInventory()
	ss := manifests["ssserver"]
	ss.Self = confgen.SelfDecls{
		"psk":     {},
		"tls_key": {Secret: confgen.Secret{Kind: confgen.KindOpaque}},
	}
	manifests["ssserver"] = ss
	implied := ImpliedPaths(inv, manifests, mustDerive(t, inv, manifests))

	root := t.TempDir()
	if err := Generate(root, implied, inv, manifests); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "srv", "ss-srv", "self", "psk")); err != nil {
		t.Fatalf("psk was not generated: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "srv", "ss-srv", "self", "tls_key")); !os.IsNotExist(err) {
		t.Fatalf("tls_key = %v, want an opaque value left for someone to write", err)
	}
	res, err := Sync(root, implied)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if len(res.Missing) != 1 || res.Missing[0].Name != "tls_key" {
		t.Fatalf("Sync missing = %v, want the opaque value reported", res.Missing)
	}
}

// TestReadSelf_MirrorsTheTree covers the render context's self datasource:
// a single value reads as a string, and a set or a record reads as a map,
// so self.psk.users is written the way its path is.
func TestReadSelf_MirrorsTheTree(t *testing.T) {
	root := t.TempDir()
	writeSecretFile(t, root, "srv/ss-srv/self/tls_key", "pem")
	writeSecretFile(t, root, "srv/ss-srv/self/psk/users", "a")
	writeSecretFile(t, root, "srv/ss-srv/self/account/main/uuid", "u")

	self, err := ReadSelf(root, "srv/ss-srv")
	if err != nil {
		t.Fatalf("ReadSelf: %v", err)
	}
	if self["tls_key"] != "pem" {
		t.Fatalf("tls_key = %v, want the string", self["tls_key"])
	}
	psk, ok := self["psk"].(map[string]any)
	if !ok || psk["users"] != "a" {
		t.Fatalf("psk = %#v, want a map holding users", self["psk"])
	}
	account, ok := self["account"].(map[string]any)
	if !ok {
		t.Fatalf("account = %#v, want a map of keys", self["account"])
	}
	main, ok := account["main"].(map[string]any)
	if !ok || main["uuid"] != "u" {
		t.Fatalf("account.main = %#v, want a map of fields", account["main"])
	}
}

// writeSecretFile writes one credential file under root, creating its
// directories, for a test constructing a tree by hand.
func writeSecretFile(t *testing.T, root, rel, value string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A link port on a forwarding service authenticates by link.to, though the
// service's own auth is none; renaming the link moves nothing.
func TestImpliedPaths_LinkPort(t *testing.T) {
	root := filepath.Join("..", "docs", "fixtures", "reverse-exit", "conf")
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
		manifests[s.Name] = s.Manifest
	}
	paths := func() []string {
		model, err := derive.Derive(inv, manifests)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, p := range ImpliedPaths(inv, manifests, model) {
			out = append(out, p.String())
		}
		return out
	}
	want := []string{"home/ss-home/users/alice/default", "nce/relay-nce/agents/home-agent-home/default"}
	if got := paths(); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("paths = %v, want %v", got, want)
	}
	inv.Links["renamed"] = inv.Links["home-nce"]
	delete(inv.Links, "home-nce")
	if got := paths(); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("after rename, paths = %v, want %v", got, want)
	}
}

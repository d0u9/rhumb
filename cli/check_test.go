package cli

import (
	"bytes"
	"errors"
	"github.com/d0u9/rhumb/engine"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/d0u9/rhumb/secretstore"
)

func checkConfig(root, secrets string) Settings {
	return Settings{Root: root, Secrets: secrets}
}

// TestCheckReport_CleanInventory covers the ordinary case. A report that
// printed the whole inventory when nothing is wrong is a report nobody reads
// the top of, so success is one line and no error.
func TestCheckReport_CleanInventory(t *testing.T) {
	root, secrets := buildInspectRoot(t), t.TempDir()
	// buildInspectRoot carries a node file that will not parse, on purpose;
	// this case is the inventory without it.
	if err := os.Remove(filepath.Join(root, "nodes", "bad.yaml")); err != nil {
		t.Fatal(err)
	}
	// Generate everything the inventory implies, so the store is in step.
	if err := writeImplied(t, root, secrets); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := Check(&out, checkConfig(root, secrets)); err != nil {
		t.Fatalf("Check: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "no problem found") {
		t.Fatalf("report = %q, want it to say so", out.String())
	}
	if strings.Count(out.String(), "\n") != 1 {
		t.Fatalf("report = %q, want one line when there is nothing to say", out.String())
	}
}

// TestCheckReport_NamesEveryKindOfProblem covers the report's reason to
// exist: one command that finds what four tabs would have shown. Each of
// these is silent in a different way — a node file that will not parse drops
// its instances, a route that does not exist renders nothing, a missing
// secret fails mid-export, and an orphaned one is never regenerated and
// never deleted.
func TestCheckReport_NamesEveryKindOfProblem(t *testing.T) {
	root, secrets := buildInspectRoot(t), t.TempDir()
	if err := writeImplied(t, root, secrets); err != nil {
		t.Fatal(err)
	}

	// A node file that will not parse is already in buildInspectRoot.
	// Add an access list naming a route that does not exist.
	users := filepath.Join(root, "users.yaml")
	body, err := os.ReadFile(users)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(users, []byte(strings.Replace(string(body), "access: [sea]", "access: [sea, nosuchroute]", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	// A credential the inventory implies and the store does not hold.
	if err := os.Remove(filepath.Join(secrets, "srv/ss-srv", "self", "psk", "main")); err != nil {
		t.Fatal(err)
	}
	// A file nothing implies.
	writeSecret(t, secrets, "srv/ss-srv/main/user/nobody", "orphan")
	// A rotation nobody finished.
	stale := writeSecret(t, secrets, "srv/ss-srv/main/user/yak.previous", "old")
	old := time.Now().Add(-30 * 24 * time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	err = Check(&out, checkConfig(root, secrets))

	var problems ErrProblems
	if !errors.As(err, &problems) {
		t.Fatalf("Check err = %v, want ErrProblems so the shell exits non-zero", err)
	}
	report := out.String()
	for _, want := range []string{
		"bad.yaml",
		"nosuchroute",
		"secret missing: srv/ss-srv/self/psk",
		"secret orphaned: srv/ss-srv/main/user/nobody",
		"days old",
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("report = %q, want it to name %q", report, want)
		}
	}
	// The rotation leftover is validate's rule and the store's comparison
	// both; it must be reported once.
	if strings.Count(report, ".previous") != 1 {
		t.Fatalf("report = %q, want the stale rotation named once", report)
	}
	// The count is the error's message, so printing it in the report too
	// would put the same line on stdout and on stderr.
	if strings.Contains(report, "problems found") {
		t.Fatalf("report = %q, want the count left to the error", report)
	}
}

// TestCheckReport_WithoutASecretsStore covers the secrets root unset. Every path
// the inventory implies is still known and none can be checked, which is
// worth one line rather than a clean bill of health.
func TestCheckReport_WithoutASecretsStore(t *testing.T) {
	var out bytes.Buffer
	err := Check(&out, checkConfig(buildInspectRoot(t), ""))
	if err == nil {
		t.Fatalf("report = %q, want an unset the secrets root to count as a problem", out.String())
	}
	if !strings.Contains(out.String(), "no secrets root given") {
		t.Fatalf("report = %q, want it to name what is unset", out.String())
	}
}

// TestCheckReport_WithoutARoot covers the generator root unset, which is not a
// problem with an inventory but the absence of one to check.
func TestCheckReport_WithoutARoot(t *testing.T) {
	var out bytes.Buffer
	err := Check(&out, checkConfig("", ""))
	if err == nil || !strings.Contains(err.Error(), "generator root") {
		t.Fatalf("err = %v, want it to name the generator root", err)
	}
}

// TestTargetReport lists what the root holds without rendering anything,
// which is what docs/export.md#targets-and-selectors asks of it.
func TestTargetReport(t *testing.T) {
	var out bytes.Buffer
	if err := Targets(&out, checkConfig(buildInspectRoot(t), "")); err != nil {
		t.Fatalf("Targets: %v", err)
	}
	report := out.String()
	for _, want := range []string{"srv", "srv/ss-srv", "ssserver", "yak (unmanaged user)", "broken"} {
		if !strings.Contains(report, want) {
			t.Fatalf("report = %q, want it to name %q", report, want)
		}
	}
}

func writeSecret(t *testing.T, root, rel, value string) string {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// writeImplied fills a store with every path root's inventory implies, so a
// test starts from one that is in step.
func writeImplied(t *testing.T, root, secrets string) error {
	t.Helper()
	l, err := engine.Load(root)
	if err != nil {
		return err
	}
	for _, p := range secretstore.ImpliedPaths(l.Inv, l.Manifests, l.Derived) {
		writeSecret(t, secrets, p.String(), "value")
	}
	return nil
}

// TestCheckReport_AnOpaqueSecretIsNotSyncsToGenerate: a value nothing here
// invents — a private key, a certificate chain, a bcrypt hash — stays
// missing whatever `secret sync` does, so the report must not send someone
// to a command that will list it and write nothing.
func TestCheckReport_AnOpaqueSecretIsNotSyncsToGenerate(t *testing.T) {
	root, secrets := buildInspectRoot(t), t.TempDir()
	manifest := filepath.Join(root, "services", "ssserver", "confgen.yaml")
	body, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	// A second own secret beside psk, this one opaque.
	if err := os.WriteFile(manifest, []byte(strings.Replace(string(body),
		"  psk: {set: true}\n",
		"  psk: {set: true}\n  tls_key: {kind: opaque}\n", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeImplied(t, root, secrets); err != nil {
		t.Fatal(err)
	}
	// writeImplied writes every implied path, opaque or not. Take the
	// opaque one back out: what it stands for is a value only a person can
	// put there, and the report about it is this test's subject.
	if err := os.Remove(filepath.Join(secrets, "srv/ss-srv", "self", "tls_key")); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := Check(&out, checkConfig(root, secrets)); err == nil {
		t.Fatalf("report = %q, want a missing secret to count as a problem", out.String())
	}
	report := out.String()
	if !strings.Contains(report, "secret missing: srv/ss-srv/self/tls_key — an opaque value, which nothing generates: write the file yourself") {
		t.Fatalf("report = %q, want the opaque path reported as one nobody generates", report)
	}
	if strings.Contains(report, "tls_key — run secret sync") {
		t.Fatalf("report = %q, want it not to send someone to sync for an opaque value", report)
	}
}

package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// secretRoot is a generator root with one per-principal service, one person
// granted a route into it, and a secrets root of its own.
func secretRoot(t *testing.T) (root, secretsDir string) {
	t.Helper()
	return buildSharedRoot(t)
}

// A path the inventory implies and the tree does not hold is generated, and
// the report says so before writing anything.
func TestSecretSync_GeneratesWhatIsMissing(t *testing.T) {
	root, _ := secretRoot(t)
	secretsDir := t.TempDir()

	var out bytes.Buffer
	err := Secret(strings.NewReader(""), &out, []string{"sync"}, map[string]string{"yes": "true"}, Settings{Root: root, Secrets: secretsDir})
	if err != nil {
		t.Fatalf("Secret: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "to generate under") {
		t.Fatalf("report = %q, want it to name what it is about to write", got)
	}
	if !strings.Contains(got, "generated") {
		t.Fatalf("report = %q, want it to say what it wrote", got)
	}

	// Running it again writes nothing: every implied path is on disk.
	out.Reset()
	if err := Secret(strings.NewReader(""), &out, []string{"sync"}, map[string]string{"yes": "true"}, Settings{Root: root, Secrets: secretsDir}); err != nil {
		t.Fatalf("second Secret: %v", err)
	}
	if !strings.Contains(out.String(), "nothing to generate") {
		t.Fatalf("second report = %q, want it to find the tree in step", out.String())
	}
}

// Sync never deletes: a file nothing implies is reported and left where it
// is, because sync cannot tell a rename from a removal.
func TestSecretSync_ReportsOrphansAndLeavesThem(t *testing.T) {
	root, secretsDir := secretRoot(t)
	orphan := filepath.Join(secretsDir, "ss-gone", "main", "dana", "default")
	if err := os.MkdirAll(filepath.Dir(orphan), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(orphan, []byte("value\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := Secret(strings.NewReader(""), &out, []string{"sync"}, map[string]string{"yes": "true"}, Settings{Root: root, Secrets: secretsDir}); err != nil {
		t.Fatalf("Secret: %v", err)
	}
	if !strings.Contains(out.String(), "ss-gone/main/dana/default") {
		t.Fatalf("report = %q, want the orphan named", out.String())
	}
	if _, err := os.Stat(orphan); err != nil {
		t.Fatalf("orphan removed: %v", err)
	}
}

// Without --yes, an answer that is not yes writes nothing.
func TestSecretSync_RefusedLeavesTheTreeAlone(t *testing.T) {
	root, _ := secretRoot(t)
	secretsDir := t.TempDir()

	var out bytes.Buffer
	if err := Secret(strings.NewReader("n\n"), &out, []string{"sync"}, map[string]string{}, Settings{Root: root, Secrets: secretsDir}); err != nil {
		t.Fatalf("Secret: %v", err)
	}
	if !strings.Contains(out.String(), "nothing generated") {
		t.Fatalf("report = %q, want it to say nothing was written", out.String())
	}
	entries, err := os.ReadDir(secretsDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("secrets root holds %d entries, want none", len(entries))
	}
}

// `secret mv` is named in the design and not implemented; saying so beats a
// generated value on one side and the old one still on the other.
func TestSecretMv_SaysItIsNotImplemented(t *testing.T) {
	root, secretsDir := secretRoot(t)
	err := Secret(strings.NewReader(""), &bytes.Buffer{}, []string{"mv"}, map[string]string{}, Settings{Root: root, Secrets: secretsDir})
	if err == nil || !strings.Contains(err.Error(), "not implemented") {
		t.Fatalf("err = %v, want it to say mv is not implemented", err)
	}
}

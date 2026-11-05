package deploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeDef writes a definition named name into dir, and beside it, when
// files is not nil, the directory of files it ships.
func writeDef(t *testing.T, dir, name string, files map[string]string) {
	t.Helper()
	def := "binary:\n  name: " + name + "\n  path: /usr/local/bin/" + name + "\ncommand: [" + name + "]\n"
	if err := os.WriteFile(filepath.Join(dir, name+".yaml"), []byte(def), 0o644); err != nil {
		t.Fatal(err)
	}
	if files == nil {
		return
	}
	if err := os.MkdirAll(filepath.Join(dir, name, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	for n, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name, n), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestShippedReadsAnExternalDefinitionsOwnFiles(t *testing.T) {
	dir := t.TempDir()
	writeDef(t, dir, "host-a", map[string]string{"hook.sh": "echo host-a.example.com\n"})
	got, err := shipped("host-a", dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || string(got["hook.sh"]) != "echo host-a.example.com\n" {
		t.Fatalf("shipped = %v, want only hook.sh", got)
	}
}

func TestShippedWithoutAFilesDirectoryIsEmpty(t *testing.T) {
	dir := t.TempDir()
	writeDef(t, dir, "example", nil)
	got, err := shipped("example", dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("shipped = %v, want nothing", got)
	}
}

func TestLoadServiceWithoutADefinition(t *testing.T) {
	for _, dir := range []string{"", t.TempDir()} {
		_, err := LoadService("example", dir)
		if err == nil || !strings.Contains(err.Error(), `service "example" has no deploy definition`) {
			t.Fatalf("dir %q: got %v", dir, err)
		}
	}
}

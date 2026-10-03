package deploy

import (
	"os"
	"path/filepath"
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

func TestShippedDoesNotMixInBuiltinFiles(t *testing.T) {
	builtinFiles, err := shipped("singbox", "")
	if err != nil || len(builtinFiles) == 0 {
		t.Fatalf("built-in singbox ships %v, %v; the test needs some", builtinFiles, err)
	}
	dir := t.TempDir()
	writeDef(t, dir, "singbox", map[string]string{"own.txt": "example.com\n"})
	got, err := shipped("singbox", dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || string(got["own.txt"]) != "example.com\n" {
		t.Fatalf("shipped = %v, want only own.txt", got)
	}
}

func TestShippedWithoutAFilesDirectoryIsEmpty(t *testing.T) {
	dir := t.TempDir()
	writeDef(t, dir, "singbox", nil)
	got, err := shipped("singbox", dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("shipped = %v, want nothing", got)
	}
}

func TestShippedFallsBackToBuiltinWhenDirLacksTheDefinition(t *testing.T) {
	want, err := shipped("singbox", "")
	if err != nil {
		t.Fatal(err)
	}
	got, err := shipped("singbox", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("shipped = %d files, want the %d built in", len(got), len(want))
	}
}

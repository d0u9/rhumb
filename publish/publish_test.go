package publish

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCreate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keys", "server1")
	if err := Create(path, []byte("key"), 0o600, 0o700); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); string(data) != "key" {
		t.Errorf("content %q", data)
	}
	for p, want := range map[string]os.FileMode{path: 0o600, filepath.Dir(path): 0o700} {
		if info, _ := os.Stat(p); info.Mode().Perm() != want {
			t.Errorf("%s mode %04o", p, info.Mode().Perm())
		}
	}
	if err := Create(path, []byte("other"), 0o600, 0o700); !errors.Is(err, ErrExists) {
		t.Errorf("second create: %v", err)
	}
	if entries, _ := os.ReadDir(filepath.Dir(path)); len(entries) != 1 {
		t.Errorf("left behind: %v", entries)
	}
}

func TestReplaceKeepsMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	if err := Replace(path, []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	os.Chmod(path, 0o640)
	if err := Replace(path, []byte("b"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	data, _ := os.ReadFile(path)
	if string(data) != "b" || info.Mode().Perm() != 0o640 {
		t.Errorf("%q %04o", data, info.Mode().Perm())
	}
}

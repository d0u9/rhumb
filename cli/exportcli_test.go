package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// exportCLIRoot is the exportable root plus a selector that matches every
// target in it.
func exportCLIRoot(t *testing.T) (root, secretsDir string) {
	t.Helper()
	return buildExportableRoot(t)
}

// A second export into the same directory asks, instead of failing, and
// writing again replaces what is there.
func TestExport_AsksBeforeOverwritingAFolder(t *testing.T) {
	root, secretsDir := exportCLIRoot(t)
	dest := t.TempDir()
	args := []string{"*"}

	var out bytes.Buffer
	if err := Export(strings.NewReader(""), &out, args, map[string]string{"to": dest, "yes": "true"}, Settings{Root: root, Secrets: secretsDir}); err != nil {
		t.Fatalf("first export: %v", err)
	}
	written := filesUnder(t, dest)
	if len(written) == 0 {
		t.Fatal("first export wrote nothing")
	}

	// Answering no leaves the files exactly as they were.
	for _, path := range written {
		if err := os.WriteFile(path, []byte("stale"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	out.Reset()
	if err := Export(strings.NewReader("n\n"), &out, args, map[string]string{"to": dest}, Settings{Root: root, Secrets: secretsDir}); err != nil {
		t.Fatalf("declined export: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "(overwrites)") || !strings.Contains(got, "Overwrite") {
		t.Fatalf("report = %q, want it to name the existing files and ask", got)
	}
	if data, _ := os.ReadFile(written[0]); string(data) != "stale" {
		t.Fatalf("%s = %q, want it untouched after no", written[0], data)
	}

	// Answering yes replaces them.
	out.Reset()
	if err := Export(strings.NewReader("y\n"), &out, args, map[string]string{"to": dest}, Settings{Root: root, Secrets: secretsDir}); err != nil {
		t.Fatalf("accepted export: %v", err)
	}
	if data, _ := os.ReadFile(written[0]); string(data) == "stale" {
		t.Fatalf("%s was not replaced after yes", written[0])
	}
}

// A script that says --yes and not --overwrite is told what is in the way
// rather than replacing it.
func TestExport_YesAloneDoesNotOverwrite(t *testing.T) {
	root, secretsDir := exportCLIRoot(t)
	dest := t.TempDir()
	args := []string{"*"}
	flags := map[string]string{"to": dest, "yes": "true"}

	var out bytes.Buffer
	if err := Export(strings.NewReader(""), &out, args, flags, Settings{Root: root, Secrets: secretsDir}); err != nil {
		t.Fatalf("first export: %v", err)
	}
	out.Reset()
	err := Export(strings.NewReader(""), &out, args, flags, Settings{Root: root, Secrets: secretsDir})
	if err == nil || !strings.Contains(err.Error(), "--overwrite") {
		t.Fatalf("second export error = %v, want it to name --overwrite", err)
	}

	out.Reset()
	flags["overwrite"] = "true"
	if err := Export(strings.NewReader(""), &out, args, flags, Settings{Root: root, Secrets: secretsDir}); err != nil {
		t.Fatalf("export --overwrite: %v", err)
	}
}

// A .zip destination is the same question about one file.
func TestExport_AsksBeforeOverwritingAZip(t *testing.T) {
	root, secretsDir := exportCLIRoot(t)
	zipPath := filepath.Join(t.TempDir(), "bundle.zip")
	args := []string{"*"}

	var out bytes.Buffer
	if err := Export(strings.NewReader(""), &out, args, map[string]string{"zip": zipPath, "yes": "true"}, Settings{Root: root, Secrets: secretsDir}); err != nil {
		t.Fatalf("first export: %v", err)
	}
	out.Reset()
	if err := Export(strings.NewReader("y\n"), &out, args, map[string]string{"zip": zipPath}, Settings{Root: root, Secrets: secretsDir}); err != nil {
		t.Fatalf("second export: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "already there") {
		t.Fatalf("report = %q, want it to say the archive is already there", got)
	}
}

func filesUnder(t *testing.T, dir string) []string {
	t.Helper()
	var paths []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return paths
}

// swapErrOut points the plan and the warning at a buffer for the duration of
// one test, so what a stdout export puts on each stream can be told apart.
func swapErrOut(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	previous := ErrOut
	ErrOut = &buf
	t.Cleanup(func() { ErrOut = previous })
	return &buf
}

// --to - writes the rendered bytes and nothing else, so what a pipe reads is
// the file, not a report about it.
func TestExport_ToStdoutWritesOneFileAndNothingElse(t *testing.T) {
	root, secretsDir := exportCLIRoot(t)
	plan := swapErrOut(t)

	var out bytes.Buffer
	err := Export(strings.NewReader(""), &out, []string{"export:link", "user:friend-a"},
		map[string]string{"to": "-"}, Settings{Root: root, Secrets: secretsDir})
	if err != nil {
		t.Fatalf("export to stdout: %v", err)
	}
	if !strings.HasPrefix(out.String(), "hysteria2://") {
		t.Fatalf("stdout = %q, want the rendered share link and nothing before it", out.String())
	}
	if strings.Contains(out.String(), "plaintext") {
		t.Fatalf("stdout = %q, want the warning on the other stream", out.String())
	}
	if !strings.Contains(plan.String(), "plaintext") {
		t.Fatalf("plan = %q, want the plaintext warning", plan.String())
	}
}

// Two files cannot both be stdout. The error names what matched, since
// narrowing the selector is what the caller does next.
func TestExport_ToStdoutRefusesSeveralFiles(t *testing.T) {
	root, secretsDir := exportCLIRoot(t)
	swapErrOut(t)

	var out bytes.Buffer
	err := Export(strings.NewReader(""), &out, []string{"*"},
		map[string]string{"to": "-"}, Settings{Root: root, Secrets: secretsDir})
	if err == nil {
		t.Fatal("export of several files to stdout: want an error")
	}
	if !strings.Contains(err.Error(), "--format yaml") || !strings.Contains(err.Error(), "share.txt") {
		t.Fatalf("error = %v, want it to name what matched and the way to write them all", err)
	}
	if out.Len() != 0 {
		t.Fatalf("stdout = %q, want nothing written", out.String())
	}
}

// --format yaml carries every file on one stream, each keeping the path it
// would have had in a bundle.
func TestExport_ToStdoutYAMLCarriesEveryFileWithItsPath(t *testing.T) {
	root, secretsDir := exportCLIRoot(t)
	swapErrOut(t)

	var out bytes.Buffer
	err := Export(strings.NewReader(""), &out, []string{"*"},
		map[string]string{"to": "-", "format": "yaml"}, Settings{Root: root, Secrets: secretsDir})
	if err != nil {
		t.Fatalf("export as yaml: %v", err)
	}

	var doc Envelope
	if err := yaml.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("stdout is not yaml: %v\n%s", err, out.String())
	}
	if len(doc.Files) < 2 {
		t.Fatalf("files = %d, want every rendered file", len(doc.Files))
	}
	for _, f := range doc.Files {
		if f.Path == "" || f.Content == "" {
			t.Fatalf("entry = %+v, want a path and its content", f)
		}
	}
}

// --overwrite answers a question stdout never asks, and --format answers one
// a directory never asks. Either one against the wrong destination is a
// mistake worth naming rather than ignoring.
func TestExport_StdoutFlagsRefuseTheWrongDestination(t *testing.T) {
	root, secretsDir := exportCLIRoot(t)
	swapErrOut(t)

	cases := []struct {
		name  string
		flags map[string]string
		want  string
	}{
		{"overwrite to stdout", map[string]string{"to": "-", "overwrite": "true"}, "--overwrite"},
		{"unknown format", map[string]string{"to": "-", "format": "json"}, "unknown --format"},
		{"format to a directory", map[string]string{"to": t.TempDir(), "format": "yaml"}, "--to -"},
		{"stdout and zip", map[string]string{"to": "-", "zip": filepath.Join(t.TempDir(), "b.zip")}, "two destinations"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var out bytes.Buffer
			err := Export(strings.NewReader(""), &out, []string{"*"}, c.flags, Settings{Root: root, Secrets: secretsDir})
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error = %v, want one naming %q", err, c.want)
			}
			if out.Len() != 0 {
				t.Fatalf("stdout = %q, want nothing written", out.String())
			}
		})
	}
}

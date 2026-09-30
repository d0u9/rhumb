package cli

import (
	"archive/zip"
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/d0u9/rhumb/engine"
	"github.com/d0u9/rhumb/publish"
)

// ExportFolder renders every checked instance and publishes each as its own
// file under destDir, each through publish.Create — checked, synced and
// linked to its final name, so destDir never holds a half-written result. It
// refuses if any target of destDir already exists, unless overwrite says the
// caller asked for those files to be replaced.
func ExportFolder(m engine.Renderer, instances []string, destDir string, overwrite bool) error {
	files, err := m.RenderAll(instances)
	if err != nil {
		return err
	}
	for _, f := range files {
		if err := writeExport(filepath.Join(destDir, f.Path), f.Bytes, f.Mode(), overwrite); err != nil {
			return err
		}
	}
	return nil
}

// writeExport publishes one export file. Overwriting is publish.Replace: the
// same temporary file in the same directory, renamed over the old one, so a
// reader sees either the old file or the new one and never a partial file.
func writeExport(path string, data []byte, mode os.FileMode, overwrite bool) error {
	if !overwrite {
		return publish.Create(path, data, mode, 0o700)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return publish.Replace(path, data, mode)
}

// ExistingOf is every path of a rendered export that is already on disk,
// relative to destDir and in the order the export writes them. It is what a
// confirmation needs before it can ask about overwriting.
func ExistingOf(files []engine.File, destDir string) []string {
	var existing []string
	for _, f := range files {
		if _, err := os.Lstat(filepath.Join(destDir, f.Path)); err == nil {
			existing = append(existing, f.Path)
		}
	}
	return existing
}

// ExportZip renders every checked instance into one .zip archive, built
// entirely in memory before anything is written, and publishes it to
// zipPath through publish.Create, or over an existing archive when overwrite
// says the caller asked for that.
func ExportZip(m engine.Renderer, instances []string, zipPath string, overwrite bool) error {
	files, err := m.RenderAll(instances)
	if err != nil {
		return err
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range files {
		// The header rather than Create, so the execute bit survives
		// the archive: a script extracted without it is a script the
		// person has to chmod before the deployment it belongs to runs.
		header := &zip.FileHeader{Name: filepath.ToSlash(f.Path), Method: zip.Deflate}
		header.SetMode(f.Mode())
		w, err := zw.CreateHeader(header)
		if err != nil {
			return fmt.Errorf("zip: %s: %w", f.Path, err)
		}
		if _, err := w.Write(f.Bytes); err != nil {
			return fmt.Errorf("zip: %s: %w", f.Path, err)
		}
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("zip: %w", err)
	}

	return writeExport(zipPath, buf.Bytes(), 0o600, overwrite)
}

// Package publish writes a file that must not already exist, without a reader
// ever seeing it half written: through a temporary file in the same directory,
// synced, read back and compared, then linked to its name.
package publish

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// ErrExists is returned for a path that already exists.
var ErrExists = errors.New("already exists")

// Create writes data to path with perm, creating missing parent directories
// with dirPerm. It refuses a path that exists, even one appearing while it
// writes.
func Create(path string, data []byte, perm, dirPerm fs.FileMode) error {
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("%s %w", path, ErrExists)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.dgs-part")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(perm); err != nil {
		temp.Close()
		return err
	}
	if _, err = temp.Write(data); err == nil {
		err = temp.Sync()
	}
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	back, err := os.ReadFile(tempPath)
	if err != nil {
		return err
	}
	same := sha256.Sum256(back) == sha256.Sum256(data) && bytes.Equal(back, data)
	clear(back)
	if !same {
		return fmt.Errorf("%s read back differently from what was written", path)
	}
	if err := os.Link(tempPath, path); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("%s %w", path, ErrExists)
		}
		return err
	}
	if handle, err := os.Open(dir); err == nil {
		handle.Sync()
		handle.Close()
	}
	return nil
}

// Replace writes data over path, which may exist, keeping its mode when it
// does and using perm when it does not. It is for a file edited in place, such
// as a configuration file gaining a line.
func Replace(path string, data []byte, perm fs.FileMode) error {
	if info, err := os.Stat(path); err == nil {
		perm = info.Mode().Perm()
	}
	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.dgs-part")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if err := temp.Chmod(perm); err != nil {
		temp.Close()
		return err
	}
	if _, err = temp.Write(data); err == nil {
		err = temp.Sync()
	}
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(temp.Name(), path)
}

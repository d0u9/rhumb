package deploy

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Leftover is something an installed bundle registered outside itself that
// outlived the bundle: a launchd entry or a shim whose bundle directory is
// gone, because someone deleted it rather than running ./ctl uninstall.
type Leftover struct {
	Kind   string // "launchd" or "shim"
	Path   string
	Label  string // launchd only
	Bundle string
}

var bundleKey = regexp.MustCompile(`<key>RhumbBundle</key><string>([^<]*)</string>`)

const shimMark = "# rhumb-bundle: "

// Leftovers lists what gc would remove under home. A bundle counts as gone
// only when its parent directory is still there: a bundle on a disk that is
// not mounted looks gone too, and its service is not ours to remove.
func Leftovers(home string) ([]Leftover, error) {
	var out []Leftover
	plists, _ := filepath.Glob(filepath.Join(home, "Library", "LaunchAgents", "rhumb.*.plist"))
	for _, p := range plists {
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		m := bundleKey.FindSubmatch(data)
		if m == nil {
			continue
		}
		bundle := unescapeXML(string(m[1]))
		if gone(bundle) {
			out = append(out, Leftover{Kind: "launchd", Path: p, Label: strings.TrimSuffix(filepath.Base(p), ".plist"), Bundle: bundle})
		}
	}
	shims, err := os.ReadDir(filepath.Join(home, ".local", "bin"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	for _, e := range shims {
		p := filepath.Join(home, ".local", "bin", e.Name())
		if bundle := shimBundle(p); bundle != "" && gone(bundle) {
			out = append(out, Leftover{Kind: "shim", Path: p, Bundle: bundle})
		}
	}
	return out, nil
}

// Remove unregisters and deletes one leftover.
func (l Leftover) Remove() error {
	if l.Kind == "launchd" {
		domain := "gui/" + strconv.Itoa(os.Getuid()) + "/" + l.Label
		if exec.Command("launchctl", "print", domain).Run() == nil {
			if out, err := exec.Command("launchctl", "bootout", domain).CombinedOutput(); err != nil {
				return errors.New(strings.TrimSpace(string(out)))
			}
		}
	}
	return os.Remove(l.Path)
}

// gone looks for the bundle's ctl rather than its directory: launchd creates
// the log paths it was given, so a deleted bundle whose service kept running
// comes back as an empty var/log.
func gone(bundle string) bool {
	if _, err := os.Stat(filepath.Join(bundle, "ctl")); !errors.Is(err, os.ErrNotExist) {
		return false
	}
	_, err := os.Stat(filepath.Dir(bundle))
	return err == nil
}

// shimBundle is the bundle a shim names on its second line, and empty for
// any other file.
func shimBundle(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for i := 0; i < 2 && s.Scan(); i++ {
		if b, ok := strings.CutPrefix(s.Text(), shimMark); ok {
			return b
		}
	}
	return ""
}

func unescapeXML(s string) string {
	return strings.NewReplacer("&lt;", "<", "&gt;", ">", "&amp;", "&").Replace(s)
}

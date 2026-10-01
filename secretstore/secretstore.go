// Package secretstore reads and writes the secrets root: one credential, one
// file, at <instance>/<port>/<group>/<name>. The group is whose the
// credential is — a person, or whoever hosts a relaying machine — and the
// name is which of their identities holds it, so every credential under a
// port is a <group>/<name> pair and a reader sees whose is whose without
// knowing what kind of principal each one is. It computes which paths an
// inventory's derivation implies, compares that against what is on disk,
// generates what is missing, and never deletes what sync no longer implies.
//
// The rules are in docs/inventory.md#secrets.
//
// Two things imply a path. A role whose auth is confgen.AuthPerPrincipal
// implies one per (principal, port) pair derive.Derive already computed. A
// role's own list implies one <instance>/own/<name> per name, for every
// instance of that role. Together they are the whole set, so a file under an
// own/ directory that no name in the list accounts for is reported orphaned
// like any other — it is a credential a template may still read and sync
// will never regenerate.
package secretstore

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/d0u9/rhumb/confgen"
	"github.com/d0u9/rhumb/derive"
	"github.com/d0u9/rhumb/inventory"
)

// SelfPort is the reserved port segment an instance's own secrets sit under,
// rather than under one of its listening ports.
const SelfPort = "self"

// PreviousSuffix names a secret's previous value during a rotation.
const PreviousSuffix = ".previous"

// randomPrintableLength is the length of the printable random string
// generated for a service that declares no confgen.Secret shape.
const randomPrintableLength = 32

// Path is one credential's identity: <instance>/<port>/<group>/<name> for a
// principal's, and <instance>/self/<name>[/<key>][/<field>] for one of the
// instance's own.
type Path struct {
	Instance string
	Port     string
	// Group is whose the credential is, and Name which of their identities
	// holds it. An instance's own secret sits under SelfPort with no group.
	Group string
	Name  string
	// Key is which of a `set` secret's values this is, and Field which part
	// of a credential made of several. Both are empty for a principal's
	// credential and for a single self secret. See
	// docs/inventory.md#a-services-own-secrets.
	Key   string
	Field string
}

// String is the path relative to the secrets root.
func (p Path) String() string {
	return filepath.Join(p.Instance, p.Port, p.Group, p.Name, p.Key, p.Field)
}

// ImpliedPaths computes every secret path a derivation implies: one file per
// (principal, port) grant for a role whose auth is per-principal, plus one
// <instance>/own/<name> file for every name in its role's own list. A role's
// shared names one of those, so it implies nothing on its own. See the
// package doc for what it deliberately leaves out beyond that.
func ImpliedPaths(inv *inventory.Root, manifests map[string]confgen.Manifest, model *derive.Model) []Path {
	roleOf := map[string]confgen.Manifest{}    // instance ID -> its service's manifest
	namesOf := map[string][]string{}           // instance ID -> the own secrets it holds
	keysOf := map[string]map[string][]string{} // instance ID -> set secret -> its keys
	for _, n := range inv.Nodes {
		if n.Broken != "" {
			continue
		}
		for _, inst := range n.Instances {
			if inst.Service == "" {
				continue // an override, not a real instance.
			}
			manifest, ok := manifests[inst.Service]
			if !ok {
				continue
			}
			roleOf[inst.ID] = manifest
			// Which of its service's own secrets an instance holds, and
			// which keys each set has, are both the instance's. See
			// inventory.Instance.SelfNames and SelfKeys.
			namesOf[inst.ID] = inst.SelfNames(manifest.Self.Names())
			keysOf[inst.ID] = inst.SelfKeys()
		}
	}

	seen := map[Path]bool{}
	var out []Path
	add := func(p Path) {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}

	for _, g := range model.Grants {
		if !derive.PortAuthenticates(roleOf[g.Instance], model, g.Instance, g.Port) {
			continue
		}
		add(Path{Instance: g.Instance, Port: g.Port, Group: g.Principal.Group, Name: g.Principal.Slot})
	}
	for id, manifest := range roleOf {
		for _, name := range namesOf[id] {
			decl := manifest.Self[name]
			leaves := func(key string) {
				fields := decl.FieldNames()
				if len(fields) == 0 {
					add(Path{Instance: id, Port: SelfPort, Name: name, Key: key})
					return
				}
				for _, field := range fields {
					add(Path{Instance: id, Port: SelfPort, Name: name, Key: key, Field: field})
				}
			}
			if !decl.Set {
				leaves("")
				continue
			}
			// A set with no key declared anywhere implies nothing: the
			// instance holds none of them yet, which is a thing to say
			// rather than a thing to guess a name for.
			for _, key := range keysOf[id][name] {
				leaves(key)
			}
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

// shapeOf returns the shape one path's value takes, and whether rhumb
// generates it at all. A principal's credential takes the service's own
// Secret block; one of an instance's own takes its declaration's, falling
// back to the service's, and a field takes the field's. A confgen.KindOpaque
// shape is never generated: see confgen.KindOpaque.
func shapeOf(inv *inventory.Root, manifests map[string]confgen.Manifest, p Path) (confgen.Secret, bool) {
	var manifest confgen.Manifest
	found := false
	for _, n := range inv.Nodes {
		for _, inst := range n.Instances {
			if inst.ID == p.Instance {
				manifest, found = manifests[inst.Service], true
			}
		}
	}
	if !found || p.Port != SelfPort {
		return manifest.Secret, manifest.Secret.Kind != confgen.KindOpaque
	}
	decl, ok := manifest.Self[p.Name]
	if !ok {
		return manifest.Secret, manifest.Secret.Kind != confgen.KindOpaque
	}
	shape := decl.Secret
	if p.Field != "" {
		if field, ok := decl.Fields[p.Field]; ok {
			shape = field
		}
	}
	if shape.Kind == "" && shape.Bytes == 0 {
		shape = manifest.Secret
	}
	return shape, shape.Kind != confgen.KindOpaque
}

// Result is what Sync found comparing implied paths against a secrets root.
type Result struct {
	// Missing are implied paths with no file on disk yet.
	Missing []Path
	// Orphaned are files on disk that nothing implies any more. Sync never
	// deletes these; see docs/inventory.md#keeping-the-tree-in-step.
	Orphaned []Path
	// RenameHint is true when Missing and Orphaned are the same size and
	// both non-empty — sync cannot tell a rename from an unrelated add and
	// remove, so it only suggests `secret mv`, it never guesses which pairs.
	RenameHint bool
}

// Sync compares the paths implied against the files already in root, a
// the secrets root tree. It reads the filesystem but changes nothing.
func Sync(root string, implied []Path) (Result, error) {
	onDisk, err := walk(root)
	if err != nil {
		return Result{}, err
	}

	impliedSet := map[string]bool{}
	for _, p := range implied {
		impliedSet[p.String()] = true
	}

	var res Result
	for _, p := range implied {
		if !onDisk[p.String()] {
			res.Missing = append(res.Missing, p)
		}
	}
	var diskPaths []string
	for rel := range onDisk {
		diskPaths = append(diskPaths, rel)
	}
	sort.Strings(diskPaths)
	for _, rel := range diskPaths {
		if strings.HasSuffix(rel, PreviousSuffix) {
			continue // a rotation's previous value, not an implied path itself.
		}
		if !impliedSet[rel] {
			res.Orphaned = append(res.Orphaned, parsePath(rel))
		}
	}

	res.RenameHint = len(res.Missing) > 0 && len(res.Missing) == len(res.Orphaned)
	return res, nil
}

func parsePath(rel string) Path {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	// The instance is two segments, <node>/<name>, as its ID is.
	if len(parts) < 2 {
		return Path{Instance: rel}
	}
	parts = append([]string{parts[0] + "/" + parts[1]}, parts[2:]...)
	// An instance's own secret is <instance>/self/<name>, and one or two
	// segments deeper when the name is a set, a record of fields, or both.
	// Everything else is a principal's, and is always four.
	if len(parts) >= 3 && parts[1] == SelfPort {
		p := Path{Instance: parts[0], Port: SelfPort, Name: parts[2]}
		if len(parts) > 3 {
			p.Key = parts[3]
		}
		if len(parts) > 4 {
			p.Field = strings.Join(parts[4:], "/")
		}
		return p
	}
	if len(parts) != 4 {
		return Path{Instance: rel}
	}
	return Path{Instance: parts[0], Port: parts[1], Group: parts[2], Name: parts[3]}
}

// walk returns every regular file under root, as paths relative to root
// using "/" separators, or an empty set if root does not exist yet.
func walk(root string) (map[string]bool, error) {
	out := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == root && os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if !isCredentialFile(rel) {
			return nil
		}
		out[rel] = true
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("secretstore: walking %s: %w", root, err)
	}
	return out, nil
}

// isCredentialFile reports whether a path under the secrets root could be a
// credential at all. Two things never are, and reporting them as orphaned
// would be noise nobody can act on:
//
//   - A file at the root. Every credential sits under an instance, so a file
//     beside the instance directories — a README saying what this tree is —
//     is not one.
//   - A dotfile, anywhere. A .gitignore, a .DS_Store and an editor's swap
//     file are the tool's or the operating system's, not this store's.
//
// Anything deeper that no path implies is still reported, because a stray
// file inside an instance directory is a credential nothing generates and
// nothing will regenerate.
func isCredentialFile(rel string) bool {
	if !strings.Contains(rel, "/") {
		return false
	}
	for _, segment := range strings.Split(rel, "/") {
		if strings.HasPrefix(segment, ".") {
			return false
		}
	}
	return true
}

// Generated splits missing into the paths Generate would write and the ones
// it would leave alone: a confgen.KindOpaque shape is a private key or a
// vendor's keyfile, which nothing here can invent. Both are returned in the
// order given, so a caller can say what it is about to write before writing
// it, and name the rest as still missing.
func Generated(inv *inventory.Root, manifests map[string]confgen.Manifest, missing []Path) (generated, opaque []Path) {
	for _, p := range missing {
		if _, ok := shapeOf(inv, manifests, p); ok {
			generated = append(generated, p)
		} else {
			opaque = append(opaque, p)
		}
	}
	return generated, opaque
}

// Generate writes a fresh value for every path in missing, taking the shape
// its instance's service declares — base64 of Secret.Bytes, or a printable
// random string when the service declares no Secret. It refuses to
// overwrite a file that already exists.
func Generate(root string, missing []Path, inv *inventory.Root, manifests map[string]confgen.Manifest) error {
	for _, p := range missing {
		shape, generated := shapeOf(inv, manifests, p)
		if !generated {
			// An opaque value is one nothing but a certificate authority
			// or a vendor can produce. Its path stays missing, which is
			// the report someone acts on.
			continue
		}
		full := filepath.Join(root, p.String())
		if _, err := os.Stat(full); err == nil {
			return fmt.Errorf("secretstore: %s already exists, refusing to overwrite", full)
		}
		value, err := generateValue(shape)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			return fmt.Errorf("secretstore: %w", err)
		}
		if err := os.WriteFile(full, []byte(value), 0o600); err != nil {
			return fmt.Errorf("secretstore: writing %s: %w", full, err)
		}
	}
	return nil
}

func generateValue(secret confgen.Secret) (string, error) {
	if secret.Kind == "base64" && secret.Bytes > 0 {
		buf := make([]byte, secret.Bytes)
		if _, err := rand.Read(buf); err != nil {
			return "", fmt.Errorf("secretstore: generating a value: %w", err)
		}
		return base64.StdEncoding.EncodeToString(buf), nil
	}
	return randomPrintable(randomPrintableLength)
}

const printableAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

func randomPrintable(length int) (string, error) {
	var b strings.Builder
	for i := 0; i < length; i++ {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(printableAlphabet))))
		if err != nil {
			return "", fmt.Errorf("secretstore: generating a value: %w", err)
		}
		b.WriteByte(printableAlphabet[n.Int64()])
	}
	return b.String(), nil
}

// ReadValue reads one secret's value, trimmed of a trailing newline if any.
func ReadValue(root string, p Path) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, p.String()))
	if err != nil {
		return "", fmt.Errorf("secretstore: reading %s: %w", p, err)
	}
	return strings.TrimSuffix(string(data), "\n"), nil
}

// ReadPrevious reads a secret's previous value — its <name>.previous
// sibling — for the second account a rotating per-principal port emits. ok
// is false, with no error, when there is no .previous file: an ordinary
// state, not a broken one.
func ReadPrevious(root string, p Path) (value string, ok bool, err error) {
	full := filepath.Join(root, p.String()) + PreviousSuffix
	data, err := os.ReadFile(full)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("secretstore: reading %s: %w", full, err)
	}
	return strings.TrimSuffix(string(data), "\n"), true, nil
}

// PreviousModTimes finds every .previous file under root and returns its
// modification time, keyed by the path of the secret it is the previous
// value of (without the .previous suffix) — validate's own input for
// docs/inventory.md#validation rule 16: no .previous file older
// than seven days.
func PreviousModTimes(root string) (map[string]time.Time, error) {
	onDisk, err := walkWithInfo(root)
	if err != nil {
		return nil, err
	}
	out := map[string]time.Time{}
	for rel, info := range onDisk {
		if base, ok := strings.CutSuffix(rel, PreviousSuffix); ok {
			out[base] = info.ModTime()
		}
	}
	return out, nil
}

// walkWithInfo is walk, but keeping each file's os.FileInfo instead of just
// its presence.
func walkWithInfo(root string) (map[string]fs.FileInfo, error) {
	out := map[string]fs.FileInfo{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == root && os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if !isCredentialFile(rel) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		out[rel] = info
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("secretstore: walking %s: %w", root, err)
	}
	return out, nil
}

// ReadSelf reads everything under <instance>/self/ — the instance's own
// secrets, docs/inventory.md#the-render-context's self datasource.
// The result mirrors the tree: a name is a string when it is one file, and a
// map when it is a set, a record of fields, or both. So self.psk.main and
// self.account.main.password read the way their paths are written.
func ReadSelf(root, instance string) (map[string]any, error) {
	dir := filepath.Join(root, instance, SelfPort)
	self := map[string]any{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == dir && os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		put(self, strings.Split(filepath.ToSlash(rel), "/"), strings.TrimSuffix(string(data), "\n"))
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("secretstore: reading %s: %w", dir, err)
	}
	return self, nil
}

// put writes value at segments in a nested map, creating the maps it needs.
// A value already at a shorter path loses to the deeper one: a stray file
// where a directory belongs should not hide the credentials under it.
func put(into map[string]any, segments []string, value string) {
	if len(segments) == 1 {
		if _, ok := into[segments[0]].(map[string]any); !ok {
			into[segments[0]] = value
		}
		return
	}
	next, ok := into[segments[0]].(map[string]any)
	if !ok {
		next = map[string]any{}
		into[segments[0]] = next
	}
	put(next, segments[1:], value)
}

// Mv moves a secret path (a node, instance or the whole tree under one
// instance) from oldPath to newPath, both relative to root. It is
// docs/inventory.md's explicit answer to a rename: sync only
// reports equal counts of new and orphaned paths, this performs it.
func Mv(root, oldRel, newRel string) error {
	oldFull, newFull := filepath.Join(root, oldRel), filepath.Join(root, newRel)
	if _, err := os.Stat(oldFull); err != nil {
		return fmt.Errorf("secretstore: %w", err)
	}
	if _, err := os.Stat(newFull); err == nil {
		return fmt.Errorf("secretstore: %s already exists", newFull)
	}
	if err := os.MkdirAll(filepath.Dir(newFull), 0o700); err != nil {
		return fmt.Errorf("secretstore: %w", err)
	}
	if err := os.Rename(oldFull, newFull); err != nil {
		return fmt.Errorf("secretstore: %w", err)
	}
	return nil
}

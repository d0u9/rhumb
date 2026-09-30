// This file is `rhumb check`: the whole of what the TUI would tell you
// about an inventory being wrong, written to stdout without opening it. A TUI
// embedding this package shows the same problems interactively.
package cli

import (
	"fmt"
	"github.com/d0u9/rhumb/engine"
	"io"
	"time"

	"github.com/d0u9/rhumb/inventory"
	"github.com/d0u9/rhumb/secretstore"
	"github.com/d0u9/rhumb/target"
	"github.com/d0u9/rhumb/validate"
)

// ErrProblems is returned when the report found something. The report itself
// is already written, so the error carries a count rather than a message a
// caller would print a second time.
type ErrProblems struct{ Count int }

func (e ErrProblems) Error() string {
	return fmt.Sprintf("%s found", plural(e.Count, "problem"))
}

// Check runs every check the inventory has and writes what failed.
// It is the report form of what the TUI shows across its four tabs: a broken
// file listed rather than dropped, the validation rules, and the secrets
// store compared against the paths the inventory implies.
//
// It writes nothing on success beyond one line, because a report that prints
// the whole inventory when the inventory is fine is a report nobody reads
// the top of.
func Check(out io.Writer, global Settings) error {
	root, secretsDir := global.Root, global.Secrets
	if root == "" {
		return fmt.Errorf("no generator root given")
	}

	l, err := engine.Load(root)
	if err != nil {
		return err
	}

	var problems []string

	// A file that would not parse. These come first: everything below them
	// is computed from what did parse, so a rule failing underneath may be
	// a consequence rather than a fault of its own.
	problems = append(problems, BrokenFiles(l.Inv)...)
	for name, manifest := range l.Manifests {
		// A service that renders nothing is a directory nothing deploys.
		// Renders is what the two forms of the declaration meet in, so a
		// service writing several files is not reported for lacking the
		// one-file form's `template`.
		if len(manifest.Renders()) == 0 {
			problems = append(problems, fmt.Sprintf("service %s: no role declared", name))
		}
	}

	previous, previousErr := previousModTimes(secretsDir)
	for _, issue := range validate.Validate(l.Inv, l.Manifests, l.Exports, l.Derived, previous) {
		problems = append(problems, issue.Message)
	}

	// The secrets store, which validate's rule 15 covers only when it is
	// given the store; the report reads it directly so the two directions
	// are named individually rather than as one rule.
	switch {
	case secretsDir == "":
		problems = append(problems, "no secrets root given: no credential can be read or checked")
	case previousErr != nil:
		problems = append(problems, fmt.Sprintf("secrets store: %v", previousErr))
	default:
		store, err := checkSecrets(l, secretsDir)
		if err != nil {
			problems = append(problems, fmt.Sprintf("secrets store: %v", err))
		}
		problems = append(problems, store...)
	}

	if len(problems) == 0 {
		fmt.Fprintf(out, "%s, %s, %s: no problem found\n",
			plural(len(l.Inv.Nodes), "node"),
			plural(len(l.Inv.Users), "user"),
			plural(len(l.Manifests), "service"))
		return nil
	}

	for _, p := range problems {
		fmt.Fprintln(out, p)
	}
	// The count is the error's own message: printing it here as well would
	// put the same line on stdout and on stderr.
	return ErrProblems{Count: len(problems)}
}

// BrokenFiles names every inventory file that would not parse. inventory.Load
// keeps them rather than dropping them, so that an instance which silently
// stopped appearing is reported instead.
func BrokenFiles(inv *inventory.Root) []string {
	var out []string
	for _, n := range inv.Nodes {
		if n.Broken != "" {
			out = append(out, fmt.Sprintf("%s: %s", n.Path, n.Broken))
		}
	}
	if inv.UsersBroken != "" {
		out = append(out, "users.yaml: "+inv.UsersBroken)
	}
	if inv.RoutesBroken != "" {
		out = append(out, "routes.yaml: "+inv.RoutesBroken)
	}
	if inv.NetworksBroken != "" {
		out = append(out, "networks.yaml: "+inv.NetworksBroken)
	}
	return out
}

// previousModTimes reads the store's rotation leftovers, or nothing at all
// when there is no store to read — validate takes a nil map for that.
func previousModTimes(secretsDir string) (map[string]time.Time, error) {
	if secretsDir == "" {
		return nil, nil
	}
	return secretstore.PreviousModTimes(secretsDir)
}

// checkSecrets compares the paths the inventory implies against the files on
// disk, in both directions, and names each one. Neither is fixed here: a
// missing path is `secret sync`'s to generate, and an orphaned one is a
// person's to remove or to rename.
func checkSecrets(l engine.Loaded, secretsDir string) ([]string, error) {
	implied := secretstore.ImpliedPaths(l.Inv, l.Manifests, l.Derived)
	res, err := secretstore.Sync(secretsDir, implied)
	if err != nil {
		return nil, err
	}

	// A path whose shape is opaque is one nothing here invents — a private
	// key, a certificate chain, a bcrypt hash. Sending someone to `secret
	// sync` for it is sending them to a command that will list it and write
	// nothing, so the two are reported apart, as `secret sync` itself
	// already reports them.
	generated, opaque := secretstore.Generated(l.Inv, l.Manifests, res.Missing)

	var out []string
	for _, p := range generated {
		out = append(out, fmt.Sprintf("secret missing: %s — run secret sync to generate it", p.String()))
	}
	for _, p := range opaque {
		out = append(out, fmt.Sprintf("secret missing: %s — an opaque value, which nothing generates: write the file yourself", p.String()))
	}
	for _, p := range res.Orphaned {
		out = append(out, fmt.Sprintf("secret orphaned: %s — nothing implies it; sync never deletes", p.String()))
	}
	if res.RenameHint {
		out = append(out, fmt.Sprintf("%s missing and %s orphaned: this may be a rename, which is secret mv rather than a generate",
			plural(len(res.Missing), "path"), plural(len(res.Orphaned), "path")))
	}

	// A stale .previous is validate's rule 16 and is already in the list
	// above; reporting it again here would say the same thing twice.
	return out, nil
}

// Targets lists every target the generator root holds, grouped by
// node, which is what docs/export.md#targets-and-selectors calls
// for: a way to see what the root holds, and what a selector would match,
// without rendering anything.
func Targets(out io.Writer, global Settings) error {
	root := global.Root
	if root == "" {
		return fmt.Errorf("no generator root given")
	}
	l, err := engine.Load(root)
	if err != nil {
		return err
	}

	groups := buildTree(target.List(l.Inv, l.Derived))
	for _, g := range groups {
		what := ""
		if g.user {
			what = " (unmanaged user)"
		}
		fmt.Fprintf(out, "%s%s\n", g.key, what)
		if g.broken != "" {
			fmt.Fprintf(out, "  broken: %s\n", g.broken)
			continue
		}
		var rows [][]string
		for _, inst := range g.instances {
			detail := inst.detail
			if inst.broken != "" {
				detail = "broken: " + inst.broken
			}
			rows = append(rows, []string{inst.name, detail})
		}
		for _, row := range columns(rows) {
			fmt.Fprintf(out, "  %s\n", row)
		}
	}
	return nil
}

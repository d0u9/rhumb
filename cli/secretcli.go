// This file is `dgs conf secret sync`: keeping the secrets tree in step with
// what the inventory implies. The Secrets tab of the inspect page shows the
// same comparison and writes nothing; this is the half that writes. See
// docs/apps/conf/inventory.md#keeping-the-tree-in-step.
package cli

import (
	"fmt"
	"github.com/d0u9/rhumb/engine"
	"io"

	"github.com/d0u9/rhumb/secretstore"
)

// Secret runs `dgs conf secret sync [--yes]`.
//
// Sync generates what is missing and never deletes what is orphaned: it
// cannot tell a rename from a removal, and guessing wrong fails silently —
// a new random value on one side and the old one still on the other.
func Secret(in io.Reader, out io.Writer, args []string, flags map[string]string, global Settings) error {
	if len(args) != 1 {
		return fmt.Errorf("give a secret command: sync")
	}
	switch args[0] {
	case "sync":
	case "mv":
		return fmt.Errorf("secret mv is not implemented yet: move the files yourself, then run `dgs conf secret sync` to confirm the tree is in step")
	default:
		return fmt.Errorf("unknown secret command %q; the one that exists is `sync`", args[0])
	}

	root, secretsDir := global.Root, global.Secrets
	if root == "" {
		return fmt.Errorf("conf.root is not configured")
	}
	if secretsDir == "" {
		return fmt.Errorf("conf.secrets is not configured")
	}

	l, err := engine.Load(root)
	if err != nil {
		return err
	}

	implied := secretstore.ImpliedPaths(l.Inv, l.Manifests, l.Derived)
	res, err := secretstore.Sync(secretsDir, implied)
	if err != nil {
		return err
	}

	// An orphan is reported whether or not anything is generated, since it
	// is the half a person has to act on: sync will not act on it ever.
	if len(res.Orphaned) > 0 {
		fmt.Fprintf(out, "%s on disk that nothing implies any more, left alone:\n", plural(len(res.Orphaned), "path"))
		for _, p := range res.Orphaned {
			fmt.Fprintf(out, "  %s\n", p)
		}
		if res.RenameHint {
			fmt.Fprintln(out, "\nAs many missing as orphaned: this may be a rename rather than an add and a remove.")
		}
		fmt.Fprintln(out)
	}

	if len(res.Missing) == 0 {
		fmt.Fprintln(out, "nothing to generate: every path the inventory implies is on disk")
		return nil
	}

	// A path whose shape is opaque — a private key, a vendor's keyfile — is
	// one dgs never invents. It stays missing, which is the report someone
	// acts on, so it is named apart from what is about to be written.
	generated, opaque := secretstore.Generated(l.Inv, l.Manifests, res.Missing)

	if len(opaque) > 0 {
		fmt.Fprintf(out, "%s nothing generates, still missing:\n", plural(len(opaque), "path"))
		for _, p := range opaque {
			fmt.Fprintf(out, "  %s\n", p)
		}
		fmt.Fprintln(out)
	}

	if len(generated) == 0 {
		return nil
	}

	fmt.Fprintf(out, "%s to generate under %s:\n", plural(len(generated), "path"), secretsDir)
	for _, p := range generated {
		fmt.Fprintf(out, "  %s\n", p)
	}
	fmt.Fprintln(out, "\nAdding a credential is a change on the server side: every instance it\nreaches has to be rendered again and deployed before anything connects with it.")

	if flags["yes"] != "true" {
		ok, err := Confirm(in, out, "Generate them? [y/N] ")
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintln(out, "nothing generated")
			return nil
		}
	}

	if err := secretstore.Generate(secretsDir, generated, l.Inv, l.Manifests); err != nil {
		return err
	}
	fmt.Fprintf(out, "generated %s\n", plural(len(generated), "path"))
	return nil
}

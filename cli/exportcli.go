// This file is `rhumb export`: the command-line half of exporting. The
// TUI half lives on the inspect page (exportflow.go), where the selection is
// what is marked, or the row under the cursor; here it is a selector, which is what makes an export
// repeatable. See docs/export.md#exporting-from-the-command-line.
package cli

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"github.com/d0u9/rhumb/engine"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"

	"github.com/d0u9/rhumb/target"
)

// stdoutDest is what --to reads as "write it to stdout" rather than as a
// directory name. A single hyphen is the spelling every other tool uses for
// it, and a directory called `-` is not a thing anyone means.
const stdoutDest = "-"

// ErrOut is where an export to stdout puts its plan and its warning. Stdout
// then carries the rendered file and nothing else, which is what makes it
// safe to pipe into a program that parses what it reads.
var ErrOut io.Writer = os.Stderr

// Export runs `rhumb export <selector>... [--to dir | --zip file]`.
//
// Everything it writes is plaintext: the same material the secrets store
// holds, in the form a server reads it. So it says what it is about to write
// and where, and asks — unless --yes, which is what a script uses.
func Export(in io.Reader, out io.Writer, args []string, flags map[string]string, global Settings) error {
	root, secretsDir := global.Root, global.Secrets
	if root == "" {
		return fmt.Errorf("no generator root given")
	}

	l, err := engine.Load(root)
	if err != nil {
		return err
	}
	r := engine.Renderer{Data: l, RootPath: root, SecretsDir: secretsDir}

	selector := strings.Join(args, " ")
	matched, err := target.Match(selector, target.List(l.Inv, l.Derived))
	if err != nil {
		return err
	}

	var instances []string
	var broken []string
	for _, t := range matched {
		if t.Broken != "" {
			broken = append(broken, fmt.Sprintf("%s: %s", t.Instance, t.Broken))
			continue
		}
		instances = append(instances, t.Instance)
	}
	if len(broken) > 0 {
		// A broken target cannot be rendered, and exporting the rest
		// silently would hand over a bundle missing something nobody asked
		// to leave out.
		return fmt.Errorf("selector matched %s that cannot be rendered:\n  %s",
			plural(len(broken), "target"), strings.Join(broken, "\n  "))
	}
	sort.Strings(instances)

	destDir, zipPath, format := flags["to"], flags["zip"], flags["format"]
	if destDir == stdoutDest {
		return exportToStdout(r, out, instances, zipPath, format, flags["overwrite"] == "true")
	}
	switch {
	case format != "":
		return fmt.Errorf("--format writes one document to stdout; it needs --to -")
	case destDir != "" && zipPath != "":
		return fmt.Errorf("--to and --zip are two destinations; give one")
	case destDir == "" && zipPath == "":
		if dir := global.ExportDir; dir != "" {
			destDir = dir
			break
		}
		return fmt.Errorf("no destination: give --to <dir> or --zip <file>")
	}

	// Render everything before writing anything, so the plan named below is
	// the plan carried out, and a failure leaves nothing behind.
	files, err := r.RenderAll(instances)
	if err != nil {
		return err
	}

	where := destDir
	if zipPath != "" {
		where = zipPath
	}

	// What is already there is part of the plan, not a failure found halfway
	// through writing it: the list says which files an export replaces before
	// anything is written.
	var existing []string
	if zipPath != "" {
		if _, err := os.Lstat(zipPath); err == nil {
			existing = []string{zipPath}
		}
	} else {
		existing = ExistingOf(files, destDir)
	}
	replacing := map[string]bool{}
	for _, path := range existing {
		replacing[path] = true
	}

	fmt.Fprintf(out, "%s to %s:\n", plural(len(files), "file"), where)
	for _, f := range files {
		suffix := ""
		if replacing[f.Path] {
			suffix = "  (overwrites)"
		}
		fmt.Fprintf(out, "  %s%s\n", f.Path, suffix)
	}
	if zipPath != "" && len(existing) > 0 {
		fmt.Fprintf(out, "\n%s is already there and would be replaced.\n", zipPath)
	}
	fmt.Fprintln(out, "\nThese are plaintext, with every credential in them.")

	overwrite := flags["overwrite"] == "true"
	if len(existing) > 0 && !overwrite {
		// --yes answers a question; it does not decide that replacing a file
		// already on disk is what was meant. A script says --overwrite.
		if flags["yes"] == "true" {
			return fmt.Errorf("%s already %s; pass --overwrite to replace %s",
				plural(len(existing), "file"), Exists(len(existing)), Them(len(existing)))
		}
		ok, err := Confirm(in, out, fmt.Sprintf("%s already %s. Overwrite %s and write the rest? [y/N] ",
			plural(len(existing), "file"), Exists(len(existing)), Them(len(existing))))
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintln(out, "nothing written")
			return nil
		}
		overwrite = true
	} else if flags["yes"] != "true" {
		ok, err := Confirm(in, out, "Write them? [y/N] ")
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintln(out, "nothing written")
			return nil
		}
	}

	if zipPath != "" {
		if err := ExportZip(r, instances, zipPath, overwrite); err != nil {
			return err
		}
		fmt.Fprintf(out, "wrote %s\n", zipPath)
		return nil
	}
	if err := ExportFolder(r, instances, destDir, overwrite); err != nil {
		return err
	}
	fmt.Fprintf(out, "wrote %s\n", filepath.Clean(destDir))
	return nil
}

// exportToStdout renders the selector's targets and writes them to stdout.
//
// Nothing is replaced and nothing is left behind, so it does not ask and
// --yes has nothing to answer. What it will not do is concatenate: two
// rendered files on one stream are no longer either of them, and a caller
// that wanted both wanted their paths too. --format yaml is that caller's
// answer; without it, a selector matching more than one file is an error
// naming what it matched, the same way a selector matching none is.
func exportToStdout(r engine.Renderer, out io.Writer, instances []string, zipPath, format string, overwrite bool) error {
	switch {
	case zipPath != "":
		return fmt.Errorf("--to - and --zip are two destinations; give one")
	case overwrite:
		return fmt.Errorf("--overwrite replaces files a destination already holds; stdout holds none")
	case format != "" && format != "yaml":
		return fmt.Errorf("unknown --format %q; the only one is yaml", format)
	}

	files, err := r.RenderAll(instances)
	if err != nil {
		return err
	}

	if format == "" {
		if len(files) != 1 {
			paths := make([]string, 0, len(files))
			for _, f := range files {
				paths = append(paths, f.Path)
			}
			return fmt.Errorf("--to - writes one file to stdout; this selector matched %s:\n  %s\nnarrow the selector, or pass --format yaml to write them all as one document",
				plural(len(files), "file"), strings.Join(paths, "\n  "))
		}
		fmt.Fprintf(ErrOut, "%s to stdout:\n  %s\n\nThese are plaintext, with every credential in them.\n",
			plural(1, "file"), files[0].Path)
		_, err := out.Write(files[0].Bytes)
		return err
	}

	doc := Envelope{Files: make([]envelopeFile, 0, len(files))}
	for _, f := range files {
		entry := envelopeFile{Path: f.Path}
		// A rendered file is text in every case there is today, and a YAML
		// string says so plainly. Anything that is not valid UTF-8 cannot be
		// a YAML scalar at all, so it says how to read it back instead of
		// being written out mangled.
		if utf8.Valid(f.Bytes) {
			entry.Content = string(f.Bytes)
		} else {
			entry.Encoding = "base64"
			entry.Content = base64.StdEncoding.EncodeToString(f.Bytes)
		}
		doc.Files = append(doc.Files, entry)
	}
	fmt.Fprintf(ErrOut, "%s to stdout:\n\nThese are plaintext, with every credential in them.\n",
		plural(len(files), "file"))
	enc := yaml.NewEncoder(out)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return err
	}
	return enc.Close()
}

// Envelope is what --format yaml writes: every rendered file, each with the
// path it would have had in a bundle, so one stream carries what a directory
// carried without either file losing its name.
type Envelope struct {
	Files []envelopeFile `yaml:"files"`
}

type envelopeFile struct {
	Path     string `yaml:"path"`
	Encoding string `yaml:"encoding,omitempty"`
	Content  string `yaml:"content"`
}

// Confirm reads one line and accepts only an explicit yes. Anything else,
// including end of input, is no: writing credentials to disk is not a thing
// to do because a pipe was empty.
func Confirm(in io.Reader, out io.Writer, prompt string) (bool, error) {
	fmt.Fprint(out, prompt)
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && line == "" {
		return false, nil
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}

// Exists and them are the verb and the pronoun that agree with a count, so
// the overwrite question reads as a sentence for one file and for many.
func Exists(n int) string {
	if n == 1 {
		return "exists"
	}
	return "exist"
}

func Them(n int) string {
	if n == 1 {
		return "it"
	}
	return "them"
}

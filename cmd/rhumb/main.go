// Command rhumb renders a generator root: every instance's configuration,
// the files that deploy it, and the manifest a deployment tool reads.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/d0u9/rhumb/cli"
)

const usage = `usage: rhumb [--root dir] [--secrets dir] <command> [flags] [args]

commands:
  check                        report every problem in the root and its secrets
  targets                      list every target, grouped by node
  export <selector>...         render targets into --to <dir>, --zip <file>, or --to -
  secret sync [--yes]          generate the secrets the inventory implies
  init [dir] [--gitignore]     write the scaffold a generator root starts from
  reservations <network>       print the address reservations a router should hold
  migrate node --node from=<old>,to=<new> [--network ...] [--apply --yes]
                               report, or apply, moving a node's workload to another node
  deploy build <export-dir> --to <bundle> [--platform os/arch] [--source name] [--bin file] [--services dir]
                               bundle one exported instance with its program and ctl
  deploy gc [--yes]            list, or remove, what deleted bundles left registered

--root and --secrets default to $RHUMB_ROOT and $RHUMB_SECRETS.
`

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "rhumb:", err)
		var problems cli.ErrProblems
		if errors.As(err, &problems) {
			os.Exit(1)
		}
		os.Exit(2)
	}
}

func run(args []string, in io.Reader, out, errOut io.Writer) error {
	global := flag.NewFlagSet("rhumb", flag.ContinueOnError)
	global.SetOutput(errOut)
	global.Usage = func() { fmt.Fprint(errOut, usage) }
	s := cli.Settings{}
	global.StringVar(&s.Root, "root", os.Getenv("RHUMB_ROOT"), "generator root")
	global.StringVar(&s.Secrets, "secrets", os.Getenv("RHUMB_SECRETS"), "secrets root")
	if err := global.Parse(args); err != nil {
		return err
	}
	rest := global.Args()
	if len(rest) == 0 {
		global.Usage()
		return errors.New("no command given")
	}
	cmd, rest := rest[0], rest[1:]

	fs := flag.NewFlagSet("rhumb "+cmd, flag.ContinueOnError)
	fs.SetOutput(errOut)
	flags := map[string]string{}
	str := func(name, help string) { fs.Func(name, help, func(v string) error { flags[name] = v; return nil }) }
	boolean := func(name, help string) {
		fs.BoolFunc(name, help, func(v string) error {
			b, err := strconv.ParseBool(v)
			flags[name] = strconv.FormatBool(b)
			return err
		})
	}

	var action func(io.Reader, io.Writer, []string, map[string]string, cli.Settings) error
	switch cmd {
	case "check":
		return cli.Check(out, s)
	case "targets":
		return cli.Targets(out, s)
	case "export":
		str("to", "write into this directory, or - for stdout")
		str("zip", "write into this .zip")
		str("format", "with --to -, yaml writes every file as one document")
		boolean("overwrite", "replace files the destination already holds")
		boolean("yes", "write without asking")
		boolean("y", "same as --yes")
		action = cli.Export
	case "secret":
		boolean("yes", "generate without asking")
		boolean("y", "same as --yes")
		action = cli.Secret
	case "init":
		boolean("gitignore", "also write a .gitignore into the secrets root")
		action = cli.Init
		if s.Secrets != "" {
			flags["secrets"] = s.Secrets
		}
	case "reservations":
		action = cli.Reservations
	case "deploy":
		return runDeploy(rest, out, errOut)
	case "migrate":
		for _, n := range []string{"node", "network", "instance", "route", "published", "scenario"} {
			str(n, "migration plan: "+n+" changes")
		}
		boolean("apply", "write the plan into the root")
		boolean("yes", "apply without asking")
		boolean("y", "same as --yes")
		action = cli.Migrate
	default:
		global.Usage()
		return fmt.Errorf("unknown command %q", cmd)
	}
	if err := fs.Parse(interleaved(fs, rest)); err != nil {
		return err
	}
	if flags["y"] == "true" {
		flags["yes"] = "true"
	}
	return action(in, out, fs.Args(), flags, s)
}

// interleaved moves flags that follow positional arguments to the front, so
// `rhumb export '*' --to out` parses as a person would expect.
func interleaved(fs *flag.FlagSet, args []string) []string {
	var flagArgs, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if len(a) > 1 && a[0] == '-' && a != "-" {
			flagArgs = append(flagArgs, a)
			name := a[1:]
			if name[0] == '-' {
				name = name[1:]
			}
			if f := fs.Lookup(name); f != nil && !isBool(f) && i+1 < len(args) {
				i++
				flagArgs = append(flagArgs, args[i])
			}
			continue
		}
		positional = append(positional, a)
	}
	return append(flagArgs, positional...)
}

func isBool(f *flag.Flag) bool {
	b, ok := f.Value.(interface{ IsBoolFlag() bool })
	return ok && b.IsBoolFlag()
}

package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/d0u9/rhumb/deploy"
)

// runDeploy is `rhumb deploy`. It reads exports, never the root, so it needs
// neither --root nor --secrets and runs as well on the machine a bundle is
// installed on.
func runDeploy(args []string, out, errOut io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: rhumb deploy build|gc")
	}
	fs := flag.NewFlagSet("rhumb deploy "+args[0], flag.ContinueOnError)
	fs.SetOutput(errOut)
	switch args[0] {
	case "build":
		var opt deploy.Options
		to := fs.String("to", "", "bundle directory to write")
		fs.StringVar(&opt.Platform, "platform", "", "target os/arch, this machine's by default")
		fs.StringVar(&opt.Binary, "bin", "", "bundle this program instead of what the source gives")
		fs.StringVar(&opt.Services, "services", "", "directory of service definitions to prefer")
		if err := fs.Parse(interleaved(fs, args[1:])); err != nil {
			return err
		}
		if fs.NArg() != 1 || *to == "" {
			return errors.New("usage: rhumb deploy build <export-dir> --to <bundle>")
		}
		if err := deploy.Build(fs.Arg(0), *to, opt); err != nil {
			return err
		}
		fmt.Fprintf(out, "wrote %s; install with %s/ctl install\n", *to, *to)
		return nil
	case "gc":
		yes := fs.Bool("yes", false, "remove what is listed")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		left, err := deploy.Leftovers(home)
		if err != nil {
			return err
		}
		if len(left) == 0 {
			fmt.Fprintln(out, "nothing left behind")
			return nil
		}
		for _, l := range left {
			fmt.Fprintf(out, "%-7s %s (bundle %s is gone)\n", l.Kind, l.Path, l.Bundle)
			if *yes {
				if err := l.Remove(); err != nil {
					return fmt.Errorf("%s: %w", l.Path, err)
				}
			}
		}
		if !*yes {
			fmt.Fprintln(out, "run again with --yes to remove these")
		}
		return nil
	}
	return fmt.Errorf("unknown deploy command %q", args[0])
}

// This file is `dgs conf reservations <network>`: what a network's router
// is to be given. See docs/apps/conf/inventory.md#what-the-router-is-given.
package cli

import (
	"fmt"
	"github.com/d0u9/rhumb/engine"
	"io"
	"text/tabwriter"

	"github.com/d0u9/rhumb/derive"
)

func Reservations(in io.Reader, out io.Writer, args []string, flags map[string]string, global Settings) error {
	if len(args) != 1 {
		return fmt.Errorf("give one network: reservations <network>")
	}
	root := global.Root
	if root == "" {
		return fmt.Errorf("conf.root is not configured")
	}
	l, err := engine.Load(root)
	if err != nil {
		return err
	}
	if _, ok := l.Inv.NetworkInfo[args[0]]; !ok {
		return fmt.Errorf("network %q is not declared in networks.yaml", args[0])
	}
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	for _, r := range derive.Reservations(l.Inv, args[0]) {
		fmt.Fprintf(w, "%s\t%s\t%s\n", r.Address, r.MAC, r.ID)
	}
	return w.Flush()
}

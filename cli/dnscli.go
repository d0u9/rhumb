package cli

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/d0u9/rhumb/confgen"
	"github.com/d0u9/rhumb/derive"
	"github.com/d0u9/rhumb/engine"
	"github.com/d0u9/rhumb/validate"
)

// DNS exports one network's desired records without contacting a DNS provider.
func DNS(in io.Reader, out io.Writer, args []string, flags map[string]string, global Settings) error {
	if len(args) != 1 {
		return fmt.Errorf("give one network: dns <network>")
	}
	if global.Root == "" {
		return fmt.Errorf("no generator root given")
	}
	l, err := engine.Load(global.Root)
	if err != nil {
		return err
	}
	if _, ok := l.Inv.NetworkInfo[args[0]]; !ok {
		return fmt.Errorf("network %q is not declared in networks.yaml", args[0])
	}
	if broken := BrokenFiles(l.Inv); len(broken) > 0 {
		return fmt.Errorf("invalid inventory: %s", broken[0])
	}
	if issues := validate.Validate(l.Inv, l.Manifests, l.Exports, l.Derived, nil); len(issues) > 0 {
		return fmt.Errorf("invalid inventory: %s", issues[0].Message)
	}
	services := map[string]string{}
	for _, n := range l.Inv.Nodes {
		for _, inst := range n.Instances {
			services[inst.ID] = inst.Service
		}
	}
	tables, issues := derive.Names(l.Inv, func(id string) bool {
		m := l.Manifests[services[id]]
		return m.FansOut() && m.DispatchesBy() == confgen.DispatchName
	})
	if len(issues) > 0 {
		return fmt.Errorf("invalid DNS: %s", issues[0])
	}
	records := tables[args[0]]
	if records == nil {
		records = []derive.Name{}
	}
	result := struct {
		Network string        `json:"network"`
		Records []derive.Name `json:"records"`
	}{args[0], records}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	_, err = out.Write(append(data, '\n'))
	return err
}

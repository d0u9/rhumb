package engine

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/d0u9/rhumb/inventory"
)

// File is one target's rendered bytes, at the path they belong under
// within an export — see docs/export.md#what-is-written.
type File struct {
	Path  string
	Bytes []byte
	// Executable is set for a file whose name says it is run rather than
	// read — a deployment's install script. It is derived from the output
	// name, so a template author writes no mode.
	Executable bool
}

// Mode is the mode a rendered file is published under. A rendered
// configuration carries credentials, so 0600 stays the rule; a script
// carries none and is written to be run, so it gets the owner's execute bit
// and nothing more.
func (f File) Mode() os.FileMode {
	if f.Executable {
		return 0o700
	}
	return 0o600
}

// RenderAll renders every instance, in order, stopping at the first failure:
// an export renders every target first and publishes only once all of them
// have rendered, so a half-written result is not among the outcomes.
func (m Renderer) RenderAll(instances []string) ([]File, error) {
	files := make([]File, 0, len(instances))
	for _, instance := range instances {
		t, err := m.findTarget(instance)
		if err != nil {
			return nil, err
		}
		// A deployment is filed under the service it runs; a file written
		// for a person, under the service and the way it was written, since
		// an export's name is unique only within its service and two of them
		// would otherwise share a directory. Both sit under the node or the
		// person the bundle is for.
		kind := t.Service
		if t.Export != "" {
			kind = t.Service + "-" + t.Export
		}
		rendered, err := m.RenderTarget(instance)
		if err != nil {
			return nil, err
		}
		for _, a := range rendered {
			bytes := a.Bytes
			if strings.EqualFold(filepath.Ext(a.Output), ".json") {
				bytes = IndentJSON(bytes)
			}
			files = append(files, File{
				Path:       filepath.Join(t.Node, kind, inventory.LocalName(instance), a.Output),
				Bytes:      bytes,
				Executable: a.Executable,
			})
		}

		// A containerised instance of a service that declares a deploy/
		// writes a second file beside the first: what starts the program,
		// next to how the program behaves. Both are rendered from one
		// `ports` field, which is what makes the port mapping in it derived
		// rather than maintained by hand.
		deploy, err := m.deployFor(instance)
		if err != nil {
			return nil, err
		}
		for _, d := range deploy {
			files = append(files, File{
				Path:       filepath.Join(t.Node, kind, inventory.LocalName(instance), d.Output),
				Bytes:      d.Bytes,
				Executable: d.Executable,
			})
		}

		// Every node instance, containerised or not, also writes the
		// manifest: the derived values a deployment tool needs, so that the
		// tool never reads the inventory.
		manifest, err := m.manifestFor(instance, append(rendered, deploy...))
		if err != nil {
			return nil, err
		}
		if manifest != nil {
			files = append(files, File{
				Path:  filepath.Join(t.Node, kind, inventory.LocalName(instance), ManifestFile),
				Bytes: manifest,
			})
		}
	}
	return files, nil
}

// IndentJSON lays out a rendered .json file two spaces per level, keeping
// the keys in the order the template wrote them. A template renders JSON as
// it is easiest to write, not to read, and an export is read by a person
// before it is pasted anywhere. Output that does not parse is left as it is:
// the file is still what the template produced, and the server reading it
// reports the error.
func IndentJSON(data []byte) []byte {
	var buf bytes.Buffer
	if err := json.Indent(&buf, bytes.TrimSpace(data), "", "  "); err != nil {
		return data
	}
	buf.WriteByte('\n')
	return buf.Bytes()
}

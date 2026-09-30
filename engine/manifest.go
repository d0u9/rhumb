package engine

import (
	"bytes"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"

	"github.com/d0u9/rhumb/confgen"
	"github.com/d0u9/rhumb/inventory"
	"github.com/d0u9/rhumb/render"
)

// ManifestFile is the name of the file every node instance's export carries
// beside its configuration: what a deployment tool reads instead of the
// inventory. See plans/deploy.md in the conf repository for the fields.
const ManifestFile = "manifest.yaml"

// ManifestSchema is the version of the manifest's shape. A reader refuses a
// schema it does not know rather than guessing at a field.
const ManifestSchema = 1

type DeployManifest struct {
	Schema   int    `yaml:"schema"`
	Node     string `yaml:"node"`
	Instance string `yaml:"instance"`
	Service  string `yaml:"service"`
	Runtime  string `yaml:"runtime"`
	Platform string `yaml:"platform,omitempty"`
	Download string `yaml:"download,omitempty"`
	Root     string `yaml:"root,omitempty"`
	// Dir is where a host instance is installed on the machine.
	Dir      string                     `yaml:"dir,omitempty"`
	Files    []manifestFile             `yaml:"files"`
	Ports    []manifestPort             `yaml:"ports,omitempty"`
	Networks []manifestNetwork          `yaml:"networks,omitempty"`
	Accounts map[string]manifestAccount `yaml:"accounts,omitempty"`
	Deploy   map[string]any             `yaml:"deploy,omitempty"`
	// Container is how a containerised instance of a service holding a
	// docker.yaml is started.
	Container *manifestContainer `yaml:"container,omitempty"`
}

type manifestFile struct {
	Path       string `yaml:"path"`
	Executable bool   `yaml:"executable,omitempty"`
	// Place is where a container's file is installed: relative to the
	// container's dir, or an absolute path on the machine. Empty with Mode
	// set means the file is not installed.
	Place string `yaml:"place,omitempty"`
	Mode  string `yaml:"mode,omitempty"`
}

type manifestPort struct {
	Name      string   `yaml:"name"`
	Port      int      `yaml:"port"`
	Bind      []string `yaml:"bind"`
	HostPort  int      `yaml:"host_port,omitempty"`
	Published string   `yaml:"published,omitempty"`
	Transport string   `yaml:"transport,omitempty"`
}

type manifestNetwork struct {
	Name    string `yaml:"name"`
	Subnet  string `yaml:"subnet"`
	Gateway string `yaml:"gateway,omitempty"`
	Address string `yaml:"address,omitempty"`
}

type manifestAccount struct {
	UID   int    `yaml:"uid"`
	GID   int    `yaml:"gid"`
	Group string `yaml:"group"`
}

// manifestFor renders instance's manifest from the same values its
// configuration and deployment files were rendered from, given the files
// already rendered for it. It returns nil for a file written for a person,
// which is not deployed.
func (m Renderer) manifestFor(instance string, files []artefact) ([]byte, error) {
	t, err := m.findTarget(instance)
	if err != nil {
		return nil, err
	}
	if t.Export != "" {
		return m.profileManifest(instance, t, files)
	}
	inst := m.InstanceByID(instance)
	if inst == nil {
		return nil, nil
	}
	var node inventory.Node
	for _, n := range m.Data.Inv.Nodes {
		if n.Broken == "" && n.ID == t.Node {
			node = n
		}
	}

	man := DeployManifest{
		Schema:   ManifestSchema,
		Node:     t.Node,
		Instance: inventory.LocalName(instance),
		Service:  t.Service,
		Runtime:  inst.RuntimeOr(),
		Platform: node.Platform,
		Download: node.Download,
		Files:    make([]manifestFile, 0, len(files)),
	}
	for _, f := range files {
		man.Files = append(man.Files, manifestFile{Path: f.Output, Executable: f.Executable})
	}

	numbers := inst.Ports.Numbers()
	mappings := m.Data.Derived.Mappings(m.Data.Inv, instance)
	published := m.publishedFor(instance)
	names := make([]string, 0, len(numbers))
	for name := range numbers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		p := manifestPort{Name: name, Port: numbers[name], Bind: []string{}, Published: published[name]}
		if hm, ok := mappings[name]; ok {
			p.Bind = append(p.Bind, hm.Addresses...)
			if hm.Number != p.Port {
				p.HostPort = hm.Number
			}
		}
		man.Ports = append(man.Ports, p)
	}

	for _, c := range node.JoinedContainers(*inst) {
		man.Networks = append(man.Networks, manifestNetwork{
			Name: c.Name, Subnet: c.Subnet, Gateway: c.Gateway, Address: inst.Containers[c.Name],
		})
	}

	if inst.Containerised() && len(node.Accounts) > 0 {
		man.Accounts = map[string]manifestAccount{}
		for name, a := range node.Accounts {
			gid, group := a.GID, a.Group
			if gid == 0 {
				gid = a.UID
			}
			if group == "" {
				group = name
			}
			man.Accounts[name] = manifestAccount{UID: a.UID, GID: gid, Group: group}
		}
	}

	if dir, ok := m.Data.DeployDirs[t.Service]; ok && inst.Containerised() {
		path := filepath.Join(m.RootPath, dir, confgen.DefaultsFilename)
		defaults, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("reading defaults %s: %w", path, err)
		}
		overlay := inst.Deploy
		if overlay == nil {
			overlay = map[string]any{}
		}
		values, err := render.DeployValues(defaults, overlay)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", instance, err)
		}
		if root, ok := values["root"].(string); ok {
			man.Root = root
			delete(values, "root")
		}
		if len(values) > 0 {
			man.Deploy = values
		}
	}

	if man.Runtime == inventory.RuntimeHost {
		dir, _ := inst.Deploy["dir"].(string)
		if dir == "" {
			dir = path.Join(DefaultHostRoot, t.Service)
		}
		if !path.IsAbs(dir) {
			return nil, fmt.Errorf("%s: deploy dir %q is not absolute", instance, dir)
		}
		man.Dir = dir
	}

	if docker, ok := m.Data.Dockers[t.Service]; ok && inst.Containerised() {
		if err := m.containerFor(instance, *inst, node, docker, &man); err != nil {
			return nil, err
		}
	}

	return encodeManifest(instance, man)
}

func encodeManifest(instance string, man DeployManifest) ([]byte, error) {
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(man); err != nil {
		return nil, fmt.Errorf("%s: manifest: %w", instance, err)
	}
	return out.Bytes(), nil
}

// profileManifest is the manifest of a device profile that names the program
// running its file, and nil for every other derived file, which is for a
// person. The profile's file is that program's whole configuration on the
// device: the program listens where the file says, so the manifest carries
// no ports, networks or accounts.
func (m Renderer) profileManifest(instance string, t targetRef, files []artefact) ([]byte, error) {
	var runs string
	for _, ci := range m.Data.Derived.ExportInstances {
		if ci.ID != instance || ci.Profile == "" {
			continue
		}
		for _, n := range m.Data.Inv.Nodes {
			if n.Broken == "" && n.ID == ci.Node {
				runs = n.Profiles[ci.Profile].Runs
			}
		}
	}
	if runs == "" {
		return nil, nil
	}
	man := DeployManifest{
		Schema:   ManifestSchema,
		Node:     t.Node,
		Instance: inventory.LocalName(instance),
		Service:  runs,
		Runtime:  inventory.RuntimeHost,
		Files:    make([]manifestFile, 0, len(files)),
	}
	for _, f := range files {
		man.Files = append(man.Files, manifestFile{Path: f.Output, Executable: f.Executable})
	}
	return encodeManifest(instance, man)
}

package engine

import (
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/d0u9/rhumb/confgen"
	"github.com/d0u9/rhumb/inventory"
	"github.com/d0u9/rhumb/render"
)

// DefaultDockerRoot is where an instance's directory is when its `deploy`
// names no `dir`: one directory per service under it.
const DefaultDockerRoot = "/srv/docker"

// manifestContainer is how a containerised instance is started, resolved
// from its service's docker.yaml and its own `deploy`: everything the
// deployment tool needs to write the compose file and install the files,
// with no expression left in it.
type manifestContainer struct {
	// Dir is the instance's directory on the machine, and the compose
	// project's: its name is the project's name.
	Dir         string            `yaml:"dir"`
	Name        string            `yaml:"name"`
	Hostname    string            `yaml:"hostname"`
	Image       string            `yaml:"image"`
	Restart     string            `yaml:"restart,omitempty"`
	User        string            `yaml:"user,omitempty"`
	DNS         []string          `yaml:"dns,omitempty"`
	EnvFiles    []string          `yaml:"env_files,omitempty"`
	Environment map[string]string `yaml:"environment,omitempty"`
	Mounts      []manifestMount   `yaml:"mounts,omitempty"`
	// Volumes are named volumes, by name, to the container path.
	Volumes    map[string]string `yaml:"volumes,omitempty"`
	Privileges map[string]any    `yaml:"privileges,omitempty"`
	Reload     []string          `yaml:"reload,omitempty"`
	Setup      string            `yaml:"setup,omitempty"`
}

// manifestMount is one bind mount. Create marks a directory of the
// instance's own that the deployment creates; any other source must already
// be there, since creating a mount point on a machine whose disk is not
// mounted writes to the wrong disk.
type manifestMount struct {
	Source      string `yaml:"source"`
	Target      string `yaml:"target"`
	ReadOnly    bool   `yaml:"ro,omitempty"`
	Propagation string `yaml:"propagation,omitempty"`
	Create      bool   `yaml:"create,omitempty"`
	// Mode is a created directory's permission bits, when not the
	// default.
	Mode string `yaml:"mode,omitempty"`
}

// dockerView renders the expressions of one instance's docker.yaml: each
// string value is a template executed with the instance's deployment view,
// the same one a deploy template used to see.
type dockerView struct {
	in render.Input
}

func (v dockerView) str(s string) (string, error) {
	if !strings.Contains(s, "{{") {
		return s, nil
	}
	in := v.in
	in.Template = s
	out, err := render.Render(in)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// containerFor resolves instance's container from its service's docker.yaml
// and fills files' places and ports' transports.
func (m Renderer) containerFor(instance string, inst inventory.Instance, node inventory.Node, docker confgen.Docker, man *DeployManifest) error {
	fail := func(format string, args ...any) error {
		return fmt.Errorf("%s: %s: %s", instance, confgen.DockerFilename, fmt.Sprintf(format, args...))
	}
	overlay := inst.Deploy
	if overlay == nil {
		overlay = map[string]any{}
	}
	if err := confgen.CheckDeployKeys("deploy", overlay); err != nil {
		return fmt.Errorf("%s: %w", instance, err)
	}
	defaults, err := yaml.Marshal(docker.Defaults)
	if err != nil {
		return err
	}
	values, err := render.DeployValues(defaults, overlay)
	if err != nil {
		return fmt.Errorf("%s: %w", instance, err)
	}

	instanceMap := instanceValues(inst, node)
	instanceMap["runtime"] = inst.RuntimeOr()
	instanceMap["deploy"] = overlay
	var subnets []any
	for _, c := range node.JoinedContainers(inst) {
		subnets = append(subnets, c.Subnet)
	}
	instanceMap["subnets"] = subnets
	mapping := map[string]render.Mapping{}
	for port, hm := range m.Data.Derived.Mappings(m.Data.Inv, instance) {
		mapping[port] = render.Mapping{Addresses: hm.Addresses, Number: hm.Number}
	}
	view := dockerView{in: render.Input{
		Target:         render.Target{Service: inst.Service, Instance: inventory.LocalName(instance)},
		Defaults:       defaults,
		DefaultsKind:   confgen.DefaultsDocument,
		Instance:       instanceMap,
		Overlay:        overlay,
		Node:           nodeValues(node),
		Published:      m.publishedFor(instance),
		PublishedNames: m.publishedNamesFor(instance),
		Names:          m.names(node.ID),
		Mapping:        mapping,
	}}

	c := manifestContainer{}
	str := func(key string) (string, error) {
		switch v := values[key].(type) {
		case nil:
			return "", nil
		case string:
			return view.str(v)
		default:
			return "", fail("deploy %s: %v is not a string", key, v)
		}
	}
	if c.Dir, err = str("dir"); err != nil {
		return err
	}
	if c.Dir == "" {
		c.Dir = path.Join(DefaultDockerRoot, inst.Service)
	}
	if !path.IsAbs(c.Dir) {
		return fail("dir %q is not absolute", c.Dir)
	}
	if c.Name, err = str("container_name"); err != nil {
		return err
	}
	if c.Name == "" {
		c.Name = inventory.LocalName(instance)
	}
	if c.Hostname, err = str("hostname"); err != nil {
		return err
	}
	if c.Hostname == "" {
		c.Hostname = c.Name
	}
	if c.Image, err = str("image"); err != nil {
		return err
	}
	if c.Image == "" {
		return fail("no image: set defaults.image, or the instance's deploy.image")
	}
	if c.Restart, err = str("restart"); err != nil {
		return err
	}
	account, err := str("account")
	if err != nil {
		return err
	}
	if account != "" {
		a, ok := node.Accounts[account]
		if !ok {
			return fail("account %q is not in node %s's accounts", account, node.ID)
		}
		gid := a.GID
		if gid == 0 {
			gid = a.UID
		}
		c.User = strconv.Itoa(a.UID) + ":" + strconv.Itoa(gid)
	}
	if dns, ok := values["dns"].([]any); ok {
		for _, d := range dns {
			c.DNS = append(c.DNS, fmt.Sprint(d))
		}
	}

	// Ports: every one the instance listens on has a transport.
	for i, p := range man.Ports {
		t, ok := docker.Ports[p.Name]
		if !ok {
			return fail("port %s has no transport in ports", p.Name)
		}
		man.Ports[i].Transport = t
	}

	// Files: each rendered file's place in the instance's directory, or on
	// the machine, and the mounts that reach them.
	mountOf := map[string]string{}
	for name, mt := range docker.Mounts {
		for _, f := range mt.Files {
			mountOf[f] = name
		}
	}
	for i, f := range man.Files {
		spec := docker.Files[f.Path]
		mode := spec.Mode
		place := f.Path
		switch {
		case mountOf[f.Path] != "":
			place = mountOf[f.Path] + "/" + f.Path
			if mode == "" {
				mode = docker.Mounts[mountOf[f.Path]].Mode
			}
		case spec.EnvFile:
			c.EnvFiles = append(c.EnvFiles, f.Path)
			if mode == "" {
				mode = "0600"
			}
		case spec.Host != "":
			host, err := view.str(spec.Host)
			if err != nil {
				return err
			}
			if host == "" {
				// The expression chose nowhere: the file is not placed.
				place = ""
				break
			}
			place = path.Join(host, f.Path)
		case spec.Target != "":
			c.Mounts = append(c.Mounts, manifestMount{Source: path.Join(c.Dir, f.Path), Target: spec.Target, ReadOnly: true})
		}
		if mode == "" {
			mode = "0644"
		}
		man.Files[i].Place = place
		man.Files[i].Mode = mode
	}
	for name := range docker.Files {
		if !hasFile(man.Files, name) {
			return fail("files.%s: the service renders no such file", name)
		}
	}
	for _, name := range sortedKeys(docker.Mounts) {
		mt := docker.Mounts[name]
		for _, f := range mt.Files {
			if !hasFile(man.Files, f) {
				return fail("mounts.%s: the service renders no file %s", name, f)
			}
		}
		dirMode := ""
		if mt.Mode != "" && strings.HasSuffix(mt.Mode, "00") {
			dirMode = "0700"
		}
		c.Mounts = append(c.Mounts, manifestMount{Source: path.Join(c.Dir, name), Target: mt.Target, ReadOnly: !mt.Writable, Create: true, Mode: dirMode})
	}
	for _, name := range sortedKeys(docker.State) {
		s := docker.State[name]
		if s.Named {
			if c.Volumes == nil {
				c.Volumes = map[string]string{}
			}
			c.Volumes[name] = s.Target
			continue
		}
		c.Mounts = append(c.Mounts, manifestMount{Source: path.Join(c.Dir, name), Target: s.Target, Create: true})
	}

	if len(docker.Environment) > 0 {
		c.Environment = map[string]string{}
		for k, v := range docker.Environment {
			r, err := view.str(v)
			if err != nil {
				return err
			}
			c.Environment[k] = r
		}
	}

	var volumes []any
	for _, v := range docker.Volumes {
		volumes = append(volumes, v)
	}
	instanceVolumes, _ := values["volumes"].([]any)
	volumes = append(volumes, instanceVolumes...)
	for i, v := range volumes {
		mv, err := volumeOf(v, view)
		if err != nil {
			return fail("volumes[%d]: %v", i, err)
		}
		if mv.Source == "" {
			continue
		}
		c.Mounts = append(c.Mounts, mv)
	}

	c.Privileges = docker.Privileges
	c.Reload = docker.Reload
	if docker.Setup != "" {
		if !hasFile(man.Files, docker.Setup) {
			return fail("setup: the service renders no file %s", docker.Setup)
		}
		c.Setup = docker.Setup
	}
	man.Container = &c
	return nil
}

// volumeOf reads one entry of `volumes`: source, target, ro, propagation.
// A source rendering empty drops the entry, which is how a mount is
// conditional.
func volumeOf(v any, view dockerView) (manifestMount, error) {
	var mv manifestMount
	entry, ok := v.(map[string]any)
	if !ok {
		return mv, fmt.Errorf("%v is not a mapping of source, target, ro, propagation", v)
	}
	for key, val := range entry {
		switch key {
		case "source", "target", "propagation":
			s, ok := val.(string)
			if !ok {
				return mv, fmt.Errorf("%s: %v is not a string", key, val)
			}
			r, err := view.str(s)
			if err != nil {
				return mv, err
			}
			switch key {
			case "source":
				mv.Source = r
			case "target":
				mv.Target = r
			default:
				mv.Propagation = r
			}
		case "ro":
			b, ok := val.(bool)
			if !ok {
				return mv, fmt.Errorf("ro: %v is not true or false", val)
			}
			mv.ReadOnly = b
		default:
			return mv, fmt.Errorf("unknown key %s", key)
		}
	}
	if mv.Source != "" && mv.Target == "" {
		return mv, fmt.Errorf("target is required")
	}
	return mv, nil
}

func hasFile(files []manifestFile, name string) bool {
	for _, f := range files {
		if f.Path == name {
			return true
		}
	}
	return false
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

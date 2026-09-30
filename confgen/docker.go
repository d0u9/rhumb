package confgen

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// DockerFilename is a service's description of how its program runs in a
// container: the image, where the container reads each rendered file, what
// state it keeps. It is data, not a template — the compose file is
// generated from it by the deployment tool — so every service's containers
// are started the same way and a service author writes no compose at all.
// Only a string value may hold an expression, and it is rendered with the
// instance's view. See plans/deploy.md in the conf repository.
const DockerFilename = "docker.yaml"

// Transports a port may declare in DockerFilename.
const (
	TransportTCP  = "tcp"
	TransportUDP  = "udp"
	TransportBoth = "both"
)

// DeployKeys are the keys an instance's `deploy` may write for a service
// that holds a DockerFilename, and what the service's own `defaults` may
// set. The set is fixed: the deployment tool reads every one, so a key it
// does not know is a typo rather than something a template might read.
var DeployKeys = []string{"image", "restart", "dir", "container_name", "hostname", "account", "dns", "volumes"}

// Privileges are the compose service keys a service may grant its
// container beyond what every container gets.
var Privileges = []string{"devices", "cap_add", "cap_drop", "sysctls", "read_only", "security_opt", "tmpfs"}

// Docker is a service's DockerFilename.
type Docker struct {
	// Ports gives each port the service listens on its transport. A port
	// the instance declares and this does not name is refused at render.
	Ports map[string]string `yaml:"ports"`
	// Files says where each rendered file goes, by output name. A file not
	// named here is still installed, into the instance's directory.
	Files map[string]DockerFile `yaml:"files"`
	// Mounts are directories of the instance's directory mounted into the
	// container, by subdirectory name, each holding some rendered files.
	Mounts map[string]DockerMount `yaml:"mounts"`
	// State is what the program keeps between runs, by name: a directory
	// of the instance's directory, or a named volume. Reinstalling leaves
	// it alone.
	State map[string]DockerState `yaml:"state"`
	// Volumes are machine paths every instance mounts, in the shape of an
	// instance's deploy.volumes, which are added after them. A source that
	// renders empty drops its entry.
	Volumes []map[string]any `yaml:"volumes"`
	// Environment is set in the container as written.
	Environment map[string]string `yaml:"environment"`
	// Privileges are merged into the compose service as written; only the
	// keys in Privileges are allowed.
	Privileges map[string]any `yaml:"privileges"`
	// Reload is run in the container after it is started, for a program
	// that does not reread its configuration on its own.
	Reload []string `yaml:"reload"`
	// Setup names a rendered file, a shell script, run on the machine
	// after the container is started, from the instance's directory, with
	// CONTAINER set to the container's name: what only this program needs
	// done once it is up, such as creating its accounts. It is rendered
	// like any other file of the service, so it reads the same values.
	Setup string `yaml:"setup"`
	// Compose names a rendered file that is the instance's compose file,
	// written by the service rather than generated: the way out for a
	// program the fields above cannot describe, such as several one-shot
	// containers. rhumb still creates the networks and the instance's
	// directory, installs the files and runs Setup; everything the compose
	// file itself says is the service's. With it, only Files, Setup,
	// Teardown, OneShot and a `dir` default may be written.
	Compose string `yaml:"compose"`
	// OneShot says the compose file starts nothing on its own: every
	// container is run by something else, such as a timer, so installing
	// does not bring the project up.
	OneShot bool `yaml:"oneshot"`
	// Teardown names a rendered file, a shell script, run from the
	// instance's directory before its containers are stopped on uninstall:
	// the undoing of what Setup did outside the project, such as a timer.
	Teardown string `yaml:"teardown"`
	// Defaults are what an instance's `deploy` overrides, key by key.
	Defaults map[string]any `yaml:"defaults"`
}

// DockerFile is where one rendered file goes.
type DockerFile struct {
	// Target mounts the file alone at this path in the container.
	Target string `yaml:"target"`
	// EnvFile hands the file to the container as its environment.
	EnvFile bool `yaml:"env_file"`
	// Host copies the file into this directory of the machine, outside the
	// instance's directory, and mounts nothing.
	Host string `yaml:"host"`
	// Mode is the file's permission bits, 0644 unless written. An env
	// file is 0600, since only the runtime reads it.
	Mode string `yaml:"mode"`
}

// DockerMount is one directory of rendered files mounted into the
// container.
type DockerMount struct {
	Target string   `yaml:"target"`
	Files  []string `yaml:"files"`
	// Writable mounts it read-write, for a program that writes its own
	// configuration back.
	Writable bool `yaml:"writable"`
	// Mode is the permission bits of the files in it, 0644 unless written.
	// A directory whose files no one else may read is itself 0700.
	Mode string `yaml:"mode"`
}

// DockerState is one place the program keeps state. Written as a bare
// string it is the container path, kept in a directory of the instance's
// directory.
type DockerState struct {
	Target string `yaml:"target"`
	// Named keeps it in a named volume rather than a directory.
	Named bool `yaml:"named"`
}

func (s *DockerState) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		s.Target = n.Value
		return nil
	}
	type plain DockerState
	return n.Decode((*plain)(s))
}

func loadDocker(path string) (*Docker, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var d Docker
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&d); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if err := d.check(); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return &d, nil
}

func (d Docker) check() error {
	for name, t := range d.Ports {
		switch t {
		case TransportTCP, TransportUDP, TransportBoth:
		default:
			return fmt.Errorf("ports.%s: %q is not tcp, udp or both", name, t)
		}
	}
	placed := map[string]string{}
	place := func(file, where string) error {
		if other, ok := placed[file]; ok {
			return fmt.Errorf("%s is placed twice: %s and %s", file, other, where)
		}
		placed[file] = where
		return nil
	}
	for name, f := range d.Files {
		n := 0
		for _, set := range []bool{f.Target != "", f.EnvFile, f.Host != ""} {
			if set {
				n++
			}
		}
		if n > 1 {
			return fmt.Errorf("files.%s: target, env_file and host exclude each other", name)
		}
		if n == 1 {
			if err := place(name, "files."+name); err != nil {
				return err
			}
		}
	}
	for name, m := range d.Mounts {
		if m.Target == "" {
			return fmt.Errorf("mounts.%s: target is required", name)
		}
		for _, f := range m.Files {
			if err := place(f, "mounts."+name); err != nil {
				return err
			}
		}
	}
	for _, script := range []struct{ key, name string }{{"setup", d.Setup}, {"teardown", d.Teardown}, {"compose", d.Compose}} {
		if script.name == "" {
			continue
		}
		if f, ok := d.Files[script.name]; ok && (f.Target != "" || f.EnvFile || f.Host != "") {
			return fmt.Errorf("%s: %s is placed elsewhere; it is used from the instance's directory", script.key, script.name)
		}
	}
	if d.Compose != "" {
		if err := d.checkCompose(); err != nil {
			return err
		}
	} else if d.OneShot || d.Teardown != "" {
		return fmt.Errorf("oneshot and teardown are only for a service that writes its own compose")
	}
	if d.Setup != "" {
		if f, ok := d.Files[d.Setup]; ok && (f.Target != "" || f.EnvFile || f.Host != "") {
			return fmt.Errorf("setup: %s is placed elsewhere; it runs from the instance's directory", d.Setup)
		}
		if _, ok := placed[d.Setup]; ok {
			return fmt.Errorf("setup: %s is in a mount; it runs from the instance's directory", d.Setup)
		}
	}
	for name, s := range d.State {
		if s.Target == "" {
			return fmt.Errorf("state.%s: target is required", name)
		}
		if _, ok := d.Mounts[name]; ok {
			return fmt.Errorf("state.%s: also a mount; the two share the instance's directory", name)
		}
	}
	for key := range d.Privileges {
		if !contains(Privileges, key) {
			return fmt.Errorf("privileges.%s: not one of %s", key, strings.Join(Privileges, ", "))
		}
	}
	return CheckDeployKeys("defaults", d.Defaults)
}

// checkCompose refuses, beside Compose, whatever would have been written into
// a generated compose file: with the service's own, it would be ignored.
func (d Docker) checkCompose() error {
	var set []string
	for key, on := range map[string]bool{
		"ports": len(d.Ports) > 0, "mounts": len(d.Mounts) > 0, "state": len(d.State) > 0,
		"volumes": len(d.Volumes) > 0, "environment": len(d.Environment) > 0,
		"privileges": len(d.Privileges) > 0, "reload": len(d.Reload) > 0,
	} {
		if on {
			set = append(set, key)
		}
	}
	for name, f := range d.Files {
		if f.Target != "" || f.EnvFile {
			set = append(set, "files."+name+" target or env_file")
		}
	}
	for key := range d.Defaults {
		if key != "dir" {
			set = append(set, "defaults."+key)
		}
	}
	if len(set) == 0 {
		return nil
	}
	sort.Strings(set)
	return fmt.Errorf("compose: the service writes its own compose file, so %s would be ignored", strings.Join(set, ", "))
}

// CheckDeployKeys refuses a key of an instance's `deploy`, or a service's
// docker defaults, that is not in DeployKeys.
func CheckDeployKeys(where string, m map[string]any) error {
	var bad []string
	for key := range m {
		if !contains(DeployKeys, key) {
			bad = append(bad, key)
		}
	}
	if len(bad) == 0 {
		return nil
	}
	sort.Strings(bad)
	return fmt.Errorf("%s: unknown %s; a container's deploy takes %s", where, strings.Join(bad, ", "), strings.Join(DeployKeys, ", "))
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

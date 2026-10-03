package deploy

import (
	"bytes"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"text/template"

	"gopkg.in/yaml.v3"
)

// Container is how a containerised instance is started, as the manifest
// resolved it: nothing in it is left to interpret.
type Container struct {
	Dir         string            `yaml:"dir"`
	Name        string            `yaml:"name"`
	Hostname    string            `yaml:"hostname"`
	Image       string            `yaml:"image"`
	Restart     string            `yaml:"restart"`
	User        string            `yaml:"user"`
	DNS         []string          `yaml:"dns"`
	EnvFiles    []string          `yaml:"env_files"`
	Environment map[string]string `yaml:"environment"`
	Mounts      []Mount           `yaml:"mounts"`
	Volumes     map[string]string `yaml:"volumes"`
	Privileges  map[string]any    `yaml:"privileges"`
	Reload      []string          `yaml:"reload"`
	Setup       string            `yaml:"setup"`
	// Compose names the file of the bundle that is the compose file, when
	// the service wrote its own; nothing above but Dir is then set.
	Compose  string `yaml:"compose"`
	OneShot  bool   `yaml:"oneshot"`
	Teardown string `yaml:"teardown"`
}

// Mount is one bind mount. Create marks a directory of the instance's own
// that installing creates; any other source must already exist.
type Mount struct {
	Source      string `yaml:"source"`
	Target      string `yaml:"target"`
	ReadOnly    bool   `yaml:"ro"`
	Propagation string `yaml:"propagation"`
	Create      bool   `yaml:"create"`
	Mode        string `yaml:"mode"`
}

// Port is one port the instance listens on and where the machine publishes
// it.
type Port struct {
	Name      string   `yaml:"name"`
	Port      int      `yaml:"port"`
	Bind      []string `yaml:"bind"`
	HostPort  int      `yaml:"host_port"`
	Transport string   `yaml:"transport"`
	Published string   `yaml:"published"`
}

// Network is one container network the instance joins.
type Network struct {
	Name    string `yaml:"name"`
	Subnet  string `yaml:"subnet"`
	Gateway string `yaml:"gateway"`
	Address string `yaml:"address"`
}

// Compose is the compose file of a containerised instance: one project, one
// service, joined to networks that exist outside it. Every container rhumb
// starts is written this way, so a service's own knowledge is only what its
// manifest's container says.
func Compose(m Manifest) ([]byte, error) {
	c := m.Container
	if c == nil {
		return nil, fmt.Errorf("%s/%s: the manifest has no container", m.Node, m.Instance)
	}
	svc := map[string]any{
		"container_name": c.Name,
		"hostname":       c.Hostname,
		"image":          c.Image,
	}
	if c.Restart != "" {
		svc["restart"] = c.Restart
	}
	if c.User != "" {
		svc["user"] = c.User
	}
	if len(c.DNS) > 0 {
		svc["dns"] = c.DNS
	}
	if len(c.EnvFiles) > 0 {
		var files []string
		for _, f := range c.EnvFiles {
			files = append(files, path.Join(c.Dir, f))
		}
		svc["env_file"] = files
	}
	if len(c.Environment) > 0 {
		svc["environment"] = c.Environment
	}

	var volumes []any
	for _, mt := range c.Mounts {
		v := map[string]any{"type": "bind", "source": mt.Source, "target": mt.Target}
		if mt.ReadOnly {
			v["read_only"] = true
		}
		if mt.Propagation != "" {
			v["bind"] = map[string]any{"propagation": mt.Propagation}
		}
		volumes = append(volumes, v)
	}
	for _, name := range sortedKeys(c.Volumes) {
		volumes = append(volumes, map[string]any{"type": "volume", "source": name, "target": c.Volumes[name]})
	}
	if len(volumes) > 0 {
		svc["volumes"] = volumes
	}

	var ports []string
	for _, p := range m.Ports {
		host := p.Port
		if p.HostPort != 0 {
			host = p.HostPort
		}
		var protos []string
		switch p.Transport {
		case "tcp", "udp":
			protos = []string{p.Transport}
		case "both":
			protos = []string{"tcp", "udp"}
		default:
			return nil, fmt.Errorf("%s/%s: port %s has no transport", m.Node, m.Instance, p.Name)
		}
		for _, addr := range p.Bind {
			for _, proto := range protos {
				ports = append(ports, fmt.Sprintf("%s:%d:%d/%s", addr, host, p.Port, proto))
			}
		}
	}
	if len(ports) > 0 {
		svc["ports"] = ports
	}

	joins := map[string]any{}
	networks := map[string]any{}
	for _, n := range m.Networks {
		join := map[string]any{"aliases": []string{m.Instance}}
		if n.Address != "" {
			join["ipv4_address"] = n.Address
		}
		joins[n.Name] = join
		networks[n.Name] = map[string]any{"external": true, "name": n.Name}
	}
	if len(joins) > 0 {
		svc["networks"] = joins
	}

	for k, v := range c.Privileges {
		if _, taken := svc[k]; taken {
			return nil, fmt.Errorf("%s/%s: privilege %s is a key rhumb writes", m.Node, m.Instance, k)
		}
		svc[k] = v
	}

	doc := map[string]any{"services": map[string]any{c.Name: svc}}
	if len(networks) > 0 {
		doc["networks"] = networks
	}
	if len(c.Volumes) > 0 {
		named := map[string]any{}
		for name := range c.Volumes {
			named[name] = map[string]any{}
		}
		doc["volumes"] = named
	}
	var out bytes.Buffer
	out.WriteString("# Written by rhumb deploy for " + m.Node + "/" + m.Instance + ". Do not edit: rebuild.\n")
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// buildDocker writes the bundle of a containerised instance: its files, the
// compose file, and a ctl that installs them into the container's directory
// and starts it. The bundle is only the carrier; what runs lives in that
// directory, so the bundle can be rebuilt or deleted freely.
func buildDocker(m Manifest, src, dst, prefix string, relabel bool) error {
	var compose []byte
	var err error
	if m.Container != nil && m.Container.Compose != "" {
		compose, err = os.ReadFile(filepath.Join(src, m.Container.Compose))
	} else {
		compose, err = Compose(m)
	}
	if err != nil {
		return err
	}
	if err := checkReplace(dst, m, Label(m, prefix), relabel); err != nil {
		return err
	}
	if err := os.RemoveAll(filepath.Join(dst, "files")); err != nil {
		return err
	}
	for _, f := range m.Files {
		if err := copyFile(filepath.Join(src, f.Path), filepath.Join(dst, "files", f.Path), 0o600); err != nil {
			return err
		}
	}
	if err := copyFile(filepath.Join(src, ManifestFile), filepath.Join(dst, ManifestFile), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dst, "compose.yaml"), compose, 0o644); err != nil {
		return err
	}
	ctl, err := renderDockerCtl(m, prefix)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dst, "ctl"), ctl, 0o755)
}

var dockerCtlTemplate = template.Must(template.New("ctl").Funcs(template.FuncMap{"q": shQuote}).Parse(ctlDocker))

func renderDockerCtl(m Manifest, prefix string) ([]byte, error) {
	type place struct{ From, To, Mode string }
	var places []place
	for _, f := range m.Files {
		if f.Place == "" {
			continue
		}
		to := f.Place
		if !path.IsAbs(to) {
			to = path.Join(m.Container.Dir, to)
		}
		places = append(places, place{From: f.Path, To: to, Mode: f.Mode})
	}
	type create struct{ Dir, Mode string }
	var creates []create
	for _, mt := range m.Container.Mounts {
		if mt.Create {
			creates = append(creates, create{Dir: mt.Source, Mode: mt.Mode})
		}
	}
	sort.Slice(creates, func(i, j int) bool { return creates[i].Dir < creates[j].Dir })
	var out bytes.Buffer
	err := dockerCtlTemplate.Execute(&out, map[string]any{
		"M": m, "C": m.Container, "Label": Label(m, prefix), "Places": places, "Creates": creates,
		"Reload": strings.Join(quoteAll(m.Container.Reload), " "),
	})
	return out.Bytes(), err
}

func shQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func quoteAll(args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = shQuote(a)
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

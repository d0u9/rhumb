package deploy

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Service is how a service's program is fetched and started on a machine:
// what the manifest does not say, because it is the same for every instance
// of the service.
type Service struct {
	Binary struct {
		Name string `yaml:"name"`
		// Exactly one of the sources is written.
		Sources `yaml:",inline"`
	} `yaml:"binary"`
	Command []string `yaml:"command"`
	// EnvFiles are rendered files, in conf/, that hold the program's
	// environment as KEY=VALUE lines. Only the service manager reads them,
	// so a secret in one never appears in the unit or plist.
	EnvFiles []string `yaml:"env_files"`
	Expose   []string `yaml:"expose"`
	// Capabilities are the Linux capabilities the program is given beyond
	// an ordinary user's, such as CAP_NET_BIND_SERVICE to listen below
	// 1024. It gets no other: a unit without any keeps none.
	Capabilities []string `yaml:"capabilities"`
	// SelfSigned is a certificate ctl install makes on the machine, in var/,
	// when it is missing and the rendered configuration names it: for a
	// server whose clients pin or skip verification rather than trust a CA.
	SelfSigned *SelfSigned `yaml:"self_signed"`
}

// SelfSigned names the pair's files, relative to var/. The certificate is
// for the instance's published names, valid ten years, and never replaced.
type SelfSigned struct {
	Cert string `yaml:"cert"`
	Key  string `yaml:"key"`
}

// Sources are the ways a service's binary may reach a machine. A
// definition writes one.
type Sources struct {
	// Release downloads a published archive when the bundle is built, so
	// the bundle carries the program and the machine needs no network.
	Release *Release `yaml:"release"`
	// Apt is the package that apt installs when the bundle is installed.
	Apt string `yaml:"apt"`
	// Path is where the program already is on the machine; nothing
	// installs it.
	Path string `yaml:"path"`
}

// Release is a published archive holding the binary at its top level.
type Release struct {
	// GitHub is owner/repo; with it, Version may be "latest", resolved
	// when the bundle is built.
	GitHub  string `yaml:"github"`
	Version string `yaml:"version"`
	// Tag is the release's tag, {version} filled in; v{version} when
	// empty. A "latest" version is what the tag holds at {version}.
	Tag string `yaml:"tag"`
	// URL is the archive's address, {version} and {target} filled in.
	// With GitHub it may be only the asset's name. An address that names
	// no tar archive is the binary itself.
	URL string `yaml:"url"`
	// Targets maps GOOS/GOARCH to the release's name for the platform.
	Targets map[string]string `yaml:"targets"`
}

// Source names.
const (
	SourceRelease = "release"
	SourceApt     = "apt"
	SourcePath    = "path"
)

// Names is the sources written, in a fixed order.
func (s Sources) Names() []string {
	var out []string
	if s.Release != nil {
		out = append(out, SourceRelease)
	}
	if s.Apt != "" {
		out = append(out, SourceApt)
	}
	if s.Path != "" {
		out = append(out, SourcePath)
	}
	return out
}

//go:embed services/*.yaml
var builtin embed.FS

// LoadService reads name's definition from dir when dir is given and holds
// one, and from the definitions built into rhumb otherwise.
func LoadService(name, dir string) (Service, error) {
	var s Service
	file := name + ".yaml"
	data, err := fs.ReadFile(builtin, "services/"+file)
	if dir != "" {
		if own, ownErr := os.ReadFile(filepath.Join(dir, file)); ownErr == nil {
			data, err = own, nil
		} else if !errors.Is(ownErr, fs.ErrNotExist) {
			return s, ownErr
		}
	}
	if err != nil {
		return s, fmt.Errorf("service %q has no deploy definition", name)
	}
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&s); err != nil {
		return s, fmt.Errorf("service %q: %w", name, err)
	}
	if s.Binary.Name == "" || len(s.Command) == 0 {
		return s, fmt.Errorf("service %q: binary.name and command are required", name)
	}
	for _, c := range s.Capabilities {
		if !strings.HasPrefix(c, "CAP_") || strings.ContainsAny(c, " \t\n") {
			return s, fmt.Errorf("service %q: capability %q is not a CAP_ name", name, c)
		}
	}
	if c := s.SelfSigned; c != nil && (c.Cert == "" || c.Key == "" || filepath.IsAbs(c.Cert) || filepath.IsAbs(c.Key)) {
		return s, fmt.Errorf("service %q: self_signed needs cert and key, both relative to var/", name)
	}
	if names := s.Binary.Names(); len(names) != 1 {
		return s, fmt.Errorf("service %q: binary writes %d of release, apt and path; it takes one", name, len(names))
	}
	return s, nil
}

// Source is the one source the definition writes.
func (s Service) Source() string { return s.Binary.Names()[0] }

// Resolve fills a "latest" version in from GitHub.
func (r Release) Resolve() (Release, error) {
	if r.Version != "latest" {
		return r, nil
	}
	if r.GitHub == "" {
		return r, errors.New("release: version latest needs github")
	}
	resp, err := http.Get("https://api.github.com/repos/" + r.GitHub + "/releases/latest")
	if err != nil {
		return r, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return r, fmt.Errorf("latest release of %s: %s", r.GitHub, resp.Status)
	}
	var rel struct {
		Tag string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return r, err
	}
	if rel.Tag == "" {
		return r, fmt.Errorf("latest release of %s has no tag", r.GitHub)
	}
	prefix, suffix, _ := strings.Cut(r.TagPattern(), "{version}")
	if !strings.HasPrefix(rel.Tag, prefix) || !strings.HasSuffix(rel.Tag, suffix) {
		return r, fmt.Errorf("latest release of %s is tagged %s, not %s", r.GitHub, rel.Tag, r.TagPattern())
	}
	r.Version = strings.TrimSuffix(strings.TrimPrefix(rel.Tag, prefix), suffix)
	return r, nil
}

// TagPattern is the release's tag with {version} in it.
func (r Release) TagPattern() string {
	if r.Tag == "" {
		return "v{version}"
	}
	return r.Tag
}

// Archive says whether the release is a tar archive holding the binary,
// rather than the binary itself.
func (r Release) Archive() bool {
	return strings.Contains(path.Base(r.URL), ".tar") || strings.HasSuffix(r.URL, ".tgz")
}

// URLFor is where the archive for platform ("darwin/arm64") is downloaded
// from. The version must already be resolved.
func (r Release) URLFor(platform string) (string, error) {
	target, ok := r.Targets[platform]
	if !ok {
		known := make([]string, 0, len(r.Targets))
		for p := range r.Targets {
			known = append(known, p)
		}
		sort.Strings(known)
		return "", fmt.Errorf("the release has no build for %s, only %s", platform, strings.Join(known, ", "))
	}
	url := r.URL
	if r.GitHub != "" && !strings.Contains(url, "://") {
		url = "https://github.com/" + r.GitHub + "/releases/download/" + r.TagPattern() + "/" + url
	}
	return strings.NewReplacer("{version}", r.Version, "{target}", target).Replace(url), nil
}

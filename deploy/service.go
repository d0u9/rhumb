package deploy

import (
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
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
		// Default is the source used when the build names none; the only
		// one when a single source is written.
		Default string  `yaml:"default"`
		Sources Sources `yaml:"sources"`
	} `yaml:"binary"`
	Command []string `yaml:"command"`
	// EnvFiles are rendered files, in conf/, that hold the program's
	// environment as KEY=VALUE lines. Only the service manager reads them,
	// so a secret in one never appears in the unit or plist.
	EnvFiles []string `yaml:"env_files"`
	Expose   []string `yaml:"expose"`
}

// Sources are the ways a service's binary may reach a machine. A build
// picks one; the others are only what could have been picked.
type Sources struct {
	// Release downloads a published archive when the bundle is built, so
	// the bundle carries the program and the machine needs no network.
	Release *Release `yaml:"release"`
	// Package installs the program with the machine's package manager when
	// the bundle is installed, by manager: apt, dnf, yum, apk, brew.
	Package map[string]string `yaml:"package"`
	// Path is where the program already is on the machine; nothing
	// installs it.
	Path string `yaml:"path"`
	// Script is shell run by ctl install, from the bundle's directory,
	// which leaves the program at bin/<name>.
	Script string `yaml:"script"`
}

// Release is a published archive holding the binary at its top level.
type Release struct {
	// GitHub is owner/repo; with it, Version may be "latest", resolved
	// when the bundle is built.
	GitHub  string `yaml:"github"`
	Version string `yaml:"version"`
	// URL is the archive's address, {version} and {target} filled in.
	// With GitHub it may be only the asset's name.
	URL string `yaml:"url"`
	// Targets maps GOOS/GOARCH to the release's name for the platform.
	Targets map[string]string `yaml:"targets"`
}

// Source names.
const (
	SourceRelease = "release"
	SourcePackage = "package"
	SourcePath    = "path"
	SourceScript  = "script"
)

// Names is the sources written, in a fixed order.
func (s Sources) Names() []string {
	var out []string
	if s.Release != nil {
		out = append(out, SourceRelease)
	}
	if len(s.Package) > 0 {
		out = append(out, SourcePackage)
	}
	if s.Path != "" {
		out = append(out, SourcePath)
	}
	if s.Script != "" {
		out = append(out, SourceScript)
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
	names := s.Binary.Sources.Names()
	if len(names) == 0 {
		return s, fmt.Errorf("service %q: binary.sources names none of release, package, path, script", name)
	}
	if s.Binary.Default == "" && len(names) == 1 {
		s.Binary.Default = names[0]
	}
	if _, err := s.Source(""); err != nil {
		return s, fmt.Errorf("service %q: %w", name, err)
	}
	return s, nil
}

// Source is the source a build uses: want when given, the default
// otherwise.
func (s Service) Source(want string) (string, error) {
	if want == "" {
		want = s.Binary.Default
	}
	names := s.Binary.Sources.Names()
	if want == "" {
		return "", fmt.Errorf("binary has sources %s and no default", strings.Join(names, ", "))
	}
	for _, n := range names {
		if n == want {
			return want, nil
		}
	}
	return "", fmt.Errorf("binary has no source %q; it has %s", want, strings.Join(names, ", "))
}

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
	r.Version = strings.TrimPrefix(rel.Tag, "v")
	return r, nil
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
		url = "https://github.com/" + r.GitHub + "/releases/download/v{version}/" + url
	}
	return strings.NewReplacer("{version}", r.Version, "{target}", target).Replace(url), nil
}

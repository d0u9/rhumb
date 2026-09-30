package deploy

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Service is how a service's program is fetched and started on a machine:
// what the manifest does not say, because it is the same for every instance
// of the service.
type Service struct {
	Binary struct {
		Name    string `yaml:"name"`
		Version string `yaml:"version"`
		URL     string `yaml:"url"`
		// Targets maps GOOS/GOARCH to the release's name for the platform.
		Targets map[string]string `yaml:"targets"`
	} `yaml:"binary"`
	Command []string `yaml:"command"`
	Expose  []string `yaml:"expose"`
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
	if err := yaml.Unmarshal(data, &s); err != nil {
		return s, fmt.Errorf("service %q: %w", name, err)
	}
	if s.Binary.Name == "" || len(s.Command) == 0 {
		return s, fmt.Errorf("service %q: binary.name and command are required", name)
	}
	return s, nil
}

// URL is where the binary for platform ("darwin/arm64") is downloaded from.
func (s Service) URL(platform string) (string, error) {
	target, ok := s.Binary.Targets[platform]
	if !ok {
		return "", fmt.Errorf("%s has no release for %s", s.Binary.Name, platform)
	}
	r := strings.NewReplacer("{version}", s.Binary.Version, "{target}", target)
	return r.Replace(s.Binary.URL), nil
}

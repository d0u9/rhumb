// Package deploy turns one exported instance into a bundle a machine can
// install: the program, its configuration, and a ctl script that registers
// it with the machine's service manager.
//
// It reads the export's manifest.yaml and nothing else of rhumb's: the
// manifest is the whole contract between the generator and this package, so
// a deployment never interprets the inventory. A test holds the import
// boundary.
package deploy

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// ManifestFile is the file an exported instance describes itself in.
const ManifestFile = "manifest.yaml"

// Schema is the manifest shape this package reads. A manifest of another
// schema is refused rather than guessed at.
const Schema = 1

// Manifest is the part of an exported instance's manifest a deployment
// reads. Fields this package does not use yet are left out.
type Manifest struct {
	Schema   int    `yaml:"schema"`
	Node     string `yaml:"node"`
	Instance string `yaml:"instance"`
	Service  string `yaml:"service"`
	Runtime  string `yaml:"runtime"`
	// Platform is the machine's GOOS/GOARCH when its node says.
	Platform string `yaml:"platform"`
	// Dir is where ctl install puts a Linux host bundle.
	Dir   string `yaml:"dir"`
	Files []struct {
		Path       string `yaml:"path"`
		Executable bool   `yaml:"executable"`
		Place      string `yaml:"place"`
		Mode       string `yaml:"mode"`
	} `yaml:"files"`
	Ports     []Port     `yaml:"ports"`
	Networks  []Network  `yaml:"networks"`
	Container *Container `yaml:"container"`
}

// ReadManifest reads the manifest of the exported instance in dir.
func ReadManifest(dir string) (Manifest, error) {
	var m Manifest
	data, err := os.ReadFile(filepath.Join(dir, ManifestFile))
	if err != nil {
		return m, err
	}
	return ParseManifest(data)
}

// ParseManifest reads a manifest from its bytes.
func ParseManifest(data []byte) (Manifest, error) {
	var m Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		return m, fmt.Errorf("%s: %w", ManifestFile, err)
	}
	if m.Schema != Schema {
		return m, fmt.Errorf("%s: schema %d, this build reads %d", ManifestFile, m.Schema, Schema)
	}
	if m.Node == "" || m.Instance == "" || m.Service == "" {
		return m, fmt.Errorf("%s: node, instance and service are required", ManifestFile)
	}
	return m, nil
}

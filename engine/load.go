package engine

import (
	"fmt"

	"github.com/d0u9/rhumb/confgen"
	"github.com/d0u9/rhumb/derive"
	"github.com/d0u9/rhumb/inventory"
)

// Loaded is the bootstrap every rhumb command starts from: the inventory,
// the service manifests, and their derivation. Inspect's views, the reports
// and export all read the same one.
type Loaded struct {
	Inv         *inventory.Root
	Manifests   map[string]confgen.Manifest
	Exports     map[string]confgen.Export
	ExportDirs  map[string]string
	ServiceDirs map[string]string
	// Deploys and deployDirs hold the deployment half of the services that
	// declare one, keyed by service name. Most services declare none.
	Deploys    map[string]confgen.Deploy
	DeployDirs map[string]string
	// Dockers holds the docker.yaml of the services that declare one: how
	// the deployment tool starts their containers.
	Dockers map[string]confgen.Docker
	// ServiceProblems names every service deployment file that would not
	// parse, which check reports; the service renders its configuration
	// regardless.
	ServiceProblems []string
	Derived         *derive.Model
}

// Load reads rootPath's inventory and services/, and derives from both.
func Load(rootPath string) (Loaded, error) {
	var l Loaded

	inv, err := inventory.Load(rootPath)
	if err != nil {
		return l, err
	}
	l.Inv = inv

	confRoot, err := confgen.Load(rootPath)
	if err != nil {
		return l, err
	}
	manifests := map[string]confgen.Manifest{}
	serviceDirs := map[string]string{}
	deploys := map[string]confgen.Deploy{}
	deployDirs := map[string]string{}
	dockers := map[string]confgen.Docker{}
	for _, svc := range confRoot.Services {
		if svc.DeployBroken != "" {
			l.ServiceProblems = append(l.ServiceProblems, fmt.Sprintf("service %s: %s", svc.Name, svc.DeployBroken))
		}
		if svc.DockerBroken != "" {
			l.ServiceProblems = append(l.ServiceProblems, fmt.Sprintf("service %s: %s", svc.Name, svc.DockerBroken))
		}
		if svc.Docker != nil {
			dockers[svc.Name] = *svc.Docker
		}
		if svc.Broken == "" {
			manifests[svc.Name] = svc.Manifest
			serviceDirs[svc.Name] = svc.Dir
		}
		if svc.Deploy != nil && svc.DeployBroken == "" {
			deploys[svc.Name] = *svc.Deploy
			deployDirs[svc.Name] = svc.DeployDir
		}
	}
	l.Manifests = manifests
	l.ServiceDirs = serviceDirs
	l.Deploys = deploys
	l.DeployDirs = deployDirs
	l.Dockers = dockers

	// An export is keyed by the service it writes out as well as its own
	// name: two services may both offer a "link", and they are two exports
	// rendering two different upstreams. See confgen.ExportKey.
	exports := map[string]confgen.Export{}
	exportDirs := map[string]string{}
	for _, def := range confRoot.Exports {
		if def.Broken == "" {
			exports[confgen.ExportKey(def.Service, def.Name)] = def.Export
			exportDirs[confgen.ExportKey(def.Service, def.Name)] = def.Dir
		}
	}
	l.Exports = exports
	l.ExportDirs = exportDirs

	derive.FillServiceDials(inv, manifests)
	derive.FillPrincipals(inv, manifests)
	model, err := derive.Derive(inv, manifests)
	if err != nil {
		return l, err
	}
	l.Derived = model

	return l, nil
}

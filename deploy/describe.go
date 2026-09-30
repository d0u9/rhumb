package deploy

import (
	"fmt"
	"runtime"
	"strings"
)

// Description is what building an instance's bundle would make, said
// before anything is built or downloaded.
type Description struct {
	// Manager is what runs the program: "docker compose", "systemd" or
	// "launchd".
	Manager string
	// Platform is the GOOS/GOARCH the bundle is for; empty for a container.
	Platform string
	// Binary says where the program comes from; empty for a container.
	Binary string
	// Err is why the bundle cannot be built, when it cannot.
	Err error
}

// Describe says what Build would make of the instance m with opt, without
// reading or writing anything but the service definitions.
func Describe(m Manifest, opt Options) Description {
	if m.Container != nil {
		return Description{Manager: "docker compose"}
	}
	var d Description
	if m.Runtime != "host" {
		d.Err = fmt.Errorf("runs in %s, and its service holds no docker.yaml", m.Runtime)
		return d
	}
	d.Platform = opt.Platform
	if d.Platform == "" {
		d.Platform = m.Platform
	}
	if d.Platform == "" {
		d.Platform = runtime.GOOS + "/" + runtime.GOARCH
	}
	switch strings.SplitN(d.Platform, "/", 2)[0] {
	case "linux":
		d.Manager = "systemd"
	case "darwin":
		d.Manager = "launchd"
	default:
		d.Err = fmt.Errorf("platform %s has no service manager rhumb registers with", d.Platform)
		return d
	}
	svc, err := LoadService(m.Service, opt.Services)
	if err != nil {
		d.Err = err
		return d
	}
	switch {
	case len(svc.EnvFiles) > 0 && d.Manager == "launchd":
		d.Err = fmt.Errorf("service %q: env_files is not supported by launchd bundles yet", m.Service)
	case len(svc.Capabilities) > 0 && d.Manager != "systemd":
		d.Err = fmt.Errorf("service %q: capabilities are Linux's; %s has none to give", m.Service, d.Platform)
	}
	switch {
	case opt.Binary != "":
		d.Binary = "local " + opt.Binary
	case svc.Binary.Release != nil:
		r := svc.Binary.Release
		d.Binary = svc.Binary.Name + " " + r.Version + " release, downloaded "
		if downloadFor(m, opt) != DownloadBuild {
			d.Binary += "on the machine"
		} else {
			d.Binary += "now"
		}
		if _, err := r.URLFor(d.Platform); err != nil {
			d.Err = err
		}
	case svc.Binary.Apt != "":
		d.Binary = "apt package " + svc.Binary.Apt
	case svc.Binary.Path != "":
		d.Binary = svc.Binary.Path + " on the machine"
	}
	return d
}

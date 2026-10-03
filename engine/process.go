package engine

import (
	"fmt"

	"github.com/d0u9/rhumb/inventory"
	"github.com/d0u9/rhumb/render"
	"github.com/d0u9/rhumb/secretstore"
)

// memberFor is what one member of a process would have been given rendered
// on its own: its service's declarations decide what it is handed, as they
// would for an instance that is its own process.
func (m Renderer) memberFor(id string) (render.Member, error) {
	inst, node := m.realInstance(id)
	if inst == nil {
		return render.Member{}, fmt.Errorf("process member %s: not found", id)
	}
	manifest, ok := m.Data.Manifests[inst.Service]
	if !ok {
		return render.Member{}, fmt.Errorf("%s: service %q is not defined", id, inst.Service)
	}
	instanceMap, _ := m.instanceAndNode(id, node.ID)
	principals, err := m.principalsFor(id)
	if err != nil {
		return render.Member{}, err
	}
	mb := render.Member{
		Name: inventory.LocalName(id), Service: inst.Service, Instance: instanceMap,
		Downstreams: m.downstreamsFor(id, manifest.FansOut()),
		Principals:  principals, Published: m.publishedFor(id),
	}
	if !manifest.FansOut() {
		if mb.Upstream, err = m.UpstreamFor(id, manifest.Upstream); err != nil {
			return render.Member{}, err
		}
	}
	if mb.Links, err = m.linksFor(id); err != nil {
		return render.Member{}, fmt.Errorf("%s: %w", id, err)
	}
	if mb.Dials, err = m.dialsFor(id); err != nil {
		return render.Member{}, err
	}
	if m.SecretsDir != "" {
		if mb.Self, err = secretstore.ReadSelf(m.SecretsDir, m.secretID(id)); err != nil {
			return render.Member{}, err
		}
	}
	return mb, nil
}

// Package confgen reads a generator root's services/ and exports/
// directories.
//
// A service is one program: `ssserver` and `sslocal` are two services, not
// two forms of one, and each is something a node deploys. An export is a way
// of handing a credential to a person — a share URI, a JSON configuration, a
// QR code — and is deployed nowhere, since nothing runs a QR code.
//
// The two are separate because they are separate kinds of thing, and because
// the same program is sometimes both a deployment and the consumer of an
// export: `sslocal` relays on a home server and also reads the JSON handed to
// a laptop. Which of the two a file is follows from the directory it is in,
// not from the program that happens to read it.
//
// It only discovers what is there, and which manifests are broken. It does
// not render anything.
//
// The rules are in docs/export.md.
package confgen

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ServicesDir is the generator root's subdirectory holding one directory per
// program a node deploys. ExportsDir sits inside one of those, and holds one
// directory per way that service's credential is handed to a person: an
// export renders one service's material and nothing else, so it belongs to
// that service rather than beside it. See
// docs/inventory.md#which-export-a-person-receives.
const (
	ServicesDir = "services"
	ExportsDir  = "exports"
)

// DeployDir is the subdirectory of a service that declares how an instance
// of it is started, rather than how the program behaves: a second template,
// rendered for a containerised instance beside the configuration. The
// directory is what declares it, the way an export's is, so the service's
// own manifest grows no key. A service holding none renders one file. See
// docs/export.md#a-second-file-what-deploys-it.
const DeployDir = "deploy"

// ManifestFilename names the file that marks a subdirectory of ServicesDir
// as a service, and declares how it renders.
const ManifestFilename = "confgen.yaml"

// Auth values a service declares. See docs/inventory.md#how-a-service-says-what-it-needs.
const (
	AuthPerPrincipal = "per-principal"
	AuthNone         = "none"
)

// DefaultsFilename is the defaults file inside every service directory.
const DefaultsFilename = "defaults.yaml"

// RotationDisruptive is the one value `rotation` may take: this service's
// template cannot emit two accounts for one principal, so rotating a
// credential drops the connection rather than overlapping.
const RotationDisruptive = "disruptive"

// DownstreamsOne and DownstreamsMany are the two values `downstreams` may
// take: whether an instance of this service dials one upstream, the same in
// every route through it, or one per route. DownstreamsOne is the default and
// is not written. See docs/inventory.md#a-service-that-fans-out.
const (
	DownstreamsOne  = "one"
	DownstreamsMany = "many"
)

// DispatchName and DispatchPort are the two ways an instance that is the
// entrance for several routes tells them apart. DispatchName is the default
// and is not written: a reverse proxy picks its upstream by the name the
// request arrived at, which is why every downstream of one needs a published
// name. DispatchPort is the other: a relay listens on one port per route and
// sends what arrives there to that route's next hop, so the port is what
// distinguishes them and a published name downstream means nothing.
const (
	DispatchName = "name"
	DispatchPort = "port"
)

// AccountsCredential and AccountsPerson are the two values `accounts` may
// take: what the account name a server sees is built from. AccountsCredential
// is the default and is not written — `<person>-<credential>`, because a
// credential is what is revocable and one person may hold two on one port.
// AccountsPerson is the person alone, for a service whose account is a POSIX
// user: Samba maps the name a client logs in with onto one, and a suffix there
// is a second name for the same account. It trades the second credential away:
// a person granted two on one port renders two accounts under one name, which
// validation reports. See docs/export.md#the-account-name.
const (
	AccountsCredential = "credential"
	AccountsPerson     = "person"
)

// DefaultsDocument and DefaultsElement are the two values `defaults` may take. See docs/export.md#two-kinds-of-defaults.
const (
	DefaultsDocument = "document"
	DefaultsElement  = "element"
)

type Secret struct {
	// Kind is the value's format, such as "base64". A service that does not
	// declare a Secret gets a printable random string.
	Kind string `yaml:"kind"`
	// Bytes is the value's length before encoding.
	Bytes int `yaml:"bytes"`
}

// Manifest is a service's confgen.yaml: everything about how one program
// renders, and what reaching it implies.
type Manifest struct {
	// Secret is the shape of this service's generated secret values. The
	// format is the protocol's: Shadowsocks 2022 needs base64 of exactly the
	// key size, not an arbitrary password.
	Secret Secret `yaml:"secret"`
	// Template is the template rendered for this service, relative to the
	// service directory. It is the one-file form, written with Output; a
	// service reading more than one file writes Files instead, and writing
	// both is an error.
	Template string `yaml:"template"`
	// Files is what this service writes when one file is not enough, in the
	// order written. A program is one service however many files it reads:
	// Samba's account table is not a second program, it is the other half
	// of the same configuration, and splitting it into a service of its own
	// would put two halves of one truth behind two manifests. Every file
	// renders from the same defaults and the same context, so the account
	// table and the configuration naming it cannot disagree.
	Files []File `yaml:"files"`
	// Defaults is DefaultsDocument or DefaultsElement, and decides how
	// defaults.yaml combines with the instance.
	Defaults string `yaml:"defaults"`
	// Output is the name the rendered file is written under. Every instance
	// of the service uses it, because the program reading it expects the
	// same name on every machine.
	Output string `yaml:"output"`
	// Auth is AuthPerPrincipal or AuthNone: whether this service's inbound
	// side authenticates each principal separately, and so whether a grant
	// on one of its ports implies a secret. Its own credentials are
	// independent of it; see Self.
	Auth string `yaml:"auth"`
	// Accounts is AccountsPerson when the account name a server sees is
	// the person alone, or empty for the ordinary `<person>-<credential>`.
	// It says nothing about which secret an account holds — that is still
	// one per credential, under the same path — only what the account is
	// called. See AccountsPerson.
	Accounts string `yaml:"accounts"`
	// Rotation is RotationDisruptive when this service's template cannot
	// emit two accounts for one principal, or empty otherwise. See
	// docs/inventory.md#rotation.
	Rotation string `yaml:"rotation"`
	// Exports names every way a credential to reach this service may be
	// handed to a person: a share URI, a JSON configuration, a QR code.
	// Every one of them is rendered, so a bundle holds them all and a
	// selector narrows to the ones a hand-over needs. Empty means reaching
	// this service produces no file for anyone — MicroBin is reached from a
	// browser — and a device's `export` does not bring one back: it chooses
	// among these, never whether any exists. See
	// docs/inventory.md#which-export-a-person-receives.
	//
	// It is not written in confgen.yaml: Load fills it from the service's
	// own ExportsDir, so the directories that exist are the list, and the
	// two cannot disagree.
	Exports []string `yaml:"-"`
	// Deploys is true when this service holds a DeployDir, and so renders a
	// second file for each of its containerised instances. Like Exports, it
	// is not written in confgen.yaml: Load fills it from the directory that
	// is there, so the two cannot disagree. It is what rule 24 reads, since
	// an instance writing `deploy` values for a service that deploys
	// nothing has written a mapping nothing renders.
	Deploys bool `yaml:"-"`
	// Self declares this service's own secrets: credentials belonging to
	// the instance rather than to anything reaching it, such as an
	// administrative password or a server PSK. They are what `secret sync`
	// generates under <instance>/self/, and a name absent from it is one
	// sync neither generates nor reports. A value that has to be edited
	// after it is generated does not belong here — it is configuration, and
	// belongs in defaults.yaml. Which of them a principal receives is a
	// port's business, not this one's; see inventory.Port.Self. See
	// docs/inventory.md#a-services-own-secrets.
	Self SelfDecls `yaml:"self"`
	// Dials is what an instance of this service calls off any route, by the
	// name its templates use: a service and a port of it. An instance that
	// writes no dial of that name gets the one instance of that service in
	// the innermost scope it shares with it. See
	// docs/inventory.md#dialling-a-service-by-type.
	Dials map[string]DialDecl `yaml:"dials"`
	// Proxy is what a reverse proxy in front of this service must do for
	// it — forward the client's address, accept a large body — by the name
	// the proxy's template reads. A port may override a key. See
	// docs/inventory.md#what-a-proxy-is-told.
	Proxy map[string]any `yaml:"proxy"`
	// Upstream declares what this service needs from the hop it connects
	// to, beyond the address, port and account every template is given.
	// Reading it is the consumer's business: a value crosses from one
	// instance to another because the program that dials says it needs it,
	// never because the program that listens happens to publish it. See
	// docs/inventory.md#what-a-service-needs-from-its-upstream.
	Upstream UpstreamDecls `yaml:"upstream"`
	// Forwards is true when an instance of this service terminates nothing:
	// it moves bytes from one of its ports to the hop that follows it and
	// reads none of them. A relay in front of a server in another country is
	// this, and it is a different thing from a reverse proxy, which
	// terminates one connection and opens another. What it changes is where
	// a client's credential comes from: the client dials this instance's
	// address and authenticates against the hop behind it, so nothing here
	// holds an account, and a route entering here is written out as the
	// service that ends it. See
	// docs/inventory.md#a-service-that-forwards.
	Forwards bool `yaml:"forwards"`
	// Dispatch is DispatchName or DispatchPort: how an instance that is the
	// entrance for several routes tells one from another. It is read only
	// for a service declaring Downstreams: DownstreamsMany, and writing it
	// without that is an error, since an instance with one successor
	// distinguishes nothing.
	Dispatch string `yaml:"dispatch"`
	// Downstreams is DownstreamsMany when one instance of this service is
	// the entrance for several routes — a reverse proxy in front of many
	// web services — or empty for the ordinary case of one upstream. It is
	// the only thing that relaxes the rule that a non-terminal hop has the
	// same successor in every route through it, and it is a statement about
	// shape rather than about credentials: a proxy forwards, it does not
	// authenticate, so declaring this hands it nothing. See
	// docs/inventory.md#a-service-that-fans-out.
	Downstreams string `yaml:"downstreams"`
}

// Terminates reports whether an instance of this service is the end of what
// a client connects to, which every service but a forwarder is. The receiver
// is a value for the same reason FansOut's is.
func (m Manifest) Terminates() bool { return !m.Forwards }

// Renders is every file an instance of this service writes, in the order
// written. It is the one place the two forms of the declaration meet, so
// nothing downstream has to ask which one a manifest used.
func (m Manifest) Renders() []File {
	if len(m.Files) > 0 {
		return m.Files
	}
	if m.Template == "" {
		return nil
	}
	return []File{{Template: m.Template, Output: m.Output}}
}

// NamesAccountsByPerson reports whether the account name a server sees is
// the person alone rather than the person and their credential.
func (m Manifest) NamesAccountsByPerson() bool { return m.Accounts == AccountsPerson }

// FansOut reports whether an instance of this service may dial a different
// upstream in each route through it. The receiver is a value so that a
// manifest read straight out of a map — the zero one for a service that is
// not there, which does not fan out — answers without being copied first.
func (m Manifest) FansOut() bool { return m.Downstreams == DownstreamsMany }

// DispatchesBy is how a fan-out instance of this service tells its routes
// apart, DispatchName when the manifest leaves it out.
func (m Manifest) DispatchesBy() string {
	if m.Dispatch == "" {
		return DispatchName
	}
	return m.Dispatch
}

// UpstreamShared is the one UpstreamDecls name rhumb understands today: the
// secrets the upstream port hands to everything granted on it, in the order
// that port writes them. Shadowsocks 2022 is the case it exists for — the
// password a client sends is the server's PSK and the client's own, joined —
// and a protocol taking them in another order authenticates nothing.
const UpstreamShared = "shared"

// UpstreamValues is the UpstreamDecls name for the upstream instance's own
// values: the parameters the program that listens was configured with, which
// the program that dials has to match. Hysteria2 is the case it exists for —
// obfuscation is a choice made per machine, and a client that does not
// obfuscate the same way never completes a handshake — and a port-hopping
// range is the same shape of fact: it belongs to the server that is reached,
// and the only place it can be written once is that instance.
//
// Values are configuration, never credentials: a secret reaches a template
// through UpstreamShared, which is read from the secrets store rather than
// from the inventory.
const UpstreamValues = "values"

// UpstreamDecl is one thing a service needs from its upstream. An empty
// declaration is how a manifest asks for it, and the one option says the hop
// may hand over nothing.
type UpstreamDecl struct {
	// Optional says a hop handing this over is not required to. Without it,
	// a target declaring `shared` against a port that hands out none is an
	// error, which is what catches the ordinary mistake: a client that
	// cannot authenticate without the server's half of a password, dialling
	// a port that was never given one. It is written where the same program
	// is configured both ways on different machines — a Hysteria2 instance
	// that obfuscates its handshake hands the client an obfuscation
	// password, and one that does not hands nothing and is still reachable.
	// A template then renders what it was given, which for an optional name
	// may be an empty list.
	Optional bool `yaml:"optional"`
}

// UpstreamDecls is a service or export manifest's `upstream` mapping: name to
// declaration, mirroring SelfDecls. A name rhumb does not understand is an
// error rather than something skipped, because silently dropping a credential
// a program needs renders a file that looks right and does not authenticate.
type UpstreamDecls map[string]UpstreamDecl

// Names is the declared names, sorted, so a caller walking them produces the
// same order every time.
func (d UpstreamDecls) Names() []string {
	out := make([]string, 0, len(d))
	for name := range d {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Wants reports whether these declarations name what.
func (d UpstreamDecls) Wants(what string) bool {
	_, ok := d[what]
	return ok
}

// KindOpaque is the one Secret.Kind rhumb never generates: a private key, a
// certificate chain, a vendor's keyfile. Its path is implied like any
// other, so sync reports it missing until someone writes it, and sync never
// invents a value nothing but a certificate authority can produce.
const KindOpaque = "opaque"

// SelfDecl is one of a service's own secrets: its shape, and whether it is
// one value, a family of values, a record of fields, or both.
type SelfDecl struct {
	// Secret is this name's shape, where it differs from the service's own
	// Secret block. Kind KindOpaque means a value rhumb never generates.
	Secret `yaml:",inline"`
	// Set is true when the name is a family of values whose keys each
	// instance declares: one file per key, <instance>/self/<name>/<key>.
	// Two ports of one program handing out different PSKs is what this is
	// for. See docs/inventory.md#a-secret-several-people-hold.
	Set bool `yaml:"set"`
	// Fields is one credential made of several generated parts, each with
	// its own shape — a uuid beside a password — one file each. With Set,
	// the files are <instance>/self/<name>/<key>/<field>.
	Fields map[string]Secret `yaml:"fields"`
}

// SelfDecls is a service manifest's `self` mapping: name to declaration. An
// empty declaration is one generated value in the service's own shape.
type SelfDecls map[string]SelfDecl

// Names is the declared names, sorted, so a caller walking them produces
// the same order every time.
func (d SelfDecls) Names() []string {
	out := make([]string, 0, len(d))
	for name := range d {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// FieldNames is the fields of one name, sorted, or nil when the name is a
// single value.
func (d SelfDecl) FieldNames() []string {
	out := make([]string, 0, len(d.Fields))
	for name := range d.Fields {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Export is an exports/<name>/confgen.yaml: how one credential is written out
// for a person. It is a rendering and nothing else — no ports, since nothing
// listens; no auth, since nothing connects to a file; no secrets of its own,
// since what it carries belongs to the service it reaches.
type Export struct {
	// Template is the template rendered, relative to the export directory.
	Template string `yaml:"template"`
	// Defaults is DefaultsDocument or DefaultsElement.
	Defaults string `yaml:"defaults"`
	// Output is the name the written file takes.
	Output string `yaml:"output"`
	// Upstream is what this export needs from the hop it writes out, read
	// exactly as a service's is. An export carries a credential a person
	// uses, so a protocol whose credential is made of the server's value
	// and the person's own needs both here too — the share URI is as
	// unusable without it as the client configuration would be.
	Upstream UpstreamDecls `yaml:"upstream"`
}

// Deploy is a service's deploy/confgen.yaml: how an instance of it is
// started. It has an export's shape — a template, a defaults kind, an output
// name — and for the same reason: the renderer, the defaults merge and the
// target are the existing ones. It is not an export, and the difference is
// who selects it and what it carries. An export is derived from a person's
// access and carries their credential; this belongs to an instance written
// in a node file, is rendered because that instance runs in a container, and
// holds no credential at all. See
// docs/export.md#a-second-file-what-deploys-it.
type Deploy struct {
	// Defaults is DefaultsDocument or DefaultsElement, and applies to
	// every file: one deploy/defaults.yaml is merged once and handed to
	// each template, so the compose file and the script that installs it
	// cannot disagree about what the instance deploys with.
	Defaults string `yaml:"defaults"`
	// Files is what this deployment writes, in the order written. A
	// container needs more than the file its runtime reads: the rendered
	// configuration has to be in place before the runtime starts, and the
	// step that puts it there is a second artefact of the same render
	// rather than something maintained by hand.
	Files []DeployFile `yaml:"files"`
}

// File is one artefact of a render: a template and the name it is written
// under, both relative to the directory that declares them. A service uses
// it for the same reason a deployment does — one program may read more than
// one file, and Samba's account table beside its smb.conf is not a second
// service.
type File struct {
	// Template is the template rendered, relative to the declaring
	// directory.
	Template string `yaml:"template"`
	// Output is the name the rendered file is written under, beside the
	// others of the same render.
	Output string `yaml:"output"`
}

// Executable reports whether this file is written with the execute bit. It
// is derived from the output name rather than declared: a deployment writes
// a script because the name says .sh, and a key saying so a second time is
// the kind of second truth this directory exists to remove.
func (f File) Executable() bool {
	return strings.EqualFold(filepath.Ext(f.Output), ".sh")
}

// DeployFile is what a deployment's files list holds. It is File: the two
// were one thing as soon as a service could write several files too, and a
// separate type would have been the same fields under a second name.
type DeployFile = File

// Service is one subdirectory of the generator root that holds a
// confgen.yaml.
type Service struct {
	// Name is the subdirectory's name.
	Name string
	// Dir is the subdirectory's path, relative to the generator root.
	Dir string
	// Manifest is the parsed confgen.yaml, valid only when Broken is empty.
	Manifest Manifest
	// Broken is the manifest's parse error if confgen.yaml is not valid YAML,
	// not a mapping, or holds an unknown key, and empty otherwise.
	Broken string
	// Deploy is the parsed DeployDir/confgen.yaml, or nil for a service
	// that holds no such directory — which is most of them. DeployDir is
	// its path relative to the generator root, and DeployBroken its parse
	// error, valid the same way Broken is.
	Deploy       *Deploy
	DeployDir    string
	DeployBroken string
	// Docker is the parsed DockerFilename, or nil for a service holding
	// none, and DockerBroken its parse error. A service holds either it or
	// a DeployDir, never both.
	Docker       *Docker
	DockerBroken string
}

// ExportDef is one subdirectory of a service's ExportsDir that holds a
// confgen.yaml.
type ExportDef struct {
	// Service is the service this export writes out. An export renders one
	// service's upstream and nothing else, so the pair is its identity: two
	// services may both offer a "link", and they are two exports.
	Service string
	// Name is the subdirectory's name.
	Name string
	// Dir is the subdirectory's path, relative to the generator root.
	Dir string
	// Export is the parsed confgen.yaml, valid only when Broken is empty.
	Export Export
	// Broken is the manifest's parse error, and empty otherwise.
	Broken string
}

// ExportKey is how an export is keyed where service and name must travel
// together: "<service>/<name>". Two services may each offer a "link", and
// the name alone would collide.
func ExportKey(service, name string) string {
	return service + "/" + name
}

// Root is a discovered generator root.
type Root struct {
	// Services is every subdirectory of ServicesDir holding a confgen.yaml,
	// sorted by name.
	Services []Service
	// Exports is every subdirectory of every service's ExportsDir holding
	// one, sorted by service and then by name.
	Exports []ExportDef
}

// Load discovers a generator root's services/, and inside each service its
// exports/: every subdirectory holding a confgen.yaml is one, and is read
// whether or not it parses cleanly. A subdirectory without a confgen.yaml —
// a README, a scratch folder, one still being written — is skipped rather
// than half-read. A root with no services/ yet is an empty Root, not an
// error.
func Load(root string) (*Root, error) {
	if _, err := os.Stat(root); err != nil {
		return nil, fmt.Errorf("confgen: reading root: %w", err)
	}

	out := &Root{}

	if err := eachManifest(root, ServicesDir, func(name, dir, path string) error {
		svc := Service{Name: name, Dir: dir}
		manifest, err := loadManifest(path)
		if err != nil {
			svc.Broken = err.Error()
		} else {
			svc.Manifest = *manifest
		}

		// A service's exports are the directories it holds, not a list it
		// repeats: a list could name a directory that is not there, or miss
		// one that is, and a reader would have no way to tell which is the
		// truth. A broken service still has its exports read — the reason
		// it is broken may be the very thing being fixed.
		if err := eachManifest(root, filepath.Join(ServicesDir, name, ExportsDir), func(exportName, exportDir, exportPath string) error {
			def := ExportDef{Service: name, Name: exportName, Dir: exportDir}
			export, err := loadExport(exportPath)
			if err != nil {
				def.Broken = err.Error()
			} else {
				def.Export = *export
			}
			out.Exports = append(out.Exports, def)
			svc.Manifest.Exports = append(svc.Manifest.Exports, exportName)
			return nil
		}); err != nil {
			return err
		}
		sort.Strings(svc.Manifest.Exports)

		// The deployment half, when the service declares one. It is read
		// beside the exports rather than among them: an export's name is
		// something a person's device may ask for, and `deploy` is a fixed
		// name nobody selects.
		deployDir := filepath.Join(ServicesDir, name, DeployDir)
		deployPath := filepath.Join(root, deployDir, ManifestFilename)
		if _, err := os.Stat(deployPath); err == nil {
			svc.DeployDir = deployDir
			svc.Manifest.Deploys = true
			deploy, err := loadDeploy(deployPath)
			if err != nil {
				svc.DeployBroken = err.Error()
			} else {
				svc.Deploy = deploy
			}
		}

		dockerPath := filepath.Join(root, ServicesDir, name, DockerFilename)
		if _, err := os.Stat(dockerPath); err == nil {
			svc.Manifest.Deploys = true
			docker, err := loadDocker(dockerPath)
			switch {
			case err != nil:
				svc.DockerBroken = err.Error()
			case svc.DeployDir != "":
				svc.DockerBroken = fmt.Sprintf("%s: the service also holds %s/; a container is started from one of the two", dockerPath, DeployDir)
			default:
				svc.Docker = docker
			}
		}

		out.Services = append(out.Services, svc)
		return nil
	}); err != nil {
		return nil, err
	}

	sort.Slice(out.Services, func(i, j int) bool { return out.Services[i].Name < out.Services[j].Name })
	sort.Slice(out.Exports, func(i, j int) bool {
		if out.Exports[i].Service != out.Exports[j].Service {
			return out.Exports[i].Service < out.Exports[j].Service
		}
		return out.Exports[i].Name < out.Exports[j].Name
	})
	return out, nil
}

// eachManifest calls fn for every subdirectory of root/which that holds a
// ManifestFilename, with the subdirectory's name, its path relative to the
// root, and the manifest's full path. A missing directory yields nothing.
func eachManifest(root, which string, fn func(name, dir, path string) error) error {
	parent := filepath.Join(root, which)
	entries, err := os.ReadDir(parent)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("confgen: reading %s: %w", parent, err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		path := filepath.Join(parent, name, ManifestFilename)
		if _, err := os.Stat(path); err != nil {
			continue
		}
		if err := fn(name, filepath.Join(which, name), path); err != nil {
			return err
		}
	}
	return nil
}

// loadExport parses one services/<service>/exports/<name>/confgen.yaml. Unknown keys are an
// error rather than a silent default, so `auth` or `ports` written on an
// export — which renders a file and listens on nothing — is reported instead
// of ignored.
func loadExport(path string) (*Export, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var e Export
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&e); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if err := checkUpstream(path, e.Upstream); err != nil {
		return nil, err
	}
	return &e, nil
}

// checkUpstream rejects a name rhumb does not understand. Skipping one would
// leave a template asking for a credential that is simply absent, and a
// configuration that renders and then fails to authenticate is worse to
// diagnose than a manifest that will not load.
func checkUpstream(path string, d UpstreamDecls) error {
	for _, name := range d.Names() {
		if name != UpstreamShared && name != UpstreamValues {
			return fmt.Errorf("parsing %s: upstream %q is not %q or %q",
				path, name, UpstreamShared, UpstreamValues)
		}
	}
	return nil
}

// loadDeploy parses one services/<service>/deploy/confgen.yaml. Unknown
// keys are an error, as everywhere else: a deployment file renders and
// listens on nothing, so `auth` or `ports` written here is reported rather
// than ignored.
func loadDeploy(path string) (*Deploy, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var d Deploy
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&d); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	// A deployment that writes nothing is a directory that exists for no
	// reason, and it would otherwise be reported at render time, once per
	// instance, rather than here where the file is.
	if len(d.Files) == 0 {
		return nil, fmt.Errorf("parsing %s: files is empty: a deployment writes at least one file", path)
	}
	if err := checkFiles(path, d.Files); err != nil {
		return nil, err
	}
	return &d, nil
}

// checkFiles rejects a files list that names no template, no output, or one
// output twice. Two files under one name would leave the second overwriting
// the first in a folder and duplicating an entry in a zip, and which of the
// two survived would depend on the writer. Every check is where the manifest
// is rather than once per instance at render time.
func checkFiles(path string, files []File) error {
	seen := map[string]bool{}
	for i, f := range files {
		if f.Template == "" {
			return fmt.Errorf("parsing %s: files[%d] names no template", path, i)
		}
		if f.Output == "" {
			return fmt.Errorf("parsing %s: files[%d] names no output", path, i)
		}
		if seen[f.Output] {
			return fmt.Errorf("parsing %s: files[%d]: output %q is written twice", path, i, f.Output)
		}
		seen[f.Output] = true
	}
	return nil
}

func loadManifest(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	var m Manifest
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	// An unrecognised auth would otherwise read as "not per-principal" and
	// silently generate nothing, which is what a service meaning none says
	// deliberately.
	switch m.Auth {
	case AuthPerPrincipal, AuthNone, "":
	default:
		return nil, fmt.Errorf("parsing %s: auth %q is not %q or %q", path, m.Auth, AuthPerPrincipal, AuthNone)
	}
	// An unrecognised accounts would read as the default and quietly give a
	// service written to name accounts by person a suffix nobody expected.
	switch m.Accounts {
	case AccountsCredential, AccountsPerson, "":
	default:
		return nil, fmt.Errorf("parsing %s: accounts %q is not %q or %q", path, m.Accounts, AccountsCredential, AccountsPerson)
	}
	// An account name exists only where there is an account table: a
	// service that authenticates nobody names nothing, and the key there is
	// a setting someone meant for a service that does.
	if m.Accounts != "" && m.Auth != AuthPerPrincipal {
		return nil, fmt.Errorf("parsing %s: accounts %q without auth %q: this service holds no account to name",
			path, m.Accounts, AuthPerPrincipal)
	}
	// An unrecognised downstreams would read as "not many" and quietly
	// reinstate the single-upstream rule on a service written to fan out,
	// so the error names the two values rather than letting a typo decide.
	switch m.Downstreams {
	case DownstreamsOne, DownstreamsMany, "":
	default:
		return nil, fmt.Errorf("parsing %s: downstreams %q is not %q or %q", path, m.Downstreams, DownstreamsOne, DownstreamsMany)
	}
	// A service that terminates nothing authenticates nobody: there is no
	// connection to it to authenticate, only bytes passing through. Written
	// together, the two say a thing that cannot happen, and the account
	// table it implies would be rendered into a file no program reads.
	if m.Forwards && m.Auth == AuthPerPrincipal {
		return nil, fmt.Errorf("parsing %s: forwards and auth %q: a service that terminates nothing authenticates nobody",
			path, AuthPerPrincipal)
	}
	if m.Forwards && len(m.Self) > 0 {
		return nil, fmt.Errorf("parsing %s: forwards and self: a service that terminates nothing holds no credential", path)
	}
	// An unrecognised dispatch would read as "by name" and quietly put a
	// relay's downstreams behind a published name none of them has.
	switch m.Dispatch {
	case DispatchName, DispatchPort, "":
	default:
		return nil, fmt.Errorf("parsing %s: dispatch %q is not %q or %q", path, m.Dispatch, DispatchName, DispatchPort)
	}
	// Dispatch answers "which of this instance's several routes is this",
	// so it says nothing about a service that has one successor. Written
	// there, it is a fan-out someone meant to declare and did not.
	if m.Dispatch != "" && !m.FansOut() {
		return nil, fmt.Errorf("parsing %s: dispatch %q without downstreams: %s, so there is nothing to tell apart",
			path, m.Dispatch, DownstreamsMany)
	}
	// The two forms of the declaration say the same thing, so a manifest
	// writing both leaves which one renders up to the reader of the code.
	// One file is template and output; more than one is files.
	if len(m.Files) > 0 && (m.Template != "" || m.Output != "") {
		return nil, fmt.Errorf("parsing %s: files together with template or output: a service declares one or the other", path)
	}
	if err := checkFiles(path, m.Files); err != nil {
		return nil, err
	}
	if err := checkUpstream(path, m.Upstream); err != nil {
		return nil, err
	}
	return &m, nil
}

// DialDecl is one dial a service declares: which service it calls, and at
// which of that service's ports.
type DialDecl struct {
	Service string `yaml:"service"`
	Port    string `yaml:"port"`
}

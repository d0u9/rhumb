package confgen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoad_DiscoversOneManifestPerService(t *testing.T) {
	root := t.TempDir()

	// A service is one program: hysteria2's server and the client that
	// reaches it are two directories, not two roles of one.
	writeFile(t, filepath.Join(root, ServicesDir, "hysteria2", ManifestFilename), `
secret:
  kind: base64
  bytes: 32
template: templates/server.yaml.tmpl
defaults: element
output: config.yaml
auth: per-principal
`)
	writeFile(t, filepath.Join(root, ServicesDir, "hysteria2", DefaultsFilename), "listen: :443\n")

	// An export sits inside the service it writes out, and the directories
	// that exist are the list: nothing in the manifest repeats them.
	writeFile(t, filepath.Join(root, ServicesDir, "hysteria2", ExportsDir, "link", ManifestFilename), `
template: templates/link.tmpl
defaults: document
output: share.txt
`)

	// A second service offering a form under the same name. The two are two
	// exports, which is why service and name travel together.
	writeFile(t, filepath.Join(root, ServicesDir, "ssserver", ManifestFilename), `
template: templates/config.json.tmpl
defaults: element
output: config.json
auth: per-principal
`)
	writeFile(t, filepath.Join(root, ServicesDir, "ssserver", ExportsDir, "link", ManifestFilename), `
template: templates/link.tmpl
defaults: element
output: share.txt
`)

	// A scratch folder without a manifest is skipped.
	writeFile(t, filepath.Join(root, ServicesDir, "scratch", "notes.yaml"), "todo: yes\n")

	got, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(got.Services) != 2 {
		t.Fatalf("Services = %d, want 2 (scratch should be skipped): %+v", len(got.Services), got.Services)
	}

	byName := map[string]Service{}
	for _, svc := range got.Services {
		if svc.Broken != "" {
			t.Fatalf("%s Broken = %q, want empty", svc.Name, svc.Broken)
		}
		byName[svc.Name] = svc
	}

	server := byName["hysteria2"].Manifest
	if server.Secret.Kind != "base64" || server.Secret.Bytes != 32 {
		t.Fatalf("Secret = %+v", server.Secret)
	}
	if server.Defaults != DefaultsElement {
		t.Fatalf("Defaults = %q, want %q", server.Defaults, DefaultsElement)
	}
	if server.Auth != AuthPerPrincipal {
		t.Fatalf("Auth = %q, want %q", server.Auth, AuthPerPrincipal)
	}
	if len(server.Exports) != 1 || server.Exports[0] != "link" {
		t.Fatalf("Exports = %v, want the ways this service is written out", server.Exports)
	}

	if len(got.Exports) != 2 {
		t.Fatalf("Exports = %+v, want one per service offering a link", got.Exports)
	}
	for _, def := range got.Exports {
		if def.Broken != "" {
			t.Fatalf("%s/%s Broken = %q, want empty", def.Service, def.Name, def.Broken)
		}
		if def.Name != "link" {
			t.Fatalf("Exports names = %+v, want both called link", got.Exports)
		}
		if def.Export.Output != "share.txt" {
			t.Fatalf("%s/%s Output = %q", def.Service, def.Name, def.Export.Output)
		}
	}
	if got.Exports[0].Service != "hysteria2" || got.Exports[1].Service != "ssserver" {
		t.Fatalf("Exports services = %q, %q, want them sorted by service", got.Exports[0].Service, got.Exports[1].Service)
	}
	if got.Exports[0].Dir != filepath.Join(ServicesDir, "hysteria2", ExportsDir, "link") {
		t.Fatalf("Dir = %q, want the export's own directory under its service", got.Exports[0].Dir)
	}
}

func TestLoad_BrokenManifestIsReportedNotFatal(t *testing.T) {
	root := t.TempDir()

	// Unknown key in the manifest.
	writeFile(t, filepath.Join(root, ServicesDir, "microbin", ManifestFilename), `
roles:
  server:
    template: templates/server.env.tmpl
    defaults: document
    output: server.env
    auth: none
    typo_field: oops
`)
	// A second, valid service, so one broken manifest does not abort discovery.
	writeFile(t, filepath.Join(root, ServicesDir, "shadowsocks-rust", ManifestFilename), `
secret:
  kind: base64
  bytes: 32
template: templates/config.json.tmpl
defaults: element
output: config.json
auth: per-principal
`)

	got, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.Services) != 2 {
		t.Fatalf("Services = %d, want 2", len(got.Services))
	}

	microbin := got.Services[0]
	if microbin.Name != "microbin" {
		t.Fatalf("Services[0].Name = %q, want microbin", microbin.Name)
	}
	if microbin.Broken == "" {
		t.Fatal("Broken = empty, want unknown-field error")
	}
	if microbin.Manifest.Template != "" {
		t.Fatalf("Manifest = %+v, want nothing parsed out of a broken manifest", microbin.Manifest)
	}

	ss := got.Services[1]
	if ss.Broken != "" {
		t.Fatalf("shadowsocks-rust Broken = %q, want empty", ss.Broken)
	}
	if ss.Manifest.Template == "" {
		t.Fatalf("shadowsocks-rust Manifest = %+v, want a parsed one beside the broken service", ss.Manifest)
	}
}

// TestLoad_UnknownAuthIsBroken covers a role declaring an auth value that is
// neither per-principal nor none — `shared`, which this design once had.
// Without the check it would read as "not per-principal" and generate
// nothing, which is exactly what `none` says on purpose, so the manifest
// would be wrong and silent.
func TestLoad_UnknownAuthIsBroken(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ServicesDir, "microbin", ManifestFilename), `
auth: shared
template: templates/server.env.tmpl
defaults: document
output: server.env
`)

	got, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.Services) != 1 {
		t.Fatalf("Services = %d, want 1", len(got.Services))
	}
	if !strings.Contains(got.Services[0].Broken, `auth "shared"`) {
		t.Fatalf("Broken = %q, want it to name the unknown auth", got.Services[0].Broken)
	}
}

// TestLoad_UnknownUpstreamNameIsBroken covers a name dgs does not
// understand. Skipping it would leave a template asking for a credential
// that is simply absent, and a file that renders and then fails to
// authenticate is harder to diagnose than a manifest that will not load.
func TestLoad_UnknownUpstreamNameIsBroken(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ServicesDir, "sslocal", ManifestFilename), `
auth: none
upstream:
  fingerprint: {}
template: templates/config.json.tmpl
defaults: element
output: config.json
`)

	got, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.Services) != 1 {
		t.Fatalf("Services = %d, want 1", len(got.Services))
	}
	if !strings.Contains(got.Services[0].Broken, `upstream "fingerprint"`) {
		t.Fatalf("Broken = %q, want it to name the unknown upstream", got.Services[0].Broken)
	}
}

// TestLoad_UpstreamSharedIsRead is the accepted spelling, on a service and
// on an export alike: an export carries a credential a person uses, so a
// protocol whose credential is half the server's needs it as much as the
// client program does.
func TestLoad_UpstreamSharedIsRead(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ServicesDir, "ssserver", ManifestFilename), `
auth: per-principal
template: templates/config.json.tmpl
defaults: element
output: config.json
`)
	writeFile(t, filepath.Join(root, ServicesDir, "ssserver", ExportsDir, "link", ManifestFilename), `
template: templates/share.txt.tmpl
defaults: element
output: share.txt
upstream:
  shared: {}
`)

	got, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.Exports) != 1 {
		t.Fatalf("Exports = %d, want 1", len(got.Exports))
	}
	if !got.Exports[0].Export.Upstream.Wants(UpstreamShared) {
		t.Fatalf("Upstream = %v, want it to want %q", got.Exports[0].Export.Upstream, UpstreamShared)
	}
}

// A service says once that one of its instances is the entrance for several
// routes; anything but the two values is a typo that would quietly reinstate
// the single-upstream rule.
func TestLoad_Downstreams(t *testing.T) {
	fansOut := func(t *testing.T, name, manifest string) bool {
		t.Helper()
		root := t.TempDir()
		writeFile(t, filepath.Join(root, ServicesDir, name, ManifestFilename), manifest)
		got, err := Load(root)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		for _, svc := range got.Services {
			if svc.Name == name {
				return svc.Manifest.FansOut()
			}
		}
		t.Fatalf("Load did not find service %q", name)
		return false
	}

	if !fansOut(t, "caddy", "auth: none\ndownstreams: many\ntemplate: t\noutput: Caddyfile\n") {
		t.Fatalf("FansOut = false, want true")
	}
	if fansOut(t, "plain", "auth: none\ntemplate: t\noutput: c\n") {
		t.Fatalf("FansOut = true for a service that declares nothing, want false")
	}

	// A value that is neither is recorded as a broken manifest rather than
	// read as "not many", which would quietly reinstate the single-upstream
	// rule on a service written to fan out.
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ServicesDir, "typo", ManifestFilename), "auth: none\ndownstreams: many-ish\ntemplate: t\noutput: c\n")
	got, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, svc := range got.Services {
		if svc.Name != "typo" {
			continue
		}
		if !strings.Contains(svc.Broken, `downstreams "many-ish"`) {
			t.Fatalf("Broken = %q, want it to name the bad value", svc.Broken)
		}
	}
}

// TestLoad_DispatchWithoutFanOutIsBroken covers `dispatch` written on a
// service with one successor. It answers "which of this instance's several
// routes is this", so a service that has one route through each instance has
// nothing for it to tell apart, and writing it is a fan-out someone meant to
// declare and did not.
func TestLoad_DispatchWithoutFanOutIsBroken(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ServicesDir, "realm", ManifestFilename), `
auth: none
forwards: true
dispatch: port
template: templates/config.toml.tmpl
defaults: document
output: config.toml
`)

	got, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !strings.Contains(got.Services[0].Broken, "downstreams: many") {
		t.Fatalf("Broken = %q, want it to name the missing fan-out", got.Services[0].Broken)
	}
}

// TestLoad_ForwardsWithPerPrincipalAuthIsBroken covers a manifest saying two
// things that cannot both be true: a program that reads nothing it is given
// cannot authenticate anyone.
func TestLoad_ForwardsWithPerPrincipalAuthIsBroken(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ServicesDir, "realm", ManifestFilename), `
auth: per-principal
forwards: true
template: templates/config.toml.tmpl
defaults: document
output: config.toml
`)

	got, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !strings.Contains(got.Services[0].Broken, "authenticates nobody") {
		t.Fatalf("Broken = %q, want the contradiction named", got.Services[0].Broken)
	}
}

// TestLoad_DeployDirectoryIsReadAndIsNotAnExport pins what declares a
// deployment file: the directory being there. It is not an export, so it is
// not in the service's export list and nobody can select it by name.
func TestLoad_DeployDirectoryIsReadAndIsNotAnExport(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ServicesDir, "microbin", ManifestFilename), `
template: templates/server.env.tmpl
defaults: document
output: server.env
auth: none
`)
	writeFile(t, filepath.Join(root, ServicesDir, "microbin", DeployDir, ManifestFilename), `
defaults: document
files:
  - template: templates/compose.yaml.tmpl
    output: compose.yaml
  - template: templates/install.sh.tmpl
    output: install.sh
`)

	got, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.Services) != 1 {
		t.Fatalf("Load = %d services, want 1", len(got.Services))
	}
	svc := got.Services[0]
	if svc.DeployBroken != "" {
		t.Fatalf("deploy: %s", svc.DeployBroken)
	}
	if svc.Deploy == nil {
		t.Fatal("Load read no deploy manifest")
	}
	if len(svc.Deploy.Files) != 2 {
		t.Fatalf("deploy = %+v, want both files", *svc.Deploy)
	}
	if svc.Deploy.Files[0].Output != "compose.yaml" || svc.Deploy.Files[0].Template != "templates/compose.yaml.tmpl" {
		t.Fatalf("deploy files[0] = %+v, want the compose template and output", svc.Deploy.Files[0])
	}
	// A deployment writes the file its runtime reads and whatever puts
	// that file in place, in the order written, and the name is what says
	// which of the two is run rather than read.
	if svc.Deploy.Files[1].Output != "install.sh" || !svc.Deploy.Files[1].Executable() {
		t.Fatalf("deploy files[1] = %+v, want an executable install.sh", svc.Deploy.Files[1])
	}
	if svc.Deploy.Files[0].Executable() {
		t.Fatal("compose.yaml is executable, want only a .sh output to be")
	}
	if !svc.Manifest.Deploys {
		t.Fatal("Deploys = false, want true for a service holding a deploy directory")
	}
	if len(svc.Manifest.Exports) != 0 || len(got.Exports) != 0 {
		t.Fatalf("deploy was read as an export: exports = %v", svc.Manifest.Exports)
	}
}

// TestLoad_ServiceWithoutADeployDirectoryHasNone is the ordinary case, and
// the one rule 24 reads.
func TestLoad_ServiceWithoutADeployDirectoryHasNone(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ServicesDir, "microbin", ManifestFilename), "template: t\noutput: server.env\n")

	got, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Services[0].Deploy != nil || got.Services[0].Manifest.Deploys {
		t.Fatalf("Load = %+v, want no deploy half", got.Services[0])
	}
}

// TestLoad_BrokenDeployManifestIsReportedNotFatal: an unknown key there is
// reported like any other, rather than leaving a setting at a default
// nobody wrote.
func TestLoad_BrokenDeployManifestIsReportedNotFatal(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ServicesDir, "microbin", ManifestFilename), "template: t\noutput: server.env\n")
	writeFile(t, filepath.Join(root, ServicesDir, "microbin", DeployDir, ManifestFilename), "files:\n  - {template: t, output: o}\nauth: none\n")

	got, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Services[0].DeployBroken == "" {
		t.Fatal("DeployBroken is empty, want the unknown-key error")
	}
	if !strings.Contains(got.Services[0].DeployBroken, "auth") {
		t.Fatalf("DeployBroken = %q, want it to name the key", got.Services[0].DeployBroken)
	}
}

// TestLoad_DeployManifestWritingOneOutputTwiceIsBroken: the second file
// would overwrite the first in a folder and duplicate an entry in a zip, and
// which survived would depend on the writer rather than on the manifest.
func TestLoad_DeployManifestWritingOneOutputTwiceIsBroken(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ServicesDir, "microbin", ManifestFilename), "template: t\noutput: server.env\n")
	writeFile(t, filepath.Join(root, ServicesDir, "microbin", DeployDir, ManifestFilename), `
files:
  - {template: a.tmpl, output: compose.yaml}
  - {template: b.tmpl, output: compose.yaml}
`)

	got, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !strings.Contains(got.Services[0].DeployBroken, "compose.yaml") {
		t.Fatalf("DeployBroken = %q, want it to name the output written twice", got.Services[0].DeployBroken)
	}
}

// TestLoad_DeployManifestWritingNoFileIsBroken: a deployment that writes
// nothing is reported where the file is, rather than once per instance at
// render time.
func TestLoad_DeployManifestWritingNoFileIsBroken(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ServicesDir, "microbin", ManifestFilename), "template: t\noutput: server.env\n")
	writeFile(t, filepath.Join(root, ServicesDir, "microbin", DeployDir, ManifestFilename), "defaults: document\n")

	got, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !strings.Contains(got.Services[0].DeployBroken, "files") {
		t.Fatalf("DeployBroken = %q, want it to say no file is written", got.Services[0].DeployBroken)
	}
}

// TestLoad_ServiceWritingSeveralFiles: a program reading two files is one
// service. Samba's account table is not a second program — it is the other
// half of the same configuration, and a service of its own would put two
// halves of one truth behind two manifests.
func TestLoad_ServiceWritingSeveralFiles(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ServicesDir, "samba", ManifestFilename), `
auth: per-principal
defaults: document
files:
  - {template: templates/smb.conf.tmpl, output: smb.conf}
  - {template: templates/smbpasswd.tmpl, output: smbpasswd}
`)

	got, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Services[0].Broken != "" {
		t.Fatalf("Broken = %q, want a service writing two files to load", got.Services[0].Broken)
	}
	renders := got.Services[0].Manifest.Renders()
	if len(renders) != 2 {
		t.Fatalf("Renders() returned %d files, want 2", len(renders))
	}
	if renders[0].Output != "smb.conf" || renders[1].Output != "smbpasswd" {
		t.Fatalf("Renders() = %v, want the declared order", renders)
	}
}

// TestLoad_OneFileServiceRendersThroughTheSameList: the one-file form is
// the same declaration written shorter, so nothing downstream has to ask
// which of the two a manifest used.
func TestLoad_OneFileServiceRendersThroughTheSameList(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ServicesDir, "microbin", ManifestFilename), "template: t.tmpl\noutput: server.env\n")

	got, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	renders := got.Services[0].Manifest.Renders()
	if len(renders) != 1 || renders[0].Template != "t.tmpl" || renders[0].Output != "server.env" {
		t.Fatalf("Renders() = %v, want the one declared file", renders)
	}
}

// TestLoad_ServiceWritingBothFormsIsBroken: files and template say the same
// thing, so a manifest writing both leaves which one renders up to whoever
// reads the code.
func TestLoad_ServiceWritingBothFormsIsBroken(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ServicesDir, "samba", ManifestFilename), `
template: templates/smb.conf.tmpl
output: smb.conf
files:
  - {template: templates/smbpasswd.tmpl, output: smbpasswd}
`)

	got, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !strings.Contains(got.Services[0].Broken, "files") {
		t.Fatalf("Broken = %q, want it to name the two forms", got.Services[0].Broken)
	}
}

// TestLoad_ServiceWritingOneOutputTwiceIsBroken: the second file would
// overwrite the first in a folder and duplicate an entry in a zip, and
// which survived would depend on the writer.
func TestLoad_ServiceWritingOneOutputTwiceIsBroken(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ServicesDir, "samba", ManifestFilename), `
files:
  - {template: a.tmpl, output: smb.conf}
  - {template: b.tmpl, output: smb.conf}
`)

	got, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !strings.Contains(got.Services[0].Broken, "smb.conf") {
		t.Fatalf("Broken = %q, want it to name the output written twice", got.Services[0].Broken)
	}
}

// TestLoad_AccountsByPersonIsRead: the one value besides the default loads,
// and says what the account is called.
func TestLoad_AccountsByPersonIsRead(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ServicesDir, "samba", ManifestFilename),
		"auth: per-principal\naccounts: person\ntemplate: t\noutput: smb.conf\n")

	got, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Services[0].Broken != "" {
		t.Fatalf("Broken = %q", got.Services[0].Broken)
	}
	if !got.Services[0].Manifest.NamesAccountsByPerson() {
		t.Fatal("NamesAccountsByPerson() = false, want true")
	}
}

// TestLoad_UnknownAccountsIsBroken: a misspelling would otherwise read as
// the default and give accounts a suffix nobody expected.
func TestLoad_UnknownAccountsIsBroken(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ServicesDir, "samba", ManifestFilename),
		"auth: per-principal\naccounts: people\ntemplate: t\noutput: smb.conf\n")

	got, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !strings.Contains(got.Services[0].Broken, "people") {
		t.Fatalf("Broken = %q, want it to name the value", got.Services[0].Broken)
	}
}

// TestLoad_AccountsWithoutAnAccountTableIsBroken: a service authenticating
// nobody has no account to name.
func TestLoad_AccountsWithoutAnAccountTableIsBroken(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ServicesDir, "microbin", ManifestFilename),
		"auth: none\naccounts: person\ntemplate: t\noutput: server.env\n")

	got, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !strings.Contains(got.Services[0].Broken, "accounts") {
		t.Fatalf("Broken = %q, want it to say there is no account to name", got.Services[0].Broken)
	}
}

package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// examplesRoot is examples/conf with a secrets store generated beside it, so
// a test renders the example inventory the way `rhumb export` would.
// internal/conf/validate checks the same tree loads, derives and validates;
// this one checks it renders, which is the half a broken template breaks.
func examplesRoot(t *testing.T) (root, secretsDir string) {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("no caller information")
	}
	root = filepath.Join(filepath.Dir(file), "..", "examples", "conf")
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("examples/conf: %v", err)
	}
	secretsDir = t.TempDir()

	var out bytes.Buffer
	if err := Secret(strings.NewReader(""), &out, []string{"sync"}, map[string]string{"yes": "true"}, Settings{Root: root, Secrets: secretsDir}); err != nil {
		t.Fatalf("secret sync: %v\n%s", err, out.String())
	}
	return root, secretsDir
}

// renderExamples exports every target the example inventory holds and returns
// the rendered files by the path they were written under.
func renderExamples(t *testing.T) map[string]string {
	t.Helper()
	root, secretsDir := examplesRoot(t)
	dest := t.TempDir()

	var out bytes.Buffer
	if err := Export(strings.NewReader(""), &out, []string{"*"}, map[string]string{"to": dest, "yes": "true"}, Settings{Root: root, Secrets: secretsDir}); err != nil {
		t.Fatalf("export: %v\n%s", err, out.String())
	}

	files := map[string]string{}
	for _, path := range filesUnder(t, dest) {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		rel, err := filepath.Rel(dest, path)
		if err != nil {
			t.Fatal(err)
		}
		files[filepath.ToSlash(rel)] = string(data)
	}
	if len(files) == 0 {
		t.Fatal("the example inventory rendered nothing")
	}
	return files
}

// exampleFile is the one rendered file whose path ends in suffix.
func exampleFile(t *testing.T, files map[string]string, suffix string) string {
	t.Helper()
	var found []string
	for path := range files {
		if strings.HasSuffix(path, suffix) {
			found = append(found, path)
		}
	}
	switch len(found) {
	case 1:
		return files[found[0]]
	case 0:
		paths := make([]string, 0, len(files))
		for path := range files {
			paths = append(paths, path)
		}
		t.Fatalf("no rendered file ends in %q; rendered %v", suffix, paths)
	default:
		t.Fatalf("%d rendered files end in %q: %v", len(found), suffix, found)
	}
	return ""
}

// TestExamples_EveryTargetRenders is the other half of
// internal/conf/validate's example test: a tree that loads and validates can
// still hold a template that will not run, and a broken example is worse than
// none, since examples/conf is what a reader copies.
func TestExamples_EveryTargetRenders(t *testing.T) {
	files := renderExamples(t)
	for path, content := range files {
		if strings.TrimSpace(content) == "" {
			t.Errorf("%s rendered empty", path)
		}
	}
}

// TestExamples_HysteriaMasqueradeComesFromTheInstance pins
// docs/export.md#two-kinds-of-defaults over the whole pipeline: the
// example server sets masquerade_url in its instance's values, and that is
// what reaches the rendered file — not the service default it overrides.
func TestExamples_HysteriaMasqueradeComesFromTheInstance(t *testing.T) {
	files := renderExamples(t)
	got := exampleFile(t, files, "hy2-sea01/config.yaml")

	if !strings.Contains(got, "url: https://www.example.com/") {
		t.Errorf("rendered config.yaml does not carry the instance's masquerade_url:\n%s", got)
	}
	if strings.Contains(got, "https://example.org/") {
		t.Errorf("rendered config.yaml still carries the service's default masquerade_url:\n%s", got)
	}
	// A default the instance does not override still stands.
	if !strings.Contains(got, "cert: /etc/hysteria/tls/fullchain.pem") {
		t.Errorf("rendered config.yaml lost a default the instance did not override:\n%s", got)
	}
}

// TestExamples_MicrobinAdminIsNotBasicAuth pins that the two switches are two
// things: the example instance opens the administrative interface and leaves
// site-wide basic auth off, and the rendered file says exactly that.
func TestExamples_MicrobinAdminIsNotBasicAuth(t *testing.T) {
	files := renderExamples(t)
	got := exampleFile(t, files, "microbin-sea01/server.env")

	for _, want := range []string{
		"MICROBIN_ADMIN_USERNAME=webadmin",
		"MICROBIN_ADMIN_PASSWORD=",
		"MICROBIN_UPLOADER_PASSWORD=",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered server.env has no %s:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{
		"MICROBIN_BASIC_AUTH_USERNAME=",
		"MICROBIN_BASIC_AUTH_PASSWORD=",
	} {
		if strings.Contains(got, unwanted) {
			t.Errorf("rendered server.env carries %s though auth_enabled is false:\n%s", unwanted, got)
		}
	}
}

// TestExamples_ExportsDialThePortsPublishedName pins
// docs/inventory.md#the-name-a-port-is-published-at from the client
// side: two services on one node answer to names of their own, and each
// export writes its service's name rather than the node's address.
func TestExamples_ExportsDialThePortsPublishedName(t *testing.T) {
	files := renderExamples(t)
	for suffix, want := range map[string]string{
		"phone-sea-01-ss-ssserver-link/share.txt":            "@ss.example.net:38250#",
		"laptop-sea-01-ss-ssserver-json-singbox/config.json": `"server": "ss.example.net"`,
		"phone-sea-01-hy2-hysteria2-link/share.txt":          "@hy2.example.net:443/",
	} {
		got := exampleFile(t, files, suffix)
		if !strings.Contains(got, want) {
			t.Errorf("%s does not carry %q:\n%s", suffix, want, got)
		}
		if strings.Contains(got, "sea1.example.net") {
			t.Errorf("%s still carries the node's address:\n%s", suffix, got)
		}
	}
}

// TestExamples_ProfilesListenWhereTheirValuesSay pins
// docs/inventory.md#a-device-with-several-profiles: the example
// laptop's two profiles render one configuration each from the same export,
// and each listens on its own profile's port rather than the defaults'.
func TestExamples_ProfilesListenWhereTheirValuesSay(t *testing.T) {
	files := renderExamples(t)
	for suffix, want := range map[string]string{
		"laptop-sea-01-ss-ssserver-json-singbox/config.json": `"local_port": 2080`,
		"laptop-sea-01-ss-ssserver-json-browser/config.json": `"local_port": 1080`,
	} {
		if got := exampleFile(t, files, suffix); !strings.Contains(got, want) {
			t.Errorf("%s does not carry %q:\n%s", suffix, want, got)
		}
	}
}

// TestExamples_ContainerisedInstanceComposesFromItsManifest is a
// docker.yaml service end to end: the example MicroBin runs in a container,
// so its manifest says how, and the compose file is generated from that
// manifest alone. The port mapping in it is derived — the port is entered
// only by the Caddy on its own machine, so it publishes on loopback — and
// the number is the same one the configuration was rendered from.
func TestExamples_ContainerisedInstanceComposesFromItsManifest(t *testing.T) {
	files := renderExamples(t)
	if _, ok := files["sea1/microbin/microbin-sea01/compose.yaml"]; ok {
		t.Error("a docker.yaml service rendered a compose.yaml of its own")
	}
	got := composeFromManifest(t, exampleFile(t, files, "microbin-sea01/manifest.yaml"))

	for _, want := range []string{
		"- 127.0.0.1:8080:8080/tcp",
		"image: danielszabo99/microbin:2.0.4", // the service's docker.yaml defaults
		"- /srv/microbin-sea01/server.env",    // the credential stays in the file beside it
		"target: /var/lib/microbin/data_dir",  // its state
	} {
		if !strings.Contains(got, want) {
			t.Errorf("composed file has no %q:\n%s", want, got)
		}
	}
	// The compose file carries no credential: what MicroBin's own
	// configuration holds does not cross into it.
	env := exampleFile(t, files, "microbin-sea01/server.env")
	for _, line := range strings.Split(env, "\n") {
		name, value, ok := strings.Cut(line, "=")
		if !ok || !strings.HasSuffix(name, "PASSWORD") || value == "" {
			continue
		}
		if strings.Contains(got, value) {
			t.Errorf("composed file carries %s from server.env", name)
		}
	}
}

// TestExamples_AHostProcessRendersOneFile is the other side of it: every
// other example instance runs on the host, and nothing renders a second file
// for it. The containerised ones are named, so adding a container to the
// examples without meaning to still fails here.
func TestExamples_AHostProcessRendersOneFile(t *testing.T) {
	containerised := []string{"samba-nas"}
	files := renderExamples(t)
	for path := range files {
		if !strings.HasSuffix(path, "compose.yaml") {
			continue
		}
		deployed := false
		for _, instance := range containerised {
			if strings.Contains(path, instance) {
				deployed = true
			}
		}
		if !deployed {
			t.Errorf("%s rendered a deployment file for a host process", path)
		}
	}
}

// TestExamples_DeploymentWritesTheScriptThatPutsItInPlace is the rest of a
// deployment: the compose file says what to start, and the script beside it
// puts the rendered configuration where the runtime expects it and starts
// it. Both come from one render, so the script reaches the same deploy
// values as the compose file, and it is written executable because its
// output name ends in .sh — nothing declares a mode. It is a deploy/
// service's: one holding a docker.yaml renders neither file, see
// TestExamples_ContainerisedInstanceComposesFromItsManifest.
func TestExamples_DeploymentWritesTheScriptThatPutsItInPlace(t *testing.T) {
	root, secretsDir := examplesRoot(t)
	dest := t.TempDir()

	var out bytes.Buffer
	if err := Export(strings.NewReader(""), &out, []string{"*"}, map[string]string{"to": dest, "yes": "true"}, Settings{Root: root, Secrets: secretsDir}); err != nil {
		t.Fatalf("export: %v\n%s", err, out.String())
	}

	var script, compose string
	for _, path := range filesUnder(t, dest) {
		switch {
		case strings.HasSuffix(path, "samba-nas/install.sh"):
			script = path
		case strings.HasSuffix(path, "samba-nas/compose.yaml"):
			compose = path
		}
	}
	if script == "" {
		t.Fatal("the containerised instance rendered no install script")
	}

	info, err := os.Stat(script)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o100 == 0 {
		t.Errorf("install.sh is mode %v, want the owner's execute bit", info.Mode().Perm())
	}
	// A rendered configuration carries credentials and keeps 0600: the
	// execute bit follows the name, not every file of the export.
	other, err := os.Stat(compose)
	if err != nil {
		t.Fatal(err)
	}
	if other.Mode().Perm() != 0o600 {
		t.Errorf("compose.yaml is mode %v, want 0600", other.Mode().Perm())
	}

	data, err := os.ReadFile(script)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	for _, want := range []string{
		"#!/bin/sh",
		"docker compose -f", // what starts it
	} {
		if !strings.Contains(got, want) {
			t.Errorf("install.sh has no %q:\n%s", want, got)
		}
	}
}

// TestExamples_ServiceWritingSeveralFilesRendersEachOne is the multi-file
// service end to end: Samba reads its configuration and its account table,
// and both come out of one render of one instance. A program reading two
// files is one service, so the accounts a share names and the accounts the
// table holds are written from the same facts in one pass and cannot
// disagree.
func TestExamples_ServiceWritingSeveralFilesRendersEachOne(t *testing.T) {
	files := renderExamples(t)

	conf := exampleFile(t, files, "samba-nas/smb.conf")
	table := exampleFile(t, files, "samba-nas/smbpasswd")

	// The service names accounts by person, so the account the share
	// admits, the one the table holds and the POSIX user that owns the
	// files are one name.
	for _, want := range []string{"valid users = alice carol\n", "path = /mnt/vault/00-vault"} {
		if !strings.Contains(conf, want) {
			t.Errorf("rendered smb.conf has no %q:\n%s", want, conf)
		}
	}
	if !strings.HasPrefix(table, "alice:3001:") {
		t.Errorf("rendered smbpasswd does not open with the account and its uid:\n%s", table)
	}
	if strings.Contains(conf+table, "alice-default") {
		t.Errorf("an account carries the credential's name though the service names accounts by person")
	}

	// A home directory belongs to a person. Samba's own [homes] resolves
	// it per user, and %S is the name they logged in with.
	if !strings.Contains(conf, "path = /mnt/vault/11-home/%S\n") {
		t.Errorf("rendered smb.conf has no [homes] directory:\n%s", conf)
	}
}

// TestExamples_TheAccountTableHoldsNoPlaintext is why the table is rendered
// at all rather than the passwords being written into the deployment: what
// reaches the file server is the NT hash, which is what the protocol proves
// knowledge of, and the plaintext stays in the secret store on the machine
// that hands it to a person.
func TestExamples_TheAccountTableHoldsNoPlaintext(t *testing.T) {
	_, secretsDir := examplesRoot(t)
	secret, err := os.ReadFile(filepath.Join(secretsDir, "nas/samba-nas", "smb", "alice", "default"))
	if err != nil {
		t.Fatalf("reading alice's credential: %v", err)
	}
	if len(secret) == 0 {
		t.Fatal("alice's credential is empty, so this test would pass for the wrong reason")
	}

	files := renderExamples(t)
	for _, name := range []string{"samba-nas/smb.conf", "samba-nas/smbpasswd", "samba-nas/users.txt", "samba-nas/compose.yaml"} {
		if strings.Contains(exampleFile(t, files, name), string(secret)) {
			t.Errorf("%s carries the plaintext credential", name)
		}
	}
}

// A profile naming the program that runs it is deployed, so its export
// carries a manifest; a profile without one is for a person, and has none.
func TestExamples_ProfileThatRunsWritesAManifest(t *testing.T) {
	files := renderExamples(t)
	got := exampleFile(t, files, "laptop-sea-01-ss-ssserver-json-browser/manifest.yaml")
	for _, want := range []string{"service: sslocal", "runtime: host", "- path: config.json"} {
		if !strings.Contains(got, want) {
			t.Errorf("manifest does not carry %q:\n%s", want, got)
		}
	}
	for path := range files {
		if strings.HasSuffix(path, "laptop-sea-01-ss-ssserver-json-singbox/manifest.yaml") {
			t.Errorf("singbox profile names no program, yet wrote %s", path)
		}
	}
}

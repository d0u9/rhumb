// This file is `rhumb init`: the scaffold a generator root starts from.
// It writes the files an inventory cannot do without, each carrying the
// comments that say what belongs in it, and nothing that has to be deleted
// afterwards. See docs/inventory.md#starting-a-generator-root.
package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/d0u9/rhumb/confgen"
	"github.com/d0u9/rhumb/inventory"
)

// Init runs `rhumb init [<dir>] [--secrets <dir>] [--gitignore]`.
//
// It is a scaffold, not an example: every file it writes is a valid, empty
// one, so `rhumb check` is clean the moment it finishes and nothing has
// to be removed before real content goes in. A worked inventory to copy from
// lives in examples/conf/ instead.
//
// It never overwrites. A root half-built by hand, or built by an older
// version of this command, gains what it is missing and keeps what it has.
func Init(_ io.Reader, out io.Writer, args []string, flags map[string]string, global Settings) error {
	root := global.Root
	if len(args) > 0 {
		root = args[0]
	}
	if root == "" {
		return fmt.Errorf("no directory given and no generator root set")
	}

	secrets := global.Secrets
	if v := flags["secrets"]; v != "" {
		secrets = v
	}

	// Two roots, two maps: a path is relative to the one it belongs to, and
	// joining the wrong one silently writes into the working directory.
	planned := map[string]string{}
	for rel, content := range scaffold() {
		planned[filepath.Join(root, rel)] = content
	}
	if secrets != "" {
		planned[filepath.Join(secrets, "README.md")] = secretsReadme
		if flags["gitignore"] == "true" {
			planned[filepath.Join(secrets, ".gitignore")] = secretsGitignore
		}
	} else if flags["gitignore"] == "true" {
		return fmt.Errorf("--gitignore writes into the secrets root, and no secrets root is set")
	}

	paths := make([]string, 0, len(planned))
	for p := range planned {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	var written, kept []string
	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			kept = append(kept, path)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(planned[path]), 0o644); err != nil {
			return err
		}
		written = append(written, path)
	}

	for _, p := range written {
		fmt.Fprintf(out, "wrote  %s\n", p)
	}
	for _, p := range kept {
		fmt.Fprintf(out, "kept   %s\n", p)
	}
	if secrets == "" {
		fmt.Fprintln(out, "\nno secrets root: --secrets was not given.")
	}
	fmt.Fprintf(out, "\nThe root holds no nodes and no services yet, so `rhumb check` has\nnothing to report. Add a machine under %s/, a program under %s/, and a way\nof handing that program's credential to a person under %s/<service>/%s/.\n",
		inventory.NodesDir, confgen.ServicesDir, confgen.ServicesDir, confgen.ExportsDir)
	return nil
}

// scaffold is every file a generator root starts with, by path relative to
// the root.
func scaffold() map[string]string {
	return map[string]string{
		inventory.NetworksFilename:                      networksSkeleton,
		inventory.UsersFilename:                         usersSkeleton,
		inventory.RoutesFilename:                        routesSkeleton,
		filepath.Join(inventory.NodesDir, "README.md"):  nodesReadme,
		filepath.Join(confgen.ServicesDir, "README.md"): servicesReadme,
	}
}

const networksSkeleton = `# The networks nodes belong to, most preferred first. When two nodes share
# more than one, the address comes from the first of these they both reach.
#
# ` + "`universal`" + ` names the one every node reaches without saying so. Leave it
# out and nothing is implicit: every node then states what it reaches.
#
# Neither name is known to rhumb. These are this inventory's own.

networks:
  - name: internet

universal: internet
`

const usersSkeleton = `# People. Not Linux accounts, and not passwords.
#
#   access        the routes this person may use, by name
#   credentials   their credentials, each against a note saying where it is
#                 used — free text rhumb never reads. Omitted, they have one
#                 called ` + "`default`" + `. A device names which one it uses, and two
#                 devices naming the same one share a password.
#   username      the account name services see. Defaults to the key here.
#   devices       ` + "`none`" + ` when this inventory deliberately holds no node file
#                 for them, which tells that apart from device files put in
#                 the wrong place.
#   client        which form their files render as, when they are known to
#                 run one program. Omitted, they get every form the services
#                 they reach offer.
#
# A change here is the audit: one diff says who gained or lost access.

users: {}
#  dana:
#    access: [sea]
#    credentials:
#      default: the laptop and the phone
`

const routesSkeleton = `# Routes: one chain each, from where traffic enters to where it leaves,
# written as an ordered list of hops. A hop is ` + "`<node>/<instance>:<port>`" + `, and each
# adjacent pair is one edge whose address rhumb works out from the two nodes.
#
# A route's name is what a person sees when they pick a line in their client,
# so keep the machine out of it — a route may enter one server today and its
# replacement tomorrow — and keep the protocol out unless the protocol is the
# choice being offered.
#
# A one-hop route is an ordinary route. Reaching a service directly is not a
# special form.

routes: {}
#  sea:
#    hops: [sea1/ss-sea01:users]
`

const nodesReadme = `# nodes/

One file per machine or device. Servers and phones are the same kind of thing
here, which is why the word is "node" rather than "host".

Files sit one level down, in a directory naming whose machines these are:

    nodes/
    ├── cloud/sea1.yaml
    ├── home/server.yaml
    └── dana/phone.yaml

A directory naming a user in users.yaml is that person's devices, and fills
"owner" for every file in it. One naming no user is whoever the machines
belong to — a provider, a household, a company — and rhumb reads nothing
further into it. Only one level is read: a directory is a group, never a path.

A server says where it can be reached:

    id: sea1
    networks:
      internet: 203.0.113.10
    instances:
      - id: ss-sea01
        service: ssserver
        ports:
          users: 38250

A device says only what it can reach, since nothing connects to a phone:

    id: dana-phone
    reaches: [home]

For a node with many instances, the node may instead say:

    instances:
      directory: server.instances

Each direct .yaml file in that directory defines one complete instance or a
list of related instances.

See docs/inventory.md#nodes.
`

const servicesReadme = `# services/

One directory per program a node deploys. ssserver and sslocal are two
services, not two forms of one: they read configurations with nothing in
common.

    services/ssserver/
    ├── confgen.yaml
    ├── defaults.yaml
    ├── templates/config.json.tmpl
    └── exports/
        ├── link/
        └── json/

confgen.yaml declares what the renderer cannot infer:

    secret:
      kind: base64
      bytes: 32
    auth: per-principal
    self:
      psk: {set: true}
    template: templates/config.json.tmpl
    defaults: element
    output: config.json

defaults.yaml holds what every instance of the service shares. What one
instance needs to say differently goes in that instance's own values.

See docs/export.md#the-service-manifest.

## exports/

One directory per way of handing this service's credential to a person: a
share URI, a JSON configuration, a QR code. Nothing deploys an export —
nothing runs a QR code — so the manifest is a rendering and nothing else.

    services/ssserver/exports/link/
    ├── confgen.yaml
    └── templates/share.txt.tmpl

An export sits inside the service it writes out because it renders that
service's upstream and nothing else. The directories that exist are the list:
a service offers what it holds, and there is no second place saying so for the
two to disagree.

An export's confgen.yaml has three keys and no more. No ports, since nothing
listens; no auth, since nothing connects to a file; no secrets of its own,
since what it carries belongs to the service it reaches:

    template: templates/share.txt.tmpl
    defaults: element
    output: share.txt

Every one a service offers is written, so a bundle holds each and a selector
narrows to the ones a hand-over needs.

An export is named after the format, not after the program that reads it, and
not after the protocol: the directory above it already says which service this
is. A device asking for "link" takes each service's own link, which is what a
device that can read a share URI means. json is the JSON a shadowsocks-rust
client reads; sslocal is the program, and it is a service, because a home
server deploys it.

See docs/inventory.md#which-export-a-person-receives.
`

const secretsReadme = `# The secrets store

One credential, one file, at <instance>/<port>/<group>/<name>. The group is
whose the credential is — a person, or an instance relaying through — and the
name is which of their identities holds it. An instance's own secrets sit
under <instance>/self/<name> instead, since they belong to the whole instance
rather than to one of its listeners; a name that is a set of values or a
record of fields is one directory deeper again, at
<instance>/self/<name>/<key> or .../<field>. Which of them a port hands to
everything granted on it is the port's own self list.

Everything here is plaintext, and these are random values held nowhere else.
Losing this directory loses the credentials themselves: the recovery is a
rotation of everything, not a restore.

This is a separate root from the generator root on purpose. A directory that
is not under the repository cannot be committed by accident, which a
.gitignore entry only promises.

"rhumb secret sync" fills in what the inventory implies and reports what it
no longer does. It never deletes.

See docs/inventory.md#secrets.
`

const secretsGitignore = `# Everything here is a plaintext credential. Nothing in this directory
# belongs in version control.
*
`

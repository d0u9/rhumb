# Configuration: Export

`rhumb export` renders service configuration files from templates, the
[inventory](inventory.md) and secrets, and writes a chosen set of them to a
folder or a zip archive.

For deployments that declare `install.sh` and `uninstall.sh`, both are exported
beside `compose.yaml` for the instance. `install.sh` starts or updates the
service. `uninstall.sh` stops its runtime and removes disposable generated files
while keeping persistent volumes, mounted data and backups. Run either
script on the host that owns the instance; export itself does not run them.

The templates and the values are in version control, the secrets they draw on are
not, and the rendered result is plaintext that leaves the machine only when
someone asks for it.

## Why it exists

The configuration it renders is kept as one directory per service, with one
value file per instance:

```text
confgen/
├── hysteria2/
│   ├── templates/server.yaml.tmpl
│   └── server/
│       ├── default.yaml
│       ├── u-node-group-09-01.yaml
│       └── u-node-group-09-01.sh
├── microbin/
└── ssserver/
```

Every instance has a `.sh` beside its values that calls the renderer with the
paths worked out from its own name. The scripts are copies of one another:
adding an instance means copying a script and editing it.

Three things follow, and this page addresses each:

- **Per-instance boilerplate.** An instance should be a few lines of values and
  nothing else.
- **Per-service difference.** Services differ in ways that matter — see
  [defaults](#two-kinds-of-defaults) — so the differences become a declaration
  read by one renderer rather than a script per instance.
- **Getting the result out.** Rendering prints to stdout, so collecting the
  files for a machine, or for a person, is done by hand.

### Names get unified

The directories are spelled four ways across three services — `server/` beside
`servers/`, `default.yaml` beside `defaults.yaml` — and the spellings are
accidents rather than decisions. They are unified as part of this work, and
nothing here is designed to keep an old name working: these files have one
consumer, who would rather rewrite them than carry the difference. A design that
finds a cleaner shape should take it and rename what it must.

## What it reads

### The generator root

the generator root (`--root`) holds the services and the inventory:

```text
~/confgen/
├── services/<service>/       confgen.yaml, templates, defaults
├── nodes/<group>/*.yaml       node files
├── nodes/<group>/*.instances/*.yaml  optional instance files
├── users.yaml
├── routes.yaml
└── networks.yaml
```

A subdirectory of `services/` is a service when it holds a `confgen.yaml`, so a
`README`, a scratch folder or a service still being written is skipped rather
than half-read.

The four files beside it are the inventory: the machines, the people, and the
chains between them. [`inventory.md`](inventory.md) describes them, and this page
assumes them.

### The service manifest

`services/<service>/confgen.yaml` declares what the renderer cannot infer. A
service is one program, so a manifest describes one inbound contract and the
files that program reads — one template and one output name for most of them,
a [`files` list](#a-service-that-reads-more-than-one-file) for a program that
reads several. A service that runs in a container holds a second template
beside it, in [`deploy/`](#a-second-file-what-deploys-it):

```yaml
# services/ssserver/confgen.yaml
secret:
  kind: base64
  bytes: 32

auth: per-principal
self:
  psk: {set: true}
template: templates/config.json.tmpl
defaults: element
output: config.json
```

```yaml
# services/ssserver/exports/link/confgen.yaml
template: templates/share.txt.tmpl
defaults: element
output: share.txt
upstream:
  shared: {}
```

`ssserver` and `sslocal` are two services rather than two forms of one: they
read configurations with nothing in common. An **export** is not a service at
all — nothing runs a QR code — so it lives in its service's own `exports/` and
its manifest has a template, a defaults kind, an output name, and
`upstream` when the credential it writes is partly the server's.
The directories a service holds are the ways it offers; nothing lists them a
second time. See
[which export a person receives](inventory.md#which-export-a-person-receives).

The keys in the table below are fixed. Every name around them — the services,
the templates, the output files — is this manifest author's, and no rule keys
off any particular spelling. The two lists are in
[fixed names and example names](inventory.md#fixed-names-and-example-names).

| Key | Meaning |
| --- | --- |
| `secret` | The shape of a generated credential for this service: `kind` and its size. Omitted, a printable random string. |
| `auth` | Whether this service's inbound side authenticates each principal separately: `per-principal` or `none`. Its own credentials are `self`'s business, not this key's. See [inventory](inventory.md#how-a-service-says-what-it-needs). |
| `template` | The template rendered for this service, relative to the service directory. The one-file form, written with `output`. |
| `files` | What this service writes when one file is not enough: a list of `template` and `output` pairs, in the order written. A manifest declares this or `template` and `output`, never both. See [a service that reads more than one file](#a-service-that-reads-more-than-one-file). |
| `defaults` | How `defaults.yaml` applies: `document` or `element`. See below. |
| `output` | The name the rendered file is written under — the name the program expects where it runs, such as `config.json` or `server.env`. |
| `accounts` | `person` when the account name a server sees is the person's username alone, rather than `<username>-<credential>`. Written only beside `auth: per-principal`. See [the account name](#the-account-name). |
| `rotation` | `disruptive` when this service's template cannot emit two accounts for one principal, so rotating it drops the connection instead of overlapping old and new. Omitted, it can. See [rotation](inventory.md#rotation). |
| `self` | This service's own secrets — credentials belonging to the instance rather than to anything reaching it — declared by name, each with its shape (`kind`, `bytes`), and optionally `set: true` for a family of values whose keys an instance declares, or `fields` for a credential made of several generated parts. One file per leaf, under `<instance>/self/`. It is the only place they are named, so `secret sync` generates what is declared and reports what is not. See [a service's own secrets](inventory.md#a-services-own-secrets). |
| `forwards` | `true` when an instance of this service terminates nothing: it moves the bytes of one of its ports to the hop that follows it and reads none of them. A client entering here dials this instance and authenticates against the hop behind it, so the route is written out as the service that ends it, and this one holds no account and no secret. Written with `auth: per-principal` or with `self`, the manifest does not load. See [a service that forwards](inventory.md#a-service-that-forwards). |
| `dispatch` | How an instance that is the entrance for several routes tells one from another: `name`, the published name of the port each route reaches, which is what a reverse proxy matches on; or `port`, the port of this instance each route arrived on, which is what a relay listens on. Read only beside `downstreams: many`, written elsewhere it does not load, and `name` when left out. See [a service that fans out](inventory.md#a-service-that-fans-out). |
| `downstreams` | `many` when one instance of this service is the entrance for several routes — a reverse proxy in front of many web services — or omitted for the ordinary `one`. It is the only thing that relaxes the rule that a non-terminal hop has the same successor in every route through it, and it says nothing about credentials: a proxy forwards and does not authenticate, so declaring it hands the service nothing. See [a service that fans out](inventory.md#a-service-that-fans-out). |
| `upstream` | What this service needs from the hop it dials, beyond the address, port and account every template is given, declared by name. Two names today: `shared`, the secrets that hop's port hands to everything granted on it, in the port's own order; and `values`, that hop's instance's own values, for a parameter the two ends have to agree on — a Hysteria2 obfuscation mode, a port-hopping range — written once on the instance that listens. A secret crosses from one instance to another because the program dialling declares it needs it, never because the one listening publishes it, so a service that writes nothing here is handed nothing. An export takes the same keys for the same reason. A declaration carries `optional: true` where the hop may hand over nothing, which is how one program obfuscating on one machine and not on another is written. A name rhumb does not understand is an error. See [what a service needs from its upstream](inventory.md#what-a-service-needs-from-its-upstream). |

The defaults file is always `defaults.yaml` in the service directory, so it is
not declared.

There is no `secrets` key naming a file. Which credentials a service draws on is
derived from the inventory, and where each one lives is derived from what it
opens; see [Secrets](inventory.md#secrets).

Services are directories rather than two fixed names `server` and `client` so
that a protocol wanting a third — a relay, a bridge, a second client program —
gets one by adding a directory rather than by waiting for the renderer to grow
a case for it. Exports are directories for the same reason: a QR code is one
more directory, not a new case in the renderer.

### A service that reads more than one file

Most programs read one configuration file. Some read several, and the several
are one configuration: Samba keeps passwords nowhere near `smb.conf` — they are
NT hashes in an account table beside it.

Those are not two services. They are read by one program, they describe one
set of accounts, and a service each would put one truth behind two manifests
that could disagree — a share admitting an account the table does not hold
renders cleanly and authenticates nobody.

So a manifest may write `files` in place of `template` and `output`:

```yaml
# services/samba/confgen.yaml
auth: per-principal
accounts: person
defaults: document
files:
  - template: templates/smb.conf.tmpl
    output: smb.conf
  - template: templates/smbpasswd.tmpl
    output: smbpasswd
```

Every file renders from one `defaults.yaml` and one render context, in the
order declared, so what one names and what another holds come from the same
facts in one pass. The rules are the deployment's, for the same reasons:
writing one output name twice is an error where the manifest is rather than
once per instance at render time, and an output ending in `.sh` is written
with the execute bit.

Writing `files` together with `template` or `output` is an error: the two say
the same thing, and a manifest holding both leaves which one renders up to
whoever reads the code.

### The account name

An account name is ordinarily `<username>-<credential>`, because a credential is
what is revocable and one person may hold two on one port; see
[credentials belong to the person](inventory.md#credentials-belong-to-the-person). A service whose accounts are POSIX
users writes `accounts: person`, and the name is the username alone.

Samba is the case. The name a client logs in with is the POSIX user that owns
their files, and `[homes]` resolves a home directory from it, so a suffix there
is a second name for the same account — one that either needs a username map
beside `smb.conf` to undo, or names every home directory after a credential.

It is the service's choice rather than a person's, and it changes the name
only. The secret is still the credential's, under the same path, so switching a
service to it regenerates nothing. What it gives up is a second credential per
person on that service's ports: two would be two accounts under one name, and
[rule 13](inventory.md#validation) reports it where the grant is written.

A template grouping by person rather than by account still has
[`grantees`](inventory.md#the-render-context), for a service that keeps the
ordinary name.

### Two kinds of defaults

`defaults` is declared because the services genuinely differ, not because they
drifted.

- **`document`** — the defaults are a whole configuration, and an instance's
  `values` are merged over them, key by key, the instance winning. Hysteria2
  and MicroBin work this way: the TLS paths, the masquerade target and the
  MicroBin environment block are the baseline, and an instance overrides keys
  of it. What merges is `values` alone: `id`, `service`, `bind` and `ports`
  are rhumb's own, are reached through the `instance` function, and are not keys
  of the document. So the dot a template reads is the service's settings and
  nothing else, and `values` is the one place a node file writes them. See
  [an instance's own values](inventory.md#an-instances-own-values).
- **`element`** — the defaults are one entry of a list, and apply to *every*
  entry of that list in an instance. Shadowsocks works this way: an instance's
  `servers` is a list of services on different ports, and each one starts from
  the same baseline entry. Merging at the document level here would put the
  baseline's one-entry `servers` list against the instance's list rather than
  into each of its entries.

The existing Shadowsocks templates do the element-level merge themselves,
reading the defaults through a second datasource. Whether the renderer performs
it instead and hands the template a finished list is
[open](#open-questions).

Unknown keys are an error, as everywhere else rhumb reads configuration, so a
misspelled key cannot leave a setting quietly at its default.

### Instances

An instance is an entry in a node file's `instances` list, a mapping or list
entry in a file in the directory that node names under `instances.directory`,
or one derived from a user's access — see
[What is derived](inventory.md#what-is-derived). Nothing registers it: adding an
instance is adding a few lines to the node it runs on or a file to its instance
directory, and the next run lists it.

A node file that is not valid YAML, or an instance entry or file that is not a
mapping, is listed as **broken** with the parse error, rather than omitted — an
instance that silently stops appearing is worse than one that appears with a
reason it cannot be rendered.

### An instance is one file on one machine

One machine may hold several instances of one service: two Hysteria2 servers, or
several Shadowsocks clients reaching different servers. An instance is one
rendered configuration file, and the node it belongs to is the node file that
contains it or names its directory.

Two things follow:

- **No values file names a secret.** Which credential a rendered file carries
  follows from what it connects to and who it connects as, both of which the
  inventory states. A key typed into a values file and matched by hand against a
  server is the thing this replaces.
- **The instance name is free.** It is an identifier for the reader, not a host
  name the renderer parses. It must be unique across the inventory, because hops
  name instances with nothing to qualify them; beyond that nothing checks it.

### Output names do not vary

A service's `output` is one name for every instance of it, because the program
reading it expects one name wherever it runs. Two clients reaching different
servers both render to `config.json`, and are told apart by the directory the
export puts them in, not by the file name. Copying the right one to the right
machine is done by hand and deliberately so: a name that varied per instance
would mean editing a service unit to match every time.

### How a container is started: docker.yaml

A service's configuration says how the program behaves; something else says how
it is started. For a containerised instance that is a compose file, and most of
one is the same for every program: one project per instance, a container name,
the container networks it joins with its alias and fixed address, each port
published at the addresses the model derives, and a script that creates the
networks and directories and copies the files into place. Only a little of it is
the program's own: its image, which transport each port speaks, where in the
container it reads each rendered file, what state it keeps.

So a service says only that, as data, in `services/<service>/docker.yaml`, and
the compose file is generated from it by `rhumb deploy build`. No service writes
a compose template:

```yaml
# services/caddy/docker.yaml
ports: {http: tcp, https: tcp}          # every port the instance declares
mounts:                                 # a directory of the instance's, mounted
  conf: {target: /etc/caddy, files: [Caddyfile]}
files:
  caddy.env: {env_file: true}           # the container's environment, 0600
  index.html: {host: "{{ (instance).values.static_root }}"}
state: {data: /data, config: /config}   # kept across reinstalls
volumes:                                # machine paths every instance mounts
  - {source: "{{ (instance).values.static_root }}", target: "{{ (instance).values.static_root }}", ro: true}
reload: [caddy, reload, --config, /etc/caddy/Caddyfile]
defaults:                               # what an instance's deploy overrides
  image: d0u9/caddy-cloudflare:latest
  restart: unless-stopped
  container_name: srv.caddy
```

| Key | Meaning |
| --- | --- |
| `ports` | Each port's transport: `tcp`, `udp` or `both`. A port the instance declares and this does not name is an error. |
| `files` | Where a rendered file goes, by output name: `target` mounts it alone at that container path; `env_file` hands it to the container as its environment; `host` copies it to that machine directory and mounts nothing. `mode` is its permission bits, `0644` unless written, `0600` for an env file. A file named nowhere is installed into the instance's directory. |
| `mounts` | Directories of the instance's directory, by name, mounted at `target` and holding the named `files`; read-only unless `writable`. `mode` applies to its files, and a directory whose files only their owner reads is itself `0700`. |
| `state` | What the program keeps between runs, by name: a container path, kept in that directory of the instance's directory, or `{target, named: true}` for a named volume. Reinstalling leaves it alone. |
| `volumes` | Machine paths every instance mounts, in the shape of an instance's `deploy.volumes`, which are added after them. |
| `environment` | Set in the container as written. |
| `privileges` | Merged into the compose service: only `devices`, `cap_add`, `cap_drop`, `sysctls`, `read_only`, `security_opt`, `tmpfs`. |
| `reload` | Run in the container once it is started, for a program that does not reread its configuration. |
| `setup` | A rendered file of the service, a shell script, run on the machine after the container starts, from the instance's directory, with `CONTAINER` set to its name: what only this program needs done once it is up, such as creating its accounts. |
| `defaults` | The keys an instance's [`deploy`](inventory.md#what-a-container-needs-beyond-the-model) may override. |

**Only a string value may hold an expression.** It is rendered with the view a
configuration template has, plus the instance's `deploy` over the defaults as
the document, and `(instance).subnets`, the ranges of the container networks it
joins. A value that renders empty drops what it is part of — a mount, a file's
placement — which is the one way anything here is conditional. The file itself
is never a template: the deployment tool reads its structure before anything is
rendered, which is what lets it generate the compose file for every service the
same way.

The export writes the rendered files and the [manifest](#the-manifest), which
carries the resolved `container`; no compose file and no script are exported.
`rhumb deploy build <export-dir> --to <bundle>` writes them from the manifest
alone: `compose.yaml`, the files, and a `ctl` whose `install` creates the
networks and directories, places the files (keeping a replaced one as `.bak`),
and starts the container. The instance's directory is the compose project's, so
its name is the project's name and a named volume's prefix.

A service holds a `docker.yaml` or a [`deploy/`](#a-second-file-what-deploys-it),
never both. `deploy/` remains for a service whose containers do not fit the
shape above — several one-shot containers, each with its own mounts — and
renders its own compose file from templates.

### A second file: what deploys it

A service's configuration says how the program behaves. Something else says how
the program is started, and under a container runtime that second file repeats
the ports — the same numbers, in another spelling, maintained by hand. Two
truths, and the model's own [one-to-one rule](inventory.md#what-runs-the-process)
is only a convention until one of them is generated.

So a service directory may hold a `deploy/`, laid out the way an export is. It
is the older of the two ways and the one for a container that does not fit
[`docker.yaml`](#how-a-container-is-started-dockeryaml); the example below
predates it:

```text
services/microbin/
├── confgen.yaml
├── defaults.yaml
├── templates/server.env.tmpl
└── deploy/
    ├── confgen.yaml          defaults, and one entry per file written
    ├── defaults.yaml         what every instance of this service deploys with
    └── templates/
        ├── compose.yaml.tmpl
        └── install.sh.tmpl
```

```yaml
# services/microbin/deploy/confgen.yaml
defaults: document
files:
  - template: templates/compose.yaml.tmpl
    output: compose.yaml
  - template: templates/install.sh.tmpl
    output: install.sh
```

A deployment writes **more than the file its runtime reads**, and the second
file is why `files` is a list rather than one template and one output. The
compose file says what to start; it does not put the rendered configuration
where the runtime expects it, and until something does, the export is two
files and a paragraph someone follows by hand on every machine — which
directory, which order, what starts it afterwards. That paragraph is a
template like any other, rendered from the same view, so it says the instance's
own port, path and image rather than a placeholder a reader fills in.

Each entry is a template and an output name. `defaults` is the deployment's
rather than the entry's: one `deploy/defaults.yaml` is merged once and handed
to every file, so a script and the compose file beside it cannot disagree about
what the instance deploys with. Two entries writing one output name is an
error — the second would overwrite the first in a folder and duplicate an entry
in a zip, and which survived would depend on the writer. A `deploy/` writing no
file at all is an error where the manifest is, rather than once per instance at
render time.

**An output ending in `.sh` is written with the execute bit**, in a folder and
in a zip alike. Nothing declares it: the name is the declaration, and a `mode`
key saying it a second time is the kind of second truth this directory exists
to remove. Every other rendered file keeps `0600`, because it may carry a
credential and this one does not.

**Where the script puts things is an ordinary deploy value.** The destination
path is a property of the machine, not something rhumb derives, so it lays over
`deploy/defaults.yaml` exactly as `image` and `restart` do, and an instance on a
host that keeps its services elsewhere overrides it. rhumb deriving an install
path would be rhumb holding an opinion about the filesystem of a machine it
never opens a connection to.

The directory is what declares it, the way `exports/<name>/` declares a way a
service offers, so the service's own manifest grows no key. `deploy` is a fixed
name and the only one read there; a service holding no `deploy/` renders its
configuration alone, and an instance of it writing `deploy` values is an error.

**It is not an export**, and the difference is who selects it and who reads it.
An export is derived from a person's access, one file per route they hold, it
carries their credential, and a node or a user names it with `export:`. A deploy
file belongs to an instance written in a node file, is rendered because that
instance's `runtime` is a container runtime, holds no credential, and nobody
selects it. Putting it under `exports/` would put one name in two selection
rules: `export: compose` would pass [rule 3](inventory.md#validation), whose
error message lists the ways a person's services offer, and a reader would be
told a lie about why their file is missing.

What it does share is the shape — templates, a defaults kind, output names — so
the renderer, the defaults merge and the target are the existing ones.
`deploy/defaults.yaml` carries what every instance of the service deploys with,
an image and its tag being the clear case, and an instance's `deploy` mapping
lays over it key by key, exactly as `values` lays over `defaults.yaml`.

It is the same renderer, the same target, the same directory. A target that
renders a deployment writes its configuration and every file of that
deployment side by side:

```text
node-1-server-linux-01/
└── microbin/microbin-network-1-01/
    ├── server.env
    ├── compose.yaml
    └── install.sh
```

**What a deploy template is given** — every file of the deployment is rendered
from one view — is the instance's own, plus the two things only the model can
resolve:

```yaml
node:         as elsewhere: id, networks
instance:     as elsewhere, plus runtime and deploy
mapping:      per port, the addresses the container runtime binds on the host
              and the number, which is the port's own number. A template reads
              one as `mapping "<port>"`, the way it reads `published "<port>"`,
              and writes one published port per address
downstreams:  as elsewhere, for a service declaring downstreams: many
```

`mapping` is the whole point of the file, and it is derived, never written:

- An edge between two instances sharing a
  [container network](inventory.md#container-networks) asks for nothing: it
  never reaches the host. A backend behind a proxy on a shared container
  network, entered by nothing else, publishes nowhere, and its mapping is
  empty. A template writes no `ports` for it.
- A port entered by hops from its own node, from outside its container
  network, publishes on `127.0.0.1`. A
  reverse proxy's backend is this case, and a backend published on every
  interface because someone typed it is what the derivation removes.
- A port entered from another node publishes on this node's address on the
  network that edge resolved, when that address is a literal one, and on
  `0.0.0.0` when it is a name — a container runtime binds addresses, and a
  node writing `internet: example.net` has not given it one.
- A port no edge enters — one reached from a browser, or by a resolver's
  clients, which this inventory does not model — publishes the same way as
  the second case, on every network this node answers on: it is reached from
  outside, and nothing here says from where. A node with no address anywhere
  publishes on `0.0.0.0`, since there is no interface to name and publishing
  nothing renders a container nobody can reach.
- A port some route starts at is reached from outside too, whatever else
  enters it, and publishes the same way. A web interface behind a proxy on
  its own node, kept reachable directly as a way in when the proxy is down,
  is this case: loopback for the proxy, this node's address for the route
  starting at it.

**It is a list, because the two above are not exclusive and neither is one
network.** A port a proxy beside it dials and another machine dials is
reached at loopback *and* at this node's address, and publishing one of the
two leaves the other end dialling a number nothing published. A machine with
a port on each of two segments answers on both, and one address would leave
the second segment with nothing listening. The addresses come in the
inventory's [preference order](inventory.md#networks-and-how-an-address-is-chosen),
loopback first, so a rendered file does not change because a network was
added above another — and `0.0.0.0` among them takes the whole port, since it
already covers every interface and a second bind on one of them would fail.

The number is the port's number on both sides of the mapping. There is no other
number to choose from: the program's own configuration is rendered from the same
field, so a mapping that changed it would point at a listener that does not
exist. The [one-to-one rule](inventory.md#what-runs-the-process) stops being a
convention here, because both spellings now come from one field.

**A deploy file carries no secret**, script included. The credential the
instance holds is in the file beside it, and a deploy template references that
file by its `output` name — `env_file`, a mount, an argument. A rendered `docker-compose.yml` with a
password inlined would put one in a file people paste into chat, and would make
two files that must be rotated together out of one.

**rhumb still does not deploy.** It renders these files and stops: it opens no
connection to any machine, runs no container runtime, copies nothing anywhere,
and reads nothing back. Rendering is local and offline, and a rendered file
carrying plaintext credentials is a reason to keep it that way: what leaves this
machine, and how, stays the operator's decision and their transport. What is
in that file beyond ports and mounts — the image and its tag, volumes, restart
policy, health checks — is the instance's `deploy` values, which rhumb treats
the way it treats [`values`](inventory.md#an-instances-own-values): as opaque
YAML it hands to a template. [Deployment is not
here](inventory.md#boundaries) is unchanged. Generating the file that a
deployment tool consumes is not deploying, in the same way that rendering
`config.json` has never been running the server.

### Targets and selectors

The unit of everything below is a target, and a target is one instance.

A selector is one or more `<field>:<value>` terms, separated by spaces. **Terms
naming one field are alternatives, and different fields narrow each other.** A
value may contain `*`:

```text
node:host-a                          everything that machine runs
user:dana                            everything every device of one person needs
service:hysteria2                    one service
route:home-sea                       every instance along one chain
instance:ss-sea01                    one
node:u-node-group-09-*                        a machine name with a wildcard
user:erin export:link                one person, one way
export:link export:json              either way, whoever holds it
node:laptop profile:singbox          one use of one device
```

A bare word with no field is an instance name.

`profile:` picks among the files a device with
[profiles](inventory.md#a-device-with-several-profiles) is written out as, and
matches nothing else: a machine's instances, a device without profiles and a
file a person carries have no profile.

Alternatives within a field are what make picking several ways of handing one
credential over possible — a Shadowsocks server is written out as a share URI
and as a `config.json` alike, and a hand-over sometimes wants one, sometimes
both. It is also what makes a shell's own brace expansion do what it looks like
it does, since `export:{link,json}` expands to exactly those two terms:

```bash
rhumb export user:erin export:{link,json}
```

The selector reads fields rather than a `<service>/<instance>` path
because the machine is a field. A path glob could only pick out one machine's
instances when the machine's name had been written into each instance's name,
and `user:` and `route:` could not be expressed at all — while those two are the
exports performed most often, one per person and one per chain.

A selector matching no target is an error naming the selector, not an empty
export.

`rhumb targets` writes every target found, with its status, to stdout and
returns without opening the TUI, as App reports
describes. It is how a reader checks what the generator root holds, and what a
selector will match, without rendering anything.

## Rendering

A target renders from four inputs:

1. the service's `defaults.yaml`,
2. the instance's own values, applied over the defaults at the level the
   service declares,
3. what the inventory derives for it — the node, the resolved upstream, the
   principals holding a grant on each port, and the secrets those imply,
4. the target itself — its service and instance names, as a datasource named
   `target`.

The third is the whole of [`inventory.md`](inventory.md) arriving as data, and
its shape is pinned there as
[the render context](inventory.md#the-render-context): it is the contract
between the inventory and every template, so changing it is a breaking change.

The context is a set of datasources rather than one deep-merged mapping so that
the values stay exactly what the service's own configuration says, with nothing
reserved inside them, and so a template that fails can be told which level it
was reading.

Secrets are read at render time and held only as long as the render. They are
not written anywhere except the output the export publishes.

### The template language

The existing templates are gomplate. Across all three services they use ten
functions: `coll.Append`, `coll.Has`, `coll.Merge`, `coll.Omit`, `coll.Slice`,
`data.ToJSON`, `data.ToYAML`, `dict`, `ds` and `required`.

rhumb installs one binary and may not call a `gomplate` on `PATH`, so gomplate
is compiled in or it is not used. gomplate does work as a library —
`gomplate.NewRenderer` with the datasources given as `RenderOptions`, which
renders these templates unchanged — but importing the whole of it brings its
datasource backends with it. Measured against a stripped 1.6 MB empty program,
with rhumb itself at 24.6 MB today:

| What is imported | Binary | Added |
| --- | --- | --- |
| `gomplate/v4` | 84.3 MB | +82.7 MB |
| `gomplate/v4/coll` and `/data` | 18.7 MB | +17.1 MB |
| `gomplate/v4/coll`, marshalling YAML and JSON directly | 3.6 MB | +2.0 MB |
| nothing | 1.6 MB | 0 |

The first line pulls in 155 modules — the AWS, Azure and Google Cloud SDKs,
`k8s.io/client-go`, Vault, Consul and OpenTelemetry — for datasource backends
this reads none of, since every datasource here is a local file. Most of the
second line is `cuelang.org/go`, behind `data`'s CUE parser.

**The last line is the one taken: gomplate is not a dependency.** The functions
are small — `coll.Merge` is a twenty-five line recursive map merge where the
override wins and a list replaces a list rather than merging into it — and
`Masterminds/sprig`, the obvious alternative at +5.1 MB, does not carry a
`toYaml` at all, so that would be paid for and then written anyway.

Not inheriting gomplate's function set means not inheriting its spelling either.
`coll.Merge` is a function named `coll` returning a struct with a `Merge`
method, a namespace worked around rather than a namespace; with the names free,
they are flat:

| Function | What |
| --- | --- |
| `merge` | Merge maps, later argument losing, lists replaced whole. |
| `omit`, `pick` | A map without, or with only, the named keys. |
| `has` | Whether a map has a key or a list holds a value. |
| `append`, `slice`, `dict` | Building a list or a map in a template. |
| `toYAML`, `toJSON` | `yaml.Marshal` and `json.Marshal`, which rhumb already has. |
| `required` | The value, or an error naming what is missing. |
| `b64` | base64url without padding, which is what a share URI's userinfo is. |
| `join` | Values joined by a separator, which is how the protocols taking more than one credential take them: a Shadowsocks 2022 password is the server's PSK and the user's own, joined by a colon — `join ":" (upstream).shared` beside `(upstream).secret`, for a manifest that declared it needs them. |
| `nthash` | The NT hash of a password: the MD4 digest of its UTF-16 little-endian encoding, which is the value SMB proves knowledge of. It is not a password hash and nothing treats it as one — it is unsalted, and knowing it is knowing the password — so a file holding one is as sensitive as the password it stands for. |
| `smbpasswd` | One line of a Samba account table, from an account name, the POSIX uid it maps to, and the credential. It writes the whole line rather than leaving a template to place six colons correctly, and it writes the hash, so a deployed account table holds no plaintext. |
| `secret` | The instance's own secret of a given name, narrowed by further arguments: a key for a `set` name, a field for a name with `fields`, both for a name with both. Given fewer arguments than the name has levels, it returns the map of what is under it. |

| `mapping` | Where a container runtime publishes one of this instance's ports on the machine it runs on: `.Addresses`, one per network it is reached over, and `.Number`. Both are derived, and it is read only in a [deploy template](#a-second-file-what-deploys-it) — every other render is handed none. |
| `published` | The name one of this instance's own ports answers to, by port name, or empty. A service behind a reverse proxy renders the same string the proxy matches its site block on — `DOMAIN=https://{{ published "web" }}` — so the two cannot disagree. See [the name a port is published at](inventory.md#the-name-a-port-is-published-at). |

Beside them, one accessor per datasource — `defaults`, `node`, `instance`,
`upstream`, `downstreams`, `principals`, `grantees` and `target` — which is how a template reads
[the render context](inventory.md#the-render-context). They are functions
rather than fields of one value so that a template naming a level that does
not exist for it, an `upstream` on a terminal instance, fails where it is
written.

`secret` is new, and replaces the loop every template currently opens with: each
walks the whole secrets file with `coll.Has` to find the entry whose `servers`
list holds its key. That loop goes away entirely. A per-principal credential
arrives in the context already matched to the principal that holds it, and
`secret` is left for an instance's own secrets — a TLS key, an administrative
password — named rather than searched for. A name that matches nothing is an
error naming it rather than an empty `dict` that fails further down.

`merge`'s behaviour is pinned by tests rather than by a dependency, the
list-replaces-list rule above especially: element-level defaults exist precisely
because of it.

The cost this accepts is error messages. `text/template`'s own are poor —
`executing at <.foo>: nil pointer evaluating interface {}.foo` says nothing
about which instance failed — so every render error is wrapped with its service,
service and instance before it reaches the page, and `required` says what was
missing and where it was looked for.

## What is written

A file whose output name ends in `.json` is indented two spaces per level,
keeping its keys in the order the template wrote them. This applies to the
page, Show and the command line alike. A template writes JSON the way that is
easiest to template; an export is read by a person first. Output that does not
parse is written unchanged, so the server that reads it reports the error.

Every target becomes its own directory, under the node it belongs to, whether or
not it is the only one:

```text
u-node-group-09-01/
├── hysteria2/hy2-sea01/config.yaml
├── microbin/bin-sea01/server.env
└── ssserver/ss-sea01/config.json

macbook/
├── hysteria2-link/macbook-jp-hysteria2-link/share.txt
├── ssserver-link/macbook-sea-ssserver-link/share.txt
└── ssserver-json/macbook-sea-ssserver-json/config.json
```

A deployment is filed under the service it runs; a file written for a person,
under the service and the way it was written. Both halves are needed, since an
export's name is unique only within its service and two services each offering
a `link` would otherwise share a directory. A device gets one directory per way
the services it reaches offer, unless it narrows with its own `export`.

The bundle of someone with no device file is named for the person rather than a
node, since there is none, and holds one directory per credential they keep:

```text
friend-a/
├── hysteria2-link/yak-default-jp-hysteria2-link/share.txt
├── ssserver-link/yak-default-sea-ssserver-link/share.txt
└── ssserver-json/yak-default-sea-ssserver-json/config.json
```

The layout says which machine each file is for, where it came from and what it
is, without a note beside it, and two instances of one service cannot collide. Both
the node directory and the instance directory are kept in the single-target case
too: a layout that changes shape with the number of targets is one a script
reading it has to handle twice.

### The manifest

Every instance on a node also writes `manifest.yaml` beside its files, whether
it runs on the host or in a container. A file written for a person has none,
because nothing deploys it -- unless it is a device profile that names, with
`runs`, the service whose program reads it on the device. That manifest has
`runtime: host`, the profile's `service` as `runs` names it, and `files`
only: the program listens where the file says, so there are no ports,
networks or accounts to carry. The manifest is what a deployment tool reads in
place of the inventory: the derived values a deployment needs, and nothing
the tool would have to interpret.

```yaml
schema: 1                 # a reader refuses a schema it does not know
node: node-8-server-linux-01
instance: caddy-01
service: caddy
runtime: docker           # the instance's runtime, host when unset
root: /srv/docker         # deploy values' root, lifted out of deploy
files:                    # every file written for this instance, in order
  - path: Caddyfile
  - path: install.sh
    executable: true
ports:                    # by name; bind is the derived host mapping
  - {name: https, port: 443, bind: [10.0.30.10], published: server.example.com}
networks:                 # container networks joined, with a fixed address
  - {name: tailnet, subnet: 172.29.250.0/24, address: 172.29.250.10}
accounts: {}              # the node's accounts, containerised instances only
deploy: {}                # deploy/defaults.yaml with the instance's deploy over it
container: {}             # a docker.yaml service's container, resolved
```

`host_port` appears on a port only when the host publishes it under another
number. `deploy` is carried as the templates see it and is not given a schema
here: its keys are each service's own mechanism, which belongs to the tool
that deploys it. An instance's `values` are not in the manifest; they
configure the program, and reach it through its rendered files.

For a service holding a [`docker.yaml`](#how-a-container-is-started-dockeryaml),
the manifest carries `container` instead of `deploy`: the instance's `dir`,
container `name` and `hostname`, `image`, `restart`, `user`, `dns`,
`env_files`, `environment`, every bind mount with its absolute `source` (and
`create` for the ones inside `dir` that installing makes), named `volumes`,
`privileges`, `reload` and `setup`, with no expression left in any of them.
Each port gains its `transport`, and each file its `place` — relative to `dir`,
or an absolute machine path — and `mode`. It is everything `rhumb deploy`
needs, and all it reads.

A test renders every containerised instance and checks that the ports,
networks, addresses, container name and deploy volumes of the compose.yaml
beside the manifest, or composed from it for a `docker.yaml` service, agree
with it; the test lives in rhumb's `cli` package. Set `RHUMB_ROOT` and `RHUMB_SECRETS`
to run the same check against a real generator root.

An export renders every target first, and publishes only once all of them have
rendered. A failure anywhere reports the target and the error and writes
nothing — a half-written folder of configuration is the one outcome worth
ruling out, since what is missing from it is not visible in it.

Publication is the toolbox's existing one: a checked temporary file linked to
its final name, as `cred/vault.md` describes, so
an export cannot silently replace a folder or an archive that is already there.

Exporting the same selection twice is the normal case, though — a machine's
configuration changes and is sent again — so a destination already holding
these files is a question, not a failure. The plan names every file and marks
the ones already on disk, and the confirmation asks about replacing them; a
replaced file is written through the same temporary file, renamed over the old
one, so a reader sees either the old file or the new one. Answering no writes
nothing at all, including the files that were not in the way: a bundle written
half from this export and half from the last one is not a thing anyone asked
for.

`--yes` answers that question only when nothing is in the way. Replacing a file
from a script is `--overwrite`, because `--yes` means "do not ask me what I
already told you" and not "whatever is there, discard it".

### Writing to stdout

`--to -` writes to stdout instead of into a directory. The one command wanted
most often is a share URI on its way to a clipboard or a QR encoder, and a
directory holding one file, made to be read once and deleted, is a detour
through the disk that the credential did not need to take.

```bash
rhumb export user:erin export:link --to - | pbcopy
```

**Stdout carries the rendered bytes and nothing else.** The plan and the
plaintext warning go to stderr, so a program reading the pipe reads the file a
directory would have held, byte for byte, whatever its format.

**One file, or an error.** `--to -` with a selector matching several files
fails, naming each of them. Two rendered files concatenated are no longer
either of them — a `config.json` following a `share.txt` parses as neither —
and separators that made them readable to a person would take the pipe away
from every program. The error names what matched, because narrowing the
selector is what the caller does next.

`--format yaml` is the other half: every file on one stream, each keeping the
path it would have had in a bundle.

```yaml
files:
  - path: erin/ssserver-link/erin-default-sea-01-ssserver-link/share.txt
    content: |
      ss://...
  - path: dana/ssserver-json/dana-default-sea-01-ssserver-json/config.json
    content: |
      {"server": "..."}
```

It is a document rather than a concatenation: `yq -r '.files[0].content'` hands
back the file, and nothing is lost by the file next to it. A rendered file that
is not valid UTF-8 cannot be a YAML scalar, so it carries `encoding: base64`
and says how to read itself back rather than being written out mangled.

The two are separate spellings because they answer different questions. `--to
-` alone is "give me this file"; `--format yaml` is "give me these files and
their names". One flag meaning both would make the shape of the output depend
on how many targets a selector happened to match, which is the thing a script
cannot handle.

`--format` needs `--to -`; against a directory it is an error, since a bundle
already carries paths in its layout. `--overwrite` against `--to -` is an error
too: it replaces files a destination already holds, and stdout holds none.
Neither `--yes` nor the confirmation applies, because nothing is replaced and
nothing is left behind — the pipe is the answer.

## Secrets leaving the machine

Everything an export writes is plaintext. It is the same material the vault
holds encrypted, in the form a server reads it.

- The secrets tree is a root of its own, outside the generator root (`--root`) and so outside the
  repository the inventory lives in; a directory that is not under the
  repository cannot be committed by accident, which a `.gitignore` entry only
  promises. An export destination is not covered by that, so the export destination
  defaults outside any repository, and a destination inside one is a warning on
  the confirmation dialog naming the repository.
- The confirmation dialog names every file to be written and says that the
  result is plaintext.
- A plain `.zip` sent to someone is the secret in transit. The recipients and
  sealing `rhumb cred` already has answer this: an archive encrypted to a host or
  a group, published as `.zip.age`, is the same export with `seal` in front of
  the writer. It is [deferred](#deferred) to its own milestone, not to another
  design.

rhumb does not claim more than the process boundary allows here either: a
rendered file is on disk, and what the reader then does with it — a copy, a
backup, a chat window — is outside what this page can promise.

## Configuration

Every command takes the generator root and the secrets root as `--root` and
`--secrets`, or from `$RHUMB_ROOT` and `$RHUMB_SECRETS`. `export` writes to
`--to <dir>`, `--zip <file>` or `--to -`. A program embedding the `cli`
package passes the same three directories in a `cli.Settings`.

## Open questions

- **Where the element-level defaults merge happens.** Now that the renderer owns
  the function set, it can merge each list entry against the defaults itself and
  hand the template a finished list, or keep handing the defaults over for the
  template to merge as the Shadowsocks templates do today. The first makes the
  templates shorter and the manifest's `defaults: element` meaningful to the
  renderer; the second keeps the renderer from knowing which key holds the list.
- **What `(unmanaged user)` labels.** `--targets` groups by node and falls back
  to the user's key for targets with no node, marking those groups
  `(unmanaged user)`. The group is right — a credential no device names has no
  machine to sit under — but the words read as a statement about the person,
  and a person with a node file and a person-carried credential appears twice,
  once under their device and once under their own key with that label. A
  reader asks whether `dana` is managed or not, which is not the question the
  line answers. Whether to reword it, to say what the group is instead of what
  the person is, or to leave the grouping and the wording alone, is open.
- **How a command-line export fits the command model.** The TUI comes first and
  the command line follows it — see [Deferred](#deferred) — but a leaf command
  opens a TUI today, and reports are read-only, so an invocation that writes
  files is outside the current command model. That is the part still open, and
  it needs a decision in `tui.md` rather than a flag added here.

  It is open only for the command line now.
  [`rhumb secret show`/`edit`](inventory.md#viewing-and-editing-several-at-once)
  no longer waits on it: `inspect.md` settles that
  half by keeping the write inside a leaf command's page, behind the same
  confirmation dialog every other write in rhumb uses, rather than adding a
  side-effecting Cobra subcommand. An export writing from its own page is the
  same shape; what stays undecided is an invocation that writes without opening
  one.

The per-instance `.sh` files are not among these: they are deleted with the
directory rename, not kept working alongside the manifest.

## Deferred

- **Encrypted archives.** `--encrypt-to <host|group>`, writing `.zip.age`
  through `internal/cred/seal` and the recipient folder, so an export can be
  handed over without the plaintext leaving the vault's model. Deferred to keep
  the first pass to rendering and writing, not because the design is unclear.
- **Checking every target.** Rendering all targets and reporting only the
  failures answers "did that secrets change break a host?", which today is
  answered by exporting and reading. It is the preview with no output and no
  selection, and is worth its own report once rendering exists.
- **Installing to a host.** Copying an export to the machine it is for, over
  SSH, with the service reload that follows. It is a different problem — reach,
  permissions, restart — and putting it behind the same key would make a
  transfer look like a render.

## Milestones

1. **Manifests and discovery.** Read the generator root, parse manifests, list
   targets, report broken ones. A package of its own, with tests over a
   constructed root.
2. **Rendering.** One target to bytes, in a package that knows nothing of the
   TUI: the function set above, the two kinds of defaults, the secrets lookup,
   and render errors that name their target.
3. **Selectors.** Matching, and the `--targets` report.
4. **The page.** Tri-state marking on the inspect page's Nodes and Users tabs,
   and the marked count in the status bar. Built.
5. **Preview.** `Enter` on an instance, with errors shown as they come back.
6. **Export.** `x` on the inspect page: the form, the confirmation dialog,
   render-all-then-publish, folder and zip. Built.
7. **Encrypted archives.** As deferred above.

The first of these were built before the inventory existed, against a target
that was a path and a secret that was a lookup key in a values file. The list
above is left as the record of that, and the work that revisits it is
[`inventory.md`](inventory.md#milestones), which covers both pages: discovery
gains the node files, rendering takes the render context in place of the
per-service secrets file, and selectors match fields in place of path segments.
The page, the preview and the export keep their shape; what changes under them
is the tree's top level, from services to nodes.

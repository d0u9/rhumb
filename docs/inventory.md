# Configuration: Inventory

The inventory is the set of facts `rhumb conf` renders from: which machines exist,
which services run on them, who is allowed to reach what, and which chains relay
through which. [`export.md`](export.md) describes the renderer and what it
writes. This page describes what it reads.

## Why it exists

Before the inventory, a service's values file carried three different kinds of
information at once:

```yaml
port: 1080                    # the shape of this service's configuration
server: 203.0.113.30          # where the upstream is
secret_server: exit-jp        # which credential opens it
```

Only the first line belongs to the service. The other two are facts about the
world, copied by hand into every values file that needs them, and copied again
into the server on the other end. Three things follow from that, and they are
what this page removes:

- **An address is written in many places.** A machine changing address means
  finding every values file that names it.
- **A credential is written twice and must match.** Adding a person means one
  edit on the server and one on their device, with the password typed into both.
- **Nothing knows what runs where.** Which instances share a machine can only be
  guessed from their names.

The inventory states each of these once. The renderer derives the rest.

## The model

| Term | What it is |
| --- | --- |
| **node** | A machine or device: a server, a home server, a laptop, a phone. |
| **network** | A named network that nodes belong to, such as `home` or `internet`. |
| **service** | One program a node deploys, with its template, defaults and output name: `ssserver`, `sslocal`, `hysteria2`, `microbin`. |
| **export** | One way of handing a credential to a person: a share URI, a JSON configuration, a QR code. Deployed nowhere. |
| **instance** | One service running on one node: its own ports, grants and values. Ordinarily one process and one rendered file. |
| **process** | One running program on a node, when it runs several instances: one rendered file, one container or unit. See [Processes](#processes). |
| **port** | A named listening port of an instance. Every port an instance listens on is named; an instance that listens on nothing has none. |
| **route** | An ordered list of hops, from where traffic enters to where it leaves. |
| **link** | A session one instance opens to another instance's port on another node, which route traffic may travel inside, with or against the direction it was opened. See [links.md](links.md). |
| **hop** | One step of a route, written `<node>/<instance>:<port>`. |
| **user** | A logical identity: a person, not a Linux account and not a password. |
| **credential** | One of a person's identities. A device names which one it uses; two naming the same one share a secret. |
| **group** | The directory a node's file sits in: whose machines these are — a person, or whoever hosts them. |
| **grant** | A credential for one party to reach one port. Derived, never written. |
| **secret** | One credential value, one file. |

Two terms describe things the reader never writes. A **grant** is derived from a
user's access list or from a route's hops; a **principal** is whatever holds one
— one of a person's credentials, or an upstream instance. They are named here
because the secrets layout and the rendered account tables are organised by
them.

A principal is never a device. How many credentials someone keeps is theirs to
decide: one password across a laptop and a phone is as real as one per machine,
and a model that made the device the identity could write down only the second.
A device names one of its owner's credentials instead, so both are one shape.

`connection` is deliberately absent. In an earlier design a connection was
something written by hand for every client reaching every server, which is the
N×M table this model exists to avoid. What remains of it is the route, which is
written once per chain, and the grant, which is derived.

## Fixed names and example names

Everything on this page is written in terms of one worked inventory, and most of
the words in it are that inventory's, not the model's. An implementation that
recognises `ssserver`, `sslocal` or `internet` has hard-coded someone's
data.

**Fixed.** rhumb knows these and nothing else:

| What | The names |
| --- | --- |
| Files | `services/`, `services/<service>/exports/`, `services/<service>/deploy/`, `nodes/`, `users.yaml`, `routes.yaml`, `links.yaml`, `networks.yaml`, `hosts.yaml`, `confgen.yaml`, `defaults.yaml` |
| Node keys | `id`, `networks`, `reaches`, `owner`, `export`, `profiles`, `credential`, `runtime`, `platform`, `download`, `containers`, `accounts`, `processes`, `instances` |
| Container network keys | `name`, `subnet`, `gateway`, per entry of a node's `containers` |
| Profile keys | `export`, `values`, `access`, `runs`, `bind`, `ports` |
| Process keys | `service`, `values`, `deploy`, per entry of a node's `processes` |
| Instance keys | `id`, `service`, `ports`, `process`, `runtime`, `bind`, `containers`, `self`, `values`, `deploy`, `dials` |
| Port keys | `port`, `protocol`, `self`, `published` (a string or a list), when a port is written as a mapping rather than a bare number |
| User keys | `username`, `devices`, `export`, `credentials`, `access` |
| Credential keys | `note`, `access`, `reaches` |
| Route keys | `hops` |
| Network keys | `networks`, `universal`; per network `name`, `subnet`, `gateway` |
| Node address keys | `address`, `mac`, when a node's address on a network is written as a mapping |
| Host keys | `hosts`; per host `network`, `address`, `mac`, `names` |
| Service manifest keys | `secret`, `auth`, `template`, `defaults`, `output`, `rotation`, `self`, `upstream` |
| Self declaration keys | `set`, `fields`, `kind`, `bytes` |
| Export manifest keys | `template`, `defaults`, `output`, `upstream` |
| Enumerated values | `devices: none`; `auth: per-principal`, `none`; `defaults: document`, `element`; `export: none`; `rotation: disruptive`; `protocol: tcp`, `udp`; `kind: opaque`; `upstream: shared` |
| Reserved words | `self`, as a port name and as a secrets path segment |
| Defaulted names | `default`, the credential a person has when they declare none and a device uses when it names none |
| Secrets path segments | `self`, and the `.previous` suffix |
| Selector fields | `node`, `user`, `service`, `export`, `profile`, `instance`, `route` |
| The loopback address | `127.0.0.1`, for two hops on one node |

**An example.** Every one of these is a name the inventory's author chose, and
the model attaches no meaning to any particular spelling:

| What | As written here |
| --- | --- |
| Service names | `ssserver`, `sslocal`, `hy2client`, `hysteria2`, `microbin`, `httpproxy` |
| Export names | `link`, `json`, `shadowrocket` |
| Network names | `home`, `internet` |
| Port names | `main`, `alt`, `proxy`, `local`, `web`, `socks`, `http` |
| Node, instance, user and route identifiers | `u-node-group-09-01`, `ss-sea01`, `dana`, `jp` |
| Host identifiers and dial names | `nas`, `printer`, `rss` |
| Names of an instance's own secrets | `auth_password`, `server_password`, `psk`, `tls_key` |
| Keys and fields of one of them | `main`, `backup`, `username`, `password` |
| Everything inside `values` | `masquerade`, `fast_open`, `public_path` — a template's own, not rhumb's |
| Output file names | `config.json`, `share.txt` |

`ssserver` is in the second table and not the first. No rule keys off it: a
service authenticates because it says `auth: per-principal`, and is reached
because something names it, not because of what it is called. The same is true
of `internet`, which is the universal network only because `networks.yaml` says
so.

## Files

```text
~/confgen/                    the generator root
├── services/<service>/       a program a node deploys
│   └── exports/<export>/     a way of handing that service's credential over
├── nodes/<group>/*.yaml      a machine, its networks, its instances or instance directory
├── nodes/<group>/*.instances/*.yaml  optional instance files
├── users.yaml                people and what they may reach
├── routes.yaml               chains
├── links.yaml                sessions between two instances that routes ride
├── networks.yaml             networks, their preference order and address ranges
└── hosts.yaml                machines on a network that run no instance

~/confgen-secrets/            the secrets root
```

The secrets tree is a separate root rather than a directory inside the generator root (`--root`).
A directory that is not under the repository cannot be committed by accident,
which a `.gitignore` entry only promises.

Everything except the secrets tree belongs in version control. Reviewing a
change to `users.yaml` is the audit: it says, in one diff, who gained or lost
access to what. The secret values are random bytes with no history worth
keeping — see [Secrets](#secrets).

### Starting a generator root

```bash
rhumb init [<dir>] [--secrets <dir>] [--gitignore]
```

`<dir>` is the generator root to create, and defaults to the generator root (`--root`). The
secrets root is a separate root, so it comes from `--secrets` or the secrets root (`--secrets`)
rather than from the same path, and is skipped when neither gives one.
`--gitignore` writes one into the secrets root as well, for the case where it
does sit somewhere a repository can see.

**It writes a scaffold, not an example.** Each file is a valid, empty one
carrying the comments that say what belongs in it, so `rhumb check` is
clean the moment it finishes and nothing has to be deleted before real content
goes in. A worked inventory to copy from is [`examples/conf/`](../examples/conf)
instead — an example written into someone's own root is a machine that does
not exist, and deleting it is a step the scaffold should not create.

**It never overwrites.** A root half-built by hand, or built by an earlier
version of this command, gains what it is missing and keeps what it has; the
report says which files were which. Running it twice is the same as running it
once.

`nodes/` and `services/` get a `README.md` each rather than being left empty,
since an empty directory does not survive version control, and the file
explaining what goes in the directory is the one that should be sitting there
when someone opens it.

## Nodes

A node is a machine or a device. Servers and phones are the same kind of thing
here, which is why the word is `node` rather than `host`.

Node files sit one level down, in a directory naming whose machines these are:

```text
nodes/
├── cloud/u-node-group-09-01.yaml
├── cloud/mn-ume-vps-linux-01.yaml
├── home/server.yaml
├── home/nas.yaml
└── dana/phone.yaml
```

The directory is the **group**. A group naming a user in `users.yaml` is that
person's devices, and fills `owner` for every file in it, so the owner is not
repeated in each one. A group naming no user is whoever the machines belong to
— a provider, a household, a company — and rhumb reads nothing further into it.
Only one level is read: a directory is a group, never a path. A file written
straight into `nodes/` has no group, which is a group of one rather than a
special case.

```yaml
# nodes/cloud/u-node-group-09-01.yaml

id: u-node-group-09-01

networks:
  internet: 203.0.113.10

instances:
  - id: ss-sea01
    service: ssserver
    process: ss-main
    ports:
      users: 38250

  - id: ss-sea01-relay
    service: ssserver
    process: ss-main
    ports:
      relays: 49217

  - id: hy2-sea01
    service: hysteria2
    ports:
      users: {port: 443, protocol: udp}
```

Instances may live in the node file as above, or in a directory beside it when
the list makes the node file hard to read. The node file remains the source of
the machine's identity and networks:

```text
nodes/home/
├── server.yaml
└── server.instances/
    ├── http-home.yaml
    └── ss-home.yaml
```

```yaml
# nodes/home/server.yaml
id: home-server
networks:
  home: 10.0.1.10
instances:
  directory: server.instances
```

```yaml
# nodes/home/server.instances/http-home.yaml
id: http-home
service: httpproxy
bind: "0.0.0.0"
ports:
  proxy: 8118
```

The directory name is a single relative name beside the node file. Only its
direct `*.yaml` files are read, sorted by filename. Each file contains one
complete instance mapping, including its `id`, or a list of related instances
(for example, two MeTube instances). List entries keep their written order.
Other files, such as a README, are ignored. A node uses either an inline
`instances` list or a directory reference. A missing directory or malformed
instance file makes the node
**broken**, with the offending path in the error, rather than silently dropping
an instance. Instance IDs retain the same inventory-wide uniqueness rule.

A machine that both serves and reaches out is not a special kind of node. The
home server below answers requests on one instance, relays through a client
instance beside it, and hosts a service of its own:

```yaml
# nodes/home/server.yaml

id: home-server

networks:
  home: 10.0.1.10

instances:
  - id: http-home
    service: httpproxy
    bind: "0.0.0.0"
    ports:
      proxy: 8118

  - id: ss-home
    service: ssserver
    bind: 127.0.0.1
    ports:
      local: 1080

  - id: bin-home
    service: microbin
    bind: "0.0.0.0"
    ports:
      web: 8080
```

It has no address on `internet`, and does not need one: it reaches out through
NAT, and nothing connects to it from outside. `networks` says where a node can
be reached, not where it can reach — see
[below](#networks-and-how-an-address-is-chosen).

A node hosting no services needs only two lines:

```yaml
# nodes/dana/macbook.yaml

id: dana-macbook
reaches: [home]
```

`owner` is the group, so it is not written. A node kept somewhere else may name
one anyway, and an `owner` written wins over the directory. A node that hosts
services has no owner either way.

`credential` names which of its owner's credentials this device authenticates
with, and defaults to `default` — see [Users](#users). Two devices naming the
same one hold one secret between them.

A device has no `networks` at all: nothing connects *to* a phone, so it has no
address to advertise. `reaches` is how it says it can use the home network when
it is there.

### Networks and how an address is chosen

```yaml
# networks.yaml

networks:
  - name: home
    subnet: 10.0.0.0/8
    gateway: 10.0.0.1
  - name: internet
universal: internet
```

`networks` is a preference order, most preferred first. `universal` names the one
network every node can reach without saying so, and may be omitted, in which case
nothing is implicit and every node states what it reaches.

**A network may say what range it covers.** `subnet` is a CIDR prefix, and
`gateway` the router's address inside it. Both are optional, and both are
written only for a network someone administers: the internet has no subnet
worth checking. A network with a `subnet` has every literal address on it —
a node's and a [host's](#hosts) — checked to fall inside it, and every address
on it checked to be written once. Nothing is inferred from the prefix: rhumb
does not pick addresses, and a network without one is the same network,
unchecked.

One network is one layer-2 segment, the range its members' masks say. A
virtual machine bridged onto it is a member with an address in that range,
even when the address looks like another subnet: `10.0.31.31` on a `/8` is
on `home`, and writing it as its own network would claim a router between
the two that is not there.

Neither name is known to rhumb. `home` and `internet` are this inventory's
names, and the rule that one network is reachable from everywhere is expressed
by `universal` naming it rather than by the renderer recognising a word.

### Reached, and reaching

A node says two different things about networks, and NAT is why they cannot be
one field:

- **`networks`** maps a network to this node's address on it. It says where
  others can reach this node.
- **`reaches`** lists networks this node can open a connection on. It says
  nothing about being reachable.

A node reaches every network it has an address on, and every node reaches the
`universal` network, so `reaches` is written only for a network a node can use
but has no address on: a phone on the home LAN, or a laptop there. A home server
behind NAT writes neither — its `home` address covers the LAN, and the universal
network is implicit.

`networks` is where others reach this node, which is not always what its own
interfaces hold. A machine published through a forwarded port writes the address
others dial, not the private one it is configured with; the field answers "where
do I send a packet for this node", and a reader copying an interface address into
it has answered a different question.

A node sitting on two networks writes both, and that is the whole of an edge
host in front of a private subnet:

```yaml
id: edge-fra
networks:
  internet: 203.0.113.40
  backend: 10.0.0.1
```

Backends on that subnet have a `backend` address and no `internet` one. They
still reach the internet, because reaching and being reached are separate
fields and `universal` covers the first — they fetch updates and send mail as
before. Nothing outside can open a connection to them, because nothing outside
has an address to dial. The separation that exists for NAT does this second job
unchanged, which is why a private subnet needs nothing new here.

### Choosing an address

The address at either end of a hop is never written down. It is chosen from the
two nodes involved:

It is the downstream's address in the innermost scope the two ends share.
Scopes nest: the networks in `networks.yaml`, then one node's loopback, then
the [container networks](#container-networks) on that node.

| The two ends | The address used |
| --- | --- |
| Two instances sharing a container network | The downstream instance's name on the first they share, and the downstream port |
| The same node, the upstream on no container network | `127.0.0.1` and the downstream port |
| The same node, the upstream on container networks the downstream shares none of | An error: loopback inside the container is the container itself |
| The downstream has an address on a network the upstream reaches | That address, on the first such network in preference order |
| The upstream ends a link whose other end is on the downstream's node | The downstream's address as the link's far end dials it, by the same-node rows |
| Neither | An error naming both nodes and what each one reaches |

The rule is one-directional on purpose. A client reaching a server needs the
server to be reachable and the client only to be able to reach; requiring both
to be addressable would rule out every machine behind NAT, which is most of
them.

A machine changing address is one line in one node file.

### A node's accounts

A node may list its POSIX accounts, for services that write numeric owners:

```yaml
accounts:
  svcuser: {uid: 1000}
  bob: {uid: 3003, gid: 65533, group: nogroup}
```

An unwritten `gid` is the uid, an unwritten `group` the account's name. A
template reads them as `(node).accounts.<name>` — `name`, `uid`, `gid`,
`group` — and an instance names an account rather than repeating its
numbers: a file on a volume keeps the number, so it is written once.

### A node's hardware address

An address in `networks` may be written as a mapping when the network hands
addresses out by hardware address:

```yaml
networks:
  home: {address: 10.0.30.10, mac: "02:00:00:00:00:11"}
```

`mac` is read for two things only: [the reservation table](#what-the-router-is-given)
and the check that no two members of a network share one. It never affects
which address a hop resolves to. It is written lower case with colons, and
rhumb normalises what it reads to that, so a value copied from a router's
upper-case table is the same value.

A mobile device is not a special case, because "at home" and "away" are not two
addresses for one route — they are two routes. A phone at home reaches the
internet through the home server; away, it reaches a server directly. Those are
different chains, the person picks one in their client, and each renders its own
configuration. The model states which chains exist; which one to use at a given
moment is a decision no file can make.

## Hosts

Some machines on a network run nothing this inventory deploys — a NAS with its
own firmware, a printer, a virtual machine someone uses as a desktop — and are
still named, reserved an address and dialled. They are hosts, and they live in
one file:

```yaml
# hosts.yaml

hosts:
  nas:
    network: home
    address: 10.0.30.11
    mac: "02:00:00:00:00:12"
    names: [nas.example.com]
  printer:
    network: home
    address: 10.0.30.12
    mac: "02:00:00:00:00:13"
    names: [printer.example.com]
  desk-vm:
    network: home
    address: 10.0.31.31
    mac: "02:00:00:00:00:14"
```

A host is not a node. It has no instances, no owner, no `reaches`, and no
route may name it: nothing here renders a file for it, and the one thing it
takes part in is being found. Anything that grows an instance becomes a node
file, and its entry here is deleted rather than kept beside it.

`names` is the DNS names the host answers to on that network, as bare
hostnames, and may be empty: a machine with a reservation and no name is
ordinary. `mac` is optional too, for a host whose address is fixed on the host
itself. The key is the host's identifier, for a template or another file to
refer to; it need not match anything the router calls it.

Everything a host says is checked against its network: the address falls in
the network's `subnet`, no other host or node holds it, no other member has
the same `mac`, and no name is claimed twice — see
[names on a network](#names-on-a-network).

### What the router is given

A network whose router hands out addresses keeps its reservations in the
router, where rhumb cannot write. What rhumb can do is say what they should
be: an export listing, for one network, every node and host with a `mac`,
its address and its identifier, in address order. It is copied into the
router by hand, and a diff of it against the router's own table is the check
that the two agree. `rhumb reservations <network>` prints it, one line
per member: address, `mac`, identifier.

## Instances and ports

An instance is one service running on one node. Unless it names a
[process](#processes), it is also one program and one rendered configuration
file. Its `id` is unique within its node, and does not repeat the node: the
instance is written under its node already, so a site code in its name would
say the same thing twice and change twice when the service moves.

Across the inventory an instance is `<node>/<id>`, and that is its key. A
route's hops are written that way, as is a selector that has to tell two
nodes' instances apart. A [dial](#dialling-a-service-that-is-not-on-a-route)
may leave the node out when its target is on the caller's own node. Secrets
are filed under the same two segments, `<node>/<id>/...`, in the store.

What the rendered files see is the `id` alone: it names the container, the
export directory and the host name on a container network, all of which live
on one node.

### Naming

The convention is `<service>-<index>`: `ss-01`, `hy2-01`, `caddy-01`. Two
instances of one service on one node take the next index, or a purpose suffix
when the difference is worth reading: `ss-pub` beside `ss-fam`.

Node names carry the site. The site codes are IATA, which are unique worldwide:
no two airports share one, and airport and city codes are assigned from the
same namespace, so `sea` and `nrt` cannot mean two places. That uniqueness is
worth having, and it holds only while one scheme is used throughout. Three
things break it:

- **Mixing schemes.** An invented abbreviation for a place with no airport is
  registered nowhere, so two of them will eventually collide. A site near an
  airport takes that code with a suffix — `sea-home` — rather than a new code.
- **Cloud region names.** `us-east-1`, `nyc3` and `ap-northeast` mean different
  places at different providers and are not comparable between them. They are
  not site codes.
- **Reading, not collision.** `SJC` and `SJO` are distinct codes for San José in
  California and in Costa Rica, and city names repeat across countries where the
  codes do not.

Node names keep their country prefix, which is redundant for uniqueness and
worth its three characters for reading a list of machines.

None of this is enforced, and it cannot be: no checker knows which scheme a name
came from. What is enforced is that instance identifiers are unique, so the cost
of getting a name wrong is an error that names both instances, not a
configuration that renders two things over each other.

### Dialling an upstream as a principal

An instance may name an optional `principal`: a user key from `users.yaml`
whose credential the instance carries when it dials its upstream.

```yaml
  - id: sslocal-nce01-01
    service: sslocal
    principal: cn-repeater
    bind: 127.0.0.1
    ports:
      socks: 1080
```

This does not grant access. The named user's `default` credential must already
open every route the instance enters; `users.yaml` remains the one grant table
and therefore the audit of who gained or lost access. It is always that one
credential: an instance carries a program's identity, and a program has one,
so there is nothing here to choose between. The service must declare `upstream`, since
otherwise no rendered file would consume the selected credential.

Usually `principal` is not written. When a service declares `upstream`, the
instance enters at least one route, and exactly one user's `default` credential
opens every route it enters, rhumb fills that user in: `users.yaml` already names
the only identity the instance could be carrying. With no such user, or with
several, the instance dials as itself unless it writes `principal`. A filled
principal is never written back by `rhumb conf migrate`.

This is separate from a device's `credential`: that key chooses which of a
person's credentials a device carries, while `principal` chooses which person
a service instance dials as. A server hosting a client process is not made that
person's device.

### Ports

**Every port an instance listens on is named**, including the only one:

```yaml
  - id: hy2-tzr01
    service: hysteria2
    ports:
      main: 443
```

There is no scalar `port:` shorthand. The alternative — a shorthand whose secret
paths are one level shallower — makes adding a second port to an existing
instance a migration of every secret file under it, where a silently missed file
is regenerated as a new random value and breaks the peer that still holds the
old one. One extra line per instance buys one shape everywhere: in hops, in
secret paths, in validation and in the renderer.

An instance that listens on nothing has no `ports` at all. A phone client that
runs as a system tunnel has no local port to declare, and inventing one would be
a number in a file that nothing reads. The argument for naming ports is about
secret paths splitting when a second port appears, and an instance with no ports
has no secret paths under it to split.

**A port may say its transport.** A bare number is TCP, which is what a reader
assumes when nothing says otherwise. A port on anything else is written as a
mapping instead, and the other ports of the same instance are untouched:

```yaml
  - id: hy2-sea01
    service: hysteria2
    ports:
      users: {port: 443, protocol: udp}

  - id: caddy-sea01
    service: caddy
    ports:
      https: 443
```

Two ports collide only when they share an address, a number **and** a
transport. A QUIC service on 443/udp beside a web server on 443/tcp is two real
listeners, and a check that knew only the number would call one machine bound
twice.

**Name a port after what arrives on it**, not after its position. A port is the
unit an account table belongs to, so `users` and `relays` say what a reader
needs — which of an instance's two tables this is — where `main` and `alt` say
only which was written first:

```yaml
  - id: ss-sea01
    service: ssserver
    ports:
      users: 38250
      relays: 52146
```

**Several ports of one program are one instance.** An instance is one rendered
configuration file, and one program reads one file, so the ports a single
program listens on belong together. Each port still has its own principals, its
own grants and its own account table; what they share is the file they are
written into, and whichever of the instance's own secrets each of them hands
out — see [a secret several people hold](#a-secret-several-people-hold):

```yaml
  - id: ss-sea01
    service: ssserver
    ports:
      users:  {port: 38250, self: [psk.main]}
      relays: {port: 52146, self: [psk.backup]}
```

**Two instances for one program.** A second server of the same service, with
its own configuration and its own values, is a second instance. When one
program serves both, each still names it as its `process`, and the node
declares the process with the service whose template writes the program's one
file:

```yaml
processes:
  ss-multi:
    service: ssserver

instances:
  - id: ss-sea01
    service: ssserver
    process: ss-multi
    ports:
      users: 38250

  - id: ss-sea01-legacy
    service: ssserver
    process: ss-multi
    ports:
      users: 38260
```

What [Processes](#processes) describes follows from that.

### Processes

**An instance is what the model reasons about; a process is what runs.** Routes,
grants, links, account tables and validation are all per instance, and an
instance naming no `process` is a process of its own, which is the ordinary
case. A node's `processes` declares a program that runs several instances as
one: one configuration file, one container or unit.

```yaml
processes:
  xray-nce:
    service: xray
    values: {}
    deploy: {}
```

`service` is the program: its template renders the process, and its deploy
directory, when it has one, deploys it. `values` and `deploy` are the
program's own, as an instance's are.

**The members keep everything that is theirs.** Each member's service still
decides what it is: whether it terminates, forwards or fans out, how it
authenticates, which ends of a link it may be. A program offering two modes is
two services, one per mode, and one process running an instance of each — a
relay that is both a tunnel's near end and a reverse proxy's portal is the case
this exists for. A member's service may then be a manifest alone, with no
template: it renders nothing of its own.

**The process is the target.** It is listed, selected and rendered in its
members' place, and it belongs to every route its members do. Its template
reads each member with `members`: name, service, instance, `upstream`,
`downstreams`, `links`, `principals`, `published` and `dials`, each what that
member would have been given on its own. Its ports are its members', published
as theirs are; the name a port is published at stays the member's.

What one program shares, its members share: what runs it, its bind and its
container networks. Port names and numbers are distinct across them, since
they listen in one place.

`bind` is optional and defaults from the service's defaults file. A server binds
every interface; a client's local listener binds loopback.

A port named `self` is rejected; see [Secrets](#secrets).

### The name a port is published at

**A port reached from a browser says the name it answers to**, as a bare
hostname:

```yaml
  - id: vault-fra
    service: vaultwarden
    bind: 127.0.0.1
    ports:
      web: {port: 8222, published: vault.example.com}
```

It is written on the port rather than on the route because three different
readers need it, and only one of them is the proxy. A reverse proxy in front
matches its site block on it. **The service behind it needs the same string in
its own configuration** — Vaultwarden builds absolute URLs, a cookie domain and
a WebAuthn relying-party identifier out of it, and a value that disagrees with
what the proxy serves breaks sign-in rather than the page. And a person types it
into a browser.

A route could carry it instead, and then the second reader could not have it. An
instance's render context is a local view — its node, itself, its upstream, its
principals, its own secrets — and a value living on another hop of a route that
merely ends here is a backwards lookup. Reaching it would mean handing every
instance the route table, which is the one shape [the render
context](#the-render-context) is arranged to avoid.

A bare hostname, not a URL: the proxy's site block wants the name alone, and a
template that needs `https://vault.example.com` writes the scheme itself, where
the other direction would have every template take a URL apart.

A template reads its own port's name with `published "<port>"`, the way it
reads an account table with `principals "<port>"`. A proxy reads the names of
the ports it fronts from [`downstreams`](#the-render-context) instead, because
those belong to other instances.

A client reads the name of the port it dials as `(upstream).published`. The
services on a node can answer to a name other than the node's — one shared, or
one each — while the node keeps its own address in `networks`, and an exported
link or configuration writes the service's name rather than the node's.
`(upstream).address` is still the node's address chosen by the rule above, so
which of the two a file carries is the template's choice, usually
`or (upstream).published (upstream).address`.

The name is given only on an edge resolved on the `universal` network, which is
where a name is taken to resolve. An edge between two ends on one node, or one
resolved on a private network such as `home`, was chosen an address that
network needs — loopback, a LAN address — and a public name would send the
client out and back in, or nowhere. On those edges `(upstream).published` is
absent, and `or (upstream).published (upstream).address` falls back to the
address the rule chose. An inventory with no `universal` network gives no
names this way.

`published` stands on its own. A service reached from a browser on its own
address, with no proxy anywhere, still has a name it answers to and still has to
render it into its own configuration. It is a property of the port, which is why
a port is where it is written.

**One name per port.** Two ports may share a name — it is a DNS name, and two
services on one machine answering to it on different ports, Shadowsocks on
38250/tcp and Hysteria2 on 443/udp, is an ordinary deployment, since a client
dialing the name dials the number too. Sharing is broken in three cases, and
the check reports each wherever it is written: ports on two nodes, because a
name reaches one machine; a port a proxy fronts, because the proxy tells its
downstreams apart by name alone; and two ports on one number and transport,
because nobody dialing the name can tell them apart.

### What a proxy is told

A reverse proxy renders one site per downstream, and what it needs to know
about each is the downstream's, not its own:

- **Scheme** is the proxy port the route enters: a route entering at `http`
  is a plain-text site, at `https` one with a certificate.
- **`title` and `description`** on a port are what a page listing the proxy's
  sites shows for it.
- **`proxy`** in a service manifest is what a proxy in front of it must do —
  `forward_remote_addr: true`, `max_body: 128MB` — by the name the proxy's
  template reads. A port's own `proxy` overrides it key by key.

```yaml
ports:
  web:
    port: 8080
    published: clip.example.org
    title: Clip
    description: Paste bin.
    proxy: {max_body: 128MB}
```

A template reads them as `.Entry`, `.Title`, `.Description` and `.Proxy` on
each downstream. Moving a service behind another proxy moves none of it.

### An instance's own values

`id`, `service`, `bind` and `ports` are what rhumb itself needs: enough to
place an instance, find its template, and resolve every address and secret
around it. They are not enough to configure a service — Hysteria2 needs a
masquerade target, Shadowsocks a `fast_open` flag, an nginx server block
whatever an nginx server block needs — and none of that is a network fact, a
listening port, or shared by every instance of a service. `values` is where it
goes:

```yaml
  - id: bin-sea01
    service: microbin
    bind: 127.0.0.1
    ports:
      web: 8080
    values:
      public_path: https://paste.example.com/
      upload_enabled: true
```

`values` is opaque: rhumb parses it as YAML and nothing more. It does not
validate its shape and does not know what any key means. Where it reaches the
template follows from the service's defaults kind, and from nothing else: with
`document` defaults it merges over the whole defaults document, key by key,
and the instance wins; with `element` defaults it reaches the template
unmerged, and the template does the merging it wants. Either way a template
also reads it by name, as `(instance).values`. See
[two kinds of defaults](export.md#two-kinds-of-defaults).

**Only `values` merges.** `id`, `service`, `runtime`, `deploy`, `bind` and `ports` are rhumb's own and
stay out of the document: they are read through the instance function, so a
service's settings have one place a node file may write them, and a document
never grows a key no service declared.
This is deliberate, not a gap left for later: a masquerade target is Hysteria2's
concept, not the inventory's, and adding a typed field for every service's own
parameters would mean this page growing a section per service forever. What
belongs in the inventory is what routing, address resolution and secrets need to
work; what one instance of one service is configured to do belongs to that
service, expressed however its template wants it.

**A value that does not vary between instances of a service belongs in the
service's `defaults.yaml` instead.** A masquerade target chosen once and reused by every
Hysteria2 server is a default, not a value — `values` is for what a second
instance of the same service would need to say differently, MicroBin's public path
being the clear case: two MicroBin instances cannot share one.

An authored override on a client node carries `values` the same way it carries
`ports` and `bind`; see [what is derived](#what-is-derived).

### Dialling a service that is not on a route

A route is a chain someone chooses to use: a person picks it in a client, or a
program's single upstream follows it. Programs also call each other in
passing — a digest reading a feed reader's API, a feed reader fetching from a
feed generator — and those calls are neither a choice anyone makes nor an
entrance anything listens on. The caller often listens on nothing at all, so
it has no port to start a route from.

`dials` names them, on the caller:

```yaml
  - id: digest-01
    service: ai-digest
    runtime: docker
    dials:
      rss: freshrss-01:web
```

Each key is the caller's own name for the dependency, and each value a hop,
`[<node>/]<instance>:<port>`: a route's form, with the node left out when the
target runs on the caller's own node. A template reads
it with `dial "<name>"`, which returns the same shape a proxy's
[`downstreams`](#the-render-context) entry has: the target's `Instance` and
`Port`, its `Number`, the `Address` chosen by [the usual rule](#choosing-an-address),
and its `Published` name when it has one:

```text
{{ with dial "rss" }}http://{{ .Instance }}:{{ .Number }}{{ end }}
```

A container on the same bridge network as its target dials the instance
identifier, which the target's deployment registers as a network alias; the
loopback address chosen for two ends on one node is the caller's own
container there. A host process dials `Address`. Which of the two is the
template's choice, as it is for a proxy.

A dial is not an edge. It grants nothing, carries no credential, adds no
host mapping to the target's ports and plays no part in rule 8. A dependency
that needs a credential, or that must be reachable from another node through
a published port, is a route.

`dial` of a name the instance does not declare is a render error, not an
empty string: the value is an address, and an empty one renders a file that
looks complete and connects to nothing.

### Dialling a service by type

A service may declare what it dials, by the name its templates use:

```yaml
# services/digest/confgen.yaml
dials:
  rss: {service: freshrss, port: web}
```

An instance that writes no dial of that name gets the one instance of that
service, holding that port, in the innermost scope it shares with it: a
container network they both join, then its node, then each network in preference order it
reaches. Two in the first scope holding any is an error naming them; so is
none anywhere. A written dial always wins. The resolved dial is an ordinary
dial from then on — the render, the tree and a migration see it — except that
a migration never writes it into the file.

### What runs the process

`runtime` says what the process is delivered by, and it is one of `host`,
`docker` or `podman`. An instance that writes none takes its node's `runtime`,
and a node that writes none means `host`:

```yaml
id: network-1
runtime: docker
containers:
  - {name: web, subnet: 172.29.0.0/24}
instances:
  - id: bin-01
    service: microbin
    ports:
      web: 8080
  - id: sshd-01
    service: sshd
    runtime: host
```

A node may also write `platform`, its GOOS/GOARCH such as `linux/amd64`. Nothing
in the model reads it; it is carried into each instance's
[manifest](export.md#the-manifest), where a deployment picks the program's
release and the service manager by it. `download` rides along the same way:
`build` downloads a release when the bundle is built, for a machine that cannot
reach it, and `install` on the machine, the default. A builder's own choice
overrides it.

**A containerised instance binds `0.0.0.0`, and it is not written.** Inside a
container every interface is the container's own, so no other bind is
reachable, and what may actually arrive is decided by the host mapping, which
is [derived](export.md#a-second-file-what-deploys-it). Writing another bind on
a container is an error.

**`ports` is the number reached from outside the container**, always, because
that is the number every edge dials and every proxy writes. A container's
internal number, when it differs, lives with the deployment tool that
publishes it and nowhere here — two numbers in this file would be two truths,
and the one the model needs is the outer one. Rule [14](#validation) reads
these numbers: two instances on one node may not bind the same address, port
and transport.

A service declaring a [deploy template](export.md#a-second-file-what-deploys-it)
renders the mapping from the same `ports` field the program's own
configuration is rendered from, so the two cannot disagree. For a service that
declares none, the mapping is a convention the inventory cannot check.

The connectivity graph badges a process
box for it.

### Container networks

A node lists the container networks on it, in preference order, each with the
range it covers:

```yaml
containers:
  - {name: apps, subnet: 172.29.0.0/24}
  - {name: tailnet, subnet: 172.29.250.0/24, gateway: 172.29.250.1}
```

**The range is written, not left to the runtime.** A runtime creating a
network with no range picks a free one, and picks differently on every machine
that creates it, and after every time it is removed and created again. An
address fixed on the network is only fixed if its range is, so `subnet` is
required. `gateway` is optional; written, it lies inside the subnet, and no
instance holds it. Two of a node's container networks may not overlap.

A containerised instance joins the first unless it writes `containers`, a
mapping from each network it joins to the address it holds there:

```yaml
containers:
  apps:
  tailnet: 172.29.250.10
```

A network written with no address is joined with one the runtime assigns. A
written address is fixed: it lies inside the network's subnet, is not its
gateway, and no other instance on the node holds it there. Fix one when
something outside the model dials the container by address — a router
forwarding into the network, or a record naming it — and leave it to the
runtime otherwise, since every dial the model makes is by name. Every network
named is one the node lists. A host process joins none. A container on no
network — a node listing none — shares the host's network, and dials loopback
like a host process.

**A container network is a scope inside the node.** Two instances sharing one
dial each other by instance name, which the deploy template gives the
container as its alias on that network, and the edge between them never
reaches the host: it publishes nothing. A reverse proxy and its backends in
containers on one network is the case this exists for — the backends publish
no port at all, and only the proxy's entry does. Two instances sharing
several dial over the first of them in the node's order, the way two nodes
sharing several networks use the first in `networks.yaml`.

A container on a network reaching a host process on its own node is an error.
Loopback inside it is the container itself, and reaching the host takes a
gateway the model does not carry.

Another network on the same node is another scope: two instances sharing no
container network of one node share no scope below the node, and neither can
dial the other.

### What a container needs beyond the model

`runtime` says a process runs in a container. It does not say which image, which
volumes or which restart policy, and those are what a deployment file is mostly
made of. They go in `deploy`:

```yaml
  - id: microbin-network-1-01
    service: microbin
    runtime: docker
    bind: "0.0.0.0"
    ports:
      web:
        port: 8080
        published: clip.home.lan
    deploy:
      image: danielszabo99/microbin:2.0.4
      dir: /srv/microbin
      volumes:
        - {source: /srv/uploads, target: /uploads}
```

For a service holding a [`docker.yaml`](export.md#how-a-container-is-started-dockeryaml),
`deploy` is a fixed set of keys, because the deployment tool reads every one of
them: `image`, `restart`, `dir` (the instance's directory on the machine, and
its compose project's name; `/srv/docker/<service>` by default),
`container_name`, `hostname`, `account` (a name in the node's `accounts`, the
container runs as its uid), `dns`, and `volumes`. A volume is
`{source, target, ro, propagation}`; one whose source is not the instance's own
directory is never created, since creating a mount point on a machine whose
disk is not mounted writes to the wrong disk. The service's `docker.yaml`
gives the defaults, and an instance's `deploy` lays over them key by key — a
list replaces a list, so the service's own mounts are in `docker.yaml`, not
in its `defaults`.

For a service holding a `deploy/` directory instead, `deploy` is opaque,
exactly as [`values`](#an-instances-own-values) is: rhumb parses it as YAML,
knows no key in it, and hands it to the service's
[deploy templates](export.md#a-second-file-what-deploys-it). Either way it is a
second mapping rather than a corner of `values` because the two have different
readers — `values` configures the program, `deploy` starts it — and a key that
reached both would be one more place a rename has to be chased.

**Ports are not in it.** The mapping a container publishes is derived from
`ports` and from the edges the model already resolves: a port its own node
enters publishes on loopback, and one entered from elsewhere publishes on this
node's address on the network that edge resolved. Both at once is an ordinary
port, and so is a node on two networks, so a port publishes on as many
addresses as it is reached over. That derivation is the reason the second file
is worth generating at all, so writing a port here would give back the second
truth it removes. See
[what deploys it](export.md#a-second-file-what-deploys-it).

**Secrets are not in it either.** The deployment names the rendered
configuration beside it; the credential stays in that file, and nothing about
rotation changes.

A service holding neither renders its configuration alone, and an instance of
it writing `deploy` is an error rather than a mapping nothing reads.

### Names on a network

A name is written once, where the thing answering to it is: a port's
`published`, or a host's `names`. What a resolver on a network needs is the
other direction — every name, and the address that answers it on that
network — and that is derived:

| The name comes from | It resolves to |
| --- | --- |
| A port's `published`, entered directly | The port's node's address on the network |
| A port's `published`, fronted by a proxy | The address of the node of the route's first hop |

"Fronted" means the route's first hop is a service with `downstreams: many`
that dispatches by name: a relay forwards bytes and answers to no name, so a
route entering through one leaves the name where it was. An address written
as a hostname is not an entry — a resolver's record wants an IP address.
| A host's `names` | The host's `address` |

A name resolves on a network only where its node has an address there; a
name whose node is on the internet alone is absent from `home`'s table
rather than pointed at a public address.

A template reads the table with `names "<network>"`, a list of `Name`,
`Address` pairs in name order. A DNS server rendering its local records is
the case it exists for, and it is why a proxy site or a host added to the
inventory needs no second edit: the resolver's records follow.

The same name reaching two addresses on one network is an error naming both
sources. That is rule 18's one-machine rule seen from the resolver's side,
extended to hosts.

**A container network has a table too, on its own node.** A name enters it
when the instance answering to the name — the proxy in front of the port, or
the port's own instance when nothing fronts it — joins that container network
at a [fixed address](#container-networks). An address the
runtime assigns is not a record a resolver can hold, so a network joined
without one contributes nothing. A resolver on a node reads its own node's
container tables with the same `names "<container network>"`; another node's
are not visible to it, since two nodes may each list a network of one name.
A resolver answering clients that reach a node through a router on one of its
bridges — a Tailscale subnet router, say — is the case this exists for: the
clients need the proxy's address on that bridge, not the node's own.

**A proxy's own sites are published on its port.** A reverse proxy also
answers to names it serves itself — a static site, a landing page — that no
port behind it publishes. They are written as the proxy's own entrance port's
`published`, which for this case is a list:

```yaml
  - id: caddy-home
    service: caddy
    ports:
      http:
        port: 80
        published: [server.example.com, example.com, daily.example.com]
```

A port's `published` is a string or a list of strings. `published "<port>"`
still returns one name, the first, since the templates reading it — a
service's own absolute URL — want one; `publishedNames "<port>"` returns
the whole list, and the names table takes all of them. What a static site
serves, its root and its log, stays in the proxy's `values`, keyed by the
same name, because it is that service's configuration and not a network
fact; a name with no entry there takes the proxy's defaults.

Rule 18 is unchanged in substance: every name in the list counts as that
port's, and the proxy fronting its own port is not fronting a downstream.

## Users

```yaml
# users.yaml

users:
  dana:
    access: [jp, sea, home-sea, bin, nas]
    credentials:
      default:
        note: the laptop, the phone and the iPad
      work:
        note: the office laptop — revocable on its own
        access: [sea]

  friend-a:
    username: yak
    devices: none
    export: link
    credentials:
      default:
    access: [jp, sea]
```

`access` lists routes. `username` is the account name the services see, and
defaults to the user's own identifier, so it is written only where the two
differ.

### Credentials belong to the person

`credentials` names this person's credentials. Each is a mapping: `note` says
where it is used — free text rhumb never reads — `access` narrows it to
some of the person's routes, and `reaches` lists networks where the person
can use it without a modeled device. A person who declares none has one, called
`default`.

**The value is always a mapping, even where only the note is written.** A
credential that is one string today and a mapping the moment it narrows would
be two shapes for one thing, and every reader would have to know both. One
shape costs the common case a line and buys a key that can grow.

**How many credentials someone keeps is theirs to decide, not the service's.**
One password across a laptop and a phone is as real as one per machine: the NAS
has one `dana` in its account table either way, and a model that made the device
the identity could write down only the second. So the credential is declared on
the person, and a device names which one it uses:

```yaml
# nodes/dana/phone.yaml
id: dana-phone
# credential: default, so it is not written
```

```yaml
# nodes/dana/work-laptop.yaml
id: dana-work-laptop
credential: work
```

| | Account | Secret | Files |
| --- | --- | --- | --- |
| `default` | `dana-default` | `u-node-group-09-01/ss-sea01/users/dana/default` | one per device naming it, per route it opens |
| `work` | `dana-work` | `u-node-group-09-01/ss-sea01/users/dana/work` | one per device naming it, per route it opens |

Two devices naming one credential are **one principal**: one row in the
server's table, one file on disk, one password. They still render a
configuration each, because their local ports and client programs differ — they
share a secret, not a file.

**A credential is an account because it is declared, not because a device names
it.** Writing one down is the decision; a device only chooses which of them it
carries. So a credential no device names is still an account, with its own row
in the server's table and its own secret — it is one the person carries
themselves, onto whatever machine is at hand:

```yaml
  dana:
    access: [sea]
    credentials:
      default: whatever machine is at hand
      mbp: the laptop, revocable on its own
```

```yaml
# nodes/dana/mbp.yaml
id: mbp
credential: mbp
```

Here `mbp` is carried by that laptop and `default` by the person. Both are
accounts on the port `sea` enters; the first renders a file for the laptop, and
the second one named for the person, since there is no device to name it for.
A file the person carries is dialed from a machine this inventory does not
model. The `universal` network is implicit; the credential may also declare
`reaches: [home]` when the person uses it on an unmodeled device in that
network. A route whose entry has no address on any of those networks is an
error, and a rendered client file dials the entry on the first of those
networks, in `networks.yaml` order, where it has an address. With no
`universal` network declared, `reaches` is all such a credential has.

The alternative would be a credential that exists in `users.yaml` and nowhere
else: no account, no secret, no file, and nothing saying why. Declaring one is
the deliberate act, and deleting one is the other.

A device naming a credential its owner does not keep is an error listing the
ones they do.

**The account name is the username and the credential, always both.** A table
of `dana-default` and `dana-work` says what it is; one where the first
credential is bare and the rest are suffixed would rename the first the day a
second appears. `default` is a name like any other — it is written out in
account names, in secret paths and in the picture — and it is simply the one
you get without choosing.

The one exception is the service's to declare, not the person's: a service
whose accounts are POSIX users — Samba — writes
[`accounts: person`](export.md#the-account-name), and every account it holds is
the username alone. That is a whole service choosing, never the first
credential going bare, so nothing is renamed when a second credential appears;
a second one on that port is instead an error, since it would be a second
account under the same name.

**Keeping one credential per device is still available**, by declaring one per
device and naming them. That is the safer default in the sense that losing a
machine costs one deletion, and it is not the model's business to insist on it:
a person who uses one password everywhere is describing what they actually do,
and writing two values the server then has to carry would be the inventory
inventing a fact.

**Deleting one is a deliberate step.** Removing a credential leaves its value on
disk, where `secret sync` reports it as orphaned and never deletes it — it
cannot tell a removal from a rename. That report is the reminder to delete the
file and re-render the servers it reached; until both are done, the password
still opens the port. `rhumb check` names it.

### Named sets

`users.yaml` may name lists of routes that several people are granted:

```yaml
sets:
  proxies: [sea-ss, sea-hy2, hel-ss]
users:
  erin:
    access: ["@proxies", home/samba]
```

An access list — a person's, a credential's, a device profile's — names a set
as `@<set>`, and Load expands it in place. A set is an explicit list, never a
pattern: adding a route to it grants every person holding the set, and that
grant is one line in this file. A set names routes only, not other sets.
Renaming a route rewrites the sets that name it.

### A credential may open fewer routes

`access` on a credential narrows it to some of the routes its owner holds:

```yaml
  dana:
    access: [jp, sea, home-sea]
    credentials:
      default:
        note: the phone
      mbp:
        note: the laptop, which leaves the house
        access: [sea]
```

`mbp` is an account on the port `sea` enters and on no other. The laptop takes
a file for `sea` and none for `jp` or `home-sea`, and the account tables those
two routes enter never hold its password. Losing the laptop costs one route,
not every route its owner has.

**Access is granted to the person, and a credential chooses among it.** A
credential may name only routes its owner already holds; one naming a route
they do not is an error listing the routes they have. The alternative — a
credential reaching past its owner — would mean reading `users.yaml` twice to
answer what one person can reach, and the `access` line would stop being the
answer.

**Omitted, a credential opens every route its owner holds.** That is what a
credential without an `access` means, so adding a route to a person adds it to
each of their credentials, and only the ones that say otherwise stay behind.

**A device carries one credential and nothing else.** A route its credential
does not open is a route that device cannot take, however the owner's own
`access` reads, so nothing is rendered for the pair.

**Narrowing is not the same as a second person.** Two people are two account
names, two sets of files and two lines in the picture. One person with a
narrowed credential is one identity whose password for one line is revocable
on its own — which is what someone means when they say the laptop should not
be able to reach the house.

### A person with no device file

`devices: none` says this inventory has no node file for this person, and that
it is deliberate:

```yaml
  friend-a:
    username: yak
    devices: none
    export: link
    credentials:
      default:
    access: [jp, sea]
```

It says only that, and it decides nothing. Credentials are declared the same
way as anybody else's, and since none of them is named by a device, every one
is [carried by the person](#credentials-belong-to-the-person) — the same rule
as everybody, with nothing left on the other side of it. What the person copies
one onto is not modelled, because it cannot be: how many machines someone else
uses a configuration on is not a fact the inventory can hold, and inventing
node files for machines nobody tracks would make it lie.

**The key exists to tell two silences apart.** A person with no node file and a
person whose node files were put in the wrong directory look identical. `devices:
none` makes the first one something written down, so validation can name the
second — and writing it beside a node file that *is* theirs is a contradiction
validation reports.

With no node to carry an `export`, it is written on the user instead and means
the same thing. `link` is usually the answer: which program someone else runs
is not a fact the inventory can hold, and a share URI is the one form nearly
every program imports.

## Routes

```yaml
# routes.yaml

routes:
  jp:
    hops: [j-node-group-07-01/hy2-01:users]

  sea:
    hops: [u-node-group-09-01/ss-01:users]

  home-sea:
    hops: [home-server/http-01:proxy, home-server/ss-01:local, u-node-group-09-01/ss-relay:relays]

  bin:
    hops: [u-node-group-09-01/bin-01:web]
```

Each adjacent pair of hops is one edge. `home-sea` says traffic enters the home
server's HTTP proxy, passes to the Shadowsocks client on the same machine, and
leaves through the relay port in San Francisco. Two of those hops share a node
and one does not, and nothing in the file says so — the address rules above
settle it.

A route is also the unit a person is granted. That is deliberate: inserting a
relay into the middle of a chain changes the route's hops and changes nothing in
`users.yaml`.

A one-hop route is an ordinary route. Reaching a service directly is not a
special form.

### Route scopes

A route written under a key that is a network name or a node id is scoped
there: the client must be on that network, or be that machine.

```yaml
routes:
  sea:
    hops: [u-node-group-09-01/ss-01:users]
  home:
    samba:
      hops: [home-server/samba-01:smb]
  home-server:
    sea:
      hops: [home-server/sslocal-01:socks, u-node-group-09-01/ss-01:relays]
```

A key holding `hops` is a route; any other key is a scope. A scoped route's
full name is `<scope>/<name>` — `home/samba` — and that is what `users.yaml`
grants and what `--route` renames. Where a name is shown to a person or used as
part of a derived id, `/` becomes `-`: `home-samba`.

The scope is a choice, and it narrows where the entry is published: a route
scoped to a network publishes its entry only on the entry node's address
there, a route scoped to a node only on loopback, and a route scoped to a
container network of its entry's node on no host address at all. A top-level
route publishes on every network the node answers on.

A container network scope says the clients are on that bridge: behind a
router joined to it — a Tailscale subnet router, say — that the model does not
carry. The entry is reached at its address on the bridge and publishes
nothing, which is how a second listener on a port number the host already
publishes stays off the host. No credential reaches a container network, so
no credential opens such a route.

Rule 33 checks it: the scope names a network, a node or a container network
of the entry's node, and no name two of those use; the entry has an address
on the network, runs on the node or joins the container network; a
credential opening a route scoped to a non-universal network reaches that
network, through its own `reaches` or a device carrying it; and no credential
opens a route scoped to a container network.

Renaming a node renames its scope key and every grant naming a route in it.

### Naming

A route's name says what the person is choosing when they pick it in their
client, because that is the only place a route name is ever read by someone who
is not editing these files.

Two things therefore stay out of the name:

- **The machine.** A route named `sea` may enter `ss-sea01` today and `ss-sea02`
  after that machine is replaced. Putting the instance in the name throws away
  the indirection that lets a chain be rebuilt without touching `users.yaml`.
- **The protocol**, unless the protocol is the choice. A line that happens to run
  over Hysteria2 is an implementation detail, and a route called `hy2` stops
  meaning anything once a second one exists. A line deliberately offered
  *alongside* the same exit over another protocol — one for speed, one for a bad
  network — is a real choice, and then it belongs in the name: `sea-ss` beside
  `sea-hy2`.

Where two things vary, the name carries both. `home-sea` names an exit and the
fact that it passes through the home server, which are independent of each
other.

Proxy exits and internal services share one namespace — `jp` beside `nas` —
because the person picks from one list and does not care which kind a name is.
That is one axis used deliberately, not two axes mixed by accident, which is
what a `sea` beside a `hy2` would be.

**A route is optional.** An instance nothing relays through and nobody is
granted — a web service reached from a browser, a port open only on the home
network — is a complete, valid instance with no route naming it. Nothing warns
about it.

### One upstream per instance

An instance forwards to one place, so a non-terminal hop must have the same
successor in every route that passes through it. Two routes sending `ss-home`
to different upstreams cannot both be rendered, and the error says so by name.

That constraint *is* rule-based routing — choosing an upstream per request by
domain or region — and it is not supported. When it is, a route will describe
which upstreams an instance can reach rather than which one it does, and the
rule set itself will stay in the instance's own configuration rather than
becoming an inventory concept. The error message names this so the reader knows
it is a boundary and not a mistake.

**Unless the service fans out.** A reverse proxy — Caddy, nginx, Apache — is one
entrance in front of many services, so it has as many successors as there are
routes through it. A service says so once, in its manifest, with
[`downstreams: many`](#a-service-that-fans-out), and rule 8 stops applying to
its instances:

```yaml
routes:
  vault:
    hops: [de-fra-htz-linux-01/caddy-01:https, de-fra-htz-linux-01/vault-01:web]

  bin:
    hops: [de-fra-htz-linux-01/caddy-01:https, de-fra-htz-linux-01/bin-01:web]
```

This is not the rule-based routing above, and the difference is not one of
degree. A proxy picks its upstream by the name the request arrived at, and every
route through it names exactly one — the value is
[the downstream port's `published`](#the-name-a-port-is-published-at), written in
the inventory and readable before anything runs. Rule-based routing picks an
upstream per request from a rule set this inventory does not hold, and one route
can then leave by several exits. The first is static fan-out and is derived; the
second is a decision at runtime, and it stays unsupported.

The hops stay a flat list either way. A route is still one chain from entrance
to exit; what changed is that two chains may share their first hop.

## What is derived

Nothing below is written by hand.

**Grants.** Every credential a person declares is a grant on the entry port of
every route it opens — declaring one is what makes it an account. That is every
route the person is granted, unless the credential's own `access` narrows it. A
device names which one it carries; it does not bring one into being.

**Export instances.** A device and each route its credential opens produce a
file named `<node>-<route>-<service>-<export>`; a credential no device of
theirs names produces `<username>-<credential>-<route>-<service>-<export>`,
one per such credential, because the person carries it themselves. The service
is in the name because an export's name is unique only within its service. Its
values come from its own export's defaults. A device with
[profiles](#a-device-with-several-profiles) produces one file per profile
instead, named `<node>-<route>-<service>-<export>-<profile>`.

It is not a deployment. Nothing runs a share URI, and the program that reads a
JSON configuration runs on a machine this inventory does not model.

### Which export a person receives

**A service is a program a node deploys. An export is a way of handing that
service's credential to a person.** They are separate kinds of thing, and an
export sits inside the service it writes out:

```text
services/     ssserver  sslocal  hy2client  hysteria2  microbin  caddy
services/ssserver/exports/     link  json  shadowrocket
services/hysteria2/exports/    link  shadowrocket
```

A QR code settles the first half: nothing runs one, so a way of writing a
credential out is not a program. The nesting settles the second: an export
renders one service's `upstream` and nothing else, so it belongs to that
service rather than beside it. Two services each offering a `link` are two
exports, and the pair `<service>/<export>` is what names one.

**An export is named after the format, not after the program that reads it,
and not after the protocol** — the directory above it already says which
service this is. `json` is the JSON a shadowsocks-rust client reads. `sslocal`
is the program, and it is a *service*, because the home server deploys one:

```yaml
# nodes/home/server.yaml — a deployment
  - id: ss-home
    service: sslocal
    bind: 127.0.0.1
    ports:
      local: 1080
```

Those two are not the same file and should not share a template. `ss-home` is
a hop: `http-home` dials into its `local` port, so that port's number and
address are a contract other instances depend on. The file written for a
laptop is a leaf: nothing dials into it, and its local port means something
only to the applications on that machine. They will diverge, and each should
be free to.

**The directories a service holds are the ways it may be written out.** It
does not list them as well: a list could name a directory that is not there,
or miss one that is, and a reader would have no way to tell which of the two
is the truth.

```yaml
# services/ssserver/confgen.yaml
auth: per-principal
self:
  psk: {set: true, kind: base64, bytes: 32}
template: templates/config.json.tmpl
output: config.json
```

```yaml
# services/ssserver/exports/link/confgen.yaml
template: templates/share.txt.tmpl
defaults: element
output: share.txt
```

**An export manifest has three keys and no more.** No `ports`, since nothing
listens; no `auth`, since nothing connects to a file; no `self`, since what
it carries belongs to the service it reaches and arrives through
`upstream`. The manifest being half the size of a service's is the sign the
split is along the right seam.

**Every one of them is written.** Which form someone wants is a question asked
when the files are handed over, not when the inventory is written. So all of
them render, a bundle holds each, and a
[selector](export.md#targets-and-selectors) narrows to the ones this hand-over
needs:

```bash
rhumb export user:erin                                # every way
rhumb export user:erin export:link                 # one of them
rhumb export user:erin export:link export:json     # two of them
```

They share a credential — the account is the person's, not the file's — so
several forms cost several renderings and no extra secret.

**A device narrows to one of them** when it is known to run one program:

```yaml
# nodes/dana/phone.yaml
export: json
```

This is a property of the device rather than of each instance, because a device
runs one client program for everything it proxies. It does not say which
service it narrows *within*, and does not have to: a device granted both
Shadowsocks and Hysteria2 routes takes each service's own `json`. A name only
one of them offers narrows that one and leaves the other at everything it
offers.

**A service's exports and a device's `export` answer two different
questions.** The service's directories say which ways exist, which is a fact it
knows. The device says which one it can read, which is a fact only it knows — a
manifest that guessed would hand a phone a configuration written for a
command-line client.

So the resolution is:

1. **A service with no `exports/` writes nothing**, whatever the device says.
   MicroBin is reached from a browser: there is no file to hand over, and a
   route entering it still produces a grant, but no file. The address and the
   password reach the person some other way, which is the truth of how such a
   service is used and not something to invent a file for. `export` does not
   bring a file back — it chooses among the ways, never whether any exists.
2. Otherwise the node's `export`, or, for a file the person carries rather
   than a device, the user's, narrowing to that one way.
3. Otherwise every way the service names.

An `export` naming something none of the services this device reaches offers is
an error listing the services it does reach. It does **not** fall back to the
full list: the failure would then happen on the device rather than here.

### A device with several profiles

One device may run several programs that each want the same credential
written differently: sing-box and a browser's SOCKS listener on one laptop,
each on its own local port. `export` cannot say that — it picks one way for
the whole device — so a device may declare **profiles**, the uses it is put
to, by name:

```yaml
# nodes/dana/macbook.yaml
id: dana-macbook
profiles:
  singbox:
    export: singbox        # a format of its own: its own template
  browser:
    export: json
    values:
      local_port: 1080     # the same format, one value changed
    access: [sea]
```

A device with profiles is written out **once per profile**, and each profile
narrows in the device's place: its `export` chooses among the ways the
services it reaches offer, exactly as a device's `export` does, and unwritten
takes every one. The file name gains the profile —
`dana-macbook-sea-ssserver-json-browser`. A device without profiles is
written out once, as before.

**Two ways to make a profile different, and both are meant.**

- **A different format is a different export.** When the program reads a
  different file altogether — sing-box's configuration is not sslocal's
  `config.json` — the difference is a template, and a template lives in the
  service's `exports/<name>/` with its own `defaults.yaml`. The profile only
  names it. Every device with the same use names the same export, and the
  settings are written once.
- **The same format with a value changed is `values`.** A second listener on
  another port is not a second format, and copying a template to change one
  number is the copying this page exists to remove. `values` reach the
  template as the file's own values, as an authored override's do.

An instance authored on the device with a profile's file name still pins it,
and its `values` win over the profile's key by key. The profile says what the
use needs; the override is the exception for one route.

`access` narrows a profile to some of the routes the device's credential
opens, as a credential's `access` narrows its owner's. Unwritten, every one.

`runs` names the service whose program the profile is, such as `sslocal` or
`singbox`. **A profile that runs a program is one instance**, and one process: a single derived instance, `<device>-<profile>`, of the named service,
whose upstreams are every route its `access` opens. The service's own template
renders it, as it renders an authored instance of that service, reading
`bind` and `ports` from the profile, and `upstreams` — or `upstream`, for a
program that takes one, which then refuses a profile opening more. The export
adds a `manifest.yaml`, and a deployment tool installs the configuration as
that program's; see [the manifest](export.md#the-manifest). A profile that
runs a program takes no `export`: a server's exports are for files a person
carries, and a server does not know the formats of the programs dialling it.

Unwritten, the profile is files for a person to put wherever they go, one per
route in its export's format, with its listener in `values`; it takes no
`bind` or `ports`. The service must be defined.

**Profiles are uses, not accounts.** Every profile carries the device's one
credential, so the server's table holds one row for the device however many
profiles it has, and revoking the device revokes all of them. A use that must
be revocable on its own is a separate credential, and a credential is chosen
by a device, not by a profile.

A device with profiles does not also write `export`: each profile says its
own, and a device-wide one beside them would be read by nothing. A profile
with `export: none` is an error rather than a way of switching one off — a
profile writing nothing is a profile to delete. Profiles belong to devices; a
node nobody owns runs services and has nothing written out for it.

`rhumb export node:dana-macbook profile:singbox` hands over one use.

### When the client program is unknown

Two answers, and both already exist in the shapes above.

A **link export** writes the protocol's own share URI — `ss://`,
`hysteria2://` — to a text file. Nearly every client imports one, so one of
them per protocol covers every program nobody has written a template for. It is
what a credential carried by the person rather than by a device gets, since the
program it will be pasted into is not a thing the inventory can know, and it
costs nothing to write beside the others.

**`export: none`** says this device receives nothing. The grant is still
derived and the server still carries the account; only the file is absent, for a
machine configured by hand.

An export instance's ports are its own local listeners — the SOCKS and HTTP
ports an application on that device connects to. Nothing reaches a phone from outside,
so they are never hop targets, and they carry no secrets. Their names are read
by the template, which needs to know which listener is which; the address and
port at the far end are not written here, because the route derives them.

A derived instance is a default, not a fixed result. Authoring an instance with
the same identifier pins what it names and leaves the rest derived:

```yaml
# nodes/dana/phone.yaml

id: dana-phone
reaches: [home]

instances:
  - id: dana-phone-sea-ssserver-json
    ports:
      socks: 10080
      http: 18080
```

Everything not written stays derived: the export, `bind`, and every value the
export's defaults supply. An override that replaced the whole instance
would mean copying a client configuration out in full to change one port, which
is the copying this page exists to remove.

`service` may not be written in an override. An export instance has none: it
follows from the route's entry hop and its `exports`, so a line naming it is
either redundant or a contradiction.

Written and derived instances share one namespace, so the uniqueness check
covers both.

**Grants.** One per (principal, port) pair: one for each credential granted a
route, and one for each non-terminal hop reaching the hop after it.

**One principal on one port is one credential**, whatever brings it there.
Two routes a person is granted that enter the same port give them two client
configurations and one account: a server's table has no way to tell which
route a connection came over, and nothing downstream could act on it if it
had. A second account on the same port therefore means a second credential,
not a second route — and declaring one is a line in `users.yaml`.

Two entries in `users.yaml` for one person are still the answer when the
accounts are meant to be independent of each other rather than one person's:
a separate access list, separate grants, and nothing tying them together.
[Rule 13](#validation) requires their rendered names to differ — a port cannot
hold two accounts called `dana-default`, and a server could not tell them apart
if it did.

**Server account tables.** The principals holding a grant on a port, rendered as
accounts. People and machines are listed together, because to the server they
are the same thing:

```text
ss-sea01, port users:
  dana-default
  dana-work
  yak-default

ss-sea01-relay, port relays:
  home-server-ss-home
```

Two ports of one instance carry two independent tables and two independent sets
of credentials.

## Secrets

One credential is one file. The path is the identity:

```text
<node>/<instance>/<port>/<group>/<name>
```

| Segment | What it says |
| --- | --- |
| `<node>/<instance>` | Which instance the credential opens: its key, two segments |
| `<port>` | Which port of it — ports have separate account tables |
| `<group>` | Whose it is: a person, or an instance relaying through |
| `<name>` | Which of theirs: a credential, or `default` for a relay's single one |

```text
~/confgen-secrets/
├── u-node-group-09-01/
│   ├── ss-sea01/
│   │   ├── users/dana/default
│   │   ├── users/dana/work
│   │   ├── users/friend-a/default
│   │   ├── relays/home-server-ss-home/default
│   │   ├── self/psk/main
│   │   └── self/psk/backup
│   └── bin-sea01/
│       └── self/auth_password
└── j-node-group-07-01/
    └── hy2-tzr01/
        ├── users/
        │   ├── dana/default
        │   └── friend-a/default
        └── self/tls_key
```

**Every credential under a port is a `<group>/<name>` pair**, whatever holds
it. Sorting by the kind of holder instead — a `node/` beside a `user/` — draws
a distinction a reader of the tree never needs, and leaves `node/phone` unable
to say whose phone it is. Grouping by the holder answers that. A relaying
instance is its own group, named `<node>-<instance>` so the path keeps its
depth: what connects is the instance, not the machine under it and not whoever
hosts that machine. When the connecting instance declares
`principal`, the named user is the group instead; for example an instance
dialling as `cn-repeater` reads
`u-node-group-09-01/ss-sea01/relays/cn-repeater/default`. This changes whose credential the
instance carries, not who is granted the route.

`self` is the exception, and sits at the instance level rather than under a
port: an instance's own secrets belong to the instance, and which of its
listeners hands one out is the port's business rather than the file's. An
instance may have several, and one of them may be a set of values or a record
of fields, so a path under `self` is one, two or three segments deep — see
[a service's own secrets](#a-services-own-secrets). `self` is therefore a
reserved port name.

The layout is grouped by what consumes the secrets, because rendering a server
reads every credential for one instance at once. Reading it the other way works
without decrypting anything: `ls` a port to see which devices can reach it, or
grep the tree for a node name to see what one device holds.

There is no index and no identifier to assign. A file's path is derived from the
same facts the renderer derives everything else from, so the set of files that
should exist is computable, which is what makes `secret sync` possible.

Nothing groups an instance's files under its node. Node membership can change —
an instance moving to a different machine is a rename, per
[naming](#naming) — and a physical directory encoding it would have to move
along, the same fragility a route name avoids by not naming a machine.
Everything scoped to one physical machine — every instance it hosts — is a
selector, `node:u-node-group-09-01`, answered by querying the inventory, not by
where files sit.

### Editing without rhumb

The layout needs no tool to read or change: every value is a plain file, so a
machine with no rhumb on it still works with what is already there.

```bash
# every credential ss-sea01 holds, without decrypting anything special
find u-node-group-09-01/ss-sea01 -type f -exec sh -c 'echo "== $1 =="; cat "$1"' _ {} \;

# change one
printf 'new-value' > u-node-group-09-01/ss-sea01/users/dana/default

# who can reach it, with nothing decrypted
ls -R u-node-group-09-01/ss-sea01
```

This is not a fallback bolted on afterward — it is what one file per credential
buys, the same reason age and SOPS-based setups converge on it: a value
readable and writable with `cat` and a redirect outlives any tool built to read
it. Whatever rhumb grows to make this more convenient, `find`, `cat` and
`printf` keep working.

### Viewing and editing several at once

Seeing every credential an instance holds today means opening each file in
turn; filling in several at once during a migration means the same, once per
value. `rhumb secret show <instance>` and `rhumb secret edit <instance>`
are for that:

- **`show`** reads everything under an instance — every port, every principal,
  its own secrets — and prints it as one structured block, generated on
  demand and never written anywhere.
- **`edit`** does the same, into a temporary file opened in `$EDITOR`. On save,
  each value that changed is written back to its own file, one write per
  changed leaf — editing several credentials in one pass still touches only the
  files that actually changed. A key added in the editor that names no path the
  inventory implies is refused: a name comes from `users.yaml` or a node file,
  never from typing it into an editor session, so `sync` cannot see it as an
  invented path with no history. A key removed in the editor is left alone —
  deleting a credential is `secret sync`'s and a person's own decision, the
  same way `sync` itself never deletes.

Both take a `node:`, `user:` or `route:` selector too, covering every instance
it matches — the query [above](#secrets) answers "everything on this
machine", these answer "let me see and change it."

Neither exists yet, and neither is a Cobra subcommand.
`inspect.md` is what implements them: `show` is a
secret view read, `edit` is `e` on it, so the write happens inside a leaf
command's page behind the usual confirmation dialog rather than from an
invocation outside the read-only reports `tui.md`
describes. What remains open in [`export.md`](export.md#open-questions) is the
command line alone.

### How a service says what it needs

A service declares whether its inbound side authenticates:

```yaml
# services/ssserver/confgen.yaml

secret:
  kind: base64
  bytes: 32

auth: per-principal
template: templates/config.json.tmpl
defaults: element
output: config.json
```

```yaml
# services/sslocal/confgen.yaml

auth: none
template: templates/config.json.tmpl
defaults: element
output: config.json
```

| `auth` | What a grant on one of this service's ports implies |
| --- | --- |
| `per-principal` | One secret per principal reaching each port, and a rendered account table |
| `none` | Nothing |

There is no third value. Declaring a secret and handing it out are two separate
things: [`self`](#a-services-own-secrets) declares one, and
[a port's own `self` list](#a-secret-several-people-hold) says it travels to
whatever is granted on that port. Either can be used without the other — MicroBin's admin password
goes to nobody, and a service can hand out a secret whose principals have none
of their own — so `auth` keeps only the question it was always answering: does
something reaching this service get a credential of its own.

`auth` is per service, and a client service usually declares `none`: its local
listener is not the protocol at all — a Shadowsocks client's loopback SOCKS
port authenticates nobody, while `ssserver` authenticates everybody.

`secret`'s shape is the protocol's:
Shadowsocks 2022 needs base64 of exactly the key size, not an arbitrary
password. A service that does not declare one gets a printable random string.

### What a service needs from its upstream

`auth` and `self` describe the inbound side. `upstream` describes the other
one: what this program needs from the hop it dials, beyond the address, port
and account every template is given.

```yaml
# services/sslocal/confgen.yaml

auth: none
upstream:
  shared: {}
```

| Name | What it holds |
| --- | --- |
| `shared` | The secrets the upstream port hands to everything granted on it, in the order that port writes them |
| `values` | The upstream instance's own values, as that instance wrote them |

A declaration is empty, or carries `optional: true`:

```yaml
upstream:
  shared: {optional: true}
  values: {}
```

`optional` says the hop may hand over nothing. Without it, declaring `shared`
against a port that hands out none is an error, which catches the ordinary
mistake: a client unable to authenticate without the server's half of a
password, dialling a port that was never given one. It is written where one
program is configured both ways on different machines — a Hysteria2 instance
that obfuscates its handshake hands out an obfuscation password, and one that
does not hands out nothing and is still reachable. The template then renders
what it was given, which for an optional name may be an empty list.

Shadowsocks 2022 is the case it exists for. The password a client sends is
the server's PSK for that port and the client's own, joined, so `sslocal`
cannot render a working configuration from its own credential alone. The
order is the port's, because a protocol taking the two in the other order
authenticates nothing.

**The declaration is the consumer's.** A secret belongs to the instance it is
filed under, and it reaches another instance because the program dialling says
it needs it — never because the program listening happens to publish it. This
is the whole of the rule, and it is what keeps a web service's upload password
out of the reverse proxy in front of it: the proxy forwards, it does not
authenticate, so it declares nothing and is handed nothing, however much the
port it reaches hands to the people granted on it.

Reading it the other way round — every upstream port's `self` list travelling
to whatever dials it — makes two different facts one key. A port's `self` list
says what the *people* granted there hold; `upstream` says what a *program*
needs to connect. They coincide only while the person and the next hop are the
same party, which is exactly what a proxy in front of a service stops being
true.

`values` is the same rule applied to configuration rather than credentials.
Some of what a server is configured with is not the server's business alone:
a Hysteria2 instance that obfuscates its handshake is unreachable by a client
that does not obfuscate it the same way, and the range a client hops ports
over is a fact about the machine it dials. Written on the instance that
listens, it reaches the client's file from there, so the two cannot drift
apart the way two copies of one number do. Credentials never arrive this way —
they are `shared`, read from the secrets store — so declaring `values` hands
over no secret, whatever the instance holds.

An export declares either the same way and for the same reason: the share URI
a person imports carries the same two-part password its client configuration
would, and is as unusable without the server's half.

A name rhumb does not understand is an error rather than something skipped.
Silently dropping a credential a program needs renders a file that looks
complete and does not authenticate, which is the worst of the ways this can
fail. Growing the list is adding a name — an upstream's certificate
fingerprint, its SNI — not changing the shape of the key.

### A service that forwards

Some machines terminate nothing. A relay in front of a server in another
country takes the bytes arriving on one of its ports and hands them to the hop
that follows it, unchanged and unread. A service whose instances do that says
so:

```yaml
# services/realm/confgen.yaml

auth: none
forwards: true
```

It is not a reverse proxy. A proxy terminates one connection and opens
another, which is why it authenticates nobody and still needs a site name to
match on. A forwarder does not have the two connections: it has one stream of
bytes with a machine at each end, and the protocol inside them is not its
business.

What it changes is where a client's credential comes from. The client dials
the relay's address and port, and authenticates against the hop behind it, so
a route entering a forwarder is written out as the service that *ends* it, in
that service's own export form. The grant is on the terminating port, and the
relay holds nothing: no account table, no secret of its own, nothing to
rotate. `forwards` with `auth: per-principal`, or with `self`, is a manifest
that will not load — a program that reads nothing it is given cannot
authenticate anyone, and saying both is saying a thing that cannot happen.

A forwarded chain therefore has two ends, and a client's file is built from
both:

| What | Which end it comes from |
| --- | --- |
| address, port, the name the port is published at | the relay: it is what the client dials |
| account, secret, `shared`, `values` | the hop that terminates the chain |

A template reaching the far end reads `(upstream).exit`, which holds that
hop's instance, port, number and published name. It is written only when the
two ends differ, so a template can tell a forwarded route from an ordinary one
by asking whether it is there. The case it exists for is TLS: a relay in
Nanjing in front of a server in San Francisco is dialed at the Nanjing name,
and the certificate is still the San Francisco one, so the client must ask for
that name and not the one it dialed.

**rhumb renders the relay's configuration and nothing else.** Whether the
bytes reach it — a firewall rule, a port range redirected into it — is the
operator's, the same way deployment is.

### A service that fans out

A service that is one entrance in front of many says so once:

```yaml
# services/caddy/confgen.yaml

auth: none
downstreams: many
template: templates/Caddyfile.tmpl
defaults: document
output: Caddyfile
```

| `downstreams` | What an instance of this service may dial |
| --- | --- |
| `one` | One upstream, the same in every route through it. The default, and not written |
| `many` | One upstream per route through it, told apart the way `dispatch` says |

**How the routes are told apart** is `dispatch`, which is read only beside
`downstreams: many` and defaults to `name`:

| `dispatch` | What distinguishes one route from another |
| --- | --- |
| `name` | The `published` name of the port each route reaches. A reverse proxy picks its upstream by the name the request arrived at, so every downstream needs one — [rule 17](#validation). The default, and not written |
| `port` | The port of this instance each route arrived on. A relay listens once per route and sends what arrives there onward, so a published name downstream means nothing and is not asked for |

The two are different machines, not two spellings. A proxy has one listening
port and many names behind it; a relay has one port per next hop and no names
at all. Which one a service is decides both what rhumb requires of it and what
its template is handed: a `downstream` carries the name the route arrived at
and the port it arrived on, and a template reads whichever of the two its
program dispatches on.

`many` relaxes [rule 8](#one-upstream-per-instance) for the instance as a
whole, and `dispatch: port` puts it back one level down:
[rule 22](#validation) holds one successor per port, because a port listens
for one next hop and two routes disagreeing about it would render two
endpoints on one number going to different places.

`many` is the only thing that relaxes
[rule 8](#one-upstream-per-instance), so a service that has not asked for it
behaves exactly as before. What an instance of such a service is handed is
[`downstreams`](#the-render-context), the resolved far end of every route
through it.

It is a declaration about shape, not about credentials. A proxy declares no
`upstream`, and the reasoning is the section above: it forwards, it does not
authenticate, so nothing a port hands to the people granted on it reaches the
proxy in front. A fan-out service needing a secret from each of its downstreams
would be a new key and is not one of these.

### A service's own secrets

`auth` describes the inbound side: what a principal needs to reach this
service. A service also has credentials of its own, belonging to the instance rather than
to anything reaching it — an administrative password, an upload password, a
TLS private key. They are independent of `auth`, and a service declares them by
name:

```yaml
# services/microbin/confgen.yaml

auth: none
self:
  auth_password: {}
  admin_password: {}
  upload_password: {}
```

Each name is one `<instance>/self/<name>` file, one per instance of the service,
generated by `secret sync`. MicroBin authenticates nobody in the inventory's
sense — nothing routes through it, no principal holds a grant on it — and still
holds three passwords, which is why the two are separate keys.

An empty declaration is one generated value in the shape the service's `secret`
block declares. Three keys change that, and no more:

| Key | What it makes the name |
| --- | --- |
| `kind`, `bytes` | The shape of this name's value, where it differs from the service's `secret` block. `kind: opaque` means a value rhumb never generates — a private key, a certificate chain, a vendor's keyfile — reported as missing until someone writes it. |
| `set: true` | A family of values under one name, whose keys each instance declares. One file per key: `<instance>/self/<name>/<key>`. |
| `fields` | One credential made of several generated parts, each with its own shape. One file per field: `<instance>/self/<name>/<field>`, or `<instance>/self/<name>/<key>/<field>` when the name is also a set. |

```yaml
# services/tuic/confgen.yaml

self:
  account:
    set: true
    fields:
      uuid:     {kind: uuid}
      password: {kind: base64, bytes: 32}
  tls_key: {kind: opaque}
```

```text
tuic-sea01/self/account/main/uuid
tuic-sea01/self/account/main/password
tuic-sea01/self/account/backup/uuid
tuic-sea01/self/account/backup/password
tuic-sea01/self/tls_key
```

An instance says which of these it holds, and the keys of each set. Naming
a value on a port is already saying the instance has it, so the ordinary
case is writing nothing and letting the ports speak:

```yaml
  - id: ss-sea01
    service: ssserver
    ports:
      users:  {port: 38250, self: [psk.main, psk.backup]}
      relays: {port: 52146, self: [psk.main]}
```

Writing `self` is for the two cases the ports do not cover. A key no port
hands out — one the instance keeps to itself — is written with its name:

```yaml
    self:
      psk: [main, backup, internal]
```

And an instance holding fewer of its service's own secrets than the service
declares narrows by writing the ones it has. A paste bin on the home network
with no administrative interface should not be handed the two passwords
guarding one:

```yaml
  - id: bin-nas
    service: microbin
    ports:
      web: {port: 8080, self: [upload_password]}
    self:
      upload_password:
```

Unwritten means every name the service declares, which is the ordinary case;
written, it is the whole list, so `self: {}` means none of them.

**The tree stops there: a name, a key, a field.** Everything deeper is
structure, and structure is configuration. Which values a port hands out, in
what order, what each one is called, what it is for — those are facts a diff
should show and a reviewer should read, so they live in the node file and in
`values`, where version control keeps them. The secrets tree holds the leaves
alone, because that is what keeps one credential one file: readable with `cat`,
writable with `printf`, rotatable on its own, and computable by `sync`. A value
that genuinely has no shape rhumb can generate is `kind: opaque`, one file, not
a structure the tree has to learn.

**The manifest and the instance are the only places these are named.** The
manifest names the secrets and their shape; the instance names the keys of each
`set`. Together they are what makes the tree computable in both directions: a
path they imply with no file is generated, and a file under `self/` they do not
imply is reported orphaned like any other. Without them, the two are the same
fact on disk, and `sync` can only leave both alone.

**A value that has to be edited after it is generated is not a secret.** A
random administrative *password* is exactly what is wanted; a random
administrative *username* is a string you will replace by hand the first time
you see it, on every machine, forever. `kind: opaque` is the one exception,
and it is an exception about generation rather than about editing: the value
belongs in the tree, and nothing but a certificate authority or a vendor can
produce it. It is configuration, and belongs in the
service's `defaults.yaml` where a change to it is a diff someone can read. The
test is not whether a value is sensitive — a username is — but whether
generating it produces the value you want.

### A secret several people hold

A port says which of its instance's own secrets everything granted on it also
holds:

```yaml
    ports:
      users:  {port: 38250, self: [psk.main, psk.backup]}
      relays: {port: 52146, self: [psk.main]}
```

A port with no `self` hands out nothing beyond each principal's own secret,
which is the ordinary case. What the list means follows from `auth`, which is
the whole of the difference:

- With `auth: per-principal`, each principal's own secret and these arrive
  together. Shadowsocks 2022's server holds a PSK for the port, and each user's
  effective password is that PSK and their own, joined — no client can connect
  from its own secret alone.
- With `auth: none` there is no secret of their own, so these arrive alone: one
  upload password, known by whoever is granted the route.

**The list is ordered**, because the protocols that take more than one are
ordered: a Shadowsocks 2022 relay chain is a sequence, and rendering it in a
different order than the peer holds produces a configuration that authenticates
nothing. It is written in the node file rather than derived, so the order is a
line someone can read and a diff someone can review.

Naming the values per port, rather than marking one of them in the service
manifest, is what lets one program serve two ports on different credentials —
which is the case the manifest could not express, since a service's manifest
describes every instance of it at once and the difference is between two ports
of one. Two ports handing out the same value say so by naming it twice.

A port's list names `<name>.<key>` for a `set` name and `<name>` for the
others. It never names a field: a field is half a credential, and half a
credential is not a thing to hand over.

**Who holds one of these is who is granted the port** — which the model already
knows, so the blast radius of a rotation is something a view can name rather
than something to remember.

This list says who holds the value, and nothing else. It does not decide
whether the program dialling that port is given it: that is the dialling
service's own declaration, in
[`upstream`](#what-a-service-needs-from-its-upstream). The two coincide while
the person and the next hop are the same party, and a reverse proxy in front of
a web service is where they stop coinciding — the people granted there still
hold the upload password, and the proxy, which only forwards, is handed
nothing.

### Keeping the tree in step

`rhumb secret sync` compares the paths the inventory implies against the
files on disk. Two things imply a path: a `per-principal` service implies one per
(principal, port) grant, and a service's [`self` declarations](#a-services-own-secrets)
imply, per instance, one path per name — per key for a `set` name, per field
for a name with `fields`, and one per key and field for a name with both. A
`kind: opaque` name is never generated: it is reported as missing until someone
writes it. A name whose keys and fields are only partly on disk is reported as
incomplete rather than completed, since the values that are there may have been
written by hand and the ones that are not may be on their way. Missing paths are generated. Paths on disk
that the inventory no longer implies are **reported and never deleted**,
because sync cannot tell a rename from a removal, and the failure mode of
guessing wrong is silent: a new random value on one side and the old one still
on the other.

Renaming a node or an instance is therefore an explicit move,
`rhumb secret mv <old> <new>`, and sync says so when the number of new paths
and orphaned paths match. `mv` is not implemented yet: sync names the orphans
and the hint, and moving the files is done by hand until it is.

Sync names what it is about to write before writing it, and asks — the values
are credentials, and a server has to be rendered again and deployed before
anything can connect with a new one. `--yes` is what a script passes.

Sync also reports which servers need re-rendering. **Adding a credential is a change
on the server side**, not only a bundle handed to its owner: every instance it
reaches must be rendered again and deployed before anything can connect with
it. Adding a device that names a credential already in use is not — it renders
a file and touches no server.

### Rotation

A secret file may have a sibling named `<name>.previous`. A service rendering an
account table emits both values as two accounts; a client renders only the
current one. That makes a rotation three ordered steps with no disconnection:

1. Write the new value, move the old one to `.previous`, render and deploy the
   server.
2. Render and deploy the clients.
3. Delete `.previous`, render and deploy the server again.

The overlap is a second working credential, so it is a debt and not a state.
`validate` reports a `.previous` file older than seven days as an error.

A service whose template cannot emit two accounts for one principal declares
`rotation: disruptive`, and rotating it says up front that the
connection will drop.

### What is not protected

The secrets tree is plaintext. Losing it loses the credentials themselves:
they are random values held nowhere else, so the recovery is a rotation of
everything, not a restore. Keeping a copy is outside what rhumb does.

## The render context

The inventory reaches templates as a structured context, and that context is the
contract between this page and every template. Changing its shape breaks every
template at once, so it is written down here and changes to it are breaking
changes.

```yaml
node:        the node this instance runs on: id, networks, and its
             container networks in order, each with name, subnet and
             gateway when written
instance:    id, service, ports (each with its number — the transport is the
             model's, not a template's — the name it is published at, when it
             has one, and the self values it hands out, in the order written),
             bind, the container networks it joins in its node's order —
             each with name, subnet, gateway when written and address when
             fixed — and the instance's own values
upstream:    the next hop, resolved: address, port, the name that hop's port
             is published at when it has one and the edge resolved on the
             universal network, the account name this
             instance connects as, and its secret; plus, for a target whose
             manifest declares it needs them, that hop's port's self values
             as shared, an ordered list, and that hop's instance's own values
             as values — absent for a terminal instance, and each of shared
             and values absent from every target that did not declare it.
             For a hop that forwards, address and port are the relay's, the
             credential is the terminating hop's, and exit names that hop:
             its instance, port, number and published name.
             See a service that forwards
             See what a service needs from its upstream
downstreams: for an instance whose service declares downstreams: many, the hop
             that follows it in each route through it, resolved: address, port,
             the published name that route arrived at, and the port of this
             instance it arrived on, as entry and its number. Ordered by
             route name, so a rendered file does not change because a route
             was added above another. Absent for every other instance
principals:  for a port with auth: per-principal, the account name and secret of
             everything holding a grant on it, each with the person whose
             credential it is and which of theirs it is — empty for a
             principal belonging to nobody, such as an instance relaying
             through
grantees:    the same port's principals grouped by the person holding them:
             each person once, with every account name their credentials
             produce there, in the order the first of them appears. An
             account is per credential, because a credential is what is
             revocable; anything belonging to a person rather than to a
             credential — a home directory, an entry in a name map — is
             rendered per grantee. Principals belonging to nobody are absent
dials:       the instance's declared dependencies, by name, each resolved as a
             downstreams entry is: instance, port, number, address, and the
             published name when it has one. Read with dial "<name>"; a name
             not declared is a render error
names:       for a network, every name resolving on it and its address there:
             ports' published names at their entrance node, and hosts'
             names; for a container network of the rendering node, the names
             whose answering instance holds a fixed address on it. Read with
             names "<network>", in name order
publishedNames: every name one of the instance's own ports is published at,
             where published alone gives the first. Read with
             publishedNames "<port>"
self:        the instance's own secrets, by name: a value, a map of fields, a
             map of keys, or a map of keys of maps of fields, as the service
             declares
target:      service and instance names
```

A deploy template is given this context with `mapping` added and `principals`,
`self` and `upstream` withheld — it starts a program and holds no credential.
See [what deploys it](export.md#a-second-file-what-deploys-it).

An export instance's `instance` carries its `export` and, for one of a
device's [profiles](#a-device-with-several-profiles), its `profile`, beside
the values a profile or an override gives it. A service whose defaults apply
per `element` reaches those values unmerged, as `(instance).values`, and it
is for its template to lay them over `defaults`.

The context is structured rather than one deep-merged mapping so that a key
present at two levels cannot silently overwrite another, and so a template
failing can be told which level it was reading.

## Validation

`rhumb conf validate` checks:

1. Node identifiers are unique; instance identifiers are unique, derived ones
   included. `universal`, if written, names a network in the list.
2. Every `instance.service` names a service. Every export a service holds
   declares a template, since one that renders nothing writes nothing. An
   instance's `self` names only the service's
   own `set` names, and a name the service declares `set` has its keys listed
   there. A port's `self` names `<name>.<key>` for a `set` name and `<name>`
   for every other, never a field, and every name and key it uses exists.
3. An `export`, on a node or on a user, is one of the ways
   offered by the services its granted routes enter, or is `none`. The same
   name under two of those services is not a conflict: the device takes each
   service's own. The error lists the services it reaches. A device reaching
   none is not consulted.
4. Every hop names an existing instance and an existing port on it.
5. No port is named `self`.
6. Every route named in an `access` list exists; every user named by an `owner`
   exists; a device's `credential` is one its owner declares, and the error
   lists the ones they do. A credential's `access` names only routes its owner
   holds, and the error lists those. `devices: none` is not written beside a
   node file owned by that person.
7. An instance authored on a client node overrides a derived one: its identifier
   matches an instance the user's access derives, so a misspelled route name
   cannot leave an override that nothing reads, and it does not set
   `service`.
8. A non-terminal hop has the same successor in every route through it, unless
   its service declares `downstreams: many`. The error names rule-based routing
   as the reason this is not allowed yet, and distinguishes it from fan-out so a
   reader knows which of the two they have written.
9. No route names one instance twice.
10. Every edge resolves an address: the two ends are on one node, or the
    downstream has an address on a network the upstream reaches.
11. A route in an `access` list has an entry the granted user's devices can
    reach, on a port not bound to loopback only.
12. The granted routes of a user who keeps a credential no device of theirs
    names enter on the `universal` network or a network in the credential's
    `reaches`. Every named network exists in `networks.yaml`, and only a
    credential no device names declares `reaches`.
13. Account names rendered for one port are distinct. On a service naming
    accounts by person, this is what two of one person's credentials on the
    same port fail.
14. Two instances on one node do not bind the same address, port and protocol.
    A container on a container network binds in its own network namespace,
    so what it holds on the node is the host mapping derived for it, and a
    port published on no host address holds nothing.
15. Every secret the inventory implies exists, and every file in the secrets
    tree is implied by it. Both directions are reported; neither is fixed here.
16. No `.previous` file is older than seven days.
17. Every port a `downstreams: many` instance dispatching by `name` reaches
    declares `published`.
    Without it the proxy has nothing to tell one downstream from another, and
    the error names the instance and the route.
18. Two ports declaring the same `published` name are on one node, neither is
    fronted by a `downstreams: many` instance dispatching by `name`, and they
    differ in number or transport. The error names both. A relay dispatching
    by `port` matches no name, so it fronts nothing in this sense.
19. A target declaring `upstream: shared`, without `optional: true` on that
    declaration, reaches a port whose `self` list hands something out. The
    declaration is the consumer's, so nothing about the port it dials makes
    it true, and a port handing out none renders an empty list into a
    credential built half from it — a file that looks complete and
    authenticates nothing. `optional` is how a service configured both ways
    on different machines says the hop may hand over nothing.
20. A device with `profiles` has an owner and writes no `export` of its own.
    Each profile's name holds no slash or space, since it ends a file name;
    its `export` is one of the ways the services it reaches offer, and not
    `none`; its `access` names only routes the device's credential opens, and
    the error lists those.
21. A route does not end on an instance whose service `forwards`. Such a
    route terminates nowhere: the last hop reads nothing it is given and has
    nowhere to pass it, and the person granted it would be handed an address
    with no account, since the account belongs to the hop that ends the chain.
22. Two routes entering the same port of an instance that dispatches by
    `port` have the same successor. A port listens for one next hop, and
    `downstreams: many` lifting rule 8 for the instance does not lift it for
    the port.
23. An instance's `runtime`, after it takes its node's, is `host`, `docker`
    or `podman`. The error lists the three.
24. An instance writing `deploy` names a service holding a `docker.yaml` or a
    `deploy/` directory, and its `runtime` is not `host`. A container's
    deployment is what the key is for, and a host process writing one starts
    nothing. For a `docker.yaml` service, `deploy` holds only the keys the
    deployment tool reads: `image`, `restart`, `dir`, `container_name`,
    `hostname`, `account`, `dns`, `volumes`. `rhumb check` names any other.
25. No `deploy` mapping writes a port mapping or a secret. Both are derived or
    live in the rendered configuration, and a second spelling of either is the
    thing the second file exists to remove. The error names the key.
26. An instance's `principal`, when written, names a user whose `default`
    credential opens every route the instance enters, and the instance's
    service declares an `upstream` value that consumes the credential. The
    credential is the one the instance carries, so a person holding the route
    under a different credential, or keeping none called `default`, does not
    satisfy this. The errors name the instance, principal and, for a missing
    grant, route.
27. Each network's `subnet`, when written, is a CIDR prefix, and its `gateway`
    an address inside it. Every literal address a node or host holds on that
    network falls inside the prefix; an address written as a hostname is not
    checked.
28. On one network, no address is held twice and no `mac` is held twice,
    nodes and hosts counted together. A `mac` is six colon-separated octets.
29. Every host names an existing network, and its identifier is not also a
    node identifier.
30. On one network, and on one node's container network, a name resolves to
    one address. The sources are ports' `published` names and hosts'
    `names`; the error names both.
31. Every `dials` value names an existing instance and an existing port on
    it, not the dialling instance itself, and the two ends resolve an address
    as rule 10 requires of an edge.
32. A containerised instance binds `0.0.0.0` or writes no bind, and every
    network in its `containers` is one its node lists. A host process names
    none. A node lists no container network twice; each has a subnet that is
    a CIDR prefix and overlaps no other of the node's, and a gateway, when
    written, inside it. An address fixed on a container network is inside
    its subnet, is not its gateway, and is held by one instance.
33. A route's scope names a network, a node, or a container network of its
    entry's node, and no container network, network or node shares a name
    with another; its entry has an address on that network, runs on that
    node or joins that container network; every credential opening a route
    scoped to a non-universal network reaches it; and no credential opens a
    route scoped to a container network.
34. Every route a set names exists, and every `@<set>` in an access list names
    a set.
35. Every dial a service declares resolves, for each instance of it that does
    not write it, to exactly one instance in the innermost shared scope.
36. A node's id is its file's name without `.yaml`, and every account in a
    node's `accounts` has a uid.
37. A link's `from` names an existing authored instance, and its `to` an
    existing authored instance and port. The two run on different nodes, and
    their services declare `link.from` and `link.to` respectively.
38. A link resolves an address as rule 10 requires of an edge, with `from`
    dialling `to`.
39. No route's hop names a port that a link's `to` names. Several links may
    share one `to` port.
40. An instance ends at most one link whose other end runs on a given node.
41. An edge riding a link resolves at the far end: the link's instance there
    reaches `To` by the same-node rules.
42. An instance's `process` names one of its node's `processes`; every process
    names a service that renders, has at least one member, and does not
    share a name with an instance on its node.
43. A process's members share a runtime, a bind and container networks, and
    their port names and their numbers on each protocol are distinct.

Rules 37 to 41 are explained in [links.md](links.md#validation).

## Boundaries

- **Rule-based routing** is not supported; see
  [One upstream per instance](#one-upstream-per-instance). Static fan-out, where
  each route through a proxy has one upstream, is a different thing and is
  supported.
- **A second name for one port** is not expressed. `published` is one hostname,
  which is what a service that builds absolute URLs out of it can actually have;
  a backend genuinely indifferent to its name cannot be offered under two. A
  list would make which one goes into that configuration a guess, so the case
  waits for something that names the canonical one.
- **Fan-out by path** — `/api` to one backend and `/` to another — is not
  expressed. A port is published at a name, not at a name and a prefix.
- **Network preference is global.** `networks` orders every edge the same way. An
  edge cannot be told to prefer a different network from the one the order picks.
- **Which parties may reach a port** is not expressed, and no check reports it.
  The inventory says an edge resolves and that a granted person can reach a
  route's entrance; it never says who *cannot* arrive. Two things do say it, and
  both are topology rather than a rule: a backend on a network no user's node has
  an address on, and a backend on the proxy's own node bound to loopback. A
  firewall is neither, and it is not modelled here — `validate` passing is not a
  statement that a port is unreachable from anywhere else.
- **Per-credential subsets of a person's access** are not expressed. Every
  credential someone keeps reaches every route they are granted. Scoping one to
  a subset would mean a credential's entry carrying an access list beside its
  note; nothing needs it yet.
- **Deployment** is not here. An export writes files; copying them to a machine
  and restarting the service is a different problem, with different failure
  modes, and putting it behind the same command would make a transfer look like
  a render. A service's [deploy template](export.md#a-second-file-what-deploys-it)
  does not cross this: it renders the file a deployment tool reads, the same way
  `config.json` is the file a server reads, and rhumb runs no container runtime
  and reads nothing back from one.
- **A group of containers started together** — a Compose project, a Pod — is not
  modelled. Each instance renders its own deployment file and stands alone, and
  the three things such a group provides are things this model does not use: its
  internal DNS, because a hop on one node resolves to `127.0.0.1` and never to a
  container name; its shared network, because the instance that dials its
  neighbours is on the host's namespace for that address to mean one thing at
  both ends; and its ordering, because a proxy started before its backend
  answers 502 and then recovers. What a group would buy is starting one
  machine's services with one command, which belongs to whatever reads these
  files. The grouping to add first, if one is ever needed, is the Pod's rather
  than the project's — a set of instances sharing a network namespace, which is
  what the address rule assumes — and the case that calls for it is a second
  instance on one node dialling a neighbour, or two services wanting one number.
- **A separate access-grant entity** is not needed. It would exist to hold
  credentials for clients outside the inventory, and a credential no device
  names answers that case without a new kind of file: it is an account and a
  file, carried by the person.
- **A virtual network** — WireGuard and the like — needs nothing new. The
  tunnel's server is an instance with a port and a grant per peer, and the
  address space it creates is a `network` like any other: peers list an address
  on it, and address resolution picks it over the internet by preference order.
  What is not built is drawing one in the picture, where a network cuts across
  the node and group boxes rather than nesting inside them.

## Milestones

These cover the whole of `rhumb conf`, this page and [`export.md`](export.md)
together, because the inventory changes what the renderer reads and what a
target is. Each is one commit leaving a tree that builds and passes. Every
package below takes plain values and returns plain values, imports no TUI and no
configuration, and has its own tests.

### What the existing packages become

Three packages were built before the inventory existed. They are not preserved
for their own sake; what survives does so because it was never about the layout
that changed.

| Package | What happens to it |
| --- | --- |
| `confgen` | The manifest half stays: discovery, strict unknown-key parsing, broken services listed with a reason. `loadInstances` and the `Instance` and `RoleInstances` types go, because an instance is no longer a file in a service directory, and `Role` goes because a service is one program. |
| `render` | `funcs.go` stays almost whole — the function set is independent of where values come from. `secret` changes meaning, from walking a per-service secrets file to naming one of an instance's own secrets. `Input` becomes the render context. |
| `target` | The path matcher goes entirely. A selector matches fields now, and a target is an instance. What survives is behavioural: a stable sort, and a selector matching nothing being an error rather than an empty result. |

### 0. Convert the existing generator root

Before any code: rewrite the current values files as node files, `users.yaml`
and `routes.yaml`. It is an hour by hand at this size, and a converter would
cost more than it saves for a thing run once.

It comes first because real data is the fastest test of whether this model is
right. A case it cannot express is worth finding before eleven milestones are
built on it.

### 1. Trim `confgen`

The root becomes `services/`. The manifest gains `secret` and `auth`; the
`secrets` key goes. Nothing in the package knows what an instance
is any more.

### 2. `inventory` — reading

`nodes/<group>/*.yaml`, `users.yaml`, `routes.yaml` and `networks.yaml` parsed
into one model. A node's group is its directory, and fills `owner` when the
directory names a user. Parsing only: no derivation, no validation. A file that will not parse is
listed with its error rather than dropped, as a broken service already is.

### 3. `derive` — derivation

Client instances, edges, resolved addresses, grants and per-port principal
tables, as a pure function from the inventory model. It touches no filesystem.

Its tests use the worked example on this page as a fixed input, and pin the
three address cases, both kinds of principal, two ports of one instance
carrying separate tables, and two devices naming one credential being one
principal with one secret between them.

This is the middle of the whole design, and the place where a test is worth
writing before the code.

### 4. `validate`

The [rules above](#validation), one case and one error message each, every
message naming what it read and where it read it.

### 5. `secretstore`

The path layout, reading, `sync`, `mv` and `.previous`. Generated values take
the shape the service declares.

Its tests run over a temporary directory, and the case that matters most is a
rename: equal counts of new and orphaned paths must produce the `secret mv`
hint rather than a silently generated value on one side of a pair.

### 6. Rework `render`

`Input` becomes the render context this page pins, and `secret` changes with it.
The rest of the function set does not move. At the end of this milestone one
target renders end to end.

### 7. Rewrite `target`

A target is an instance. A selector is `<field>:<value>` terms. `--targets`
reports grouped by node. The old matcher is deleted rather than kept working
beside the new one.

### 8. The page

The tree's top level becomes groups, then nodes, with a person who has no
device file appearing as a group holding one entry per credential.
Tri-state selection, folding and preview keep the behaviour they have.

### 9. Export

The output gains its node directory. Render-all-then-publish does not change.

### 10. Rotation

`.previous` in the rendered account tables, and the staleness check in
`validate`.

### 11. A worked example

A complete inventory under `examples/conf/`, loaded by the test that loads every
example. Until this exists, nothing has checked that the configuration on this
page can actually be written.

### 12. Containers: `runtime` and `deploy/`

The `runtime` key, its badge on the graph's process boxes, and the second
rendered file. In order: read and validate `runtime` (rule 23) with no
behaviour behind it; derive each port's host mapping from `ports` and the
resolved edges; read `services/<service>/deploy/` as a second manifest and
render its template per containerised instance, with rules 24 and 25; badge the
process box. The derivation is the part worth testing first — loopback for a
port only its own node enters, this node's literal address for one entered from
elsewhere, `0.0.0.0` when that address is a name — since the rest is the
existing renderer pointed at a second template.

The examples come with the code, not before it: `examples/conf` is loaded by a
test that rejects a key no loader reads, so `runtime`, an instance's `deploy`
and a service's `deploy/` arrive there in this milestone's own commits.

Open before it starts: `mapping "<port>"` sits one letter from
`published "<port>"` in a template, and a better name for one of the two would
be worth having.

### 13. Network ranges, hosts and dials

In order, each standing alone: `networks.yaml` in its list-of-mappings form
with `subnet` and `gateway`, and a node's address as a mapping with `mac`,
with rules 27 and 28; `hosts.yaml` and rule 29; `dials`, the `dial` function
and rule 31; `published` as a list; the `names` function and rule 30; the reservation export. The
first three change no rendered byte. `names` is the first that lets an
inventory delete hand-written records, and the order is chosen so the
records it replaces can be compared against it before they go.

### Two cautions

**Milestone 12 stands alone.** It changes no existing rendered byte and nothing
before it depends on it, so it can be done whenever, or dropped.

**Milestones 2 to 6 are one stretch.** Stopping among them leaves two ideas of
what an instance is in the tree at once. If the work has to be split, 7 and 8
are the ones to defer — the old selector and the old page can sit over an empty
target list for a while; 2 to 6 should not be interrupted.

**Do 0 and 3 first, and their tests before their code.** Real data and a pinned
derivation are the fastest way to find out whether this model is right. If they
are wrong, the other nine milestones are wasted.

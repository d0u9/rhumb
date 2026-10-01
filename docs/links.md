# Configuration: Links

**Status: implemented, with open questions.** Steps 1 to 9 of [the
implementation](#implementation) are in place; step 10, a link in
`examples/conf`, waits for a program to be chosen, since an example renders a
real one. [inventory.md](inventory.md) names the link in its model, its
address table and rules 37 to 41, and points here for the rest. This page goes
when the [open questions](#open-questions) are settled and its prose is folded
in.

## Why it exists

Two arrangements come up that the inventory cannot write.

**An exit nobody can dial.** A home server behind NAT has an address only on
its LAN. It can still be where traffic leaves — a residential address in another
country is the point of it — if it opens a connection out to a relay and the
relay sends traffic back down that connection. xray's reverse proxy, frp and
`ssh -R` all work this way. The inventory refuses it: [the address
rule](inventory.md#choosing-an-address) gives an edge the downstream's address,
the home server has none the relay reaches, and rule 10 fails.

**A stretch that must not look like what it carries.** A relay in Nanjing
forwarding Shadowsocks to San Francisco puts Shadowsocks on the wire across the
border, where it is recognised by its shape. Wrapping that stretch in another
protocol — VLESS with REALITY, TLS over WebSocket — means a second session
between the two machines, with a credential of its own, that the route's traffic
travels inside. The inventory has no place for it: [a
forwarder](inventory.md#a-service-that-forwards) holds no credential, and the
only credential a route knows is the person's, at the hop that terminates it.

The two look different and are one thing: **a connection between two instances
on two nodes, with a session of its own.** Which end opens it, and what it
authenticates with, belong to that connection, not to any route travelling
through it. In the first case it is opened against the direction the traffic
goes; in the second, along it.

The inventory comes close twice and misses both times. A
[dial](inventory.md#dialling-a-service-that-is-not-on-a-route) is an instance
calling another off any route, but it carries no credential, and that section
says outright that a dependency needing one is a route. A route can carry one,
but its first hop is a port something listens on, and the instance that opens
these connections — xray's bridge, frp's client, a tunnel's near end — listens
on nothing for them. The instance itself can be written today: an authored
instance needs no ports. What has no form is the relationship — that it dials
another instance with a credential of its own, and that route traffic travels
inside the session it opens.

## Converging rather than growing

The question here is not how to add two features. It is where each belongs, so
that the next tool of the same kind needs no new key at all. Three answers were
considered first and rejected.

**Attributes on an edge** — a `dial: reverse` flag, a second credential per hop.
Each solves its case. Each also makes the edge the place a per-stretch property
goes, and every tunnel brings its own: a transport, a multiplexer, a
fingerprint, a key. That list has no end, and the route, which today says only
who goes where, would come to describe the wire.

**An edge carried by another route** — `via: <route>`. This closes the model by
recursion: any nesting is expressible, and nothing ever needs adding again. The
price is routes depending on routes, which makes their derivation order matter
and their depth unbounded. And the carrying route would be a route in name only:
a first hop listening for nobody, no person granted it, no export, no scope —
everything a route has except its purpose.

**A reverse tunnel as an address** — the home server's port published at the
relay's address, the way a router forwards a port. That is already [how a
forwarded port is written](inventory.md#reached-and-reaching), and it covers the
first case. It does not cover the second, and the bridge's session has a
credential, which an address cannot hold.

What is left is one new layer, at a fixed depth.

## Three layers

| Layer | Written in | Answers | Holds |
| --- | --- | --- | --- |
| network | `networks.yaml`, a node's `networks` | Where is a node reached? | Addresses |
| link | `links.yaml` | How does one stretch between two nodes travel? | Sessions between two instances |
| route | `routes.yaml` | Who goes where, entering and leaving where? | Chains of hops |

Each layer uses only the one beneath it. A link resolves on networks. A route's
edge resolves on a network, or rides a link. Nothing resolves on a route, and no
link rides another link, so the depth is three and stays three.

Fixed depth is a choice of scope. The deeper arrangements in view each have a
form within it. A tunnel inside WireGuard is a link over a network: WireGuard's
address space [is a network like any other](inventory.md#boundaries). Two
tunnels in a row are two links, joined by a route with a hop on the machine
between them. What fixed depth rules out is a link inside a link — a session
inside a session between the same two machines. This version does not support
that. An arrangement needing it is not thereby invalid, only outside what this
page covers.

## A link

```yaml
# links.yaml
links:
  nce-sea:
    from: cn-nce-tct-linux-01/xray-01
    to: u-node-group-09-01/xray-01:tunnel
  home-nce:
    from: home-server/xray-01
    to: cn-nce-tct-linux-01/xray-01:bridge
```

**`from` dials `to`.** Every edge in the model already obeys that sentence, and
a link gives it no second meaning. `from` is an instance with no port, because
it listens on nothing for this. `to` is a hop: the port that accepts it. The two
ends run on different nodes; two instances on one node already reach each other
without a session in between.

A link is a logical association, not one socket. Its program may open several
connections, pool and multiplex them, and reconnect after a failure; "dials"
says which end opens them, nothing about how many.

**Its address is the usual one.** A link resolves exactly as an edge does, with
`from` dialling `to`: `to`'s node has an address on a network `from` reaches. A
home server behind NAT is a valid `from` because it reaches the universal
network, and that is all NAT traversal amounts to here. [The rule stays
one-directional](inventory.md#choosing-an-address). What is new is only that
traffic may travel against the direction a connection was opened in.

**Its credential is a grant.** Each link derives one grant: the `from` instance,
as itself, on `to`'s port. That is [the principal an instance relaying through
already is](inventory.md#dialling-an-upstream-as-a-principal), filed and rotated
the same way. An instance's `principal` does not apply: it chooses whose
credential a program carries to its route upstream, and a link is a program's
own session, not a person's.

**Nobody is granted a link.** A link has no scope, appears in no `access` list
and produces no export. Its key is its name, which errors and the render context
use.

## An edge that rides a link

Nothing is written on a route for this. An edge rides a link when its `From`
instance ends that link and the link's other end runs on `To`'s node. Every
route edge from an instance ending a link to a node rides it: replacing the
direct path is the reason the link exists.

The link's far end — its instance on `To`'s node — is what actually dials `To`,
and it does so by the same-node rules `resolveEndpoint` already applies, with
the far end as the caller:

| Far end and `To` | Address, and what `To` publishes for this edge |
| --- | --- |
| Containers sharing a network | `To`'s container name; no host mapping |
| The far end on the host network, `To` in a container | Loopback; `To` needs a loopback host mapping |
| The far end in a container sharing no network with `To` | An error, as today: its loopback is not `To`'s |

The far end may be `To` itself. The edge keeps its logical `From` — grants and
route reasoning read it — and records the far end separately as the instance
that dials, since a loopback address means nothing without knowing whose
loopback it is. [Choosing an address](inventory.md#choosing-an-address) gains
one row:

| The two ends | The address used |
| --- | --- |
| The upstream ends a link whose other end is on the downstream's node | The downstream's address as the link's far end dials it, by the same-node rows |

Two routes of the same shape show both cases:

```yaml
# routes.yaml
routes:
  nce-sea-ss:
    hops: [cn-nce-tct-linux-01/xray-01:sea, u-node-group-09-01/ss-01:users]
  nce-home-ss:
    hops: [cn-nce-tct-linux-01/xray-01:home, home-server/ss-01:users]
```

The first rides `nce-sea`, opened by Nanjing towards San Francisco, the way the
traffic goes: Shadowsocks travels inside the tunnel and is unwrapped on arrival.
The second rides `home-nce`, opened by the home server towards Nanjing, against
the traffic: the relay sends it down a connection the home server keeps open.
Neither route says which. The difference is the link's `from`, written once.
**Encapsulation and a reverse connection are one mechanism read in two
directions**, and neither the word "reverse" nor a direction flag appears on a
link or a route.

What a route says is untouched. `xray-01` forwards here, so [what a forwarder
changes](inventory.md#a-service-that-forwards) still holds: the person's grant
is on `ss-01:users`, their file is written for Shadowsocks and dialled at
Nanjing, and `(upstream).exit` names the far server. What the stretch looks like
on the wire is the link's business and reaches no client.

Which end opened the session does not decide which end may start a carried
flow. That is the program's, and differs by mode: ordinary frp publication
accepts flows at frps and delivers them through frpc, and opens nothing in the
other direction, while response bytes still travel back. Whether an edge may
ride a link from either end is therefore a property to validate, not a
consequence of `from dials to`; how a service declares it is [open](#open-questions).
A route may also cross several links, one per edge, with a hop on each machine
where one link ends and the next begins.

**Host ports follow whoever dials.** A link's `to` port is entered from another
node, so it is published on its node's address on the network the link resolved
on, as a route's entrance is. An edge riding a link has the far end as its
caller, so it asks of `To` only what the table above says: a loopback mapping or
nothing, never a public address. That is one caller's need. Publication stays
the union of every caller's, so a direct route to the same port elsewhere in
the inventory may still publish it publicly. In the example, if the home
server's `xray-01` and `ss-01` share a container network and nothing else
reaches `ss-01:users`, it publishes nothing on the home server — right, since
nothing outside can reach it and nothing has to.

The ports carried traffic enters are a route's, not the link's, even when the
link's program asks for them. With frp, a route entering `relay/frps:ss` on
6000 is listened on by frps, but the request to open that listener is written
in frpc's file, as `remotePort` — this is [how frp's TCP publication
works](https://gofrp.org/en/docs/features/tcp-udp/). Port 6000 is still
`relay/frps:ss`'s for host mappings and for rule 14, separate from the link's
control port `relay/frps:control` on 7000.

## What a service declares

```yaml
# services/xray/confgen.yaml
auth: none
forwards: true
downstreams: many
dispatch: port
link:
  from:
    values: {}
  to:
    auth: per-principal
```

**`link.from`** says an instance of this service may be a link's `from`. Its map
is what that end needs from the port it dials, under the names
[`upstream`](inventory.md#what-a-service-needs-from-its-upstream) uses —
`shared` and `values` — and for the same reason: the declaration is the
consumer's. A REALITY tunnel needs the far end's public key and the server name
it imitates. Both are values written on the far instance, and they reach the
near one because the near one asks.

**`link.to`** says an instance may be a link's `to`. Its `auth` takes the same
values as the service's own. With `per-principal`, each link's grant is an
account with a secret, and the receiving end is told which account belongs to
which link. The grant's identity is the `from` instance on the `to` port, not
the link's name, so renaming a link or adding a route over it rotates nothing.
With `none`, the port authenticates nothing per peer, and its grant needs no
secret, as a grant on an `auth: none` port already does. A shared token — frp's
token mode; its OIDC mode is not covered here — is one secret for the whole
server, however many link ports it listens on, so it belongs to the instance as
its `self` and reaches the dialling end through `shared`. A forwarding service
may not declare `self` today, which [is open](#open-questions).

Today, whether a port authenticates is read from the service alone:
`secretstore.ImpliedPaths` and the renderer's `principalsFor` both check the
service's top-level `auth`. On the xray manifest above that is `none`, so a
derived grant alone would make no account secret and no account table for the
link port. Both have to read the port's own declaration, which is a step of
[the implementation](#implementation), not a consequence of deriving the grant.

**A port is a route's or a link's, never both.** The service's own `auth`,
`forwards` and `self` describe the ports routes enter; `link.to` describes the
ports links enter. That is why `forwards: true` and an authenticated link port
sit in one manifest without contradiction: a forwarder reads nothing a route
brings it, and the session a link opens is not route traffic. The rule that a
forwarding service declares no `auth: per-principal` is unchanged; it was always
about the ports a route enters.

**An instance has one route role.** Terminating, forwarding and fanning out stay
properties of the service, not of a port. A program that should terminate on one
port and forward on another is two services, as `sslocal` and `ssserver` are two
services over one program. Ending a link is not a route role, so an instance in
any of the three may end links.

## The render context

These are additions only. An instance ending no link sees none of them.

```yaml
links:       for an instance ending a link, each by name:
             end:     from or to
             peer:    the other end. For the from end: instance, port,
                      number, address, the published name when it has one,
                      the account this instance connects as and its secret,
                      and shared and values as link.from declares. For the
                      to end: instance, and the account it connects as
             carries: one entry per mapping riding the link, the same list
                      at both ends:
                      key:      stable, from entrance and target, for
                                generated names such as an frp proxy's
                      entrance: instance, port, number, protocol, and the
                                name it is dispatched by when dispatch is
                                by name
                      target:   as the far end dials it: instance, port,
                                number, protocol, address
                      routes:   the routes using this mapping, by name
```

A mapping is an entrance and a target, not a route edge. Two routes sharing a
stretch share its mapping, so a template emitting one frp proxy per entry
requests each listener once. Where an entrance dispatches by port, two routes
entering it toward different targets already fail rule 22, so an entrance has
one target. The key comes from the entrance and target rather than position,
so inserting an unrelated route renames no proxy.

Both ends need the entrance. frpc writes `remotePort` from it, though frps is
what listens there:

```toml
serverAddr = "relay.example.com"
serverPort = 7000

[[proxies]]
name = "home-ss"
type = "tcp"
localIP = "127.0.0.1"
localPort = 8388
remotePort = 6000
```

`serverAddr` and `serverPort` are `peer`; each proxy is one `carries` entry,
`remotePort` its entrance number, and `localIP` and `localPort` its target as
frpc dials it. The fragment shows which data goes where; authentication and the
rest of the file are omitted.

An `upstream` or `downstreams` entry for an edge that rides a link gains `link`,
the link's name, and its `address` is the target's as the far end dials it. That
is exactly what a tunnel's near end needs: the destination it writes into the
tunnel is resolved on the other side. Those entries stay per route edge and
belong to `From`; an end such as frpc, which is no route's hop, reads `carries`
instead. The far end reads `carries` when its program pins the targets it
accepts, and may ignore it when it accepts whatever arrives.

## Validation

Proposed rules, numbered after [the existing ones](inventory.md#validation):

37. A link's `from` names an existing authored instance, and its `to` an
    existing authored instance and port. The two run on different nodes, and
    their services declare `link.from` and `link.to` respectively.
38. A link resolves an address as rule 10 requires of an edge, with `from`
    dialling `to`.
39. No route's hop names a port that a link's `to` names. Several links may
    share one `to` port: a relay accepts many bridges. A link's port and the
    route ports its traffic enters are separate listeners, and rule 14
    checks both.
40. An instance ends at most one link whose other end runs on a given node.
    With two, an edge from it to that node would ride one of them chosen by
    nothing. This applies at both ends: one frps cannot accept links from two
    frpc instances on the same home node, and splitting the client side does
    not help, because the ambiguity is the relay's. Nor can one instance reach
    a node directly on some edges and through a link on others. Both are
    restrictions of this version, and the error names the instance, the node
    and the links.
41. An edge riding a link resolves at the far end: the link's instance there
    reaches `To` by the same-node rules, and a far end in a container sharing
    no network with `To` fails, as it would today. This extends rule 10
    rather than replacing it.

Derivation needs no cycle rule. Links resolve on networks only, and a route is
never anything else's path, so no step of deriving the model depends on its own
result. That says nothing about the programs rendered from it: a forwarding
loop between configured programs is still prevented by the existing route rules
and successor check, which apply unchanged, and tests should compose forwarding
configurations rather than rely on the layering.

## Boundaries

**Both ends are this inventory's instances.** A tunnel to a provider's edge —
cloudflared, Tailscale Funnel — has one end that nothing here runs, so it is not
a link. What it gives the node is an address others reach it at, and that is
written in the node's `networks`, as [a forwarded port
is](inventory.md#reached-and-reaching). That address says how the node is
reached; the provider agent's authentication and lifecycle stay outside the
model. Nor does a person's device end a link:
links join instances this inventory runs, and a device's own way to a relay is a
route, as it is today.

**Runtime choice stays out**, as [it already does](inventory.md#boundaries):
failing over between links, balancing across them, picking one per request. A
link is one fixed stretch.

**Protocol stays out.** The model never names VLESS, REALITY, WebSocket or a
multiplexer. Those are a service's values and template. The model carries a
peer's values to the end that declares it needs them, and nothing more.

**The test for a new tool.** Ask these in order. The first yes says where the
tool goes:

1. Does it give a node an address others dial? A **network**, or an address on
   one: WireGuard, Tailscale's tailnet, a router's forwarded port.
2. Does it open a session of its own between two nodes — its own handshake, its
   own credential — that other traffic travels inside? A **link**: xray's tunnel
   and its reverse bridge, frp, `ssh -L` and `-R`, realm's TLS transport
   between two realm instances.
3. Does it move a route's bytes without reading them, opening no session of its
   own? A **forwarding hop**: realm in plain mode, socat.
4. Does it authenticate the person, or is it where the traffic leaves? A
   **terminating hop**: ssserver, Hysteria2, a VLESS server people connect to.
5. Does it decide where traffic goes per request, at runtime? **Outside the
   model**, in the instance's own configuration.

A tool answering none of them gets no key. What it needs goes in its service's
values or its template.

**What stays fixed**, so that this page is the last of its kind:

- A route is `hops` and nothing else, and nothing is ever written on one of its
  edges.
- "From dials to" has one meaning everywhere.
- A principal is a person's credential or an instance itself. There is no
  third kind.
- There are three layers, each using only the one beneath it.
- An instance has one route role.

## Implementation

Each step is one commit that leaves a tree that builds and passes. Where a step
derives anything, its tests come before its code. Whether two complete
fixtures come before step 1 is [open](#open-questions).

1. **`inventory`**: read `links.yaml` with the existing strict decoding and
   broken-file reporting (`Load`, `decodeStrict`). An inventory without the file
   stays valid. Parsing only.
2. **`confgen`**: the `link` key, parsed strictly. `link.from` is checked as
   `upstream` is, and `link.to.auth` as `auth` is. The forwarding restrictions
   on the top-level `auth` stay as they are.
3. **`derive`**: the model gains `Links`, each resolved like an edge, and one
   grant per link under the existing grant identity and `dedupeGrants`. A
   route edge riding a link keeps its logical `From` and records the link and
   the far end as the instance that dials; `Address`, `Network` and
   `Container` become what that instance uses, which until now was always
   `From`, so every consumer of the three is audited. The model also derives
   each link's mappings, deduplicated, with stable keys. Tests pin both routes
   above, a far end that is `To` itself, the three same-node cases, two routes
   sharing one mapping, and two candidate links being an error.
4. **Host mappings**: `derive.Mappings` reads the instance that dials rather
   than `From`. A link's `to` port publishes as an entrance does; an edge
   riding a link asks for loopback or nothing, and publication stays the union
   of every caller's need.
5. **Secrets and accounts**: `secretstore.ImpliedPaths` and `shapeOf`, and the
   renderer's `principalsFor`, read a link port's own `auth` instead of only
   the service's. Tests: a per-principal link port on a forwarding service
   gets account secrets and an account table, and renaming a link changes no
   secret path.
6. **Route selection**: `target.List` associates a route with the ends of the
   links carrying its edges, not only with its hops, which is all
   `routesContaining` reads today. Selecting one route still renders a shared
   instance's whole configuration, with its other mappings. Test: an frpc
   instance that is no route's hop is selected by `route:<name>`.
7. **`validate`**: rules 37 to 41, reusing rules 13, 14 and 22 with the facts
   links add rather than duplicating them. Rule 14's code comment still says
   protocol is not modelled although the check uses `ProtocolOr()`; correct it
   in passing.
8. **Render context**: `links`, and `link` on `upstream` and `downstreams`
   entries. A test pins that an inventory without links renders the same bytes
   as before.
9. **Topology and preview**: a link drawn between two nodes, with an edge
   riding it drawn along it, so the picture does not show a home server being
   dialled from outside. For each riding edge, preview shows the logical edge,
   the link selected, which end opened it, and the instance and address that
   actually dial. Adding a link changes existing edges without changing any
   route, and this is where that becomes visible.
10. **`examples/conf`** gains a link in the same commits, since its test
    rejects a key no loader reads.
11. **Docs**: inventory.md gains **link** in [the model](inventory.md#the-model),
    the row in [choosing an address](inventory.md#choosing-an-address), the
    rules and the boundaries. This page is deleted.

Steps 1, 2 and 7 change no rendered byte. Step 3 is the middle, as derivation
was for the inventory as a whole.

## Open questions

These came out of a review of the first draft against the code and the
programs' documentation. Each needs a decision before the step it affects.

- **Which end may start a carried flow, and which ends pair.** Opening
  direction does not decide it ([above](#an-edge-that-rides-a-link)); the
  program's mode does. xray's forward tunnel accepts flows at `from`, its
  reverse proxy and frp's publication at `to`. Declaring the `link.from` and
  `link.to` roles alone would also accept an frpc dialling a VLESS port.

  *Decided: a mode is a service.* A program with two link modes is two
  services, and one xray in Nanjing acting as a tunnel client and a reverse
  portal is two instances run by [one process](inventory.md#processes). The
  `one-process` fixture variant is that arrangement. *Still to add:* each end
  declaring whether flows may start there and the kind of peer it pairs with,
  compared as an opaque label, and the check that uses them. Until then the
  `agent-originates` variant passes.

  Rejected: a mode on the `to` port, which writes the mode in every node file
  and splits one fact between the port and the manifest; and roles inside one
  instance, which repeat what `process` already says in the other direction
  and would move `values`, `principal`, `bind` and `self` below the instance.
- **What a link can carry.** Two checks, not one. A riding edge's target port
  has a protocol, which the link's program must be able to carry: TCP-only
  carrying cannot reach a UDP port. The link's own `to` port has its protocol
  too, the outer transport, and the two are independent — TCP can travel over
  a UDP-based transport. frp configures its transport, multiplexing and pool
  separately ([client reference](https://gofrp.org/en/docs/reference/client-configures/)).
  A service could list what its links carry; it would be a check, not a
  concept.
- **How many peers one end holds.** An frpc configuration has one
  `serverAddr` and `serverPort`, while the rules above let one `from` instance
  link to several nodes. Either a service declares the limit, as
  `downstreams: many` does for routes, or each link takes its own instance.
- **A shared-token link port on a forwarding service.** frp's token mode — the
  first fixture's — needs the token as frps's `self`, and a forwarding service
  may not declare `self` today. Either that rule narrows to the `self` route
  ports hand out, or `link.to` declares its own. Either way there is one
  source per server, so two link ports on one frps never acquire different
  tokens. xray's link port is per-principal and needs neither.
- **The limits of implicit selection.** Rule 40's restrictions — one relay
  linked twice to a node, a mixture of direct and tunnelled edges to one node —
  are accepted for this version. If a real arrangement needs either, the
  selector is revisited before implementation, without a general routing
  language.
- **Which program modes are claimed.** "Supports frp" means ordinary TCP and
  UDP publication. STCP, SUDP and XTCP add visitors, each with its own
  listener, proxy identity and secret
  ([visitor reference](https://gofrp.org/en/docs/reference/visitor/)), and
  need their own examples first. For xray, the review found the [legacy
  reverse-proxy page](https://xtls.github.io/en/config/reverse.html) marked
  deprecated in favour of [VLESS reverse
  proxy](https://xtls.github.io/en/document/level-2/vless_reverse.html), which
  attaches reverse behaviour to one VLESS client and its outbound; this has not
  been checked against a pinned release. It is the better test of whether the
  receiving end can join a link's account to its routing tag.
- **Fixtures before the schema.** Before step 1, write the expected inventory
  and every expected output by hand for two complete cases with pinned program
  versions — ordinary frp publication, and forward encapsulation into a
  Shadowsocks server — covering both ends' configuration, credentials, the
  exported client address and host mappings. The VLESS reverse arrangement in
  the example is a candidate third, being what prompted this page. The first
  fixture drives the smallest schema that renders it; the questions above stay
  open until a case needs them. The implementation then proves:
  - both ends receive enough to render; two routes sharing a mapping produce
    one listener; several clients share a link port that supports it;
  - an unsupported direction, payload protocol, pairing or peer count fails
    with an error naming the link and the route;
  - the shared-network and loopback cases produce different mappings, and an
    unreachable container pairing stays an error;
  - link credentials and route users' credentials stay distinct, and renaming
    a link rotates nothing;
  - an inventory without links renders the same bytes, and a link being down
    never falls back to an undeclared direct path.

  Rendering tests fix the data contract. Before claiming a program is
  supported, validate the generated files with that program and run a small
  end-to-end forwarding check.
- **Splitting protocol from program.** A process's members have services that
  are often a manifest alone, so the knowledge of a protocol — Shadowsocks'
  secret shape, its `ss://` export — is written again for each program that
  speaks it rather than shared with `ssserver`. Reusing an existing service as
  a member, its template ignored, would avoid the copy but ignores a template
  silently. The lasting answer is two kinds of service, a protocol's and a
  program's, with each manifest key belonging to one. That is a refactor of
  every service, and waits until a program serving several protocols — sing-box
  with several inbounds — needs it.
- **A process's deployment.** A containerised process deploys through its
  program's deploy directory with its members' ports and mappings, but no test
  pins that file yet.
- **A link nothing rides.** It is still a running session, so it is not an
  error. Whether `rhumb check` should mention it is open.
- **The name.** "Tunnel" was the other candidate, but it suggests only the
  encapsulating case, and half of what a link is for opens against the
  traffic.

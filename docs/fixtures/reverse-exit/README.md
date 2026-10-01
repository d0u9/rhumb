# Fixture: reverse exit

**Status: proposal**, part of [links.md](../../links.md). Nothing loads this
directory; `links.yaml` and the `link` manifest key are not implemented.

## The arrangement

A home server behind NAT is where traffic leaves. It has an address on its LAN
only, so nothing outside can dial it. It opens a session to a relay in the
cloud and keeps it open; the relay sends client traffic down that session, and
the home server hands each flow to a local exit.

```text
alice ──dials──▶ nce/relay-nce:home ══ link home-nce ══ home/agent-home ──▶ home/ss-home:users
                 nce/relay-nce:agents ◀──dials── home/agent-home
```

The session is opened against the direction the traffic travels.

## No program is chosen

The services `reverse-relay` and `reverse-agent` stand for any program pair
working this way: xray's reverse proxy or VLESS reverse, frp, `ssh -R`. Their
manifests declare only what the model reads, and there are no templates. The
exit is `ssserver` because the exit is an ordinary route end and its program is
irrelevant here.

So the expected output is the **render context**, not configuration files: the
data any template for such a pair must be able to read. When a program is
chosen, a second fixture adds its templates and real files, and must need
nothing beyond these contexts. If it does, the contract here is wrong.

## Files

| Path | What |
| --- | --- |
| `conf/` | The inventory, in today's layout plus `links.yaml` |
| `expected/model.yaml` | The derived link, the riding edge and both grants |
| `expected/contexts/*.yaml` | Render context additions per instance |
| `expected/mappings.yaml` | Host publication per node |
| `expected/secrets.txt` | Secret paths the inventory implies |
| `expected/exports.txt` | What alice's phone receives |

`<...>` stands for a secret's value, by its path. Fields are those
[links.md](../../links.md#the-render-context) proposes; anything the
implementation names differently changes here first.

## What it pins

- The edge `relay-nce → ss-home` rides `home-nce` without the route saying so.
- Its address is `127.0.0.1`, resolved with `agent-home` as the caller, and the
  logical `From` stays `relay-nce`.
- The link's credential is `agent-home`'s own, on `relay-nce:agents`, with an
  account table there although `reverse-relay` declares `auth: none`.
- Both ends see the same `carries` entry, entrance included.
- `agent-home`, which is no route's hop, gets everything from `links`.
- The home node publishes nothing; the relay publishes both ports.
- alice's export dials the relay and uses her exit secret; nothing about the
  link reaches her.

## Variants

Each directory under `variants/` is a full copy of `conf/` with one change,
marked by a `Changed` or `Added` comment, and an `expected/` of its own. Error
texts are proposed wording; what a test should pin is the rule and the names
it reports.

| Variant | Change | Expected |
| --- | --- | --- |
| `shared-container` | Both home instances in containers on `apps` | `diff.yaml`: address `ss-home`, nothing published on home |
| `no-shared-network` | Agent in a container, exit a host process on loopback | Rule 41 error |
| `second-agent` | A second agent linked to the same relay port | Rule 40 error naming `relay-nce`, `home` and both links |
| `agent-originates` | The agent listens, and a route starts at it toward the relay | Error; pending the open question on which end may start a flow |
| `no-links` | `links.yaml` removed | Rule 10 error, as today |
| `one-process` | nce runs the reverse relay and a forward tunnel to `sea` as two instances of one process | One rendered file; pinned by `engine/process_test.go` |

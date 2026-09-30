# rhumb

Map your machines, services and the routes between them once. rhumb derives
every configuration file from that map, and the files that deploy each
instance where it runs.

A rhumb line is a course of constant bearing, drawn between ports on a
navigator's chart. rhumb works from the same kind of chart: one inventory of
nodes, networks, users and routes, and a template per service. Ports,
addresses, dial targets, credentials and container networks are derived from
the inventory rather than written into each file by hand, so a change in one
place reaches every file that depends on it.

## Install

```sh
go install github.com/d0u9/rhumb/cmd/rhumb@latest
```

## Use

```sh
export RHUMB_ROOT=~/infra          # services/ and the inventory
export RHUMB_SECRETS=~/infra-secrets

rhumb init                         # write the scaffold a root starts from
rhumb check                        # report every problem, change nothing
rhumb secret sync                  # generate the credentials the inventory implies
rhumb targets                      # list what can be rendered, by node
rhumb export '*' --to out/         # render every target into out/
rhumb reservations lan             # DHCP reservations a network's router should hold
```

`--root` and `--secrets` override the two variables. `export` writes one
directory per instance, under the node it runs on. Beside each instance's
files is `manifest.yaml`: the derived values a deployment tool needs, so that
tool never reads the inventory.

`examples/conf/` is a complete root to start from.

## Documentation

- [docs/inventory.md](docs/inventory.md): the model, what is derived from it,
  and every validation rule.
- [docs/export.md](docs/export.md): services, templates, rendering, and what
  an export writes.

## Packages

rhumb is also a library. `engine` loads a root and renders it without
writing anything; `cli` is the command line over it, taking plain
directories, for a program that embeds the commands in its own.

## Status

Rendering and export are in use. Deployment from the manifest is being
built; until then, a service can render its own deployment files beside its
configuration.

## License

MIT

# AGENTS.md

## Public repository — privacy is mandatory

**This repository is public. Anyone can access, copy and retain its contents
and Git history. Never put private, personal, confidential or deployment-specific
data in this repository, even temporarily or in an untracked or ignored file.**

- This applies to code, comments, docs, examples, tests, fixtures, snapshots,
  logs, screenshots, generated files, commit messages and PR descriptions.
- Never include real credentials, tokens, passwords, keys, secrets, personal
  records, account identifiers, local user paths, hostnames, addresses or network
  inventories. Encryption, redaction of only the secret, and `.gitignore` do not
  make real private data suitable for this public repository.
- Use wholly synthetic cases: `alice`, `bob`, `host-a`, `node-1`; domains under
  `example.com`, `example.net`, `example.org` or `.test`; IPs in `192.0.2.0/24`,
  `198.51.100.0/24`, `203.0.113.0/24` or `2001:db8::/32`; invented private
  networks such as `10.0.0.0/24`; invented MACs starting `02:00:00`.
- Keep real configuration, inputs, secrets and outputs outside the repository.
  Reproduce bugs with invented data; implement generic capabilities here and
  keep deployment-specific definitions in private configuration.
- Check changes and generated artifacts for private data before staging or
  sharing. Never bypass privacy hooks (`--no-verify`) or disguise rejected values.
- If real data seems necessary, stop and ask the maintainer for a synthetic
  alternative. If private data is found, stop propagating it and report without
  quoting it. Deleting it in a later commit does not undo disclosure.

## Project and boundaries

- Go library and `cmd/rhumb` CLI. `engine` loads and renders without writing;
  `cli` exposes commands with explicit directories for embedding in other tools.
- The inventory is the source of truth; derive addresses, ports, routes,
  principals and deployment values instead of duplicating them in templates.
  Keep service-specific behavior in declarations/templates, not hard-coded
  deployment names or addresses.
- Read [inventory](docs/inventory.md), [links](docs/links.md) and
  [export](docs/export.md) before changing their respective contracts. Update
  docs only for confirmed decisions, and keep examples synthetic and working.
- Preserve package boundaries among inventory, topology, validation, derivation,
  rendering, engine, secret storage, publication, deployment and CLI. Reuse
  existing computations rather than copying them into callers.

## Configuration, secrets and output

- Resolve roots and secret directories through the existing explicit settings
  (`--root`/`RHUMB_ROOT`, `--secrets`/`RHUMB_SECRETS`); do not assume a real
  deployment or read private configuration to construct repository fixtures.
- Secrets stay separate from inventory and templates. Treat rendered configs,
  manifests, archives, share links and QR codes as potentially sensitive;
  keep real outputs outside the repository and secrets out of diagnostics.
- Preserve the separation of checking/rendering, writing exports and executing
  deployment operations. Do not deploy, sync real secrets or run exported
  scripts as a side effect of testing or checking.
- Respect existing publication and secret-storage safeguards. Do not weaken
  validation or overwrite private inputs to make an operation succeed.

## Validation

- Keep changes scoped; add focused synthetic tests for changed behavior.
  Update examples and their load/validation tests when formats change.
- Run relevant package tests; use `go test ./...` for repository-wide validation.
  No real networks, credentials or deployments in tests or fixtures.

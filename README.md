# gloop

An MCP server and terminal interface for Gumroad, built on top of the official
[`gumroad` CLI](https://github.com/antiwork/gumroad).

```
gloop mcp     serve Gumroad to an AI agent, with a policy deciding what it may run
gloop         browse the account interactively
```

For everyday commands, use `gumroad` directly — it covers more of the API than
gloop reasonably could, and gloop runs it under the hood.

## Why this exists

Gumroad's CLI ships an excellent agent skill (`gumroad skill`, ~700 lines). It
tells the model, among other things:

> For operations that can prompt for confirmation (delete, refund, …), add
> `--yes` to skip confirmation.

That is advice. A model can read it and issue a refund. gloop's answer is to
**withhold the command**: irreversible operations are not in the tool list at
all unless the operator opts in, and even then each call needs `confirm: true`.
Refusal is structural, and a model cannot instruct its way past it.

That is the whole product. Everything else is Gumroad's.

## Install

```sh
brew install antiwork/cli/gumroad     # gloop drives this
go install github.com/gshepptech/gloop/cmd/gloop@latest
gumroad auth login                    # gloop shares the session
gloop doctor                          # verify
```

## MCP

```sh
claude mcp add gumroad -- gloop mcp
```

or in any MCP host config:

```json
{"mcpServers": {"gumroad": {"command": "gloop", "args": ["mcp"]}}}
```

### Tiers

Every command in the official CLI is classified. The counts below come from
`gloop doctor`.

| Tier | Meaning | Count | Default |
|---|---|---:|---|
| read | cannot change anything | 43 | on |
| write | undoable from the dashboard | 34 | on |
| danger | irreversible, moves money, or reaches a customer | 22 | **off** |
| never proxied | `admin`, `auth token`, `auth login/logout`, `skill` | 6 | — |

```sh
gloop mcp                 # read + write
gloop mcp --read-only     # nothing can change the account
gloop mcp --allow-danger  # adds refunds, deletes, audience sends
```

The allowlist is strict: a command absent from it is refused. When Gumroad ships
a new subcommand it stays unreachable until someone classifies it — a test
(`TestPolicyCoversTheInstalledCLI`) fails against the installed CLI to say so.

### Tools

| Tool | Purpose |
|---|---|
| `gumroad_run` | Run a command, as an argv array. The main tool. |
| `gumroad_skill` | Gumroad's own reference for every command. |
| `gumroad_help` | A command's `--help`, for exact flags. |
| `gumroad_commands` | What this server will run, with tiers. |
| `gumroad_reviews` | Product reviews — no CLI command exists. |
| `gumroad_earnings` | Tax Center annual earnings — no CLI command exists. |
| `gumroad_tax_forms` | 1099s — no CLI command exists. |
| `gumroad_public_product` / `_profile` | Any seller's public data, unauthenticated. |

`gloop` adds `--json --no-input --quiet` to every call, and `--yes` only after
the tier gate passes. The caller is refused if it tries to pass those itself.

## Terminal interface

```sh
gloop
```

Tabs across Products, Sales, Subscribers, Emails and Payouts. `↑↓` to move,
`enter` to open a record, `tab` for the next section, `r` to refresh, `s` on a
product to see its subscribers, `q` to quit. Keys are on screen at all times.

It reads through the same CLI, so it shows exactly what `gumroad ... --json`
reports.

## What gloop does not do

No product CRUD, file uploads, offer codes, or emails of its own — that is
`gumroad`'s job, and gloop only decides whether an agent may ask for it.

## Development

```sh
make check    # fmt, vet, test
make build    # ./bin/gloop
```

```
cmd/gloop/          entrypoint
internal/gum/       subprocess runner + the tier policy   ← the product
internal/mcpsrv/    MCP tools and the confirmation gate
internal/tui/       bubbletea interface
internal/gumroad/   the four endpoints the CLI lacks
internal/cli/       mcp / tui / doctor commands
```

The policy in `internal/gum/policy.go` is the file worth reviewing: it is the
single place deciding what an agent can reach.

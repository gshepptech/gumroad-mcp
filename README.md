# gloop

An MCP server that lets an AI agent run a Gumroad store, and refuses the commands that would wreck it.

It wraps the official [`gumroad` CLI](https://github.com/antiwork/gumroad-cli) rather than reimplementing the API, classifies all 99 of its commands into risk tiers, and withholds the irreversible ones unless you opt in.

```
gloop mcp     serve Gumroad to an agent, gated
gloop         browse the account in the terminal
```

## Why

Gumroad's CLI ships a good agent skill (`gumroad skill`, ~700 lines). Among its instructions:

> For operations that can prompt for confirmation (delete, refund, …), add `--yes` to skip confirmation.

That is advice. A model can read it and issue a refund.

gloop takes the other approach: the tool is **not in the list**. An agent cannot argue its way past a command it was never offered. Where a skill file asks for good behaviour, gloop removes the option.

## Tiers

| Tier | Meaning | Count | Default |
|---|---|---:|---|
| read | cannot change anything | 43 | on |
| write | undoable from the Gumroad dashboard | 31 | on |
| danger | irreversible, moves money, or reaches a customer | 25 | **off** |
| never proxied | `admin`, `auth token`, `auth login`/`logout`, `skill` | 6 | — |

```sh
gloop mcp                 # read + write
gloop mcp --read-only     # nothing can change the account
gloop mcp --allow-danger  # adds refunds, deletes, audience sends
```

Enabling the danger tier is not enough on its own: each call also needs `confirm: true`, so a model has to have been told to do that exact thing.

Run `gloop doctor` to print the live counts.

### The unit of risk is the flag, not the command

`products update --name "New name"` is an ordinary edit. `products update --file ./new.zip` rewrites the product's content page to embed only the new upload, orphaning the file buyers already download — with no version history. Same command, completely different blast radius.

So commands can name flags that promote them:

```go
{
    Path: []string{"products", "update"}, Tier: Write,
    DangerFlags: []string{"--file", "--custom-html"},
    DangerFlagWhy: "--file rewrites the content page to embed ONLY the new upload, " +
        "orphaning files buyers already download (antiwork/gumroad-cli#205) …",
}
```

That behaviour is a real defect in the upstream CLI, found while building this and reported as [antiwork/gumroad-cli#205](https://github.com/antiwork/gumroad-cli/issues/205).

### Previews are always allowed

`--dry-run` prints the request the CLI would send and stops. Nothing reaches Gumroad, so gloop treats it as a read under every posture — including `--read-only`, and including commands it would otherwise refuse. Refusing the preview would only push a caller towards running the real thing blind.

### The allowlist is strict

A command missing from the policy is refused. When Gumroad ships a new subcommand it stays unreachable until someone classifies it, and `TestPolicyCoversTheInstalledCLI` fails against the installed binary to say so. That test caught an entire missing command group during development.

## Install

```sh
brew install antiwork/cli/gumroad     # gloop drives this
go install github.com/gshepptech/gloop/cmd/gloop@latest
gumroad auth login                    # gloop shares the session; it stores no credential
gloop doctor
```

## Use it

```sh
claude mcp add gumroad -- gloop mcp
```

```json
{"mcpServers": {"gumroad": {"command": "gloop", "args": ["mcp"]}}}
```

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

gloop appends `--json --no-input --quiet` to every call, and `--yes` only once the gate has passed. A caller that tries to pass those itself is refused.

## Terminal interface

```sh
gloop
```

Read-only tabs across Products, Sales, Subscribers, Emails and Payouts, over the same CLI. `↑↓` move, `enter` opens a record, `tab` switches section, `s` on a product shows its subscribers, `q` quits.

## What gloop does not do

No product CRUD, uploads, offer codes or emails of its own — that is `gumroad`'s job, and it does it better than a second client would. gloop only decides whether an agent may ask.

## Layout

```
internal/gum/       the runner and the tier policy   ← the actual product
internal/mcpsrv/    MCP tools and the confirmation gate
internal/tui/       bubbletea interface
internal/gumroad/   the four endpoints the CLI lacks
internal/cli/       mcp / tui / doctor
```

[`internal/gum/policy.go`](internal/gum/policy.go) is the file worth reading. It is the single place deciding what an agent can reach, and every irreversible classification carries a written reason that the model is shown when refused.

```sh
make check   # fmt, vet, test
```

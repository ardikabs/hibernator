# CLI Command Reference Doc Generator — Design

- Date: 2026-11-09
- Status: approved (design)
- Component: `hack/tools/gen-cli-docs`, `website/docs/user-guides/cli.md`, `Makefile`
- Related: `cmd/kubectl-hibernator/cli/root.go`, `cmd/kubectl-hibernator/cli/exception/*`, existing generators `gen-executor-params-docs`, `gen-sink-docs`, `crd-ref-docs`

## Problem

`website/docs/user-guides/cli.md` (638 lines) is hand-maintained. The recently added
`exception` subcommand group (`cmd/kubectl-hibernator/cli/exception/` —
`create`/`list`/`describe`/`delete`) never made it into the docs site.
Every new CLI subcommand risks the same silent drift because nothing binds
`root.go` registration to documentation output.

## Goal

A generator at `hack/tools/gen-cli-docs` that derives the CLI command reference
from the live cobra tree, writes `website/docs/user-guides/cli.md`, and is
enforced via `make generate` + `make verify` so a missing doc update fails
before merge. New subcommands follow automatically.

Non-goals: multi-file `cobra/doc.GenMarkdownTree` output, mkdocs nav
restructuring, narrative user-journey content, versioned docs.

## Decisions (user-confirmed)

1. Output replaces `website/docs/user-guides/cli.md` in place (single page,
   current style preserved); no new nav entries.
2. Handwritten prose lives in cobra annotations (`Long`/`Example`) in Go code —
   single source of truth, no sidecar markdown snippets.
3. Enforcement via `make generate` + `make verify` dirty check (no separate CI
   workflow).
4. Single-page custom renderer (not upstream `GenMarkdownTree`).

## Architecture

One Go program, `hack/tools/gen-cli-docs/main.go`, following the established
`go run ./hack/tools/...` pattern:

```
main() → findModuleRoot() (walk up to go.mod)
       → cli.NewRootCommand() (import, no AST parsing)
       → recursive Visit(cmd, depth)
       → render buffer (preamble const + per-command sections)
       → MkdirAll + WriteFile(website/docs/user-guides/cli.md, 0644)
```

If a command is registered in `root.go`, it appears in the output. The mkdocs
nav path is unchanged, so `website/mkdocs.yml` needs no edit.

Rejected alternatives:

- AST source scanner (à la `gen-executor-params-docs`): brittle for
  `StringVarP`/`MarkFlagRequired`/custom validators; duplicates cobra's model.
- Thin wrapper over `cobra/doc.GenMarkdownTree`: multi-file churn, new nav
  entries per command, style mismatch with the current single-page guide with
  installation preamble and grouped `restore`/`notification` sections.

## Components

### 1. Tree walker

Recursive `Visit(cmd *cobra.Command, depth int)`:

- Depth 0 (root): skipped as a section; only persistent/global flags harvested
  (`--kubeconfig`, `-n/--namespace`, `--json`).
- Depth 1: `list`, `describe`, `preview`, `suspend`, `resume`, `retry`,
  `override`, `restart`, `restore`, `revert`, `exception`, `notification`,
  `logs`, `version` — emitted as `### <full-path>`.
- Depth 2+: e.g. `exception create`, `restore list`, `notification send` —
  emitted as `#### <full-path>` (deeper levels get one more `#` each).
- Traversal order = `root.go` registration order (stable, matches source
  reading order); flags within a table sorted lexicographically for stability.
- Hidden/deprecated commands (`cmd.Hidden`) are skipped.

### 2. Section renderer

Per command, in order:

1. Heading with full command path (`kubectl hibernator exception create`).
2. `Short` as the one-line intro (required — missing `Short` is a hard error).
3. `Aliases:` line when `Aliases` is non-empty (e.g. `list` → `ls`,
   `notification` → `notif`).
4. `Long` verbatim. This is the templating basis for handwritten sentences:
   authors may use plain markdown, tables, and material admonitions
   (`!!! warning`, `!!! tip`, `!!! note`) — the renderer passes `Long`
   through unescaped.
5. `Example` fenced as ` ```bash ` (cobra convention, one example per line
   group preserved).
6. Flag table `| Flag | Description |` from `cmd.LocalFlags()`:
   - Leading column renders shorthand + long form (``-l, --selector``),
     plus `(required)` marker resolved via `cmd.Annotations` /
     `MarkFlagRequired` lookups.
   - Default values appended as `(default: X)` when non-zero.
   - Inherited persistent flags are NOT repeated per command; they appear
     once in the preamble global-flags table.

### 3. Preamble const

Frozen header in the generator preserving today's `cli.md` lines 1–100 shape:

- `# CLI User Guide` title + intro paragraph.
- Installation section (one-line installer + manual per-platform blocks).
- Global flags table (auto-rendered from root persistent flags, so a new
  global flag also follows automatically).

### 4. Makefile wiring

```make
docs-cli-gen: ## Generate CLI command reference from cobra tree.
	@$(GOCMD) run ./hack/tools/gen-cli-docs/
```

- `generate:` gains `docs-cli-gen` alongside `docs-api-gen`,
  `docs-executor-params-gen`, `docs-sink-gen`.
- `verify` needs no change: its `git status --porcelain` dirty check already
  fails when generated output is stale.

### 5. Cobra backfill (part of implementation)

Current commands carry `Short` but mostly no `Long`/`Example` (verified:
`exception create` and `exception list` define only `Short`). A thin generator
over today's tree would emit thin sections. Implementation therefore includes
adding `Long` + `Example` to all 14 depth-1 groups and ~20 leaf commands,
porting prose from today's `cli.md` into Go, and writing full `Long`/`Example`
for `exception create/list/describe/delete` for the first time.

## Data flow

Deterministic and side-effect free apart from the single file write:

- No timestamps, no random ordering, no environment-dependent content.
- Commands in registration order; flags sorted; output byte-identical across
  runs (idempotency requirement).
- Console prints `Generated website/docs/user-guides/cli.md (N bytes)`,
  matching the existing generators' UX.

## Error handling

- Missing `Use` or `Short`: hard fail (non-zero exit, message to stderr).
  A command without identity or summary is a code bug, not a docs gap.
- Missing `Long` or `Example`: warning to stderr, generation continues. New
  skeleton commands show up thin but never block `make generate`.
- `findModuleRoot` failure (no `go.mod` in ancestors): hard fail, same
  message convention as `gen-executor-params-docs`.
- Output dir creation / write failure: hard fail with path in message.

## Testing

- Unit/golden test (`hack/tools/gen-cli-docs/main_test.go`): render to temp
  dir, assert `exception create/list/describe/delete` sections exist,
  `--type` on `exception create` marked required, aliases (`ls`, `notif`)
  present, global flags appear exactly once. This test is the executable form
  of "never be missed again".
- Idempotency check: two consecutive runs produce byte-identical output.
- Manual verification: `make generate && git status --porcelain` clean after
  backfill; `make docs-build` still succeeds.

## Success criteria

1. `make generate` regenerates `website/docs/user-guides/cli.md` including the
   full `exception` group.
2. Adding a 15th group to `root.go` with `Short`/`Long`/`Example` appears in
   `cli.md` on the next `make generate` with no other file touched.
3. Forgetting to regenerate fails `make verify` (merge blocker).
4. `cli.md` retains installation preamble, global flags, admonitions, and
   per-command examples with no content loss vs. today.

## Risks

- Doc quality now depends on cobra field discipline. Mitigation: hard error
  on missing `Short`, stderr warnings on missing `Long`/`Example`, plus the
  golden test.
- `Long` passthrough means malformed markdown in Go breaks the site build.
  Mitigation: `make docs-build` smoke check; keep `Long` to standard
  markdown + material admonitions only.
- Large initial diff when replacing hand-written `cli.md`. Mitigation:
  backfill ports prose verbatim first; stylistic cleanup only afterwards.

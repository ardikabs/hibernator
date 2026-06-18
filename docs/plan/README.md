# docs/plan/ - Deprecated

> **Notice**: This directory is deprecated and preserved for historical reference only.
> All new planning work uses the **Superpowers + Beads** workflow described in `AGENTS.md`.

---

## What Changed

| Before | After |
|--------|-------|
| Plans stored in `docs/plan/` | Design specs → `docs/superpowers/specs/` |
| Manual task tracking | Implementation plans → `docs/superpowers/plans/` |
| Ad-hoc workflow | Task tracking → Beads (`bd create`, `bd ready`, `bd close`) |

## Why the Change

1. **Superpowers** provides structured brainstorming and planning skills with clear gates
2. **Beads** provides persistent task tracking across sessions with dependency management
3. **Separation of concerns**: Strategy (Superpowers) vs Execution (Beads)

## New Workflow Examples

### Feature Development

```
1. Invoke brainstorming skill
   → Design spec saved to docs/superpowers/specs/YYYY-MM-DD-<topic>-design.md

2. Invoke writing-plans skill
   → Implementation plan saved to docs/superpowers/plans/YYYY-MM-DD-<feature>.md

3. Translate approved plan → Beads epic + child tasks
   → bd create --title="..." --type=epic
   → bd create --title="..." --type=task (multiple granular tasks)
   → bd dep add <task> <epic> --type blocks

4. Execute via Beads workflow
   → bd ready → bd update <id> --claim → bd close <id>
```

### Findings / Bug Investigation

```
1. Document findings in docs/findings/
   → Use TEMPLATE.md format
   → NOT tracked as Beads tasks (investigation only)

2. If actionable work emerges:
   → Create Beads issue with discovered-from dependency
   → bd create --title="Fix: ..." --type=bug
   → bd dep add <fix-issue> <finding-issue> --type discovered-from

3. If fix requires design work:
   → Use brainstorming skill for the solution
   → Follow feature development workflow from there
```

## Historical Files

The files in this directory are preserved for reference:

- `async-phase-driven-reconciler.md` - Async reconciler design
- `notification-flow.md` - Notification system design
- `0007-mvp-checklist.md` - MVP implementation checklist
- ... (see full list in directory)

**Do not add new files here.** Use `docs/superpowers/specs/` for new designs and `docs/superpowers/plans/` for new implementation plans.

---

For the current workflow, see `AGENTS.md` in the project root.

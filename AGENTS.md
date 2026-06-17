# Hibernator Operator - AI Agent Instructions

**Superpowers** = Strategy Layer (design, architecture, planning)
**Beads** = System of Record (task tracking, persistent memories, execution state)

---

## Critical Rules

1. **Verify Before Acting**: Never make assumptions. If unclear, verify. Only start work when goals/objectives are clearly defined.
2. **Git**: NEVER auto-commit. All commits require explicit user request.
3. **E2E tests**: NEVER run automatically (`test/e2e/...`). Always ask first.
4. **Build**: All Go binaries to `bin/`. Use `make build` or `go build -o bin/{name}`.

---

## Workflow Architecture

### Layer 1: Superpowers (Strategy)

| Skill | Purpose | Output |
|-------|---------|--------|
| `brainstorming` | Design phase - understand what to build | `docs/superpowers/specs/YYYY-MM-DD-<topic>-design.md` |
| `writing-plans` | Planning phase - define how to build it | `docs/superpowers/plans/YYYY-MM-DD-<feature>.md` |

**Rules:**
- Always use `brainstorming` first for architectural decisions
- Always use `writing-plans` after design approval
- Source of truth: `docs/superpowers/specs/` (designs), `docs/superpowers/plans/` (plans)
- **Execution boundary**: Plans in `docs/superpowers/plans/` are for planning ONLY, NOT active task tracking

### Layer 2: Beads (Execution)

| Command | Purpose |
|---------|---------|
| `bd ready` | Find available work |
| `bd show <id>` | View issue details |
| `bd update <id> --claim` | Claim work |
| `bd close <id>` | Complete work |
| `bd prime` | Get workflow context + persistent memories |

**Rules:**
- Every major feature/RFC = `type=epic`
- Break plans into granular (2-5 min) `type=task` items
- Use `bd dep add` to enforce execution order from Superpowers plan

### The Bridge (Design → Execution)

```
1. Run brainstorming skill
   → Design spec saved to docs/superpowers/specs/

2. Run writing-plans skill
   → Implementation plan saved to docs/superpowers/plans/

3. Translate approved plan → Beads epic + child tasks
   → bd create --title="..." --type=epic
   → bd create --title="..." --type=task (granular items)
   → bd dep add <task> <epic> --type blocks

4. Execute via Beads workflow
   → bd ready → bd update <id> --claim → bd close <id>
```

---

## Two Workflows: Feature Development vs Findings

### Feature Development (Superpowers + Beads)

**Use for:** New features, RFCs, architectural changes, planned improvements

**Workflow:**
```
brainstorming → writing-plans → Beads epic/tasks → execution
```

**Output:**
- `docs/superpowers/specs/` - Design specs
- `docs/superpowers/plans/` - Implementation plans
- `docs/proposals/` - RFCs (human-maintained)

### Findings (Investigative)

**Use for:** Bug reports, feedback analysis, reproducing issues, root cause investigation

**Workflow:**
```
Document findings → If actionable, create Beads issue → If design needed, use brainstorming
```

**Output:**
- `docs/findings/` - Investigation results (NOT tracked as Beads tasks)

**When findings reveal actionable work:**
1. Create findings doc in `docs/findings/` using TEMPLATE.md
2. Create Beads issue with `discovered-from` dependency
   ```bash
   bd create --title="Fix: ..." --type=bug
   bd dep add <fix-issue> <finding-issue> --type discovered-from
   ```
3. If fix requires design work, use `brainstorming` skill for the solution

**Key distinction:** Findings are for investigation and documentation. Feature development is for planned implementation work.

---

## Quick Start

### Beads Workflow (Execution Layer)

```bash
bd ready              # Find available work
bd show <id>          # View issue details
bd update <id> --claim  # Claim work
bd close <id>         # Complete work
bd prime              # Get workflow context + persistent memories
```

### Superpowers Workflow (Strategy Layer)

```
brainstorming skill   # Design phase - understand what to build
writing-plans skill   # Planning phase - define how to build it
```

---

## Project Overview

**Hibernator Operator** is a Kubernetes-native operator that manages time-based hibernation and wakeup of cloud infrastructure resources. It orchestrates coordinated shutdown and restoration of heterogeneous resources (EKS, RDS, EC2, Karpenter) based on user-defined schedules.

## Terminology

- **`HibernatePlan`**: Primary CRD (NOT "Hibernator"). Defines schedule, targets, execution strategy.
- **`CloudProvider`**: CRD for cloud credentials (IRSA preferred, static fallback)
- **`K8SCluster`**: CRD for Kubernetes cluster access configuration
- **Executor**: Component implementing Shutdown/WakeUp/Validate for a resource type
- **Runner**: Isolated K8s Job invoking an executor for a single target
- **RestoreManager**: Manages restore state persistence in ConfigMaps
- **RestoreData**: JSON-encoded metadata captured during shutdown for wakeup

## Architecture

**Core (Brain)**: Evaluates schedules, manages lifecycle, dispatches to executors.
**Executors (Hands)**: Own implementation. Core never knows "how" to shutdown—only "what intent" to apply.

Lifecycle: `Active → Hibernating → Hibernated → WakingUp`
Situational: `Pending`, `Suspended`, and `Error`

---

## Documentation Output

| Type | Location |
|------|----------|
| Design specs | `docs/superpowers/specs/` |
| Implementation plans | `docs/superpowers/plans/` |
| RFCs | `docs/proposals/` |
| Findings | `docs/findings/` |
| Historical plans | `docs/plan/` (deprecated, reference only) |

## RFC Registry

RFCs are maintained in `docs/proposals/` for human reference.

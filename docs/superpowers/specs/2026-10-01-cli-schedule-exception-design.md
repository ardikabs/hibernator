# ScheduleException CLI Design

## Goal

Add a typed `kubectl hibernator exception` command group for creating, listing,
inspecting, and deleting `ScheduleException` resources. The commands should feel
like kubectl, support exact-plan and Kubernetes label-selector targeting, and
make bulk operations explicit and safe.

## Command Surface

```text
kubectl hibernator exception create <prefix> (--plan <name> | -l <selector>) \
  --type suspend|extend|replace \
  --from <date> --until <date> \
  --window-start HH:MM --window-end HH:MM \
  [--days MON,TUE,...]

kubectl hibernator exception list|ls [-A] [-l <exception-selector>] [--plan <name>] [--type suspend,extend,replace]
kubectl hibernator exception describe [names...] [-A] [-l <exception-selector>] \
  [--plan <name>] [--state Active,Pending,Expired,Detached]
kubectl hibernator exception delete [names...] [-l <exception-selector>] \
  [--plan <name>] [--yes]
```

The root command registers the new `exception` command group. All subcommands
use the existing namespace, kubeconfig, and JSON-output root options.

## Create

`create` requires one positional exception-name prefix and exactly one plan
selection mode:

- `--plan <name>` selects one exact `HibernatePlan` in the resolved namespace.
- `--selector/-l <selector>` selects plans in the resolved namespace using
  Kubernetes selector syntax, including equality, inequality, set-based,
  existence, and non-existence requirements.

Selecting no plans is an error. Each selected plan receives one independent
`ScheduleException` whose `spec.planRef.name` references that plan.

The supplied prefix must be DNS-1123 compatible. Generated names have the form
`<compacted-prefix>-<suffix>`, where the suffix is eight random lowercase
alphanumeric characters. The prefix base is limited to 54 characters so the
full name stays within Kubernetes' 63-character limit. When compaction is
needed, vowels are removed from right to left first, then rightmost remaining
characters are removed. Separator cleanup ensures the compacted prefix remains
DNS-1123 compatible.

For selector-based creation, label keys referenced by the selector are copied
from each matching plan when present. Protected Hibernator-owned labels are not
copied. Every generated exception also receives the canonical
`hibernator.ardikabs.com/plan` label for its referenced plan. Requirements based
on label non-existence do not add a label.

Provenance is stamped at creation: `--purpose` records free text in the
`hibernator.ardikabs.com/purpose` annotation (omitted when empty), and
`--managed-by` (default `hibernator-cli`, empty omits) sets the standard
`app.kubernetes.io/managed-by` label, overwriting any plan-copied value — the
exception's manager is whoever runs the command. Annotations are visible in
JSON output and dry-run previews (table and YAML).

`--type`, `--from`, `--until`, `--window-start`, and `--window-end` are required.
`--days` is optional and accepts comma-separated, case-insensitive day
abbreviations or full day names, plus `START-END` ranges that expand in week
order (`MON-FRI`, wrapping `FRI-MON`, same-day `MON-MON` meaning the full week).
An omitted or empty value expands to all seven days. Days are stored using the
API's canonical `MON` through `SUN` abbreviations.

`--from` and `--until` use the existing `timeparse` natural-language parser.
Inputs are interpreted relative to the user's local clock and converted to UTC
before being assigned to `spec.validFrom` and `spec.validUntil`. Both values
must resolve to future times, and `validFrom` must precede `validUntil`.

The single `--window-start`/`--window-end` pair creates exactly one
`OffHourWindow`. Both use strict 24-hour `HH:MM` syntax. Window values are not
converted by the CLI; the controller evaluates them in each referenced plan's
schedule timezone, matching existing ScheduleException behavior.

Bulk creation attempts every selected plan. It prints a result row containing
the plan, generated exception name, result, and message. Successful creations
remain when another creation fails. Any failure yields a non-zero command exit
and recommends retrying failed plans with `--plan`.

## List

`list` and `ls` provide compact kubectl-get-style output. They support the
resolved namespace, `--all-namespaces/-A`, Kubernetes exception-label filtering
through `--selector/-l`, exact referenced-plan filtering through `--plan`, and
exception-type filtering through `--type` (comma-separated, ORed).
All filter categories use AND semantics.

The table columns are `NAME`, `NAMESPACE` when all namespaces are requested,
`PLAN`, `TYPE`, `STATE`, `VALID FROM`, `VALID UNTIL`, and `AGE`. Global `--json`
prints the filtered typed resources in a stable JSON representation.

## Describe

`describe` is the operational-detail view. It accepts zero or more exact exception
names, or filtered collection mode using `--selector/-l`, `--plan`, and
`--state`. Exact names cannot be mixed with collection filters. State values are
case-insensitive on input and normalized to `Active`, `Pending`, `Expired`, or
`Detached`; comma-separated values are ORed. Different filter categories use
AND semantics.

For each exception, describe reports its name, namespace, referenced plan and
current plan phase, type, exception state, valid interval, starts-in or
remaining/elapsed duration, the one schedule window and days, lifecycle
timestamps, and controller message. A missing referenced plan is displayed as
detached/unavailable rather than failing the complete collection view. Global
`--json` returns structured status records containing the same derived fields.

## Delete

`delete` supports either one or more positional ScheduleException names or
collection filters. Positional names cannot be mixed with `--selector/-l` or
`--plan`. Filter mode requires at least one filter; this prevents an accidental
unqualified namespace-wide deletion. Label and plan filters combine with AND
semantics.

Before deletion, the command lists matched exception names and asks
`Delete N ScheduleExceptions? (y/N)`. Only an explicit case-insensitive `y` or
`yes` proceeds. `--yes` skips confirmation for automation. No matches is an
error. Deletion attempts every selected exception, retains successful deletes
on partial failure, prints per-resource results, and returns non-zero if any
delete fails. API finalizers remain authoritative and may delay physical
deletion while a plan cycle is in progress.

## Internal Structure

The implementation uses a dedicated
`cmd/kubectl-hibernator/cli/exception` package with small Cobra constructors for
the group and four subcommands. Shared helpers cover selector parsing, plan and
exception selection, prefix compaction and random suffix generation, day and
state normalization, and confirmation input. Typed controller-runtime clients
and `v1alpha1` resources are used throughout.

Printer DTOs and console/JSON printer cases are added under the existing
`cmd/kubectl-hibernator/printers` package. The command layer performs retrieval,
validation, and derived status calculation; printers only format supplied data.

## Error Handling

Argument and flag validation occurs before creating a Kubernetes client where
possible. Errors identify the invalid flag or resource. Bulk operations collect
per-resource failures and return a summary error after processing all targets.
Random-name collisions are retried with a new suffix a small bounded number of
times; other API errors are reported immediately for that target.

## Testing

Unit tests use the existing controller-runtime fake-client injection pattern.
Coverage includes:

- Cobra argument and mutually-exclusive selector validation.
- Kubernetes match-label and match-expression selection.
- Exact `--plan` selection and zero-match errors.
- Prefix compaction, DNS-1123 validity, 63-character bounds, and suffix shape.
- Local-time natural-language parsing and UTC storage.
- Day normalization, all-days default, window validation, and interval order.
- Multi-plan creation, copied labels, partial failure reporting, and retry hint.
- Compact list filtering and console/JSON output.
- Status state/plan/label filtering and unavailable referenced plans.
- Delete confirmation, `--yes`, safe unqualified-delete rejection, and partial
  failures.
- Root command registration.

Only focused Go unit tests are run automatically. E2E tests are outside this
change's automatic verification and require explicit user approval.

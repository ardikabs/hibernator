# Agents Checklist & Handoff

## Purpose

This document is a compact operating checklist for any automated or human agent working on this repository. It captures the current development checkpoints, conventions, and the minimal acceptance criteria required to make progress on the Hibernator Operator.

## Agent responsibilities

- Follow the repository's `WORKPLAN.md` for design decisions and milestone scope.
- Use the `manage_todo_list` tool to create, update, and close tasks for traceability.
- Always present a one-line preamble before performing multi-step/tool operations.
- Make minimal, focused edits using `apply_patch` and run quick validations where possible.

## `.agent/rules` (best-effort constraints)

Place optional policy, lint, or guidance files under `.agent/rules/`. Automated agents should attempt to load and apply rules from this directory on startup as a best-effort constraint source (do not fail the run if files are missing or malformed). Example files:

- `.agent/rules/validation.yaml` — extra validation hints for CRDs
- `.agent/rules/ci-guidelines.md` — repository CI expectations

Agents should merge these best-effort rules with the canonical guidance in `AGENTS.md` and `WORKPLAN.md`.

## Quick links

- Workplan: `WORKPLAN.md`
- This checklist: `AGENTS.md`

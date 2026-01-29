# Agents Checklist & Handoff

## Purpose

**This is your entrypoint to the Hibernator Operator repository.** This document provides agent operating procedures and navigates you to all technical documentation needed to work effectively on this codebase.

## Documentation hierarchy

Start here and follow this order:

1. **AGENTS.md** (this file) — Agent operating procedures and navigation hub
2. **`.github/copilot-instructions.md`** — Technical project guidance, architecture, implementation status, development guidelines
3. **`.github/instructions/*`** — Detailed principles for all development aspects (security, testing, API design, concurrency, etc.)
4. **`enhancements/archived/WORKPLAN.md`** — Historical technical workplan with detailed design decisions and examples
5. **`enhancements/0001-hibernate-operator.md`** — Canonical architecture RFC (Control Plane + Runner Model)

**CRITICAL**: Always consult `.github/copilot-instructions.md` first for project context, then review relevant files in `.github/instructions/*` for specific guidance before implementing features.

## Agent responsibilities

- **Understand the project**: Read `.github/copilot-instructions.md` for architecture, CRDs, executors, and status
- **Follow principles**: Always consult `.github/instructions/*` files for design, security, testing, and coding standards
- **Reference history**: Use `enhancements/archived/WORKPLAN.md` for historical design decisions if needed
- **Track work**: Use the `manage_todo_list` tool to create, update, and close tasks for traceability
- **Be concise**: Present a one-line preamble before performing multi-step/tool operations
- **Edit efficiently**: Make minimal, focused edits using `apply_patch` or `multi_replace_string_in_file` and run quick validations

## Quick navigation

### Essential documentation

- **Copilot Instructions**: `.github/copilot-instructions.md` (start here for technical context)
- **Design Principles**: `.github/instructions/core-design-principles.md`
- **Architecture**: `.github/instructions/architectural-pattern.md`
- **RFC**: `enhancements/0001-hibernate-operator.md`
- **Workplan**: `enhancements/archived/WORKPLAN.md` (historical reference)

### Development guidance

- **Testing**: `.github/instructions/testing-strategy.md`
- **Security**: `.github/instructions/security-mandate.md`, `.github/instructions/security-principles.md`
- **Error Handling**: `.github/instructions/error-handling-principles.md`
- **API Design**: `.github/instructions/api-design-principles.md`
- **Code Quality**: `.github/instructions/code-organization-principles.md`, `.github/instructions/code-idioms-and-conventions.md`

### Operational guidance

- **Logging**: `.github/instructions/logging-and-observability-mandate.md`
- **Concurrency**: `.github/instructions/concurrency-and-threading-mandate.md`
- **Performance**: `.github/instructions/performance-optimization-principles.md`
- **Configuration**: `.github/instructions/configuration-management-principles.md`

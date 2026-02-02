# User Journey Templates

This directory contains templates for creating new user journey documentation.

## Quick Start

1. **Copy the template:**
   ```bash
   cp templates/journey-template.md your-journey-name.md
   ```

2. **Fill in the placeholders:**
   - Replace `[Journey Title]` with your journey name
   - Set the appropriate tier: MVP, Enhanced, or Advanced
   - List relevant personas
   - Complete all bracketed `[...]` sections

3. **Remove unused sections:**
   - Delete optional sections if not needed
   - Remove example branches if journey is linear
   - Keep only relevant troubleshooting items

4. **Add to README.md:**
   - Add journey to the appropriate tier table in `docs/user-journey/README.md`
   - Include status badge and RFC references

## Template Structure

### Required Sections
- **Title, Tier, Personas, When, Why** — Journey metadata
- **User Stories** — Agile-format stories
- **Business Outcome** — Measurable value delivered
- **Step-by-Step Flow** — Detailed implementation steps

### Optional Sections
- **RFC Implementation Status Notice** — For multi-phase features
- **Decision Branches** — For conditional workflows
- **Common Examples** — Additional use case variations
- **Troubleshooting** — Common issues and solutions
- **Related Journeys** — Links to prerequisite/follow-up journeys

## API Group Convention

**CRITICAL:** Always use the correct API group in YAML examples:

✅ **Correct:**
```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: HibernatePlan
```

❌ **Wrong:**
```yaml
apiVersion: hibernator.ardikasaputro.io/v1alpha1  # NEVER use this
```

### All API Groups

| Resource Type | API Group | Example |
|---------------|-----------|---------|
| HibernatePlan | `hibernator.ardikabs.com/v1alpha1` | `apiVersion: hibernator.ardikabs.com/v1alpha1` |
| ScheduleException | `hibernator.ardikabs.com/v1alpha1` | `apiVersion: hibernator.ardikabs.com/v1alpha1` |
| CloudProvider | `hibernator.ardikabs.com/v1alpha1` | `apiVersion: hibernator.ardikabs.com/v1alpha1` |
| K8SCluster | `hibernator.ardikabs.com/v1alpha1` | `apiVersion: hibernator.ardikabs.com/v1alpha1` |

## Tier Guidelines

### MVP Tier
- **Purpose:** Core functionality for basic hibernation workflows
- **Audience:** First-time users, essential operations
- **Examples:** Create plan, deploy operator, configure connectors

### Enhanced Tier
- **Purpose:** Advanced workflows, exceptions, governance
- **Audience:** Teams with established hibernation practices
- **Examples:** Schedule exceptions, RBAC, multi-environment

### Advanced Tier
- **Purpose:** Enterprise-scale, compliance, multi-tenant
- **Audience:** Large organizations, regulated industries
- **Examples:** Cross-account, audit trails, org-wide scaling

## Status Badge Reference

- **✅ Implemented** — Feature is complete and production-ready
- **🚀 In Progress** — Feature is actively being built
- **📋 Planned** — Feature is scheduled but not yet started
- **📋 Planned (Future)** — Feature is planned for future RFC phase
- **⏳ Proposed** — Feature concept exists but not yet approved
- **🔧 Under Maintenance** — Feature exists but needs improvements

## RFC Phase Tracking

For features that span multiple RFC phases, add an implementation status notice:

```markdown
> **👉 RFC-XXXX Implementation Status**
> This journey covers features from **RFC-XXXX Phase 1-3** (✅ Implemented):
> - Feature A
> - Feature B
>
> **NOT covered:** Feature D (Phase 4, future work)
```

For entirely unimplemented features:

```markdown
> **⚠️ FUTURE WORK NOTICE**
> This journey describes the [feature] documented in **RFC-XXXX Phase N**, which is **NOT YET IMPLEMENTED**.
> See [RFC-XXXX "Future Work"](../../enhancements/XXXX-name.md#section) for details.
```

## Validation Checklist

Before submitting a new journey:

- [ ] Title is descriptive and action-oriented
- [ ] Tier is appropriate (MVP/Enhanced/Advanced)
- [ ] All personas are from the [Personas Reference](../README.md#personas-reference)
- [ ] User stories follow "As a [persona], I want to [action], so that [benefit]" format
- [ ] API groups use `hibernator.ardikabs.com` (NOT `ardikasaputro.io`)
- [ ] YAML examples are tested and valid
- [ ] Step-by-step flow includes verification commands
- [ ] Related journeys are linked
- [ ] RFC references are included
- [ ] Status badge matches actual implementation status
- [ ] Added to appropriate tier table in README.md

## Example Journeys

Good reference journeys to study:

- **[hibernation-plan-initial-design.md](../hibernation-plan-initial-design.md)** — Comprehensive MVP journey
- **[create-emergency-exception.md](../create-emergency-exception.md)** — Enhanced tier with decision branches
- **[setup-cross-account-hibernation.md](../setup-cross-account-hibernation.md)** — Advanced tier pattern

## Need Help?

- Review existing journeys in the parent directory
- Check [User Journey Documentation](../README.md) for persona reference
- Consult relevant RFCs in `enhancements/` directory
- Ask in #hibernator-dev channel

---

**Template Version:** 1.0
**Last Updated:** February 2, 2026

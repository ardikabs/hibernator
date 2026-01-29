# RFC-0002: User-Friendly Schedule Format Migration

**Status**: Implemented
**Author**: Hibernator Team
**Created**: 2026-01-29
**Updated**: 2026-01-29

## Summary

Migrate the HibernatePlan schedule format from technical cron expressions to a user-friendly time window format with explicit start/end times and day-of-week specifications.

## Motivation

The original schedule format required users to write cron expressions:

```yaml
schedule:
  hibernate: "0 20 * * 1-5"  # 8 PM Mon-Fri
  wakeUp: "0 6 * * 1-5"      # 6 AM Mon-Fri
```

**Problems:**
- Requires cron expression knowledge
- Error-prone (e.g., day-of-week differs between tools: 0=Sunday or 7=Sunday)
- Not intuitive for non-technical users
- Difficult to validate at API level

## Proposal

Replace cron expressions with explicit time windows:

```yaml
schedule:
  offHours:
    - start: "20:00"
      end: "06:00"
      daysOfWeek: ["MON", "TUE", "WED", "THU", "FRI"]
```

**Benefits:**
- Human-readable format
- Clear validation rules (HH:MM time format, MON-SUN day names)
- Extensible for multiple time windows
- GitOps-friendly

## Design

### API Changes

**Before:**
```go
type Schedule struct {
    Hibernate string `json:"hibernate"`
    WakeUp    string `json:"wakeUp"`
    Timezone  string `json:"timezone"`
}
```

**After:**
```go
type Schedule struct {
    OffHours []OffHourWindow `json:"offHours"`
    Timezone string          `json:"timezone"`
}

type OffHourWindow struct {
    Start      string   `json:"start"`      // HH:MM format
    End        string   `json:"end"`        // HH:MM format
    DaysOfWeek []string `json:"daysOfWeek"` // MON-SUN
}
```

### Validation

Add kubebuilder validation markers:
- `Start` and `End`: Pattern `^([0-1]?[0-9]|2[0-3]):[0-5][0-9]$`
- `DaysOfWeek`: Enum `["MON","TUE","WED","THU","FRI","SAT","SUN"]`

Webhook validation:
- Validates HH:MM time format
- Validates day abbreviations (case-insensitive)
- Can extend to check logical constraints (e.g., at least one day specified)

### Conversion Layer

Since the internal scheduler still uses cron expressions, implement a conversion function:

```go
// internal/scheduler/schedule.go
func ConvertOffHoursToCron(windows []OffHourWindow) (hibernateCron, wakeUpCron string, err error)
```

**Conversion logic:**
- Parse HH:MM format: `"20:00"` → hour=20, min=0
- Map day names to cron day-of-week: `["MON", "TUE"]` → `"1,2"`
- Build cron expression: `"0 20 * * 1,2"`

**Example:**
- Input: `{Start: "20:00", End: "06:00", DaysOfWeek: ["MON", "TUE", "FRI"]}`
- Output:
  - `hibernateCron: "0 20 * * 1,2,5"`
  - `wakeUpCron: "0 6 * * 1,2,5"`

### Controller Integration

Update `evaluateSchedule()` in controller:

```go
func (r *HibernatePlanReconciler) evaluateSchedule(plan *HibernatePlan) (bool, time.Duration, error) {
    // Convert API format to cron expressions
    hibernateCron, wakeUpCron, err := scheduler.ConvertOffHoursToCron(plan.Spec.Schedule.OffHours)
    if err != nil {
        return false, time.Minute, err
    }

    // Pass to existing scheduler logic
    window := scheduler.ScheduleWindow{
        HibernateCron: hibernateCron,
        WakeUpCron:    wakeUpCron,
        Timezone:      plan.Spec.Schedule.Timezone,
    }

    return r.ScheduleEvaluator.Evaluate(window, time.Now())
}
```

## Implementation Status

### Completed ✅

- [x] API schema updated (`api/v1alpha1/hibernateplan_types.go`)
- [x] Validation webhook updated (`api/v1alpha1/hibernateplan_webhook.go`)
- [x] Webhook tests updated (`api/v1alpha1/hibernateplan_webhook_test.go`)
- [x] Conversion function implemented (`internal/scheduler/schedule.go`)
- [x] Controller integration (`internal/controller/hibernateplan_controller.go`)
- [x] README.md updated with new format
- [x] WORKPLAN.md updated

### Pending 🔄

- [ ] Unit tests for `ConvertOffHoursToCron()` function
  - Valid single window
  - Multiple days handling
  - Invalid time format
  - Invalid day name

- [ ] Update remaining sample configurations
  - `config/samples/hibernateplan_samples.yaml` (2 examples pending)

- [ ] Update controller tests to use new format
  - `internal/controller/hibernateplan_controller_test.go`

## Future Enhancements

### 1. Multiple Window Support

Current limitation: Only the first `OffHourWindow` is used.

**Proposed approach:**
- Generate multiple cron expressions
- Use OR logic in scheduler evaluation
- Example: Weekend schedule differs from weekday schedule

```yaml
offHours:
  - start: "20:00"
    end: "06:00"
    daysOfWeek: ["MON", "TUE", "WED", "THU", "FRI"]
  - start: "00:00"  # All day weekend
    end: "23:59"
    daysOfWeek: ["SAT", "SUN"]
```

### 2. Overnight Window Handling

Current behavior: Treats `end < start` as same-day.

**Problem:**
```yaml
start: "20:00"  # 8 PM
end: "06:00"    # 6 AM next day
```

Currently generates:
- Hibernate: `0 20 * * 1,2,3,4,5`
- WakeUp: `0 6 * * 1,2,3,4,5`

But the scheduler evaluates both on the same day, which doesn't represent overnight windows correctly.

**Proposed solution:**
- Detect when `end < start` (overnight case)
- Adjust wake-up day-of-week to next day
- Example: Friday 20:00 → Saturday 06:00

### 3. Complex Schedule Patterns

Support for:
- Holiday exclusions
- One-time schedule overrides
- Variable start/end times per day

## Testing Strategy

### Unit Tests

```go
func TestConvertOffHoursToCron(t *testing.T) {
    tests := []struct{
        name     string
        windows  []OffHourWindow
        wantHib  string
        wantWake string
        wantErr  bool
    }{
        {
            name: "weekday nights",
            windows: []OffHourWindow{{
                Start: "20:00",
                End: "06:00",
                DaysOfWeek: []string{"MON", "TUE", "WED", "THU", "FRI"},
            }},
            wantHib: "0 20 * * 1,2,3,4,5",
            wantWake: "0 6 * * 1,2,3,4,5",
        },
        // ... more test cases
    }
}
```

### Integration Tests

- Create HibernatePlan with new schedule format
- Verify webhook validation accepts valid formats
- Verify webhook rejects invalid formats
- Verify controller generates correct cron expressions
- Verify scheduler evaluates schedule correctly

## Migration Guide

For existing users with cron-based schedules:

**Before:**
```yaml
apiVersion: hibernator.ardikasaputro.io/v1alpha1
kind: HibernatePlan
metadata:
  name: dev-offhours
spec:
  schedule:
    hibernate: "0 20 * * 1-5"
    wakeUp: "0 6 * * 1-5"
    timezone: "Asia/Jakarta"
```

**After:**
```yaml
apiVersion: hibernator.ardikasaputro.io/v1alpha1
kind: HibernatePlan
metadata:
  name: dev-offhours
spec:
  schedule:
    offHours:
      - start: "20:00"
        end: "06:00"
        daysOfWeek: ["MON", "TUE", "WED", "THU", "FRI"]
    timezone: "Asia/Jakarta"
```

## Alternatives Considered

### 1. Keep Cron Format

**Pros:** No migration needed, powerful and flexible
**Cons:** Steep learning curve, error-prone

### 2. Natural Language Parsing

Example: `"every weekday from 8 PM to 6 AM"`

**Pros:** Most user-friendly
**Cons:** Complex parsing, ambiguous inputs, i18n challenges

### 3. Hybrid Approach

Allow both formats with explicit field:

```yaml
schedule:
  format: "cron" # or "timewindow"
  cron: "0 20 * * 1-5"  # if format=cron
  offHours: [...]        # if format=timewindow
```

**Decision:** Rejected - adds complexity without clear benefit. Power users can extend if needed.

## References

- Cron expression format: [Wikipedia](https://en.wikipedia.org/wiki/Cron)
- robfig/cron library: [GitHub](https://github.com/robfig/cron)
- Kubernetes validation markers: [Kubebuilder Book](https://book.kubebuilder.io/reference/markers/crd-validation.html)

---

**Review Status:** Implemented
**Next Review:** After integration tests are complete

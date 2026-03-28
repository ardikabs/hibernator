# Multi-Window Schedules

Hibernator supports multiple off-hour windows in a single plan, evaluated with OR-logic.

## Why Multiple Windows?

A single window may not capture all off-hours. For example:

- **Weeknights**: 8 PM to 6 AM
- **Weekends**: All day Saturday and Sunday
- **Extended lunch**: 12 PM to 1 PM for a staging environment

## Defining Multiple Windows

```yaml
schedule:
  timezone: "Asia/Jakarta"
  offHours:
    # Weeknight off-hours
    - start: "20:00"
      end: "06:00"
      daysOfWeek: ["MON", "TUE", "WED", "THU", "FRI"]
    # Full weekend
    - start: "00:00"
      end: "23:59"
      daysOfWeek: ["SAT", "SUN"]
```

## How It Works

- Each window is independently converted to cron expressions
- The controller evaluates all windows with **OR-logic**: if any window matches the current time, hibernation is active
- **Next event times** are computed as the earliest wakeup/shutdown across all windows
- Windows can overlap — the union of all windows determines the effective off-hours

## Example: Three-Window Schedule

```yaml
apiVersion: hibernator.ardikabs.com/v1alpha1
kind: HibernatePlan
metadata:
  name: staging-offhours
  namespace: hibernator-system
spec:
  schedule:
    timezone: "US/Eastern"
    offHours:
      # Weeknight window
      - start: "19:00"
        end: "07:00"
        daysOfWeek: ["MON", "TUE", "WED", "THU"]
      # Friday night through Monday morning
      - start: "17:00"
        end: "23:59"
        daysOfWeek: ["FRI"]
      - start: "00:00"
        end: "07:00"
        daysOfWeek: ["MON"]
      # Full weekend
      - start: "00:00"
        end: "23:59"
        daysOfWeek: ["SAT", "SUN"]
  execution:
    strategy:
      type: Sequential
  targets:
    - name: staging-db
      type: rds
      connectorRef:
        kind: CloudProvider
        name: aws-staging
      parameters:
        selector:
          instanceIds: ["staging-db"]
```

## Adjacent Windows

If a wakeup time from one window coincides with the start of another window on the same or next listed day, the controller handles this correctly — the system remains hibernated without an unnecessary wakeup/re-hibernate cycle. This is demonstrated in the three-window example above, where the Friday 17:00 window, weekend windows, and Monday 07:00 wakeup form a continuous hibernation period from Friday afternoon through Monday morning.

## Window Interactions with Exceptions

When a `ScheduleException` of type `extend` adds windows, the new windows are merged with the base schedule using the same OR-logic. If an extend window contains a base wakeup time, the collision is resolved correctly.

For combining multiple exceptions on the same plan (e.g., extend + suspend, replace + extend), see [Composing Multiple Exceptions](composing-multiple-exceptions.md).

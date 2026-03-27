# Schedule Boundaries

This guide covers edge cases and boundary conditions in schedule evaluation.

## Overnight Windows

When `end` is earlier than `start`, the window spans midnight:

```yaml
offHours:
  - start: "20:00"   # 8 PM today
    end: "06:00"     # 6 AM tomorrow
    daysOfWeek: ["MON", "TUE", "WED", "THU", "FRI"]
```

- Monday 20:00 → Tuesday 06:00
- Tuesday 20:00 → Wednesday 06:00
- Friday 20:00 → Saturday 06:00

The `daysOfWeek` refers to the day when the window **starts**.

## Day Boundaries

### Friday Night into Saturday

A Friday window of `start: "20:00"` / `end: "06:00"` extends into Saturday morning. If you also have a weekend window starting at `00:00` Saturday, Hibernator handles the overlap — resources stay hibernated without unnecessary wakeup/re-hibernate transitions.

### Same-Day Windows

When `start` is before `end`, the window is contained within a single day:

```yaml
offHours:
  - start: "12:00"
    end: "13:00"    # Lunch break
    daysOfWeek: ["MON", "TUE", "WED", "THU", "FRI"]
```

## Timezone Handling

All schedule evaluation happens in the configured timezone:

```yaml
schedule:
  timezone: "Asia/Jakarta"    # UTC+7
```

- **Daylight Saving Time**: If the timezone observes DST, schedule windows shift accordingly. A 20:00 shutdown remains at 20:00 local time even when clocks change.
- **UTC is recommended** for environments spanning multiple timezones to avoid ambiguity.

## Near-Midnight Boundaries

Windows near midnight require care:

```yaml
# This window runs from 23:30 to 00:30 the next day
offHours:
  - start: "23:30"
    end: "00:30"
    daysOfWeek: ["MON"]    # Monday 23:30 → Tuesday 00:30
```

## Schedule Evaluation Frequency

The controller evaluates schedules on each reconciliation loop. The reconciliation period determines the maximum delay between the scheduled time and the actual action.

!!! note
    If the controller restarts during a schedule window, it re-evaluates on startup and takes the appropriate action based on the current time and plan state.

## Exception Interactions

When schedule exceptions are active, they modify the effective schedule:

- **Extend**: New windows are unioned with base windows (OR-logic)
- **Suspend**: Specified windows are carved out of the effective schedule
- **Replace**: Base schedule is completely ignored; only exception windows apply

These modifications affect all boundary calculations. For example, a `suspend` exception that covers a Friday night window prevents the overnight hibernation even though the base schedule includes it.

# Schedule Boundaries

This guide covers edge cases and boundary conditions in schedule evaluation.

## The `daysOfWeek` Execution Boundary

**All executions — both hibernation and wakeup — are bounded to `daysOfWeek`.** A day not listed in `daysOfWeek` is never a subject of execution. This means:

- Hibernation only **starts** on days listed in `daysOfWeek`
- Wakeup only **triggers** on days listed in `daysOfWeek`
- If a hibernation period would naturally end on a day **not** in `daysOfWeek`, resources remain hibernated until the next listed day

## Overnight Windows

When `end` is earlier than `start`, the window spans midnight:

```yaml
offHours:
  - start: "20:00"   # 8 PM today
    end: "06:00"     # 6 AM next eligible day
    daysOfWeek: ["MON", "TUE", "WED", "THU", "FRI"]
```

- Monday 20:00 → Tuesday 06:00 (Tuesday is in `daysOfWeek`, wakeup proceeds)
- Tuesday 20:00 → Wednesday 06:00 (Wednesday is in `daysOfWeek`, wakeup proceeds)
- Thursday 20:00 → Friday 06:00 (Friday is in `daysOfWeek`, wakeup proceeds)
- **Friday 20:00 → Monday 06:00** (Saturday is **not** in `daysOfWeek`, so wakeup does not trigger Saturday; resources stay hibernated through the weekend until Monday — the next listed day)

## Day Boundaries

### Friday Night with Weekday-Only Schedule

With `daysOfWeek: ["MON", "TUE", "WED", "THU", "FRI"]` and `start: "20:00"` / `end: "06:00"`, Friday's hibernation at 20:00 does **not** wake up Saturday at 06:00. Saturday is not in `daysOfWeek`, so it is not an execution day. Resources remain hibernated through the entire weekend, and wakeup occurs at **Monday 06:00**.

To cover weekends explicitly with separate behavior (e.g., keep resources hibernated all day), add a weekend window:

```yaml
offHours:
  # Weeknight window
  - start: "20:00"
    end: "06:00"
    daysOfWeek: ["MON", "TUE", "WED", "THU", "FRI"]
  # Full weekend
  - start: "00:00"
    end: "23:59"
    daysOfWeek: ["SAT", "SUN"]
```

With this configuration, the weekend window explicitly governs Saturday and Sunday execution. Without the weekend window, the weekday-only schedule simply keeps resources hibernated from Friday 20:00 through Monday 06:00.

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
# This window starts Monday 23:30 and ends at 00:30 on the next listed day
offHours:
  - start: "23:30"
    end: "00:30"
    daysOfWeek: ["MON", "TUE"]    # Monday 23:30 → Tuesday 00:30
```

!!! warning
    If only `["MON"]` were listed, the 00:30 wakeup would not trigger on Tuesday (since Tuesday is not listed). The resources would remain hibernated from Monday 23:30 all the way until the **next Monday at 00:30** — a full week later. Ensure that both the hibernation start day **and** the expected wakeup day are included in `daysOfWeek`.

## Execution Timing: Schedule Buffer and Safety Buffer

The controller intentionally delays every execution past the scheduled time by applying two buffers:

| Buffer | Default | Configurable | Purpose |
|--------|---------|--------------|--------|
| **Schedule buffer** | 1 minute | Yes (`--schedule-buffer-duration`) | Ensures the controller evaluates *after* the schedule boundary has passed |
| **Safety buffer** | 10 seconds | No (hardcoded) | Additional guard against clock-skew and race conditions |

**Total offset: up to 1m 10s** after the nominal schedule time (with default settings).

This means a `start: "20:00"` hibernation actually executes around 20:01:10, and an `end: "06:00"` wakeup executes around 06:01:10.

### Why This Exists

The schedule buffer solves the narrow gap in **full-day windows**:

```yaml
# Full-day shutdown — resources hibernate all day
offHours:
  - start: "00:00"
    end: "23:59"
    daysOfWeek: ["SAT", "SUN"]
```

The gap between `23:59:00` and `00:00:00` is only 60 seconds. Without the buffer, the controller could evaluate at exactly `00:00:00` and race against the boundary. The 1-minute schedule buffer pushes evaluation to `00:01:00` (plus 10s safety buffer), well past the `00:00` start time.

The same applies to full-day wakeup windows:

```yaml
# Full-day wakeup — resources stay awake all day
offHours:
  - start: "23:59"
    end: "00:00"
    daysOfWeek: ["MON", "TUE", "WED", "THU", "FRI"]
```

Here the gap between `00:00:00` (end/wakeup) and `23:59:00` (start/hibernate) is even narrower in the other direction. The buffer ensures the wakeup at `00:00` executes cleanly at `00:01:10`.

### Adjusting the Schedule Buffer

The schedule buffer is configurable on the controller:

```bash
# Increase to 2 minutes (e.g., for slow API environments)
--schedule-buffer-duration="2m"

# Or via environment variable
SCHEDULE_BUFFER_DURATION=2m
```

The safety buffer (10s) is not configurable.

!!! note
    If the controller restarts during a schedule window, it re-evaluates on startup and takes the appropriate action based on the current time and plan state.

## Exception Interactions

When schedule exceptions are active, they modify the effective schedule:

- **Extend**: New windows are unioned with base windows (OR-logic)
- **Suspend**: Specified windows are carved out of the effective schedule
- **Replace**: Base schedule is completely ignored; only exception windows apply

These modifications affect all boundary calculations, including the `daysOfWeek` execution boundary. For example, a `suspend` exception that covers a Friday night window prevents the overnight hibernation even though the base schedule includes it. An `extend` exception that adds `SAT` to `daysOfWeek` would cause a Saturday 06:00 wakeup that would otherwise not occur.

---
name: calendar-queries
description: Use when picking which icalendar_* tool to call, deciding whether to sync, interpreting cached event data, or handling icalendar plugin errors.
---

# Calendar queries with the icalendar plugin

This plugin exposes ten read-only iCloud calendar tools backed by a local
SQLite cache that lazy-syncs from CalDAV. Pick the right tool for the user's
intent, and don't sync more than necessary.

## Tool selection

Map natural-language requests to tools directly. Prefer named time-window
tools over `icalendar_range` when the user's intent fits a named window — the
named tools handle local-time boundaries correctly and avoid you having to
compute dates.

| User says (any language) | Tool to call |
|---|---|
| today / today's events / what's on today | `icalendar_today` |
| tomorrow / what's tomorrow | `icalendar_tomorrow` |
| this week / rest of the week / my week | `icalendar_week` |
| next week | `icalendar_next_week` |
| specific date, "events on May 27", arbitrary span | `icalendar_range` |
| "find the dentist appointment", keyword recall | `icalendar_search` |
| "tell me more about that event", details for one event | `icalendar_show` |
| which calendars do I have, list my calendars | `icalendar_list_calendars` |
| is the cache up to date, when did it last sync | `icalendar_status` |
| refresh, sync, pull latest | `icalendar_sync` |

When the user gives a date, parse it to `YYYY-MM-DD` before calling
`icalendar_range`. Both `from_date` and `to_date` are inclusive.

## Sync etiquette

The cache lazy-syncs on read when it is older than the configured threshold
(default 15 minutes). You almost never need to call `icalendar_sync`
explicitly.

- Default: just call the query tool. If the cache is stale, the binary will
  sync transparently before answering.
- "Is my calendar fresh?" → call `icalendar_status`. It reports last sync
  time. Do NOT call `icalendar_sync` to answer this — that would force a
  sync just to check.
- "Refresh my calendar" / "sync now" → `icalendar_sync` (incremental).
- "Re-sync everything" / "I think the cache is corrupt" → `icalendar_sync`
  with `full=true`.
- Offline / fast path: pass `no_sync=true` to any query tool to skip the
  freshness check and serve only from cache.

## Calendar filtering

The user may have multiple calendars (Work, Personal, Family, etc.). Use
`icalendar_list_calendars` once to learn the exact names — they are
case-sensitive in the filter.

Pass `calendar="<exact name>"` on query tools to restrict results to a
single calendar. The filter does NOT apply to `icalendar_sync`,
`icalendar_status`, or `icalendar_list_calendars`.

## Event detail flow

List/search tools return events with an `id` field (the CalDAV UID).
When the user asks for more detail on a specific event, call
`icalendar_show` with that `id`. The detail view includes full
description text, location, all-day flag, and recurrence info that may
be truncated in list output.

## Error handling

Every tool returns a JSON string. On failure the shape is:

```json
{"ok": false, "data": null, "error": {"code": "...", "message": "..."}}
```

| `error.code` | What it means | What you should do |
|---|---|---|
| `timeout` | Operation took too long. | Retry once if the user wants. |
| `binary_unavailable` | Install is incomplete (binary couldn't be resolved or downloaded). | Surface the message verbatim. Do not retry. |
| `binary_not_found` | The resolved binary disappeared between resolution and execution. | Same as above — install needs attention. |
| `invalid_json` | The binary returned malformed JSON. Likely a bug. | Report the message; suggest filing an issue. |
| `subprocess_error` | Subprocess failed (non-zero exit, OS error). | Report the message. |
| `handler_error` | A bug in the plugin wrapper — most often a missing required parameter. | Check the schema; fix the call. |

Success responses come straight from the Go binary as raw JSON. Event
arrays look like:

```json
[
  {"id": "...", "title": "...", "start": "2026-05-24T09:00:00+02:00",
   "end": "2026-05-24T10:00:00+02:00", "all_day": false,
   "location": "...", "description": "...", "calendar_name": "...",
   "recurrence_info": "", "last_modified": "..."}
]
```

## Date and time conventions

- All input dates use `YYYY-MM-DD`.
- Event `start` / `end` / `last_modified` are RFC 3339 strings.
- Times are rendered in the timezone the user configured at setup. If you
  need to compute relative dates ("Monday", "in two weeks"), do so in the
  agent's logic — the tools don't accept natural-language date strings.

## Read-only by design

The plugin cannot create, edit, or delete calendar events. There is no
`icalendar_create_event` tool. If the user asks to add or change an event,
explain this and suggest they do it in Apple's Calendar app, on
[icloud.com/calendar](https://www.icloud.com/calendar/), or via a different
tool that supports CalDAV writes.

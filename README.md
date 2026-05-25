# icloud-calendar

Read-only iCloud Calendar tools for [Hermes](https://hermes-agent.nousresearch.com/) agents (and a standalone `icalendar` CLI). Syncs events via CalDAV into a local SQLite cache and serves fast queries.

Read-only by design: V1 cannot create, edit, delete, invite, or schedule. It is meant to give an agent a fast, accurate view of what's on the calendar — not to manage it.

## What you get

- 10 first-class Hermes tools: status, sync, list_calendars, today, tomorrow, week, next_week, range, search, show.
- One bundled agent skill (`icalendar:calendar-queries`) with tool-selection and sync-etiquette guidance.
- A standalone `icalendar` CLI for the same operations from a terminal or cron.

## Install — end users

The simple path; no Go required.

```bash
hermes plugins install addvanced/icloud-calendar
```

Hermes will prompt for two values and save them to its `.env`:

- `ICALENDAR_APPLE_ID` — your Apple ID email.
- `ICALENDAR_PASSWORD` — an [app-specific password](https://appleid.apple.com/account/manage). **Not your normal Apple password.** Generate one under *Sign-In and Security → App-Specific Passwords*.

Then enable the plugin and restart the gateway:

```bash
hermes plugins enable icalendar
systemctl --user restart hermes-gateway
```

The first time the agent calls one of the calendar tools, the plugin downloads a pre-built binary matching your platform (`linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`) from GitHub Releases, verifies its SHA256, and caches it at `~/.hermes/plugins/icalendar/bin/icalendar`. Subsequent calls are instant.

If you'd rather configure persistent options (custom timezone, longer sync range), drop a `~/.config/icalendar/config.toml` (see [Configuration](#configuration)). Env vars take precedence over the file for credentials, so you can leave Apple credentials out of the TOML entirely.

## Install — developers (building from source)

Requires Go 1.26+:

```bash
git clone https://github.com/addvanced/icloud-calendar
cd icloud-calendar
make install
hermes plugins enable icalendar
systemctl --user restart hermes-gateway
```

`make install` builds `~/bin/icalendar` and copies the plugin files (plus `skills/`) into `~/.hermes/plugins/icalendar/`. The runtime resolver prefers a bundled `bin/icalendar` inside the plugin dir, then `~/bin/icalendar`, then a release download — so a developer's `make install` continues to work without going through GitHub Releases.

## Tools

| Tool | Purpose |
|---|---|
| `icalendar_status` | Cache health: calendar count, event count, last sync time |
| `icalendar_sync` | Trigger CalDAV sync (incremental by default; `full=true` for full re-sync) |
| `icalendar_list_calendars` | Enumerate available calendars |
| `icalendar_today` | Events for today |
| `icalendar_tomorrow` | Events for tomorrow |
| `icalendar_week` | Events for this week (today + 6 days) |
| `icalendar_next_week` | Events for next week (days 7–13 from today) |
| `icalendar_range` | Events in inclusive `YYYY-MM-DD` date range |
| `icalendar_search` | FTS5 keyword search across title/location/description |
| `icalendar_show` | Full event detail by UID/href |

All tools return either the binary's JSON output verbatim, or a wrapper error JSON:

```json
{"ok": false, "data": null, "error": {"code": "...", "message": "..."}}
```

See [`skills/calendar-queries/SKILL.md`](skills/calendar-queries/SKILL.md) for full error-code semantics and tool-selection guidance.

## Bundled skill

The plugin ships an opt-in skill at `icalendar:calendar-queries`. Load it from an agent for explicit guidance on which tool to call for which question, when to (and not to) sync, and how to interpret errors:

```python
skill_view("icalendar:calendar-queries")
```

Plugin skills are not in the auto-discovered index — load them explicitly when relevant.

## CLI usage

```bash
icalendar setup                                    # interactive first-time setup
icalendar sync [--full] [--quiet]
icalendar status
icalendar list-calendars
icalendar today
icalendar tomorrow
icalendar week
icalendar next-week
icalendar range --from 2026-05-21 --to 2026-05-28
icalendar search "keyword" [--field title|location|description]
icalendar show <event-id-or-href>
```

Global flags:

```bash
--json                 machine-readable output (default in Hermes tools)
--calendar <name>      filter by calendar name (case-sensitive)
--no-sync              skip lazy auto-sync before query commands
```

`range --to` is inclusive for whole-day date ranges.

## Configuration

Two ways to supply credentials:

1. **Env vars** — recommended for Hermes-managed installs. `ICALENDAR_APPLE_ID` and `ICALENDAR_PASSWORD` always override TOML values.
2. **`~/.config/icalendar/config.toml`** — for direct CLI use or to set non-credential fields (timezone, sync range, date format).

`icalendar setup` generates the TOML interactively. Sample shape:

```toml
[auth]
apple_id = "you@example.com"
app_password = "xxxx-xxxx-xxxx-xxxx"

[sync]
range_years = 2
auto_sync_threshold_minutes = 15

[output]
date_format = "2006-01-02 15:04"   # any Go time layout
timezone    = "Europe/Berlin"      # any IANA TZ name
```

Default timezone is detected from the host system (`$TZ`, `/etc/timezone`, or `/etc/localtime`), falling back to UTC. Default date format is `2006-01-02 15:04` (ISO-style, locale-neutral).

Config and cache files must be private (`0600`); the tool refuses looser permissions on startup.

## Sync behavior

- Full sync fetches events in the configured range, default `now ± range_years` (default ±2 years).
- Incremental sync uses RFC 6578 `sync-token` per calendar.
- If iCloud rejects a sync token, the tool falls back to full sync for that calendar.
- Query commands lazy-sync first when the newest cache sync is older than `auto_sync_threshold_minutes`.
- `--no-sync` disables lazy sync for that invocation.
- A SQLite lock prevents concurrent sync runs; stale locks older than 30 minutes are cleared.

## JSON event format

Query commands with `--json` return an array of events:

```json
[
  {
    "id": "event-uid",
    "title": "Event title",
    "start": "2026-05-21T10:00:00+02:00",
    "end": "2026-05-21T11:00:00+02:00",
    "all_day": false,
    "location": "Conference room",
    "description": "",
    "calendar_name": "Work",
    "recurrence_info": "RRULE:FREQ=YEARLY",
    "last_modified": "2026-05-20T09:00:00Z"
  }
]
```

Times are RFC 3339 in the configured timezone.

## Periodic sync (cron)

Sync is lazy by default — query tools refresh the cache on read when stale. For background freshening, a cron job works:

```cron
*/15 * * * * ~/bin/icalendar sync --quiet
```

Hermes cron can run the same command. This repo intentionally does not install a job for you.

## Troubleshooting

### `config permissions must be 600 or stricter`

```bash
chmod 600 ~/.config/icalendar/config.toml
chmod 700 ~/.config/icalendar
```

### iCloud auth fails

Most common causes:

- Wrong Apple ID.
- Normal Apple password used instead of an app-specific password.
- App-specific password revoked or expired.
- Apple temporarily rejecting CalDAV auth (rare, transient).

Generate a fresh app-specific password at <https://appleid.apple.com/> and re-run `icalendar setup` (or update `ICALENDAR_PASSWORD` in your Hermes `.env`).

### Sync says another sync is running

The tool prevents concurrent syncs via a SQLite lock. If a sync crashed, wait up to 30 minutes for stale lock cleanup, or inspect the local DB manually.

### Calendar appears with zero events

Some iCloud collections are calendar-like but do not expose normal `VEVENT` data in the configured date range — for example, reminder/special calendars. They still appear in `list-calendars`.

### Binary won't download

If `binary_unavailable` errors persist, the runtime expects a release tagged `v<plugin_version>` at <https://github.com/addvanced/icloud-calendar/releases>. Either build from source (`make install`) or check that your platform is one of the supported four: `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`.

## Development notes

- CalDAV is implemented with `net/http` plus small XML structs/builders.
- `github.com/emersion/go-webdav` was evaluated but not used as core because CalDAV high-level sync-collection support was missing.
- `github.com/emersion/go-ical` parses iCalendar data.
- `modernc.org/sqlite` provides CGO-free SQLite.

## Contributing

Issues and PRs welcome at <https://github.com/addvanced/icloud-calendar>. Before submitting:

```bash
make lint
make test
```

## License

MIT — see [LICENSE](./LICENSE).

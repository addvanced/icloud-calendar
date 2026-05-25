"""Tool schemas for the icalendar Hermes plugin."""

_NO_SYNC = {
    "type": "boolean",
    "description": (
        "If true, skip the normal stale-cache auto-sync and read only from the "
        "local cache. Use for fast cache-only reads or when avoiding network "
        "access matters."
    ),
}

_CALENDAR = {
    "type": "string",
    "description": "Optional calendar name filter (case-sensitive).",
}

_DATE = {
    "type": "string",
    "pattern": r"^\d{4}-\d{2}-\d{2}$",
    "description": "Date in YYYY-MM-DD format. Inclusive for range queries.",
}

ICALENDAR_STATUS_SCHEMA = {
    "name": "icalendar_status",
    "description": (
        "Report iCloud calendar cache health: calendar count, event count, "
        "and last sync time. READ-ONLY: cannot create, edit, or delete "
        "events. Setup/credentials are handled outside the plugin."
    ),
    "parameters": {"type": "object", "properties": {}},
}

ICALENDAR_SYNC_SCHEMA = {
    "name": "icalendar_sync",
    "description": (
        "Trigger a CalDAV sync from iCloud into the local SQLite cache. "
        "Incremental by default; set full=true for a full re-sync. READ-ONLY: "
        "this only pulls remote state down and never writes calendar changes."
    ),
    "parameters": {
        "type": "object",
        "properties": {
            "full": {
                "type": "boolean",
                "description": "Force a full re-sync. Default: false (incremental).",
            },
        },
    },
}

ICALENDAR_LIST_CALENDARS_SCHEMA = {
    "name": "icalendar_list_calendars",
    "description": (
        "List every iCloud calendar available in the configured account. "
        "READ-ONLY."
    ),
    "parameters": {
        "type": "object",
        "properties": {"no_sync": _NO_SYNC},
    },
}


def _date_query_schema(name, blurb):
    return {
        "name": name,
        "description": f"{blurb} READ-ONLY.",
        "parameters": {
            "type": "object",
            "properties": {
                "calendar": _CALENDAR,
                "no_sync": _NO_SYNC,
            },
        },
    }


ICALENDAR_TODAY_SCHEMA = _date_query_schema(
    "icalendar_today",
    "List calendar events for today.",
)
ICALENDAR_TOMORROW_SCHEMA = _date_query_schema(
    "icalendar_tomorrow",
    "List calendar events for tomorrow.",
)
ICALENDAR_WEEK_SCHEMA = _date_query_schema(
    "icalendar_week",
    "List calendar events for this week (today + 6 days).",
)
ICALENDAR_NEXT_WEEK_SCHEMA = _date_query_schema(
    "icalendar_next_week",
    "List calendar events for next week (days 7-13 from today).",
)

ICALENDAR_RANGE_SCHEMA = {
    "name": "icalendar_range",
    "description": (
        "List calendar events in an inclusive YYYY-MM-DD date range. READ-ONLY."
    ),
    "parameters": {
        "type": "object",
        "properties": {
            "from_date": _DATE,
            "to_date": _DATE,
            "calendar": _CALENDAR,
            "no_sync": _NO_SYNC,
        },
        "required": ["from_date", "to_date"],
    },
}

ICALENDAR_SEARCH_SCHEMA = {
    "name": "icalendar_search",
    "description": (
        "Search the iCloud calendar cache by keyword (FTS5 full-text search "
        "across event titles, locations, and descriptions). READ-ONLY."
    ),
    "parameters": {
        "type": "object",
        "properties": {
            "query": {"type": "string", "description": "Search keyword."},
            "field": {
                "type": "string",
                "enum": ["title", "location", "description"],
                "description": "Optional field restriction.",
            },
            "calendar": _CALENDAR,
            "no_sync": _NO_SYNC,
        },
        "required": ["query"],
    },
}

ICALENDAR_SHOW_SCHEMA = {
    "name": "icalendar_show",
    "description": (
        "Show full details for a calendar event by ID or href. READ-ONLY."
    ),
    "parameters": {
        "type": "object",
        "properties": {
            "id": {
                "type": "string",
                "description": "Event UID or href as returned by other icalendar tools.",
            },
            "no_sync": _NO_SYNC,
        },
        "required": ["id"],
    },
}

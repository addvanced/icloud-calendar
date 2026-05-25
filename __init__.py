"""Hermes plugin registering read-only iCloud calendar tools."""

from pathlib import Path

try:
    from . import schemas, tools
except ImportError:  # Allows pytest to import this root __init__.py as a test package.
    import schemas  # type: ignore[no-redef]
    import tools  # type: ignore[no-redef]


PLUGIN_DIR = Path(__file__).resolve().parent
SKILLS_DIR = PLUGIN_DIR / "skills"

# Env vars required by the Go binary. Declared at the tool registration level
# (and also in plugin.yaml at the plugin level) so Hermes can gate tool
# availability rather than letting the model attempt a call that will fail
# inside the binary with a config-loading error.
_REQUIRES_ENV = ["ICALENDAR_APPLE_ID", "ICALENDAR_PASSWORD"]


_REGISTRATIONS = (
    (
        "icalendar_status",
        schemas.ICALENDAR_STATUS_SCHEMA,
        tools.handle_status,
        "Report iCloud calendar cache health.",
    ),
    (
        "icalendar_sync",
        schemas.ICALENDAR_SYNC_SCHEMA,
        tools.handle_sync,
        "Sync iCloud calendars into the local cache.",
    ),
    (
        "icalendar_list_calendars",
        schemas.ICALENDAR_LIST_CALENDARS_SCHEMA,
        tools.handle_list_calendars,
        "List available iCloud calendars.",
    ),
    (
        "icalendar_today",
        schemas.ICALENDAR_TODAY_SCHEMA,
        tools.handle_today,
        "List today's calendar events.",
    ),
    (
        "icalendar_tomorrow",
        schemas.ICALENDAR_TOMORROW_SCHEMA,
        tools.handle_tomorrow,
        "List tomorrow's calendar events.",
    ),
    (
        "icalendar_week",
        schemas.ICALENDAR_WEEK_SCHEMA,
        tools.handle_week,
        "List this week's calendar events.",
    ),
    (
        "icalendar_next_week",
        schemas.ICALENDAR_NEXT_WEEK_SCHEMA,
        tools.handle_next_week,
        "List next week's calendar events.",
    ),
    (
        "icalendar_range",
        schemas.ICALENDAR_RANGE_SCHEMA,
        tools.handle_range,
        "List calendar events in an inclusive date range.",
    ),
    (
        "icalendar_search",
        schemas.ICALENDAR_SEARCH_SCHEMA,
        tools.handle_search,
        "Search iCloud calendar events by keyword.",
    ),
    (
        "icalendar_show",
        schemas.ICALENDAR_SHOW_SCHEMA,
        tools.handle_show,
        "Show event details by ID or href.",
    ),
)


def register(ctx):
    """Register icalendar tools and bundled skills with Hermes."""
    for name, schema, handler, description in _REGISTRATIONS:
        ctx.register_tool(
            name=name,
            toolset="icalendar",
            schema=schema,
            handler=handler,
            check_fn=tools.check_icalendar_available,
            description=description,
            emoji="📅",
            requires_env=_REQUIRES_ENV,
        )

    if SKILLS_DIR.is_dir():
        for child in sorted(SKILLS_DIR.iterdir()):
            skill_md = child / "SKILL.md"
            if child.is_dir() and skill_md.is_file():
                ctx.register_skill(child.name, skill_md)

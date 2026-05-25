"""Registration tests for the icalendar Hermes plugin."""

import importlib.util
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
EXPECTED_TOOLS = {
    "icalendar_status",
    "icalendar_sync",
    "icalendar_list_calendars",
    "icalendar_today",
    "icalendar_tomorrow",
    "icalendar_week",
    "icalendar_next_week",
    "icalendar_range",
    "icalendar_search",
    "icalendar_show",
}


class FakeContext:
    def __init__(self):
        self.registrations = []
        self.skills = []

    def register_tool(self, **kwargs):
        self.registrations.append(kwargs)

    def register_skill(self, name, path):
        self.skills.append((name, path))


def load_plugin():
    spec = importlib.util.spec_from_file_location(
        "icalendar_plugin_test",
        ROOT / "__init__.py",
        submodule_search_locations=[str(ROOT)],
    )
    assert spec is not None
    module = importlib.util.module_from_spec(spec)
    assert spec.loader is not None
    spec.loader.exec_module(module)
    return module


def parse_provided_tools():
    tools = []
    in_section = False
    for line in (ROOT / "plugin.yaml").read_text(encoding="utf-8").splitlines():
        stripped = line.strip()
        if stripped == "provides_tools:":
            in_section = True
            continue
        if in_section:
            if stripped.startswith("-"):
                tools.append(stripped.removeprefix("-").strip())
            elif stripped and not line.startswith(" "):
                break
    return set(tools)


def test_registers_expected_tools_and_toolset():
    plugin = load_plugin()
    ctx = FakeContext()

    plugin.register(ctx)

    names = {registration["name"] for registration in ctx.registrations}
    assert names == EXPECTED_TOOLS
    assert len(ctx.registrations) == 10
    for registration in ctx.registrations:
        assert registration["toolset"] == "icalendar"
        assert registration["schema"]["name"] == registration["name"]
        assert callable(registration["handler"])
        assert callable(registration["check_fn"])
        assert registration["emoji"] == "📅"
        # Every tool requires the same Apple credentials. Declaring it at the
        # tool level lets Hermes gate availability cleanly when env vars are
        # missing, instead of every call failing inside the Go binary.
        assert registration["requires_env"] == [
            "ICALENDAR_APPLE_ID",
            "ICALENDAR_PASSWORD",
        ]


def test_plugin_yaml_does_not_drift_from_registration():
    plugin = load_plugin()
    ctx = FakeContext()

    plugin.register(ctx)

    registered = {registration["name"] for registration in ctx.registrations}
    assert parse_provided_tools() == registered


def test_register_calls_register_skill_for_calendar_queries():
    plugin = load_plugin()
    ctx = FakeContext()

    plugin.register(ctx)

    skill_names = [name for name, _ in ctx.skills]
    assert "calendar-queries" in skill_names, (
        "register() must call ctx.register_skill for each bundled skill; "
        "without this, skill_view(\"icalendar:calendar-queries\") does not work"
    )
    # The path argument must point at an existing SKILL.md on disk.
    for name, path in ctx.skills:
        assert Path(path).is_file(), f"register_skill({name!r}, {path!r}) — path does not exist"
        assert Path(path).name == "SKILL.md"


def test_register_only_registers_skills_with_a_skill_md(tmp_path, monkeypatch):
    """A directory under skills/ without a SKILL.md must be ignored."""
    plugin = load_plugin()
    # Build a fake skills tree: one valid, one missing SKILL.md.
    fake_skills = tmp_path / "skills"
    valid = fake_skills / "valid-skill"
    valid.mkdir(parents=True)
    (valid / "SKILL.md").write_text("---\nname: valid-skill\ndescription: x\n---\n\nbody")
    incomplete = fake_skills / "incomplete-skill"
    incomplete.mkdir(parents=True)  # no SKILL.md
    monkeypatch.setattr(plugin, "SKILLS_DIR", fake_skills)

    ctx = FakeContext()
    plugin.register(ctx)

    names = [name for name, _ in ctx.skills]
    assert "valid-skill" in names
    assert "incomplete-skill" not in names


def test_plugin_ships_calendar_queries_skill():
    skill_path = ROOT / "skills" / "calendar-queries" / "SKILL.md"
    assert skill_path.is_file(), "calendar-queries skill must ship with the plugin"
    content = skill_path.read_text(encoding="utf-8")
    # Sanity floor: a meaningful skill is at least a few hundred bytes.
    assert len(content) > 500, "SKILL.md is suspiciously short"
    # The skill must remain generic so anyone can use it.
    lower = content.lower()
    assert "kenneth" not in lower
    assert "hermy" not in lower
    assert "copenhagen" not in lower
    # And it must mention each tool at least once so an agent loading it
    # learns about the full surface.
    for name in EXPECTED_TOOLS:
        assert name in content, f"SKILL.md never mentions {name}"


def test_skill_md_has_frontmatter():
    skill_path = ROOT / "skills" / "calendar-queries" / "SKILL.md"
    content = skill_path.read_text(encoding="utf-8")
    assert content.startswith("---\n"), "SKILL.md must start with YAML frontmatter"
    # Frontmatter sits between the first two --- delimiters.
    frontmatter = content.split("---", 2)[1]
    assert "name: calendar-queries" in frontmatter
    assert "description:" in frontmatter

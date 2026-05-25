"""Handler tests for the icalendar Hermes plugin."""

import importlib.util
import json
import subprocess
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]


def load_tools():
    spec = importlib.util.spec_from_file_location("icalendar_tools_test", ROOT / "tools.py")
    assert spec is not None
    module = importlib.util.module_from_spec(spec)
    assert spec.loader is not None
    spec.loader.exec_module(module)
    return module


def force_binary(monkeypatch, tools):
    """Bypass the three-tier binary resolution dance.

    Tests that exercise handler/subprocess behavior shouldn't depend on
    whether a real binary happens to be installed on the test machine.
    """
    monkeypatch.setattr(tools, "_resolve_binary", lambda: tools.ICALENDAR_BINARY)


class Completed:
    def __init__(self, stdout='{"ok":true}', stderr="", returncode=0):
        self.stdout = stdout
        self.stderr = stderr
        self.returncode = returncode


def test_status_uses_json_before_subcommand(monkeypatch):
    tools = load_tools()
    force_binary(monkeypatch, tools)
    calls = []

    def fake_run(cmd, **kwargs):
        calls.append((cmd, kwargs))
        return Completed('{"calendars":7}')

    monkeypatch.setattr(tools.subprocess, "run", fake_run)

    assert json.loads(tools.handle_status({})) == {"calendars": 7}
    cmd, kwargs = calls[0]
    assert cmd == [tools.ICALENDAR_BINARY, "--json", "status"]
    assert kwargs["shell"] is False
    assert kwargs["capture_output"] is True
    assert kwargs["text"] is True
    assert kwargs["check"] is False


def test_query_global_flags_come_before_subcommand(monkeypatch):
    tools = load_tools()
    force_binary(monkeypatch, tools)
    calls = []

    def fake_run(cmd, **kwargs):
        calls.append(cmd)
        return Completed('{"events":[]}')

    monkeypatch.setattr(tools.subprocess, "run", fake_run)

    tools.handle_today({"calendar": "Private", "no_sync": True})

    assert calls[0] == [
        tools.ICALENDAR_BINARY,
        "--json",
        "--calendar",
        "Private",
        "--no-sync",
        "today",
    ]


def test_sync_ignores_calendar_and_no_sync(monkeypatch):
    tools = load_tools()
    force_binary(monkeypatch, tools)
    calls = []

    def fake_run(cmd, **kwargs):
        calls.append((cmd, kwargs))
        return Completed('{"ok":true}')

    monkeypatch.setattr(tools.subprocess, "run", fake_run)

    tools.handle_sync({"full": True, "calendar": "Private", "no_sync": True})

    cmd, kwargs = calls[0]
    assert cmd == [tools.ICALENDAR_BINARY, "--json", "sync", "--full"]
    assert kwargs["timeout"] == tools._SYNC_TIMEOUT_SECONDS


def test_range_maps_from_date_and_to_date_to_cli_flags(monkeypatch):
    tools = load_tools()
    force_binary(monkeypatch, tools)
    calls = []

    def fake_run(cmd, **kwargs):
        calls.append(cmd)
        return Completed('{"events":[]}')

    monkeypatch.setattr(tools.subprocess, "run", fake_run)

    tools.handle_range({"from_date": "2026-05-24", "to_date": "2026-05-25"})

    assert calls[0] == [
        tools.ICALENDAR_BINARY,
        "--json",
        "range",
        "--from",
        "2026-05-24",
        "--to",
        "2026-05-25",
    ]


def test_search_maps_query_field_calendar_and_no_sync(monkeypatch):
    tools = load_tools()
    force_binary(monkeypatch, tools)
    calls = []

    def fake_run(cmd, **kwargs):
        calls.append(cmd)
        return Completed('{"events":[]}')

    monkeypatch.setattr(tools.subprocess, "run", fake_run)

    tools.handle_search(
        {"query": "Atlas", "field": "title", "calendar": "Private", "no_sync": True}
    )

    assert calls[0] == [
        tools.ICALENDAR_BINARY,
        "--json",
        "--calendar",
        "Private",
        "--no-sync",
        "search",
        "--field",
        "title",
        "Atlas",
    ]


def test_show_maps_id_and_no_sync(monkeypatch):
    tools = load_tools()
    force_binary(monkeypatch, tools)
    calls = []

    def fake_run(cmd, **kwargs):
        calls.append(cmd)
        return Completed('{"event":{}}')

    monkeypatch.setattr(tools.subprocess, "run", fake_run)

    tools.handle_show({"id": "event-123", "no_sync": True})

    assert calls[0] == [tools.ICALENDAR_BINARY, "--json", "--no-sync", "show", "event-123"]


def test_invalid_json_returns_wrapper_error(monkeypatch):
    tools = load_tools()
    force_binary(monkeypatch, tools)

    def fake_run(cmd, **kwargs):
        return Completed("not json", "bad", 0)

    monkeypatch.setattr(tools.subprocess, "run", fake_run)

    out = json.loads(tools.handle_status({}))
    assert out["ok"] is False
    assert out["error"]["code"] == "invalid_json"


def test_no_stdout_returns_subprocess_error(monkeypatch):
    tools = load_tools()
    force_binary(monkeypatch, tools)

    def fake_run(cmd, **kwargs):
        return Completed("", "boom", 2)

    monkeypatch.setattr(tools.subprocess, "run", fake_run)

    out = json.loads(tools.handle_status({}))
    assert out["ok"] is False
    assert out["error"]["code"] == "subprocess_error"
    assert "boom" in out["error"]["message"]


def test_timeout_returns_timeout_error(monkeypatch):
    tools = load_tools()
    force_binary(monkeypatch, tools)

    def fake_run(cmd, **kwargs):
        raise subprocess.TimeoutExpired(cmd, kwargs["timeout"])

    monkeypatch.setattr(tools.subprocess, "run", fake_run)

    out = json.loads(tools.handle_status({}))
    assert out["ok"] is False
    assert out["error"]["code"] == "timeout"
    assert f"{tools._TIMEOUT_SECONDS}s" in out["error"]["message"]


def test_missing_binary_returns_binary_not_found(monkeypatch):
    tools = load_tools()
    force_binary(monkeypatch, tools)

    def fake_run(cmd, **kwargs):
        raise FileNotFoundError()

    monkeypatch.setattr(tools.subprocess, "run", fake_run)

    out = json.loads(tools.handle_status({}))
    assert out["ok"] is False
    assert out["error"]["code"] == "binary_not_found"


def test_safe_handler_catches_arbitrary_exception():
    tools = load_tools()

    def boom(params, **kwargs):
        raise RuntimeError("synthetic")

    wrapped = tools._safe_handler(boom)
    out = json.loads(wrapped({}))
    assert out["ok"] is False
    assert out["error"]["code"] == "handler_error"
    assert "RuntimeError" in out["error"]["message"]
    assert "synthetic" in out["error"]["message"]
    assert "boom" in out["error"]["message"]


def test_safe_handler_preserves_function_name():
    tools = load_tools()

    def some_handler(params, **kwargs):
        return "{}"

    wrapped = tools._safe_handler(some_handler)
    assert wrapped.__name__ == "some_handler"


def test_handle_range_missing_required_param_returns_handler_error(monkeypatch):
    tools = load_tools()
    force_binary(monkeypatch, tools)
    called = []

    def fake_run(*args, **kwargs):
        called.append(True)
        return Completed()

    monkeypatch.setattr(tools.subprocess, "run", fake_run)

    # No subprocess call should happen — KeyError on params["from_date"]
    # fires inside the handler before subprocess.run is ever reached.
    out = json.loads(tools.handle_range({}))
    assert out["ok"] is False
    assert out["error"]["code"] == "handler_error"
    assert "handle_range" in out["error"]["message"]
    assert called == []


def test_handle_show_missing_id_returns_handler_error(monkeypatch):
    tools = load_tools()
    force_binary(monkeypatch, tools)
    called = []

    def fake_run(*args, **kwargs):
        called.append(True)
        return Completed()

    monkeypatch.setattr(tools.subprocess, "run", fake_run)

    out = json.loads(tools.handle_show({}))
    assert out["ok"] is False
    assert out["error"]["code"] == "handler_error"
    assert called == []


# --- binary resolution / download path -------------------------------------


def test_detect_target_linux_amd64(monkeypatch):
    tools = load_tools()
    monkeypatch.setattr(tools.platform, "system", lambda: "Linux")
    monkeypatch.setattr(tools.platform, "machine", lambda: "x86_64")
    assert tools._detect_target() == ("linux", "amd64")


def test_detect_target_linux_arm64(monkeypatch):
    tools = load_tools()
    monkeypatch.setattr(tools.platform, "system", lambda: "Linux")
    monkeypatch.setattr(tools.platform, "machine", lambda: "aarch64")
    assert tools._detect_target() == ("linux", "arm64")


def test_detect_target_darwin_arm64(monkeypatch):
    tools = load_tools()
    monkeypatch.setattr(tools.platform, "system", lambda: "Darwin")
    monkeypatch.setattr(tools.platform, "machine", lambda: "arm64")
    assert tools._detect_target() == ("darwin", "arm64")


def test_detect_target_unsupported_returns_none(monkeypatch):
    tools = load_tools()
    monkeypatch.setattr(tools.platform, "system", lambda: "FreeBSD")
    monkeypatch.setattr(tools.platform, "machine", lambda: "amd64")
    assert tools._detect_target() is None


def test_resolve_binary_prefers_cached_path(monkeypatch, tmp_path):
    tools = load_tools()
    cached = tmp_path / "cached" / "icalendar"
    cached.parent.mkdir(parents=True)
    cached.write_text("#!/bin/sh\n")
    monkeypatch.setattr(tools, "_BINARY_PATH", str(cached))
    assert tools._resolve_binary() == str(cached)


def test_resolve_binary_prefers_bundled_over_user(monkeypatch, tmp_path):
    tools = load_tools()
    bundled = tmp_path / "bundled" / "icalendar"
    bundled.parent.mkdir(parents=True)
    bundled.write_text("#!/bin/sh\n")
    user = tmp_path / "user" / "icalendar"
    user.parent.mkdir(parents=True)
    user.write_text("#!/bin/sh\n")
    monkeypatch.setattr(tools, "_BINARY_PATH", None)
    monkeypatch.setattr(tools, "BUNDLED_BIN", bundled)
    monkeypatch.setattr(tools, "USER_BIN", user)
    assert tools._resolve_binary() == str(bundled)


def test_resolve_binary_falls_back_to_user_bin(monkeypatch, tmp_path):
    tools = load_tools()
    bundled_missing = tmp_path / "bundled" / "icalendar"  # never created
    user = tmp_path / "user" / "icalendar"
    user.parent.mkdir(parents=True)
    user.write_text("#!/bin/sh\n")
    monkeypatch.setattr(tools, "_BINARY_PATH", None)
    monkeypatch.setattr(tools, "BUNDLED_BIN", bundled_missing)
    monkeypatch.setattr(tools, "USER_BIN", user)
    assert tools._resolve_binary() == str(user)


def test_resolve_binary_triggers_download_when_no_binary(monkeypatch, tmp_path):
    tools = load_tools()
    bundled_missing = tmp_path / "bundled" / "icalendar"
    user_missing = tmp_path / "user" / "icalendar"
    monkeypatch.setattr(tools, "_BINARY_PATH", None)
    monkeypatch.setattr(tools, "BUNDLED_BIN", bundled_missing)
    monkeypatch.setattr(tools, "USER_BIN", user_missing)

    called = []

    def fake_download():
        called.append(True)
        return "/downloaded/path/icalendar"

    monkeypatch.setattr(tools, "_download_binary", fake_download)
    assert tools._resolve_binary() == "/downloaded/path/icalendar"
    assert called == [True]


def test_release_asset_name_matches_workflow_asset_names(monkeypatch):
    tools = load_tools()
    monkeypatch.setattr(tools.platform, "system", lambda: "Linux")
    monkeypatch.setattr(tools.platform, "machine", lambda: "x86_64")
    assert tools._release_asset_name() == "icalendar-linux-amd64"


def test_download_binary_falls_back_to_gh_when_urllib_fails(monkeypatch, tmp_path):
    tools = load_tools()
    bundled = tmp_path / "bin" / "icalendar"
    monkeypatch.setattr(tools, "BUNDLED_BIN", bundled)
    monkeypatch.setattr(tools, "_release_asset_name", lambda: "icalendar-linux-amd64")
    calls = []

    def fail_urllib(asset):
        calls.append(("urllib", asset))
        raise RuntimeError("HTTP Error 404: Not Found")

    def fake_gh(asset):
        calls.append(("gh", asset))
        bundled.parent.mkdir(parents=True, exist_ok=True)
        bundled.write_text("#!/bin/sh\n")
        return str(bundled)

    monkeypatch.setattr(tools, "_download_binary_with_urllib", fail_urllib)
    monkeypatch.setattr(tools, "_download_binary_with_gh", fake_gh)

    assert tools._download_binary() == str(bundled)
    assert calls == [
        ("urllib", "icalendar-linux-amd64"),
        ("gh", "icalendar-linux-amd64"),
    ]


def test_download_binary_with_gh_downloads_and_verifies(monkeypatch, tmp_path):
    tools = load_tools()
    bundled = tmp_path / "plugin" / "bin" / "icalendar"
    bundled.parent.mkdir(parents=True)
    monkeypatch.setattr(tools, "BUNDLED_BIN", bundled)
    monkeypatch.setattr(tools.shutil, "which", lambda name: "/usr/bin/gh")
    calls = []

    class FakeCompleted:
        returncode = 0
        stdout = ""
        stderr = ""

    def fake_run(cmd, **kwargs):
        calls.append((cmd, kwargs))
        out_dir = Path(cmd[cmd.index("--dir") + 1])
        asset = out_dir / "icalendar-linux-amd64"
        asset.write_bytes(b"binary")
        digest = tools.hashlib.sha256(b"binary").hexdigest()
        (out_dir / "icalendar-linux-amd64.sha256").write_text(
            f"{digest}  icalendar-linux-amd64\n"
        )
        return FakeCompleted()

    monkeypatch.setattr(tools.subprocess, "run", fake_run)

    assert tools._download_binary_with_gh("icalendar-linux-amd64") == str(bundled)
    assert bundled.read_bytes() == b"binary"
    assert bundled.stat().st_mode & 0o111
    cmd, kwargs = calls[0]
    assert cmd[:4] == ["/usr/bin/gh", "release", "download", f"v{tools.PLUGIN_VERSION}"]
    assert "--repo" in cmd
    assert "addvanced/icloud-calendar" in cmd
    assert kwargs["shell"] is False


def test_check_icalendar_available_true_when_user_bin_exists(monkeypatch, tmp_path):
    tools = load_tools()
    user = tmp_path / "user" / "icalendar"
    user.parent.mkdir(parents=True)
    user.write_text("#!/bin/sh\n")
    monkeypatch.setattr(tools, "BUNDLED_BIN", tmp_path / "missing-bundled")
    monkeypatch.setattr(tools, "USER_BIN", user)
    assert tools.check_icalendar_available() is True


def test_check_icalendar_available_true_on_supported_platform_without_binary(
    monkeypatch, tmp_path
):
    tools = load_tools()
    monkeypatch.setattr(tools, "BUNDLED_BIN", tmp_path / "missing-bundled")
    monkeypatch.setattr(tools, "USER_BIN", tmp_path / "missing-user")
    monkeypatch.setattr(tools.platform, "system", lambda: "Linux")
    monkeypatch.setattr(tools.platform, "machine", lambda: "x86_64")
    assert tools.check_icalendar_available() is True


def test_check_icalendar_available_false_on_unsupported_platform_without_binary(
    monkeypatch, tmp_path
):
    tools = load_tools()
    monkeypatch.setattr(tools, "BUNDLED_BIN", tmp_path / "missing-bundled")
    monkeypatch.setattr(tools, "USER_BIN", tmp_path / "missing-user")
    monkeypatch.setattr(tools.platform, "system", lambda: "FreeBSD")
    monkeypatch.setattr(tools.platform, "machine", lambda: "riscv64")
    assert tools.check_icalendar_available() is False


def test_run_icalendar_returns_binary_unavailable_on_resolve_failure(monkeypatch):
    tools = load_tools()

    def boom():
        raise RuntimeError("unsupported platform: Plan9/m68k")

    monkeypatch.setattr(tools, "_resolve_binary", boom)
    out = json.loads(tools.handle_status({}))
    assert out["ok"] is False
    assert out["error"]["code"] == "binary_unavailable"
    assert "Plan9/m68k" in out["error"]["message"]

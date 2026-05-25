"""Tool handlers for the icalendar Hermes plugin.

Thin subprocess wrappers around the Go icalendar binary. All CalDAV, cache,
validation, and formatting logic lives in the Go CLI; this module only maps
Hermes tool parameters to CLI flags and validates JSON stdout.

Binary resolution is three-tier:

1. `{plugin_dir}/bin/icalendar` — auto-downloaded from GitHub Releases on
   first use. Self-contained: removing the plugin removes the binary.
2. `~/bin/icalendar` — developer build via `make install`. Used when the
   bundled binary isn't present.
3. GitHub Release download — fallback that makes `hermes plugins install
   addvanced/icloud-calendar` work on machines without Go.
"""

import hashlib
import json
import platform
import shutil
import subprocess
import tempfile
import urllib.request
from pathlib import Path
from typing import Optional

PLUGIN_DIR = Path(__file__).resolve().parent
BUNDLED_BIN = PLUGIN_DIR / "bin" / "icalendar"
USER_BIN = Path.home() / "bin" / "icalendar"

# Back-compat alias. Equals USER_BIN. Older tests and external code may still
# reference this name; new code should call _resolve_binary() instead.
ICALENDAR_BINARY = str(USER_BIN)

PLUGIN_VERSION = "1.0.0"  # keep in sync with plugin.yaml
GITHUB_REPO = "addvanced/icloud-calendar"

_TIMEOUT_SECONDS = 30
_SYNC_TIMEOUT_SECONDS = 120
_DOWNLOAD_TIMEOUT_SECONDS = 60

# Cached binary path after first resolution. Tests can preset this to skip
# the resolution dance entirely.
_BINARY_PATH: Optional[str] = None

# Mapping from (platform.system(), platform.machine()) — both lower-cased —
# to the (GOOS, GOARCH) pair used in our release asset names.
_SUPPORTED_TARGETS = {
    ("linux", "x86_64"): ("linux", "amd64"),
    ("linux", "amd64"): ("linux", "amd64"),
    ("linux", "aarch64"): ("linux", "arm64"),
    ("linux", "arm64"): ("linux", "arm64"),
    ("darwin", "x86_64"): ("darwin", "amd64"),
    ("darwin", "arm64"): ("darwin", "arm64"),
}


def _detect_target():
    """Return (goos, goarch) for the current platform, or None if unsupported."""
    key = (platform.system().lower(), platform.machine().lower())
    return _SUPPORTED_TARGETS.get(key)


def _error(code: str, message: str) -> str:
    return json.dumps(
        {
            "ok": False,
            "data": None,
            "error": {
                "code": code,
                "message": message,
            },
        },
        ensure_ascii=False,
    )


def _safe_handler(fn):
    """Wrap a handler so any unexpected exception becomes an error-JSON string.

    The Hermes plugin guide requires that tool handlers never raise; they must
    always return a JSON string. Subprocess-level errors are already mapped to
    specific codes inside _run_icalendar. This decorator catches anything that
    escapes — typically KeyError on a missing required parameter, or surprise
    exceptions from upstream changes — and returns a uniform error JSON.
    """

    def wrapper(params, **kwargs):
        try:
            return fn(params, **kwargs)
        except Exception as exc:
            return _error(
                "handler_error",
                f"{fn.__name__} failed: {type(exc).__name__}: {exc}",
            )

    wrapper.__name__ = fn.__name__
    wrapper.__doc__ = fn.__doc__
    return wrapper


def _release_asset_name() -> str:
    """Return the GitHub Release asset name for the current platform."""
    target = _detect_target()
    if target is None:
        raise RuntimeError(
            f"Unsupported platform: {platform.system()}/{platform.machine()}. "
            f"Supported: linux/amd64, linux/arm64, darwin/amd64, darwin/arm64. "
            f"Build from source: https://github.com/{GITHUB_REPO}"
        )
    goos, goarch = target
    return f"icalendar-{goos}-{goarch}"


def _verify_and_install_download(tmp_path: Path, sha_path: Path, asset: str) -> str:
    """Verify a downloaded binary against its SHA256 sidecar and install it."""
    expected = sha_path.read_text().strip().split()[0]
    actual = hashlib.sha256(tmp_path.read_bytes()).hexdigest()
    if expected.lower() != actual.lower():
        raise RuntimeError(
            f"SHA256 mismatch for {asset}: expected {expected}, got {actual}"
        )
    tmp_path.chmod(0o755)
    tmp_path.replace(BUNDLED_BIN)
    return str(BUNDLED_BIN)


def _download_binary_with_urllib(asset: str) -> str:
    """Download a public GitHub Release asset using urllib."""
    base = f"https://github.com/{GITHUB_REPO}/releases/download/v{PLUGIN_VERSION}"
    bin_url = f"{base}/{asset}"
    sha_url = f"{base}/{asset}.sha256"

    tmp_fd = tempfile.NamedTemporaryFile(
        delete=False, dir=str(BUNDLED_BIN.parent), prefix=".dl-"
    )
    tmp_path = Path(tmp_fd.name)
    tmp_fd.close()
    sha_path = BUNDLED_BIN.parent / f".{asset}.sha256"
    try:
        with urllib.request.urlopen(  # noqa: S310 — URL is pinned to our own release
            bin_url, timeout=_DOWNLOAD_TIMEOUT_SECONDS
        ) as resp:
            tmp_path.write_bytes(resp.read())

        with urllib.request.urlopen(  # noqa: S310
            sha_url, timeout=_DOWNLOAD_TIMEOUT_SECONDS
        ) as resp:
            sha_path.write_bytes(resp.read())
        return _verify_and_install_download(tmp_path, sha_path, asset)
    finally:
        tmp_path.unlink(missing_ok=True)
        sha_path.unlink(missing_ok=True)


def _download_binary_with_gh(asset: str) -> str:
    """Download a GitHub Release asset via authenticated gh CLI.

    Private repositories return 404 for unauthenticated browser_download_url
    requests. Hermes can still install/update private plugins when `gh` is
    authenticated, so use it as a fallback before surfacing the urllib error.
    """
    gh = shutil.which("gh")
    if gh is None:
        raise RuntimeError("gh CLI is not installed")

    with tempfile.TemporaryDirectory(dir=str(BUNDLED_BIN.parent)) as tmp_dir:
        cmd = [
            gh,
            "release",
            "download",
            f"v{PLUGIN_VERSION}",
            "--repo",
            GITHUB_REPO,
            "--pattern",
            f"{asset}*",
            "--dir",
            tmp_dir,
        ]
        result = subprocess.run(
            cmd,
            capture_output=True,
            text=True,
            timeout=_DOWNLOAD_TIMEOUT_SECONDS,
            check=False,
            shell=False,
        )
        if result.returncode != 0:
            stderr = result.stderr.strip() or result.stdout.strip()
            raise RuntimeError(f"gh release download failed: {stderr}")
        tmp_path = Path(tmp_dir) / asset
        sha_path = Path(tmp_dir) / f"{asset}.sha256"
        if not tmp_path.is_file() or not sha_path.is_file():
            raise RuntimeError(f"gh release download did not fetch {asset} and sidecar")
        return _verify_and_install_download(tmp_path, sha_path, asset)


def _download_binary() -> str:
    """Download the binary matching this platform from GitHub Releases.

    Verifies SHA256 against the .sha256 sidecar file before installing.
    Public releases are fetched via urllib. Private repository releases return
    404 without authentication, so we fall back to authenticated `gh` when
    available.
    """
    asset = _release_asset_name()
    BUNDLED_BIN.parent.mkdir(parents=True, exist_ok=True)
    try:
        return _download_binary_with_urllib(asset)
    except Exception as urllib_exc:
        try:
            return _download_binary_with_gh(asset)
        except Exception as gh_exc:
            raise RuntimeError(
                f"failed to download {asset}: urllib={urllib_exc}; gh={gh_exc}"
            ) from gh_exc


def _resolve_binary() -> str:
    """Return path to the icalendar binary, downloading on first call if needed.

    The resolved path is cached in module-global _BINARY_PATH. Tests can
    preset that variable to bypass resolution entirely.
    """
    global _BINARY_PATH
    if _BINARY_PATH and Path(_BINARY_PATH).is_file():
        return _BINARY_PATH
    if BUNDLED_BIN.is_file():
        _BINARY_PATH = str(BUNDLED_BIN)
        return _BINARY_PATH
    if USER_BIN.is_file():
        _BINARY_PATH = str(USER_BIN)
        return _BINARY_PATH
    _BINARY_PATH = _download_binary()
    return _BINARY_PATH


def check_icalendar_available() -> bool:
    """Return True if the binary is installed, or we can download one.

    Hermes calls this to decide whether to enable each tool. We allow the
    tool to register as long as the platform is supported — the first
    handler call will trigger the actual download.
    """
    if BUNDLED_BIN.is_file() or USER_BIN.is_file():
        return True
    return _detect_target() is not None


def _run_icalendar(args, timeout=_TIMEOUT_SECONDS):
    """Run icalendar with --json and return raw JSON output."""
    try:
        binary = _resolve_binary()
    except Exception as exc:
        return _error("binary_unavailable", str(exc))

    cmd = [binary, *args]
    try:
        result = subprocess.run(
            cmd,
            capture_output=True,
            text=True,
            timeout=timeout,
            check=False,
            shell=False,
        )
    except subprocess.TimeoutExpired:
        return _error("timeout", f"icalendar command timed out after {timeout}s")
    except FileNotFoundError:
        return _error(
            "binary_not_found", f"icalendar binary not found at {binary}"
        )
    except OSError as exc:
        return _error("subprocess_error", f"failed to run icalendar: {exc}")

    stdout = result.stdout.strip()
    if stdout:
        try:
            json.loads(stdout)
        except json.JSONDecodeError as exc:
            return _error(
                "invalid_json",
                f"icalendar returned invalid JSON: {exc}; stderr={result.stderr.strip()}",
            )
        return stdout

    stderr = result.stderr.strip()
    return _error(
        "subprocess_error",
        f"icalendar exited {result.returncode} with no stdout: {stderr}",
    )


def _root_args(params, include_calendar=True, include_no_sync=True):
    args = ["--json"]
    if include_calendar and params.get("calendar"):
        args.extend(["--calendar", params["calendar"]])
    if include_no_sync and params.get("no_sync"):
        args.append("--no-sync")
    return args


def _query_args(params, subcommand):
    return [*_root_args(params), subcommand]


@_safe_handler
def handle_status(params, **kwargs):
    del params, kwargs
    return _run_icalendar(["--json", "status"])


@_safe_handler
def handle_sync(params, **kwargs):
    del kwargs
    args = ["--json", "sync"]
    if params.get("full"):
        args.append("--full")
    return _run_icalendar(args, timeout=_SYNC_TIMEOUT_SECONDS)


@_safe_handler
def handle_list_calendars(params, **kwargs):
    del kwargs
    return _run_icalendar(
        [*_root_args(params, include_calendar=False), "list-calendars"]
    )


@_safe_handler
def handle_today(params, **kwargs):
    del kwargs
    return _run_icalendar(_query_args(params, "today"))


@_safe_handler
def handle_tomorrow(params, **kwargs):
    del kwargs
    return _run_icalendar(_query_args(params, "tomorrow"))


@_safe_handler
def handle_week(params, **kwargs):
    del kwargs
    return _run_icalendar(_query_args(params, "week"))


@_safe_handler
def handle_next_week(params, **kwargs):
    del kwargs
    return _run_icalendar(_query_args(params, "next-week"))


@_safe_handler
def handle_range(params, **kwargs):
    del kwargs
    args = [
        *_root_args(params),
        "range",
        "--from",
        params["from_date"],
        "--to",
        params["to_date"],
    ]
    return _run_icalendar(args)


@_safe_handler
def handle_search(params, **kwargs):
    del kwargs
    args = [*_root_args(params), "search"]
    if params.get("field"):
        args.extend(["--field", params["field"]])
    args.append(params["query"])
    return _run_icalendar(args)


@_safe_handler
def handle_show(params, **kwargs):
    del kwargs
    args = [*_root_args(params, include_calendar=False), "show", params["id"]]
    return _run_icalendar(args)

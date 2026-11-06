#!/usr/bin/env python3
"""Exercise an installed CLI, native window and its HTTP MCP over a fixture."""

import argparse
import ctypes
import json
import os
from pathlib import Path
import socket
import signal
import subprocess
import tempfile
import time
import urllib.request
import xml.etree.ElementTree as ET


def check_package(desktop, cli, repo, version, work, native_ui=False):
    dump = work / "watch"
    for folder in ("Apps", "Settings", "Activity", "Music"):
        (dump / "GARMIN" / folder).mkdir(parents=True, exist_ok=True)
    fixtures = {
        "testdump/testdata/GarminDevice.xml": "GarminDevice.xml",
        "testdump/testdata/OUT.BIN": "Apps/OUT.BIN",
        "fit/testdata/Settings.fit": "Settings/Settings.fit",
    }
    for source, target in fixtures.items():
        (dump / "GARMIN" / target).write_bytes((repo / "internal" / source).read_bytes())
    model = ET.parse(dump / "GARMIN/GarminDevice.xml").findtext("{*}Model/{*}Description")
    cli_version = subprocess.check_output([str(cli), "version"], text=True, timeout=10).strip()
    assert version in cli_version, cli_version
    port = free_port()
    base = f"http://127.0.0.1:{port}"
    headers = {"Accept": "application/json, text/event-stream", "Content-Type": "application/json"}

    def request(path, method="GET", data=None):
        body = None if data is None else json.dumps(data).encode()
        req = urllib.request.Request(base + path, data=body, headers=headers, method=method)
        with urllib.request.urlopen(req, timeout=5) as response:
            raw = response.read()
            return json.loads(raw) if raw else None, response.headers

    def mcp(call_id, method, params):
        result, _ = request("/mcp", "POST", {"jsonrpc": "2.0", "id": call_id, "method": method, "params": params})
        assert "error" not in result, result
        return result["result"]

    log_path = work / "desktop.log"
    env = dict(os.environ, XDG_CONFIG_HOME=str(work / "config"), XDG_CACHE_HOME=str(work / "cache"),
               APPDATA=str(work / "appdata"), LOCALAPPDATA=str(work / "localappdata"))
    with log_path.open("wb") as log:
        app = subprocess.Popen([str(desktop), "-root", str(dump), "-addr", f"127.0.0.1:{port}"], stdout=log, stderr=log, env=env, start_new_session=os.name != "nt")
    try:
        deadline = time.monotonic() + 45
        while True:
            assert app.poll() is None, f"Desktop exited {app.returncode}"
            try:
                server, _ = request("/api/server")
                if "desktop window ready" in log_path.read_text(errors="replace"):
                    break
            except (OSError, ValueError):
                pass
            assert time.monotonic() < deadline, "Native window did not become ready"
            time.sleep(0.25)
        assert server["application"] == "garminctl" and server["version"] == version, server
        initialized, response_headers = request("/mcp", "POST", {
            "jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {
                "protocolVersion": "2025-06-18", "capabilities": {},
                "clientInfo": {"name": "installed-package-smoke", "version": "1"},
            },
        })
        assert "error" not in initialized, initialized
        headers["Mcp-Session-Id"] = response_headers["Mcp-Session-Id"]
        headers["MCP-Protocol-Version"] = initialized["result"]["protocolVersion"]
        request("/mcp", "POST", {"jsonrpc": "2.0", "method": "notifications/initialized", "params": {}})
        tools = mcp(2, "tools/list", {})
        info = mcp(3, "tools/call", {"name": "watch_info", "arguments": {}})
        assert not info.get("isError"), info
        assert info["structuredContent"]["model"] == model, info
        files = mcp(4, "tools/call", {"name": "watch_list_files", "arguments": {"path": "GARMIN"}})
        assert not files.get("isError"), files
        assert files["structuredContent"]["entries"], files
        status, _ = request("/api/mcp")
        assert any(c["name"] == "installed-package-smoke" and c["sessionActive"] for c in status["status"]["clients"]), status
        request("/mcp", "DELETE")
        status, _ = request("/api/mcp")
        assert any(c["name"] == "installed-package-smoke" and not c["sessionActive"] for c in status["status"]["clients"]), status
        check_second_launch(app, desktop, dump, env, work)
        close_window(app)
        try:
            request("/api/server")
        except OSError:
            pass
        else:
            raise AssertionError("App-owned server remained running after normal close")
        stop_app(app)
        check_reused_backend(desktop, cli, dump, port, request, env, work)
        result = {"version": version, "cli": cli_version, "nativeWindowReady": True, "mcpTools": len(tools["tools"]), "fixtureModel": model, "nativeClose": True, "singleInstance": True, "reusedBackendRetained": True}
        if native_ui:
            from native_acceptance import run_native_acceptance
            result["nativeAcceptance"] = run_native_acceptance(desktop, cli, dump, env, work, free_port, stop_app)
        return result
    finally:
        try:
            stop_app(app)
        finally:
            print(log_path.read_text(errors="replace"))


def free_port():
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        return listener.getsockname()[1]


def check_second_launch(first, desktop, dump, env, work):
    """A second instance must exit before starting a backend at a different port."""
    port = free_port()
    log_path = work / "second-instance.log"
    with log_path.open("wb") as log:
        second = subprocess.Popen([str(desktop), "-root", str(dump), "-addr", f"127.0.0.1:{port}"], stdout=log, stderr=log, env=env, start_new_session=os.name != "nt")
    try:
        assert second.wait(timeout=10) == 0, "Second desktop instance failed"
        assert first.poll() is None, "Second launch stopped the primary desktop"
        with socket.socket() as connection:
            connection.settimeout(2)
            assert connection.connect_ex(("127.0.0.1", port)) != 0, "Second launch started another backend"
    finally:
        stop_app(second)
        print(log_path.read_text(errors="replace"))


def check_reused_backend(desktop, cli, dump, port, request, env, work):
    """Closing the native window must leave an independently started server alive."""
    server_log = work / "reused-server.log"
    desktop_log = work / "reused-desktop.log"
    with server_log.open("wb") as log:
        server = subprocess.Popen([str(cli), "-root", str(dump), "serve", "-addr", f"127.0.0.1:{port}"], stdout=log, stderr=log, env=env, start_new_session=os.name != "nt")
    app = None
    try:
        deadline = time.monotonic() + 45
        while True:
            assert server.poll() is None, f"Standalone server exited {server.returncode}"
            try:
                before, _ = request("/api/server")
                break
            except OSError:
                assert time.monotonic() < deadline, "Standalone server did not become ready"
                time.sleep(0.1)
        with desktop_log.open("wb") as log:
            app = subprocess.Popen([str(desktop), "-root", str(dump), "-addr", f"127.0.0.1:{port}"], stdout=log, stderr=log, env=env, start_new_session=os.name != "nt")
        deadline = time.monotonic() + 45
        while "desktop window ready" not in desktop_log.read_text(errors="replace"):
            assert app.poll() is None, f"Reusing desktop exited {app.returncode}"
            assert time.monotonic() < deadline, "Reusing native window did not become ready"
            time.sleep(0.1)
        close_window(app)
        assert server.poll() is None, "Closing the desktop stopped the separately owned server"
        after, _ = request("/api/server")
        assert after == before, (before, after)
    finally:
        try:
            if app is not None:
                stop_app(app)
        finally:
            stop_app(server)
            for path in (desktop_log, server_log):
                if path.exists():
                    print(path.read_text(errors="replace"))


def close_window(app):
    if os.name != "nt":
        # Wails handles SIGTERM through App.Quit, including the frontend veto.
        app.send_signal(signal.SIGTERM)
    else:
        from ctypes import wintypes

        user32 = ctypes.windll.user32
        user32.GetWindowThreadProcessId.argtypes = [wintypes.HWND, ctypes.POINTER(wintypes.DWORD)]
        user32.IsWindowVisible.argtypes = [wintypes.HWND]
        user32.PostMessageW.argtypes = [wintypes.HWND, wintypes.UINT, wintypes.WPARAM, wintypes.LPARAM]
        windows = []
        callback_type = ctypes.WINFUNCTYPE(wintypes.BOOL, wintypes.HWND, wintypes.LPARAM)
        user32.EnumWindows.argtypes = [callback_type, wintypes.LPARAM]
        user32.EnumWindows.restype = wintypes.BOOL
        owned = []

        @callback_type
        def collect(hwnd, _):
            pid = wintypes.DWORD()
            user32.GetWindowThreadProcessId(hwnd, ctypes.byref(pid))
            if pid.value == app.pid:
                owned.append(hwnd)
                if user32.IsWindowVisible(hwnd):
                    windows.append(hwnd)
            return True

        # Frontend Ready can arrive before the native ShowWindow call.
        deadline = time.monotonic() + 10
        while True:
            windows.clear()
            owned.clear()
            assert user32.EnumWindows(collect, 0), "EnumWindows failed"
            if windows or time.monotonic() >= deadline:
                break
            assert app.poll() is None, f"Desktop exited {app.returncode} before its window appeared"
            time.sleep(0.1)
        assert windows, f"No visible native application window for PID {app.pid}; owned handles: {owned}"
        for hwnd in windows:
            assert user32.PostMessageW(hwnd, 0x0010, 0, 0), "WM_CLOSE failed"
    assert app.wait(timeout=10) == 0, "Native close failed"


def stop_app(app):
    """Stop the test application's process group before deleting WebKit caches."""
    try:
        if app.poll() is None:
            if os.name == "nt":
                result = subprocess.run(["taskkill", "/PID", str(app.pid), "/T", "/F"], capture_output=True, text=True, timeout=10)
                if result.returncode and app.poll() is None:
                    raise RuntimeError(result.stdout + result.stderr)
            else:
                app.terminate()
        try:
            app.wait(timeout=5)
        except subprocess.TimeoutExpired:
            app.kill()
            app.wait(timeout=5)
    finally:
        if os.name != "nt":
            try:
                os.killpg(app.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            deadline = time.monotonic() + 5
            while group_running(app.pid):
                if time.monotonic() >= deadline:
                    raise TimeoutError("Desktop child processes did not stop")
                time.sleep(0.01)


def group_running(group):
    """Report Linux process-group members that can still write to the cache."""
    for entry in Path("/proc").iterdir():
        if not entry.name.isdigit():
            continue
        try:
            fields = (entry / "stat").read_text(errors="replace").rsplit(")", 1)[1].split()
        except (FileNotFoundError, ProcessLookupError, PermissionError):
            continue
        if int(fields[2]) == group and fields[0] not in ("Z", "X"):
            return True
    return False


def cleanup_package_directory(directory):
    """Wait briefly for Windows WebView cache handles, retaining cleanup failures."""
    deadline = time.monotonic() + 5
    while True:
        try:
            directory.cleanup()
            return
        except OSError as err:
            if os.name != "nt" or getattr(err, "winerror", None) != 32 or time.monotonic() >= deadline:
                raise
            time.sleep(0.1)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--desktop", required=True, type=Path)
    parser.add_argument("--cli", required=True, type=Path)
    parser.add_argument("--repo", required=True, type=Path)
    parser.add_argument("--version", required=True)
    parser.add_argument("--native-ui", action="store_true", help="Run dialog/edit/origin checks on an isolated CI desktop")
    args = parser.parse_args()
    directory = tempfile.TemporaryDirectory(prefix="garminctl-package-")
    try:
        print(json.dumps(check_package(args.desktop, args.cli, args.repo, args.version, Path(directory.name), args.native_ui), ensure_ascii=False))
    finally:
        cleanup_package_directory(directory)

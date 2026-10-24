#!/usr/bin/env python3
"""Exercise an installed CLI, native window and its HTTP MCP over a fixture."""

import argparse
import ctypes
import json
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import time
import urllib.request
import xml.etree.ElementTree as ET


def check_package(desktop, cli, repo, version, work):
    dump = work / "watch"
    for folder in ("Apps", "Settings", "Activity", "Music"):
        (dump / "GARMIN" / folder).mkdir(parents=True, exist_ok=True)
    fixtures = {
        "garmin/testdata/GarminDevice.xml": "GarminDevice.xml",
        "garmin/testdata/OUT.BIN": "Apps/OUT.BIN",
        "fit/testdata/Settings.fit": "Settings/Settings.fit",
    }
    for source, target in fixtures.items():
        (dump / "GARMIN" / target).write_bytes((repo / "internal" / source).read_bytes())
    model = ET.parse(dump / "GARMIN/GarminDevice.xml").findtext("{*}Model/{*}Description")
    cli_version = subprocess.check_output([str(cli), "version"], text=True, timeout=10).strip()
    assert version in cli_version, cli_version
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        port = listener.getsockname()[1]
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
    env = dict(os.environ, XDG_CONFIG_HOME=str(work / "config"), XDG_CACHE_HOME=str(work / "cache"))
    with log_path.open("wb") as log:
        app = subprocess.Popen([str(desktop), "-root", str(dump), "-addr", f"127.0.0.1:{port}"], stdout=log, stderr=log, env=env)
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
        if os.name == "nt":
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
            window_deadline = time.monotonic() + 10
            while True:
                windows.clear()
                owned.clear()
                assert user32.EnumWindows(collect, 0), "EnumWindows failed"
                if windows or time.monotonic() >= window_deadline:
                    break
                assert app.poll() is None, f"Desktop exited {app.returncode} before its window appeared"
                time.sleep(0.1)
            assert windows, f"No visible native application window for PID {app.pid}; owned handles: {owned}"
            for hwnd in windows:
                user32.PostMessageW(hwnd, 0x0010, 0, 0)  # WM_CLOSE
            assert app.wait(timeout=10) == 0, "Native close failed"
            try:
                request("/api/server")
            except OSError:
                pass
            else:
                raise AssertionError("App-owned server remained running after normal close")
        return {"version": version, "cli": cli_version, "nativeWindowReady": True, "mcpTools": len(tools["tools"]), "fixtureModel": model}
    finally:
        if app.poll() is None:
            app.terminate()
            try:
                app.wait(timeout=5)
            except subprocess.TimeoutExpired:
                app.kill()
                app.wait()
        print(log_path.read_text(errors="replace"))


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--desktop", required=True, type=Path)
    parser.add_argument("--cli", required=True, type=Path)
    parser.add_argument("--repo", required=True, type=Path)
    parser.add_argument("--version", required=True)
    args = parser.parse_args()
    with tempfile.TemporaryDirectory(prefix="garminctl-package-") as directory:
        print(json.dumps(check_package(args.desktop, args.cli, args.repo, args.version, Path(directory)), ensure_ascii=False))

"""Regression checks for WebKit children outliving the native test process."""

import os
from pathlib import Path
import runpy
import shutil
import signal
import subprocess
import sys
import tempfile
import textwrap
import time
from types import SimpleNamespace
import unittest
from unittest import mock

package_tests = runpy.run_path(str(Path(__file__).with_name("test-package.py")))
stop_app = package_tests["stop_app"]


class WindowsCacheCleanupTests(unittest.TestCase):
    def cleanup_with(self, directory, platform, ticks):
        cleanup = package_tests["cleanup_package_directory"]
        clock = SimpleNamespace(monotonic=mock.Mock(side_effect=ticks), sleep=mock.Mock())
        with mock.patch.dict(cleanup.__globals__, os=SimpleNamespace(name=platform), time=clock):
            cleanup(directory)
        return clock

    def test_delayed_webview_cache_release_is_retried(self):
        blocked = PermissionError("WebView cache is still open")
        blocked.winerror = 32
        directory = mock.Mock()
        directory.cleanup.side_effect = [blocked, None]
        clock = self.cleanup_with(directory, 'nt', [10, 10.1])
        self.assertEqual(directory.cleanup.call_count, 2)
        clock.sleep.assert_called_once_with(0.1)

    def test_permanent_cache_lock_still_fails(self):
        blocked = PermissionError("WebView cache is still open")
        blocked.winerror = 32
        directory = mock.Mock()
        directory.cleanup.side_effect = blocked
        with self.assertRaises(PermissionError) as caught:
            self.cleanup_with(directory, 'nt', [10, 10.1, 15])
        self.assertIs(caught.exception, blocked)
        self.assertEqual(directory.cleanup.call_count, 2)

    def test_other_cleanup_errors_are_not_retried(self):
        for platform, code in [('nt', 5), ('posix', 32)]:
            with self.subTest(platform=platform, code=code):
                blocked = PermissionError("Not a delayed Windows cache release")
                blocked.winerror = code
                directory = mock.Mock()
                directory.cleanup.side_effect = blocked
                with self.assertRaises(PermissionError):
                    self.cleanup_with(directory, platform, [10])
                directory.cleanup.assert_called_once()


class WindowsProcessCleanupTests(unittest.TestCase):
    def test_forced_cleanup_stops_the_process_tree(self):
        app = mock.Mock(pid=321)
        app.poll.return_value = None
        result = subprocess.CompletedProcess([], 0, '', '')
        runner = mock.Mock(return_value=result)
        platform = SimpleNamespace(run=runner, TimeoutExpired=subprocess.TimeoutExpired)
        with mock.patch.dict(stop_app.__globals__, os=SimpleNamespace(name='nt'), subprocess=platform):
            stop_app(app)
        runner.assert_called_once_with(
            ['taskkill', '/PID', '321', '/T', '/F'],
            capture_output=True, text=True, timeout=10,
        )
        app.terminate.assert_not_called()
        app.kill.assert_not_called()
        app.wait.assert_called_once_with(timeout=5)

    def test_cleanup_failure_is_not_reported_as_success(self):
        app = mock.Mock(pid=321)
        app.poll.return_value = None
        app.wait.side_effect = subprocess.TimeoutExpired('desktop', 5)
        runner = mock.Mock(return_value=subprocess.CompletedProcess([], 1, 'blocked', 'denied'))
        platform = SimpleNamespace(run=runner, TimeoutExpired=subprocess.TimeoutExpired)
        with mock.patch.dict(stop_app.__globals__, os=SimpleNamespace(name='nt'), subprocess=platform):
            with self.assertRaisesRegex(RuntimeError, 'blockeddenied') as caught:
                stop_app(app)
        app.wait.assert_called_once_with(timeout=5)
        self.assertIsInstance(caught.exception.__cause__, subprocess.TimeoutExpired)
        app.kill.assert_not_called()
        runner.assert_called_once()

    def test_nonzero_taskkill_waits_for_delayed_parent_exit(self):
        app = mock.Mock(pid=321)
        app.poll.return_value = None
        app.wait.return_value = 0
        runner = mock.Mock(return_value=subprocess.CompletedProcess(
            [], 1, 'Parent terminated', 'Child has already exited',
        ))
        platform = SimpleNamespace(run=runner, TimeoutExpired=subprocess.TimeoutExpired)
        with mock.patch.dict(stop_app.__globals__, os=SimpleNamespace(name='nt'), subprocess=platform):
            stop_app(app)
        app.wait.assert_called_once_with(timeout=5)
        app.terminate.assert_not_called()
        app.kill.assert_not_called()
        runner.assert_called_once()


@unittest.skipUnless(sys.platform == "linux", "Linux native-window process groups")
class ProcessCleanupTests(unittest.TestCase):
    def test_children_stop_even_after_parent_exits(self):
        child = textwrap.dedent('''
            from pathlib import Path
            import signal, sys, time
            signal.signal(signal.SIGTERM, signal.SIG_IGN)
            cache = Path(sys.argv[1])
            while True:
                try:
                    cache.mkdir(parents=True, exist_ok=True)
                    (cache / "state").write_text(str(time.monotonic()))
                except FileNotFoundError:
                    pass
                time.sleep(0.01)
        ''')
        parent = textwrap.dedent('''
            from pathlib import Path
            import subprocess, sys, time
            subprocess.Popen([sys.executable, "-c", sys.argv[1], sys.argv[2]],
                             stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            deadline = time.monotonic() + 5
            while not (Path(sys.argv[2]) / "state").exists():
                if time.monotonic() >= deadline:
                    raise TimeoutError("Child did not create its cache")
                time.sleep(0.01)
            print("ready", flush=True)
            if sys.argv[3] == "stay":
                time.sleep(60)
        ''')
        for state in ("stay", "exit"):
            with self.subTest(parent=state), tempfile.TemporaryDirectory() as directory:
                cache = Path(directory) / "cache"
                app = subprocess.Popen(
                    [sys.executable, "-c", parent, child, str(cache), state],
                    stdout=subprocess.PIPE, text=True, start_new_session=True,
                )
                try:
                    self.assertEqual(app.stdout.readline().strip(), "ready")
                    if state == "exit":
                        app.wait(timeout=5)
                    stop_app(app)
                    self.assertIsNotNone(app.poll())
                    shutil.rmtree(cache)
                    time.sleep(0.1)
                    self.assertFalse(cache.exists(), "Child recreated the deleted WebKit cache")
                finally:
                    try:
                        os.killpg(app.pid, signal.SIGKILL)
                    except ProcessLookupError:
                        pass
                    app.wait(timeout=5)
                    app.stdout.close()


if __name__ == "__main__":
    unittest.main()

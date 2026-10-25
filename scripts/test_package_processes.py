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
import unittest

stop_app = runpy.run_path(str(Path(__file__).with_name("test-package.py")))["stop_app"]


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

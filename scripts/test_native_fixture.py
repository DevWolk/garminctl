"""Network-level checks for the CI fixture proxy and command receipts."""

from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import threading
import unittest
import urllib.error
import urllib.request

from native_fixture import Commands, fixture_proxy


class NativeFixtureTests(unittest.TestCase):
    def test_channel_preserves_server_errors_and_injects_only_html(self):
        seen = []

        class Backend(BaseHTTPRequestHandler):
            def log_message(self, *_):
                pass

            def do_GET(self):
                seen.append((self.path, self.headers['Host'], self.headers.get('Origin')))
                if self.path == '/settings':
                    body = b'<html><body>real SPA</body></html>'
                    self.send_response(200)
                    self.send_header('Content-Type', 'text/html')
                else:
                    body = b'{"error":"fixture failure"}'
                    self.send_response(409)
                    self.send_header('Content-Type', 'application/json')
                self.send_header('Content-Length', str(len(body)))
                self.end_headers()
                self.wfile.write(body)

        backend = ThreadingHTTPServer(('127.0.0.1', 0), Backend)
        worker = threading.Thread(target=backend.serve_forever, daemon=True)
        worker.start()
        try:
            with fixture_proxy(backend.server_port, Commands()) as port:
                base = f'http://127.0.0.1:{port}'
                request = urllib.request.Request(base + '/settings', headers={'Origin': 'wails://localhost'})
                with urllib.request.urlopen(request, timeout=5) as response:
                    html = response.read()
                    self.assertEqual(len(html), int(response.headers['Content-Length']))
                    self.assertIn(b'<body>real SPA', html)
                    self.assertEqual(html.count(b'/__native/probe.js'), 1)
                with self.assertRaises(urllib.error.HTTPError) as raised:
                    urllib.request.urlopen(base + '/error', timeout=5)
                error = raised.exception
                with error:
                    self.assertEqual(error.code, 409)
                    self.assertEqual(error.read(), b'{"error":"fixture failure"}')
                self.assertTrue(all(host == f'127.0.0.1:{backend.server_port}' and origin is None for _, host, origin in seen))
        finally:
            backend.shutdown()
            backend.server_close()
            worker.join(timeout=5)

    def test_real_http_receipts_and_browser_errors_reach_the_waiter(self):
        commands = Commands()
        with fixture_proxy(1, commands) as port:
            base = f'http://127.0.0.1:{port}'

            def request(path, value=None):
                payload = None if value is None else json.dumps(value).encode()
                with urllib.request.urlopen(base + path, data=payload, timeout=5) as response:
                    body = response.read()
                    return response.status, json.loads(body) if body else None

            self.assertEqual(request('/__native/next'), (204, None))
            request('/__native/ready', {'boot': 'first-page'})
            self.assertEqual(commands.wait(lambda: commands.boots[0]), 'first-page')
            identifier = commands.send('snapshot')
            status, command = request('/__native/next')
            self.assertEqual(status, 200)
            self.assertEqual(command, {'id': identifier, 'action': 'snapshot'})
            request('/__native/result', {'id': identifier, 'value': {'pending': True}})
            self.assertEqual(commands.wait(lambda: commands.results.pop(identifier, None))['value'], {'pending': True})

            def browser():
                _, command = request('/__native/next')
                while command is None:
                    _, command = request('/__native/next')
                request('/__native/result', {'id': command['id'], 'error': 'real page assertion failed'})

            thread = threading.Thread(target=browser)
            thread.start()
            with self.assertRaisesRegex(AssertionError, 'real page assertion failed'):
                commands.call('edit')
            thread.join(timeout=5)
            self.assertFalse(thread.is_alive())

    def test_missing_receipt_is_a_failure(self):
        commands = Commands()
        with self.assertRaises(TimeoutError):
            commands.wait(lambda: None, timeout=0)


if __name__ == '__main__':
    unittest.main()

"""Network-level checks for the CI fixture proxy and command receipts."""

from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import ctypes
import http.client
import json
from pathlib import Path
import threading
import unittest
import urllib.error
import urllib.request

from native_fixture import Commands, PROBE_LOADER, fixture_proxy


class NativeFixtureTests(unittest.TestCase):
    def test_browser_module_syntax_with_headless_javascriptcore(self):
        engine = ctypes.CDLL('libjavascriptcoregtk-4.1.so.0')
        objects = ctypes.CDLL('libgobject-2.0.so.0')
        objects.g_object_unref.argtypes = [ctypes.c_void_p]
        engine.jsc_context_new.restype = ctypes.c_void_p
        engine.jsc_context_check_syntax.argtypes = [
            ctypes.c_void_p, ctypes.c_char_p, ctypes.c_ssize_t, ctypes.c_int,
            ctypes.c_char_p, ctypes.c_uint, ctypes.POINTER(ctypes.c_void_p),
        ]
        source = Path(__file__).with_name('native-acceptance.js').read_bytes()
        for code, valid in ((source, True), (source + b'\nconst =', False), (PROBE_LOADER, True)):
            with self.subTest(valid=valid):
                context = engine.jsc_context_new()
                exception = ctypes.c_void_p()
                try:
                    result = engine.jsc_context_check_syntax(context, code, len(code), 1,
                                                             b'/__native/probe.js', 1, ctypes.byref(exception))
                    self.assertEqual(result == 0, valid)
                finally:
                    if exception.value:
                        objects.g_object_unref(exception)
                    objects.g_object_unref(context)

    def test_channel_preserves_nonce_and_loads_probe_from_trusted_app_script(self):
        seen = []

        class Backend(BaseHTTPRequestHandler):
            def log_message(self, *_):
                pass

            def do_GET(self):
                seen.append((self.path, self.headers['Host'], self.headers.get('Origin')))
                if self.path == '/settings':
                    body = b'<html><body>real SPA<script defer src="https://unpkg.com/leaflet@1.9.4/dist/leaflet.js"></script></body></html>'
                    self.send_response(200)
                    self.send_header('Content-Type', 'text/html')
                    self.send_header('X-Garminctl-Page-Nonce', self.headers.get('X-Garminctl-Page-Nonce', ''))
                elif self.path == '/app.js':
                    body = b'window.fixtureAppLoaded = true;'
                    self.send_response(200)
                    self.send_header('Content-Type', 'text/javascript')
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
                request = urllib.request.Request(base + '/settings', headers={
                    'Origin': 'wails://localhost', 'X-Garminctl-Page-Nonce': 'AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA',
                })
                with urllib.request.urlopen(request, timeout=5) as response:
                    html = response.read()
                    self.assertEqual(len(html), int(response.headers['Content-Length']))
                    self.assertEqual(html, b'<html><body>real SPA<script defer src="/__native/map.js"></script></body></html>')
                    self.assertEqual(response.headers['X-Garminctl-Page-Nonce'], 'AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA')
                with urllib.request.urlopen(base + '/app.js', timeout=5) as response:
                    script = response.read()
                    self.assertTrue(script.startswith(b'window.fixtureAppLoaded = true;'))
                    self.assertEqual(script.count(b"import('/__native/probe.js')"), 1)
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

    def test_deferred_map_response_waits_for_native_readiness_on_each_load(self):
        commands = Commands()
        with fixture_proxy(1, commands) as port:
            for _ in range(2):
                commands.map_requested.clear()
                commands.map_release.clear()
                received = []

                def browser():
                    with urllib.request.urlopen(f'http://127.0.0.1:{port}/__native/map.js', timeout=5) as response:
                        received.append((response.status, response.read(), response.headers['Cache-Control']))

                request = threading.Thread(target=browser)
                request.start()
                try:
                    self.assertTrue(commands.map_requested.wait(timeout=3), 'Browser did not request the deferred script')
                    self.assertFalse(received, 'Deferred script completed before the native readiness barrier')
                finally:
                    commands.map_release.set()
                    request.join(timeout=5)
                self.assertFalse(request.is_alive())
                self.assertEqual(received, [(200, b'', 'no-store')])
        self.assertEqual([event['event'] for event in commands.events],
                         ['map-script-held', 'map-script-released'] * 2)

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

    def test_chunked_browser_receipt_is_decoded(self):
        commands = Commands()
        with fixture_proxy(1, commands) as port:
            connection = http.client.HTTPConnection('127.0.0.1', port, timeout=5)
            try:
                connection.request('POST', '/__native/ready', body=iter([b'{"boot":', b'"chunked-page"}']),
                                   headers={'Content-Type': 'text/plain'}, encode_chunked=True)
                response = connection.getresponse()
                response.read()
                self.assertEqual(response.status, 200)
                self.assertEqual(commands.boots, ['chunked-page'])
            finally:
                connection.close()


if __name__ == '__main__':
    unittest.main()

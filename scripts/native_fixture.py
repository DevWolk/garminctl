"""CI-only SPA command channel over the existing reused-backend boundary."""

import contextlib
import http.client
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
from pathlib import Path
import queue
import threading
import time
import uuid


class Commands:
    def __init__(self):
        self.pending = queue.Queue()
        self.results = {}
        self.boots = []
        self.events = []
        self.changed = threading.Condition()

    def send(self, action, **values):
        identifier = uuid.uuid4().hex
        self.pending.put(dict(values, id=identifier, action=action))
        self.events.append({'event': 'command', 'action': action, 'id': identifier})
        return identifier

    def wait(self, read, timeout=20):
        deadline = time.monotonic() + timeout
        with self.changed:
            while True:
                result = read()
                if result is not None:
                    return result
                remaining = deadline - time.monotonic()
                if remaining <= 0:
                    raise TimeoutError('Native fixture command timed out')
                self.changed.wait(min(remaining, 0.1))

    def call(self, action, **values):
        identifier = self.send(action, **values)
        result = self.wait(lambda: self.results.pop(identifier, None))
        if 'error' in result:
            raise AssertionError(result['error'])
        return result['value']


@contextlib.contextmanager
def fixture_proxy(upstream, commands):
    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *_):
            pass

        def reply(self, status, value=None, kind='application/json'):
            body = b'' if value is None else (json.dumps(value).encode() if kind == 'application/json' else value)
            self.send_response(status)
            self.send_header('Content-Type', kind)
            self.send_header('Content-Length', str(len(body)))
            self.end_headers()
            self.wfile.write(body)

        def do_GET(self):
            if self.path == '/__native/probe.js':
                commands.events.append({'event': 'probe-script-request'})
                self.reply(200, Path(__file__).with_name('native-acceptance.js').read_bytes(), 'text/javascript')
            elif self.path == '/__native/next':
                try:
                    self.reply(200, commands.pending.get(timeout=0.1))
                except queue.Empty:
                    self.reply(204)
            else:
                self.forward()

        def do_POST(self):
            if self.path in ('/__native/ready', '/__native/result'):
                size = int(self.headers.get('Content-Length', '0'))
                if not 0 < size <= 65536:
                    self.reply(400)
                    return
                result = json.loads(self.rfile.read(size))
                with commands.changed:
                    if self.path == '/__native/ready':
                        commands.boots.append(result['boot'])
                        commands.events.append({'event': 'page-ready', 'boot': result['boot']})
                    else:
                        commands.results[result['id']] = result
                        commands.events.append({'event': 'result', **result})
                    commands.changed.notify_all()
                self.reply(200, {})
            else:
                self.forward()

        def do_DELETE(self):
            self.forward()

        def forward(self):
            headers = dict(self.headers)
            for name in ('Host', 'Origin', 'Sec-Fetch-Site', 'Connection'):
                headers.pop(name, None)
            size = int(self.headers.get('Content-Length', '0'))
            body = self.rfile.read(size) if size else None
            connection = http.client.HTTPConnection('127.0.0.1', upstream, timeout=20)
            try:
                connection.request(self.command, self.path, body=body, headers=headers)
                response = connection.getresponse()
                kind = response.getheader('Content-Type', '')
                if kind.startswith('text/event-stream'):
                    self.send_response(response.status)
                    self.send_header('Content-Type', kind)
                    self.end_headers()
                    while chunk := response.read1(4096):
                        self.wfile.write(chunk)
                        self.wfile.flush()
                    return
                data = response.read()
                if kind.startswith('text/html'):
                    script = b'<script type="module" src="/__native/probe.js"></script>'
                    data = data.replace(b'</body>', script + b'</body>')
                self.send_response(response.status)
                for name, value in response.getheaders():
                    if name.lower() not in ('content-length', 'transfer-encoding', 'connection'):
                        self.send_header(name, value)
                self.send_header('Content-Length', str(len(data)))
                self.end_headers()
                self.wfile.write(data)
            except (BrokenPipeError, ConnectionResetError):
                pass
            finally:
                connection.close()

    server = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
    worker = threading.Thread(target=server.serve_forever, daemon=True)
    worker.start()
    try:
        yield server.server_port
    finally:
        server.shutdown()
        server.server_close()
        worker.join(timeout=5)

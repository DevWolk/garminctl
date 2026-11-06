"""Native dialog input for isolated package CI runners."""

import base64
import ctypes
import json
import os
from pathlib import Path
import re
import subprocess
import time


def wait_for(read, timeout=15):
    deadline = time.monotonic() + timeout
    while True:
        value = read()
        if value:
            return value
        if time.monotonic() >= deadline:
            raise TimeoutError('Native UI element did not appear')
        time.sleep(0.1)


def driver_for(pid):
    return WindowsUI(pid) if os.name == 'nt' else LinuxUI(pid)


class WindowsUI:
    def __init__(self, pid):
        self.pid = pid

    def command(self, action, **values):
        script = Path(__file__).with_name('native-ui-windows.ps1')
        result = subprocess.run([
            'powershell.exe', '-NoProfile', '-NonInteractive', '-STA',
            '-File', str(script), '-AppProcessId', str(self.pid),
            '-RequestBase64', base64.b64encode(json.dumps(dict(values, action=action)).encode()).decode(),
        ], capture_output=True, text=True, encoding='utf-8-sig', timeout=25)
        if result.returncode:
            raise AssertionError(result.stdout + result.stderr)
        return json.loads(result.stdout)

    def key(self, key):
        self.command('key', key=key)

    def close(self):
        self.command('close')

    def save(self, path):
        return self.command('save', path=str(path))

    def cancel_save(self):
        self.command('cancel-save')

    def replace(self, choice):
        return self.command('replace', choice=choice)

    def menu_reload(self):
        self.command('menu-reload')


class LinuxUI:
    def __init__(self, pid):
        import pyatspi
        self.atspi = pyatspi
        self.pid = pid

    def dialog(self, title):
        observed = []
        def read():
            observed.clear()
            desktop = self.atspi.Registry.getDesktop(0)
            for application in desktop:
                try:
                    pid = application.get_process_id()
                    observed.append((application.name, pid))
                except Exception as error:
                    observed.append(str(error))
                    continue
                if pid != self.pid:
                    continue
                for window in application:
                    observed.append(('window', window.name))
                    if re.fullmatch(title, window.name or ''):
                        return window
            return None
        try:
            return wait_for(read)
        except TimeoutError as error:
            raise TimeoutError(f'Native dialog {title!r} not found; accessible applications: {observed}') from error

    def focus(self, title):
        self.dialog(re.escape(title))
        windows = subprocess.check_output([
            'xdotool', 'search', '--onlyvisible', '--pid', str(self.pid), '--name', '^' + re.escape(title) + '$',
        ], text=True, timeout=5).splitlines()
        subprocess.run(['xdotool', 'windowfocus', '--sync', windows[-1]], check=True, timeout=5)
        return int(windows[-1])

    def key(self, key):
        self.focus('garminctl')
        subprocess.run(['xdotool', 'key', '--clearmodifiers', key], check=True, timeout=5)

    def close(self):
        send_delete_window(self.focus('garminctl'))

    def save(self, path):
        self.focus('Save file')
        subprocess.run(['xdotool', 'key', '--clearmodifiers', 'ctrl+l'], check=True, timeout=5)
        subprocess.run(['xdotool', 'key', '--clearmodifiers', 'ctrl+a'], check=True, timeout=5)
        subprocess.run(['xdotool', 'type', '--clearmodifiers', '--delay', '0', str(path)], check=True, timeout=5)
        dialog = self.dialog('Save file')
        pending, observed = [dialog], []
        while pending:
            element = pending.pop(0)
            try:
                value = element.queryText().getText(0, -1)
            except Exception:
                value = None
            if value == str(path):
                self.click(dialog, 'Save')
                return {'enteredPath': value, 'saveButton': True}
            if value and len(observed) < 10:
                observed.append(value[:200])
            pending.extend(list(element))
        raise AssertionError(f'Save location {str(path)!r} not present in native dialog: {observed}')

    def cancel_save(self):
        self.click(self.dialog('Save file'), 'Cancel')

    def click(self, root, name):
        pending = [root]
        while pending:
            element = pending.pop(0)
            if element.name == name:
                action = element.queryAction()
                if action.nActions:
                    assert action.doAction(0), f'Native {name} button did not activate'
                    return
            pending.extend(list(element))
        raise AssertionError(f'Native {name} button not found in {root.name}')

    def replace(self, choice):
        dialog = self.dialog(r'Replace file\?|Question')
        pending, replacement = [dialog], False
        while pending:
            element = pending.pop(0)
            if (element.name or '').endswith(' already exists. Replace it?'):
                replacement = True
            pending.extend(list(element))
        assert replacement, 'Native dialog does not contain the replacement question'
        if choice in ('yes', 'no'):
            self.click(dialog, 'Replace' if choice == 'yes' else 'Cancel')
        elif choice == 'escape':
            self.focus(dialog.name)
            subprocess.run(['xdotool', 'key', '--clearmodifiers', 'Escape'], check=True, timeout=5)
        elif choice == 'close':
            send_delete_window(self.focus(dialog.name))
        else:
            raise ValueError(choice)


def send_delete_window(window):
    class Data(ctypes.Union):
        _fields_ = [('bytes', ctypes.c_char * 20), ('shorts', ctypes.c_short * 10), ('longs', ctypes.c_long * 5)]

    class ClientMessage(ctypes.Structure):
        _fields_ = [
            ('type', ctypes.c_int), ('serial', ctypes.c_ulong), ('send_event', ctypes.c_int),
            ('display', ctypes.c_void_p), ('window', ctypes.c_ulong),
            ('message_type', ctypes.c_ulong), ('format', ctypes.c_int), ('data', Data),
        ]

    class Event(ctypes.Union):
        _fields_ = [('client', ClientMessage), ('padding', ctypes.c_long * 24)]

    x11 = ctypes.CDLL('libX11.so.6')
    x11.XOpenDisplay.restype = ctypes.c_void_p
    x11.XInternAtom.argtypes = [ctypes.c_void_p, ctypes.c_char_p, ctypes.c_int]
    x11.XInternAtom.restype = ctypes.c_ulong
    x11.XSendEvent.argtypes = [ctypes.c_void_p, ctypes.c_ulong, ctypes.c_int, ctypes.c_long, ctypes.POINTER(Event)]
    x11.XFlush.argtypes = [ctypes.c_void_p]
    x11.XCloseDisplay.argtypes = [ctypes.c_void_p]
    display = x11.XOpenDisplay(None)
    if not display:
        raise RuntimeError('No CI X display')
    try:
        event = Event()
        event.client.type = 33  # ClientMessage
        event.client.display = display
        event.client.window = window
        event.client.message_type = x11.XInternAtom(display, b'WM_PROTOCOLS', 0)
        event.client.format = 32
        event.client.data.longs[0] = x11.XInternAtom(display, b'WM_DELETE_WINDOW', 0)
        assert x11.XSendEvent(display, window, 0, 0, ctypes.byref(event)), 'WM_DELETE_WINDOW failed'
        x11.XFlush(display)
    finally:
        x11.XCloseDisplay(display)

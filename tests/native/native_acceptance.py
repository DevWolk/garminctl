"""Exercise the installed native host and real SPA through a fixture proxy."""

import json
import os
from pathlib import Path
import shutil
import socket
import subprocess
import time
import urllib.request

from native_fixture import Commands, fixture_proxy
from native_ui import driver_for, wait_for


def run_native_acceptance(desktop, cli, dump, env, work, free_port, stop_app, version):
    upstream = free_port()
    log_path = work / 'native-acceptance.log'
    server_log = work / 'native-fixture-server.log'
    result = {'status': 'running', 'version': version, 'passed': [], 'failures': []}
    traces = []
    with server_log.open('wb') as log:
        server = subprocess.Popen([
            str(cli), '-root', str(dump), 'serve', '-addr', f'127.0.0.1:{upstream}',
        ], stdout=log, stderr=log, env=env, start_new_session=os.name != 'nt')
    try:
        if os.name != 'nt':
            result['dbus'] = missing_bus(desktop, dump, env, work, free_port, stop_app)
            result['passed'].append('linux-missing-dbus-diagnostic')
        def server_ready():
            assert server.poll() is None, 'Native fixture backend exited'
            try:
                with urllib.request.urlopen(f'http://127.0.0.1:{upstream}/api/server', timeout=2) as response:
                    return response.status == 200
            except OSError:
                return False
        wait_for(server_ready)
        for suite, scenario in (('runtime', runtime_scenarios), ('ui', ui_scenarios)):
            before = len(result['failures'])
            try:
                run_native_window(suite, scenario, desktop, dump, env, work, upstream, result, traces, stop_app)
            except Exception as error:
                if len(result['failures']) == before:
                    result['failures'].append({'scenario': 'native-' + suite, 'error': str(error)})
        assert server.poll() is None, 'Native acceptance stopped the reused backend'
        result['status'] = 'failed' if result['failures'] else 'passed'
    except Exception as error:
        result['status'] = 'failed'
        if not result['failures']:
            result['failures'].append({'scenario': 'native-session', 'error': str(error)})
        raise
    finally:
        stop_app(server)
        print(json.dumps({'nativeAcceptance': result}, sort_keys=True))
        logs = (log_path, work / 'native-runtime.log', server_log, work / 'native-missing-dbus.log')
        for log in logs:
            if log.exists():
                print(log.read_text(errors='replace'))
        evidence = os.environ.get('NATIVE_RESULTS_DIR')
        if evidence:
            folder = Path(evidence)
            folder.mkdir(parents=True, exist_ok=True)
            (folder / 'native-acceptance.json').write_text(json.dumps(result, indent=2))
            (folder / 'native-commands.json').write_text(json.dumps(traces, indent=2))
            for log in logs:
                if log.exists():
                    shutil.copyfile(log, folder / log.name)
    assert not result['failures'], result['failures']
    return result


def run_native_window(suite, scenario, desktop, dump, env, work, upstream, result, traces, stop_app):
    commands = Commands()
    log_path = work / ('native-runtime.log' if suite == 'runtime' else 'native-acceptance.log')
    app = None
    state = work / ('native-' + suite)
    window_env = dict(env, XDG_CONFIG_HOME=str(state / 'config'), XDG_CACHE_HOME=str(state / 'cache'),
                      APPDATA=str(state / 'appdata'), LOCALAPPDATA=str(state / 'localappdata'))
    try:
        with fixture_proxy(upstream, commands) as port:
            with log_path.open('wb') as log:
                app = subprocess.Popen([
                    str(desktop), '-root', str(dump), '-addr', f'127.0.0.1:{port}',
                ], stdout=log, stderr=log, env=window_env, start_new_session=os.name != 'nt')
            commands.events.append({'event': 'window-start', 'pid': app.pid, 'proxy': port,
                                    'upstream': upstream, 'state': str(state)})
            def page_ready():
                if commands.errors:
                    raise AssertionError(commands.errors[-1])
                assert app.poll() is None, 'Native window exited before its page receipt'
                return commands.boots[0] if commands.boots else None
            commands.wait(page_ready, timeout=45)
            wait_for(lambda: 'desktop window ready' in log_path.read_text(errors='replace'))
            scenario(commands, driver_for(app.pid), app, dump, work, log_path, result)
    finally:
        try:
            if app is not None:
                stop_app(app)
        finally:
            traces.extend(dict(event, suite=suite) for event in commands.events)


def missing_bus(desktop, dump, env, work, free_port, stop_app):
    port = free_port()
    missing = work / 'missing-session-bus'
    log_path = work / 'native-missing-dbus.log'
    with log_path.open('wb') as log:
        child = subprocess.Popen([
            str(desktop), '-root', str(dump), '-addr', f'127.0.0.1:{port}',
        ], env=dict(env, DBUS_SESSION_BUS_ADDRESS='unix:path=' + str(missing)),
            stdout=log, stderr=log, start_new_session=True)
    try:
        code = child.wait(timeout=15)
        diagnostic = log_path.read_text(errors='replace')
        assert 'desktop host error' in diagnostic, diagnostic
        with socket.socket() as connection:
            assert connection.connect_ex(('127.0.0.1', port)) != 0, 'Failed native initialization started a backend'
        return {'exitCode': code, 'diagnostic': diagnostic}
    finally:
        stop_app(child)


def check_scenario(result, name, action):
    try:
        action()
    except Exception as error:
        result['failures'].append({'scenario': name, 'error': str(error)})
        raise
    result['passed'].append(name)


def runtime_scenarios(commands, driver, app, dump, work, log_path, result):
    def runtime():
        before = log_path.read_text(errors='replace').count('desktop window ready')
        observed = commands.call('runtime')
        result['runtime'] = observed
        assert observed['get'] == 405, observed
        assert observed['post'] == 422, observed
        assert observed['positive'] == 200, observed
        assert observed['foreign'] == {'loaded': True, 'executed': False}, observed
        assert observed['csp']['documentURL'] == 'about:srcdoc', observed
        assert observed['csp']['directive'].startswith('script-src'), observed
        assert observed['csp']['blockedURI'] == 'inline', observed
        after = log_path.read_text(errors='replace').count('desktop window ready')
        assert after == before + 1, 'Foreign-origin Ready call reached the native service'
    check_scenario(result, 'native-runtime-origin-and-method', runtime)


def ui_scenarios(commands, driver, app, dump, work, log_path, result):
    def check(name, action):
        check_scenario(result, name, action)


    target = work / 'native-export.xml'
    expected = (dump / 'GARMIN/GarminDevice.xml').read_bytes()

    def start_download():
        return commands.call('download')['id']

    def finished(identifier):
        def read():
            outcome = commands.call('download-result', download=identifier)
            if not outcome or not outcome['done']:
                return False
            assert not outcome.get('error'), outcome
            return True
        wait_for(read, timeout=5)

    def cancel_save():
        identifier = start_download()
        driver.cancel_save()
        finished(identifier)
        assert not target.exists(), 'Cancel created the export'
    check('native-save-cancel', cancel_save)

    def save():
        identifier = start_download()
        result['saveInput'] = driver.save(target)
        finished(identifier)
        locations = (work, Path.cwd(), Path.home(), Path.home() / 'Documents',
                     Path.home() / 'Desktop', Path.home() / 'Downloads')
        candidates = [str(folder / name) for folder in locations for name in ('GarminDevice.xml', 'native-export.xml')
                      if (folder / name).is_file()]
        assert target.is_file(), {'expectedExport': str(target), 'candidateExports': candidates,
                                  'nativeInput': result['saveInput']}
        assert target.read_bytes() == expected, 'Native Save changed export bytes'
    check('native-save-bytes', save)

    def replacement(choice):
        observation = replace_export(commands, driver, target, expected, choice, finished)
        result.setdefault('replacementOutcomes', {})[choice] = observation or {'confirmation': 'dismissed'}
    for choice in ('no', 'yes'):
        check('native-replacement-' + choice, lambda choice=choice: replacement(choice))

    def edit_guard():
        edited = commands.call('edit')
        assert edited['pending'], edited
        driver.key('F5')
        wait_for(lambda: commands.call('snapshot')['dialog'])
        commands.call('confirm', accept=False)
        kept = commands.call('snapshot')
        assert kept['boot'] == edited['boot'] and kept['pending'], kept
        driver.close()
        wait_for(lambda: commands.call('snapshot')['dialog'])
        commands.call('confirm', accept=False)
        assert app.poll() is None, 'Cancel closed the native window'
        assert commands.call('snapshot')['pending'], 'Close cancellation discarded edits'
        if os.name == 'nt':
            result['menuInput'] = driver.menu_reload()
        else:
            driver.key('F5')
        wait_for(lambda: commands.call('snapshot')['dialog'])
        commands.send('confirm', accept=True)
        commands.wait(lambda: commands.boots[-1] if commands.boots[-1] != edited['boot'] else None)
        assert not commands.call('snapshot')['pending'], 'Reload retained discarded edits'
    check('native-edit-close-and-reload', edit_guard)
    if os.name == 'nt':
        result['passed'].append('windows-application-menu-reload')

    for choice in ('escape', 'close'):
        check('native-replacement-' + choice, lambda choice=choice: replacement(choice))

    check('native-application-versions-and-channel', lambda: application_scenario(commands, result))

    def final_close():
        assert commands.call('edit')['pending']
        driver.close()
        wait_for(lambda: commands.call('snapshot')['dialog'])
        commands.send('confirm', accept=True)
        assert app.wait(timeout=10) == 0, 'Confirmed native close failed'
    check('native-confirmed-edit-close', final_close)


def application_scenario(commands, result):
    initial = commands.call('application-open')
    result['application'] = [initial]
    facts = initial['facts']
    for role in ('Desktop', 'CLI', 'Backend (reused)'):
        assert facts.get(role) == result['version'], {'role': role, 'observed': initial}
    kind = 'Windows Setup' if os.name == 'nt' else {'deb': 'DEB', 'rpm': 'RPM'}.get(os.environ.get('PACKAGE_FORMAT'))
    assert kind is not None and facts.get('Installation') == kind, initial
    assert facts.get('Platform') == ('windows / amd64' if os.name == 'nt' else 'linux / amd64'), initial
    assert initial['includePrerelease'] is False and initial['downloadDisabled'], initial
    # Only saved-history channel reads; never click Check or start a download.
    for include in (True, False):
        changed = commands.call('application-channel', includePrerelease=include)
        result['application'].append(changed)
        assert changed['includePrerelease'] is include and changed['downloadDisabled'], changed
        assert changed['facts'] == facts, changed


def replace_export(commands, driver, target, expected, choice, finished):
    protected = b'keep existing export'
    target.write_bytes(protected)
    identifier = commands.call('download')['id']
    driver.save(target)
    observation = driver.replace(choice)
    if observation and observation.get('confirmation') == 'remained-open':
        assert choice in ('escape', 'close'), 'Unexpected retained confirmation'
        assert target.read_bytes() == protected, 'Unanswered confirmation replaced the existing file'
        assert not commands.call('download-result', download=identifier)['done'], 'Visible confirmation lost its pending download'
        driver.replace('no')
    finished(identifier)
    wanted = expected if choice == 'yes' else protected
    assert target.read_bytes() == wanted, 'Replacement consent did not protect the existing file'
    return observation

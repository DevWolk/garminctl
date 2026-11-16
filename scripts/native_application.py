"""Exercise confirmed Application installation on disposable native CI runners."""

import argparse
import json
import os
from pathlib import Path
import re
import runpy
import shutil
import subprocess
import urllib.request

from native_ui import driver_for, find_application, wait_for


runner = runpy.run_path(str(Path(__file__).with_name('test-package.py')))


def read_json(path):
    with path.open('rb') as source:
        data = source.read((128 << 10) + 1)
    assert len(data) <= 128 << 10, f'Oversized fixture/receipt: {path.name}'
    return json.loads(data)


def cache_directory(state):
    return state / ('localappdata' if os.name == 'nt' else 'cache') / 'garminctl'


def seed_catalog(catalog, assets, cache, versions):
    """Copy fixture data only; the installed CLI remains the package verifier."""
    records = sorted(catalog.glob('*/entry.json'))
    assert len(records) == 2, 'Expected two retained native package manifests'
    tags = set()
    for record in records:
        value = read_json(record)
        identifier = record.parent.name
        assert re.fullmatch('[0-9a-f]{64}', identifier) and value['id'] == identifier, 'Invalid fixture catalog ID'
        tags.add(value['release']['release']['tag_name'])
        destination = cache / 'application-packages' / identifier
        destination.mkdir(parents=True, mode=0o700)
        for item in ('package', 'signature'):
            asset = value['release'][item]
            name = asset['name']
            assert name and '/' not in name and '\\' not in name and name not in ('.', '..'), 'Unsafe fixture asset name'
            candidates = [directory / name for directory in assets if (directory / name).is_file()]
            assert len(candidates) == 1, f'Expected one fixture asset: {name}'
            source = candidates[0]
            assert not source.is_symlink() and source.stat().st_size == asset['size'], f'Invalid fixture asset: {name}'
            shutil.copyfile(source, destination / name)
            (destination / name).chmod(0o600)
        shutil.copyfile(record, destination / 'entry.json')
        (destination / 'entry.json').chmod(0o600)
    assert tags == set(versions), f'Catalog labels differ: {tags}'


def native_environment(state):
    return dict(os.environ, XDG_CONFIG_HOME=str(state / 'config'), XDG_CACHE_HOME=str(state / 'cache'),
                APPDATA=str(state / 'appdata'), LOCALAPPDATA=str(state / 'localappdata'))


def fixture_watch(repo, work):
    dump = work / 'watch'
    for source, target in [('testdump/testdata/GarminDevice.xml', 'GarminDevice.xml'),
                           ('testdump/testdata/OUT.BIN', 'Apps/OUT.BIN'),
                           ('fit/testdata/Settings.fit', 'Settings/Settings.fit')]:
        path = dump / 'GARMIN' / target
        path.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(repo / 'internal' / source, path)
    return dump


def request(base, path, body=None, headers=None, method=None):
    encoded = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(base + path, data=encoded, method=method,
                                 headers=dict({'Content-Type': 'application/json',
                                               'Accept': 'application/json, text/event-stream'}, **(headers or {})))
    with urllib.request.urlopen(req, timeout=5) as response:
        data = response.read((1 << 20) + 1)
        assert len(data) <= 1 << 20, 'Oversized native acceptance response'
        return (json.loads(data) if data else None), response.headers


def check_mcp(base):
    initialized, response = request(base, '/mcp', {'jsonrpc': '2.0', 'id': 1, 'method': 'initialize', 'params': {
        'protocolVersion': '2025-06-18', 'capabilities': {},
        'clientInfo': {'name': 'application-native-update', 'version': '1'},
    }})
    assert 'error' not in initialized, initialized
    headers = {'Mcp-Session-Id': response['Mcp-Session-Id'],
               'MCP-Protocol-Version': initialized['result']['protocolVersion']}
    try:
        request(base, '/mcp', {'jsonrpc': '2.0', 'method': 'notifications/initialized', 'params': {}}, headers)
        tools, _ = request(base, '/mcp', {'jsonrpc': '2.0', 'id': 2, 'method': 'tools/list', 'params': {}}, headers)
        assert 'error' not in tools and len(tools['result']['tools']) == 33, tools
        ping, _ = request(base, '/mcp', {'jsonrpc': '2.0', 'id': 3, 'method': 'ping', 'params': {}}, headers)
        assert 'error' not in ping, ping
        return len(tools['result']['tools'])
    finally:
        request(base, '/mcp', headers=headers, method='DELETE')


def confirm_transition(driver, target, rollback):
    driver.activate(r'(?:◉\s*)?Application')
    if rollback:
        if '-' in target:
            driver.toggle('Include prereleases')
        driver.toggle(r'Keep current data \(requires compatibility check\)')
        driver.activate('Prepare rollback review…')
        driver.wait_text(re.escape('Prepare rollback to ' + target + '?'))
        driver.activate('Prepare review')
        driver.wait_text('Review the data replacements below. Installation has not started.')
        driver.toggle(r'I agree to install the older version while keeping current data\..*')
        return 'Keep current data and install older version'
    driver.activate('Install saved release…')
    driver.wait_text(re.escape('Install ' + target + ' and restart?'))
    return 'Install and restart'


def installation_result(cache, previous, target):
    candidates = [path for path in (cache / 'application-installing').glob('stage-*/install-result.json')
                  if path.parent.name not in previous]
    assert len(candidates) <= 1, 'Multiple installer attempts started for one confirmation'
    if not candidates:
        return None
    path = candidates[0]
    result = read_json(path)
    assert result['tag'] == target and result['status'] == 'installed', result
    ready = read_json(path.with_name('startup-ready.json'))
    assert ready['operation'] == result['operation'] and ready['desktopVersion'] == target and ready['backendOwned'], \
        'Startup receipt does not match the operation, selected desktop version or owned backend'
    return path.parent, result, ready


def retain_diagnostics(cache, previous, work):
    observations = []
    for operation in sorted((cache / 'application-installing').glob('stage-*')):
        if operation.name in previous:
            continue
        observation = {'directory': operation.name}
        for filename in ('install-result.json', 'startup-ready.json'):
            path = operation / filename
            if path.exists():
                value = read_json(path)
                observation[filename] = {key: value[key] for key in
                    ('operation', 'tag', 'status', 'error', 'desktopVersion', 'backendURL', 'backendOwned') if key in value}
        log = operation / 'installer.log'
        if log.exists():
            with log.open('rb') as source:
                (work / (operation.name + '-installer.log')).write_bytes(source.read(256 << 10))
        observations.append(observation)
    (work / 'installer-observations.json').write_text(json.dumps(observations, indent=2))


def run_transition(desktop, cli, repo, state, work, current, target, rollback=False):
    assert subprocess.check_output([str(cli), 'version'], text=True, timeout=10).strip() == 'garminctl ' + current
    work.mkdir(parents=True, exist_ok=True)
    cache = cache_directory(state)
    previous = {path.name for path in (cache / 'application-installing').glob('stage-*')}
    dump = fixture_watch(repo, work)
    base = f'http://127.0.0.1:{runner["free_port"]()}'
    log_path = work / 'desktop.log'
    with log_path.open('wb') as log:
        app = subprocess.Popen([str(desktop), '-root', str(dump), '-addr', base.removeprefix('http://')],
                               env=native_environment(state), stdout=log, stderr=log, start_new_session=os.name != 'nt')
    committed = False
    completed = False
    try:
        def ready():
            assert app.poll() is None, f'Initial native desktop exited: {app.returncode}'
            if 'desktop window ready' not in log_path.read_text(errors='replace'):
                return False
            value, _ = request(base, '/api/server')
            assert value['version'] == current, value
            return True
        wait_for(ready, timeout=45)
        driver = driver_for(app.pid)
        confirmation = confirm_transition(driver, target, rollback)
        # The next click may reach Commit even if its UIA/AT-SPI reply is lost.
        # Never kill children, retry a click or clean their state after this point.
        committed = True
        driver.activate(confirmation)
        assert app.wait(timeout=180) == 0, 'Application did not exit for its installer'
        _, result, startup = wait_for(lambda: installation_result(cache, previous, target), timeout=180)
        assert startup['backendURL'] == base, 'Restarted backend differs from the reserved loopback listener'
        assert subprocess.check_output([str(cli), 'version'], text=True, timeout=10).strip() == 'garminctl ' + target
        metadata, _ = request(base, '/api/server')
        assert metadata['application'] == 'garminctl' and metadata['version'] == target, metadata
        tools = check_mcp(base)
        restarted = driver_for(find_application(desktop, app.pid))
        restarted.close()
        restarted.wait_closed()
        # The installer reported success and its restarted window has closed.
        # It is now safe to join any remaining WebKit children of this test group.
        runner['stop_app'](app)
        completed = True
        return {'from': current, 'to': target, 'rollback': rollback, 'operation': result['operation'],
                'status': result['status'], 'desktop': startup['desktopVersion'], 'backend': metadata['version'],
                'cli': target, 'mcpTools': tools, 'nativeClose': True}
    finally:
        if not committed:
            runner['stop_app'](app)
        if committed:
            try:
                retain_diagnostics(cache, previous, work)
            except (OSError, ValueError) as error:
                print(f'Could not retain installer diagnostics: {error}')
        if committed and not completed:
            print('Installation may have started; preserving the disposable process/state without replay or forced cleanup.')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--desktop', type=Path, required=True)
    parser.add_argument('--cli', type=Path, required=True)
    parser.add_argument('--repo', type=Path, required=True)
    parser.add_argument('--state', type=Path, required=True)
    parser.add_argument('--work', type=Path, required=True)
    parser.add_argument('--current', required=True)
    parser.add_argument('--target', required=True)
    parser.add_argument('--rollback', action='store_true')
    parser.add_argument('--catalog', type=Path)
    parser.add_argument('--assets', type=Path, action='append', default=[])
    args = parser.parse_args()
    assert os.environ.get('GITHUB_ACTIONS') == 'true', 'Application OS acceptance runs only on disposable CI systems'
    state, work = args.state.resolve(), args.work.resolve()
    cache = cache_directory(state)
    if args.catalog:
        assert not args.rollback, 'Do not reseed or repair retained packages during rollback'
        seed_catalog(args.catalog.resolve(), [path.resolve() for path in args.assets], cache, [args.current, args.target])
    ledger = cache / 'uploaded-4242.json'
    if args.rollback:
        expected = ledger.read_bytes()
    else:
        expected = b'{"GARMIN/Activity/fixture.fit":"1|native-acceptance"}'
        ledger.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
        ledger.write_bytes(expected)
        ledger.chmod(0o600)
    result = run_transition(args.desktop.resolve(), args.cli.resolve(), args.repo.resolve(), state, work,
                            args.current, args.target, args.rollback)
    assert ledger.read_bytes() == expected, 'Application installation changed current delivery-ledger bytes'
    if not args.rollback:
        ledger.write_bytes(b'{"GARMIN/Activity/fixture.fit":"2|edited-after-upgrade"}')
    result['deliveryLedgerPreserved'] = True
    (work / 'application-transition.json').write_text(json.dumps(result, indent=2))
    print(json.dumps(result))


if __name__ == '__main__':
    main()

"""Exercise confirmed Application installation on disposable native CI runners."""

import argparse
import hashlib
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
LEDGER_NAME = 'uploaded-4242.json'
LEDGER_INITIAL = b'{"GARMIN/Activity/fixture.fit":"1|native-acceptance"}'
LEDGER_EDITED = b'{"GARMIN/Activity/fixture.fit":"2|edited-after-upgrade"}'
LEDGER_UNSUPPORTED = b'{"schemaVersion":99,"entries":{"fixture":"unsupported ledger format"}}'


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


def prepare_rollback_review(driver, target, keep):
    driver.toggle(r'Keep current data \(requires compatibility check\)', keep)
    driver.activate('Prepare rollback review…')
    driver.wait_text(re.escape('Prepare rollback to ' + target + '?'))
    driver.activate('Prepare review')


def confirm_transition(driver, target, rollback, restore=False):
    driver.activate(r'(?:◉\s*)?Application')
    if rollback:
        if '-' in target:
            driver.toggle('Include prereleases')
        prepare_rollback_review(driver, target, not restore)
        driver.wait_text('Review the data replacements below. Installation has not started.')
        if restore:
            text = driver.wait_text(r'Restore backup: [0-9a-f]{64}\. Newer data saved in backup: [0-9a-f]{64}\.')
            target_id, safety_id = re.findall('[0-9a-f]{64}', text)
            driver.reveal(r'[0-9]+ file changes')
            driver.wait_text(r'Replace cache/uploaded-4242\.json \([0-9]+ bytes\)')
            return 'Restore backup and install older version', {'target': target_id, 'safety': safety_id}
        driver.toggle(r'I agree to install the older version while keeping current data\..*')
        return 'Keep current data and install older version', None
    driver.activate('Install saved release…')
    driver.wait_text(re.escape('Install ' + target + ' and restart?'))
    return 'Install and restart', None


def inspect_ledger_snapshot(cli, state, identifier):
    value = json.loads(subprocess.check_output([str(cli), '-json', 'application', 'recovery', 'inspect', identifier],
                                              env=native_environment(state), text=True, timeout=15))
    assert value['verified'] and value['verification'] == 'local_manifest' and value['dataCompatibility'] == 'not_checked', value
    assert value['snapshot']['snapshotId'] == identifier, value
    files = [file for file in value['files'] if file['area'] == 'cache' and file['path'] == LEDGER_NAME]
    assert len(files) == 1, 'Selected snapshot must contain exactly the fixture ledger'
    return value['snapshot'], files[0]


def verify_restore_review(cli, state, current, target, review):
    old, ledger = inspect_ledger_snapshot(cli, state, review['target'])
    safety, retained = inspect_ledger_snapshot(cli, state, review['safety'])
    assert old['from'] == target and old['to'] == current, old
    assert safety['from'] == current and safety['to'] == target, safety
    known = {hashlib.sha256(value).hexdigest(): value for value in (LEDGER_INITIAL, LEDGER_EDITED)}
    assert ledger['sha256'] in known and ledger['bytes'] == len(known[ledger['sha256']]), 'Restore target is not the known fixture backup'
    assert retained['sha256'] == hashlib.sha256(LEDGER_UNSUPPORTED).hexdigest() and retained['bytes'] == len(LEDGER_UNSUPPORTED), \
        'The fresh safety snapshot does not retain the unsupported fixture bytes'
    assert (cache_directory(state) / LEDGER_NAME).read_bytes() == LEDGER_UNSUPPORTED, 'Data changed before restore confirmation'
    return known[ledger['sha256']], dict(review, restoredSHA256=ledger['sha256'], safetySHA256=retained['sha256'])


def reject_keep_current(driver, app, cli, state, base, cache, previous, current, target):
    driver.activate(r'(?:◉\s*)?Application')
    if '-' in target:
        driver.toggle('Include prereleases')
    prepare_rollback_review(driver, target, True)
    driver.wait_text(r'Could not prepare installation: .*current data cannot be read.*cache/uploaded-4242\.json.*')
    assert app.poll() is None, 'Unreadable data stopped the live desktop'
    metadata, _ = request(base, '/api/server')
    assert metadata['version'] == current, metadata
    assert subprocess.check_output([str(cli), 'version'], text=True, timeout=10).strip() == 'garminctl ' + current
    assert (cache / LEDGER_NAME).read_bytes() == LEDGER_UNSUPPORTED, 'Refused preparation changed live data'
    assert {path.name for path in (cache / 'application-installing').glob('stage-*')} == previous, \
        'Refused keep-current preparation created an installer operation'


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


def observe_setup_barrier(pid, desktop, cli, work, desktop_hash, cli_hash):
    """Observe only; the outer CI script owns the lock and explicit interruption."""
    assert pid > 0 and all(re.fullmatch(r'[0-9a-f]{64}', value) for value in (desktop_hash, cli_hash))
    driver = driver_for(pid)
    driver.activate(r'&?Next\s*>')
    driver.activate(r'&?Install')
    driver.wait_text(r'(?s).*Error opening file for writing:.*garminctl\.exe.*')
    assert hashlib.sha256(desktop.read_bytes()).hexdigest() == desktop_hash, 'Setup has not completed the desktop replacement'
    assert hashlib.sha256(cli.read_bytes()).hexdigest() == cli_hash, 'The locked CLI changed before interruption'
    value = dict(setupPID=pid, barrierObserved=True, installerInterrupted=False,
                 desktopSHA256=desktop_hash, cliSHA256=cli_hash)
    work.mkdir(parents=True, exist_ok=True)
    (work / 'setup-barrier.json').write_text(json.dumps(value, indent=2))
    return value


def export_repair_package(desktop, cli, state, work, current, target):
    """The CI caller injects the file fault; this function only exports recovery."""
    assert not desktop.exists(), 'The disposable repair fixture must have an unavailable GUI'
    env = native_environment(state)
    assert subprocess.check_output([str(cli), 'version'], text=True, timeout=10).strip() == 'garminctl ' + current
    try:
        subprocess.run([str(desktop)], env=env, capture_output=True, timeout=5, check=True)
    except FileNotFoundError:
        pass
    else:
        raise AssertionError('The missing-GUI fixture unexpectedly started')
    cache = cache_directory(state)
    ledger = (cache / LEDGER_NAME).read_bytes()
    records = json.loads(subprocess.check_output([str(cli), '-json', 'application', 'recovery', 'list'],
                                                 env=env, text=True, timeout=30))
    assert records['verified'] is False, 'Recovery listing must remain unverified metadata'
    selected = [row for row in records['packages'] if row['tag'] == target]
    assert len(selected) == 1, 'Select exactly one retained package for the manual repair'
    identifier = selected[0]['cacheId']
    value = json.loads(subprocess.check_output([str(cli), '-json', 'application', 'recovery', 'export', identifier],
                                               env=env, text=True, timeout=120))
    assert value['verified'] and value['manualInstallationRequired'] and value['dataCompatibility'] == 'not_checked', value
    assert value['package']['cacheId'] == identifier and value['package']['tag'] == target, value
    package, signature = Path(value['path']), Path(value['signaturePath'])
    assert package.resolve().is_relative_to(cache / 'application-recovery') and not package.is_symlink(), value
    assert signature.parent == package.parent and not signature.is_symlink(), value
    assert package.stat().st_size == value['package']['bytes'] and signature.stat().st_size == 384, value
    assert (cache / LEDGER_NAME).read_bytes() == ledger, 'Read-only recovery export changed current data'
    value.update(guiUnavailable=True, installedCLIVersion=current, selectedRepairVersion=target, deliveryLedgerPreserved=True)
    work.mkdir(parents=True, exist_ok=True)
    (work / 'recovery-export.json').write_text(json.dumps(value, indent=2))
    return value


def run_transition(desktop, cli, repo, state, work, current, target, rollback=False, restore=False):
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
        if restore:
            reject_keep_current(driver, app, cli, state, base, cache, previous, current, target)
        confirmation, review = confirm_transition(driver, target, rollback, restore)
        restored_bytes, data_review = None, None
        if restore:
            restored_bytes, data_review = verify_restore_review(cli, state, current, target, review)
            assert {path.name for path in (cache / 'application-installing').glob('stage-*')} == previous, \
                'Installer operation appeared before explicit restore consent'
            driver.toggle(r'I agree to replace or remove the listed data\..*')
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
        if restore:
            assert (cache / LEDGER_NAME).read_bytes() == restored_bytes, 'Explicit restore did not recover the reviewed fixture bytes'
            _, safety = inspect_ledger_snapshot(cli, state, review['safety'])
            assert safety['sha256'] == data_review['safetySHA256'], 'Safety backup changed after installation'
        restarted = driver_for(find_application(desktop, app.pid))
        restarted.close()
        restarted.wait_closed()
        # The installer reported success and its restarted window has closed.
        # It is now safe to join any remaining WebKit children of this test group.
        runner['stop_app'](app)
        completed = True
        return {'from': current, 'to': target, 'rollback': rollback, 'operation': result['operation'],
                'status': result['status'], 'desktop': startup['desktopVersion'], 'backend': metadata['version'],
                'cli': target, 'mcpTools': tools, 'nativeClose': True, 'dataReview': data_review}
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
    parser.add_argument('--restore', action='store_true', help='Exercise unknown-data refusal followed by explicit restore review')
    parser.add_argument('--repair-export', action='store_true', help='Export a verified package while the disposable GUI fixture is unavailable')
    parser.add_argument('--setup-process', type=int, help='Observe the owned Windows Setup at the locked-CLI file barrier')
    parser.add_argument('--expected-desktop-hash')
    parser.add_argument('--expected-cli-hash')
    parser.add_argument('--catalog', type=Path)
    parser.add_argument('--assets', type=Path, action='append', default=[])
    args = parser.parse_args()
    assert os.environ.get('GITHUB_ACTIONS') == 'true', 'Application OS acceptance runs only on disposable CI systems'
    assert not args.restore or args.rollback, 'Restore acceptance requires an explicit rollback'
    state, work = args.state.resolve(), args.work.resolve()
    if args.setup_process is not None:
        assert os.name == 'nt' and not (args.rollback or args.restore or args.catalog or args.repair_export)
        result = observe_setup_barrier(args.setup_process, args.desktop.resolve(), args.cli.resolve(), work,
                                       args.expected_desktop_hash, args.expected_cli_hash)
        print(json.dumps(result))
        return
    if args.repair_export:
        assert not (args.rollback or args.restore or args.catalog), 'Recovery export starts no installation or restoration'
        result = export_repair_package(args.desktop.resolve(), args.cli.resolve(), state, work, args.current, args.target)
        print(json.dumps(result))
        return
    cache = cache_directory(state)
    if args.catalog:
        assert not args.rollback, 'Do not reseed or repair retained packages during rollback'
        seed_catalog(args.catalog.resolve(), [path.resolve() for path in args.assets], cache, [args.current, args.target])
    ledger = cache / LEDGER_NAME
    if args.restore:
        assert ledger.read_bytes() in (LEDGER_INITIAL, LEDGER_EDITED), 'Use the existing synthetic ledger fixture'
        ledger.write_bytes(LEDGER_UNSUPPORTED)
        expected = None
    elif args.rollback or ledger.exists():
        expected = ledger.read_bytes()
    else:
        expected = LEDGER_INITIAL
        ledger.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
        ledger.write_bytes(expected)
        ledger.chmod(0o600)
    result = run_transition(args.desktop.resolve(), args.cli.resolve(), args.repo.resolve(), state, work,
                            args.current, args.target, args.rollback, args.restore)
    if args.restore:
        assert hashlib.sha256(ledger.read_bytes()).hexdigest() == result['dataReview']['restoredSHA256']
    else:
        assert ledger.read_bytes() == expected, 'Application installation changed current delivery-ledger bytes'
    if not args.rollback:
        ledger.write_bytes(LEDGER_EDITED)
    result['deliveryLedgerRestored' if args.restore else 'deliveryLedgerPreserved'] = True
    (work / 'application-transition.json').write_text(json.dumps(result, indent=2))
    print(json.dumps(result))


if __name__ == '__main__':
    main()

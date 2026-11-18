"""Headless checks for the native Application acceptance runner; no OS install."""

import contextlib
import io
import json
from pathlib import Path
import subprocess
import tempfile
from types import SimpleNamespace
import unittest
from unittest import mock

import native_application as acceptance
import native_ui
from native_ui import LinuxUI


class NativeApplicationTests(unittest.TestCase):
    def test_setup_barrier_requires_the_file_error_and_exact_mixed_pair_before_receipt(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            desktop, cli, work = root / 'desktop.exe', root / 'cli.exe', root / 'evidence'
            desktop.write_bytes(b'old desktop')
            cli.write_bytes(b'old CLI')
            driver = mock.Mock()
            def activate(pattern):
                if pattern == r'&?Install':
                    desktop.write_bytes(b'new desktop')
            driver.activate.side_effect = activate
            with mock.patch.object(acceptance, 'driver_for', return_value=driver) as factory:
                value = acceptance.observe_setup_barrier(42, desktop, cli, work,
                    acceptance.hashlib.sha256(b'new desktop').hexdigest(), acceptance.hashlib.sha256(b'old CLI').hexdigest())
            factory.assert_called_once_with(42)
            self.assertEqual(driver.method_calls, [mock.call.activate(r'&?Next\s*>'), mock.call.activate(r'&?Install'),
                mock.call.wait_text(r'(?s).*Error opening file for writing:.*garminctl\.exe.*')])
            self.assertEqual(value['setupPID'], 42)
            self.assertTrue(value['barrierObserved'])
            self.assertFalse(value['installerInterrupted'])
            self.assertEqual(json.loads((work / 'setup-barrier.json').read_text()), value)

    def test_setup_barrier_never_records_success_for_partial_or_unblocked_replacement(self):
        for fault in ('partial desktop', 'changed CLI', 'missing file-error dialog'):
            with self.subTest(fault=fault), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                desktop, cli, work = root / 'desktop.exe', root / 'cli.exe', root / 'evidence'
                desktop.write_bytes(b'partial' if fault == 'partial desktop' else b'new desktop')
                cli.write_bytes(b'new CLI' if fault == 'changed CLI' else b'old CLI')
                driver = mock.Mock()
                if fault == 'missing file-error dialog':
                    driver.wait_text.side_effect = TimeoutError('no NSIS file error')
                with mock.patch.object(acceptance, 'driver_for', return_value=driver):
                    with self.assertRaises((AssertionError, TimeoutError)):
                        acceptance.observe_setup_barrier(42, desktop, cli, work,
                            acceptance.hashlib.sha256(b'new desktop').hexdigest(), acceptance.hashlib.sha256(b'old CLI').hexdigest())
                self.assertFalse((work / 'setup-barrier.json').exists())

    def test_catalog_fixture_copies_existing_bytes_without_overwriting_retained_data(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            catalog, assets, cache = root / 'catalog', root / 'assets', root / 'cache'
            assets.mkdir()
            for index, tag in enumerate(('v1.0.0-rc.1', 'v1.0.0')):
                identifier = str(index) * 64
                entry = catalog / identifier
                entry.mkdir(parents=True)
                name = f'package-{index}.deb'
                (assets / name).write_bytes(b'fixture package')
                (assets / (name + '.sig')).write_bytes(b'fixture signature')
                metadata = {'id': identifier, 'release': {'release': {'tag_name': tag},
                    'package': {'name': name, 'size': len(b'fixture package')},
                    'signature': {'name': name + '.sig', 'size': len(b'fixture signature')}}}
                (entry / 'entry.json').write_text(json.dumps(metadata))
            acceptance.seed_catalog(catalog, [assets], cache, ['v1.0.0-rc.1', 'v1.0.0'])
            saved = cache / 'application-packages' / ('0' * 64)
            self.assertEqual((saved / 'package-0.deb').read_bytes(), b'fixture package')
            self.assertEqual((saved / 'entry.json').read_bytes(), (catalog / ('0' * 64) / 'entry.json').read_bytes())
            (saved / 'package-0.deb').write_bytes(b'changed retained bytes')
            with self.assertRaises(FileExistsError):
                acceptance.seed_catalog(catalog, [assets], cache, ['v1.0.0-rc.1', 'v1.0.0'])
            self.assertEqual((saved / 'package-0.deb').read_bytes(), b'changed retained bytes')

    def test_missing_gui_uses_the_installed_cli_for_verified_manual_repair_export(self):
        with tempfile.TemporaryDirectory() as directory:
            state = Path(directory) / 'state'
            cache = acceptance.cache_directory(state)
            cache.mkdir(parents=True)
            ledger = cache / acceptance.LEDGER_NAME
            ledger.write_bytes(acceptance.LEDGER_EDITED)
            exported = cache / 'application-recovery' / 'owned-export'
            exported.mkdir(parents=True)
            package, signature = exported / 'repair.deb', exported / 'repair.deb.sig'
            package.write_bytes(b'repair fixture')
            signature.write_bytes(bytes(384))
            identifier = 'a' * 64
            listing = {'verified': False, 'packages': [{'cacheId': identifier, 'tag': 'v1.0.0-rc.1'}]}
            export = {'verified': True, 'manualInstallationRequired': True, 'dataCompatibility': 'not_checked',
                      'package': {'cacheId': identifier, 'tag': 'v1.0.0-rc.1', 'bytes': len(b'repair fixture')},
                      'path': str(package), 'signaturePath': str(signature)}
            responses = ['garminctl v1.0.0\n', json.dumps(listing), json.dumps(export)]
            with mock.patch.object(acceptance.subprocess, 'check_output', side_effect=responses) as command:
                result = acceptance.export_repair_package(Path(directory) / 'missing-desktop', Path('/fixture/cli'), state,
                                                           Path(directory) / 'evidence', 'v1.0.0', 'v1.0.0-rc.1')
            self.assertTrue(result['guiUnavailable'])
            self.assertEqual(result['installedCLIVersion'], 'v1.0.0')
            self.assertEqual(result['selectedRepairVersion'], 'v1.0.0-rc.1')
            self.assertTrue(result['manualInstallationRequired'])
            self.assertEqual([call.args[0] for call in command.call_args_list], [
                ['/fixture/cli', 'version'], ['/fixture/cli', '-json', 'application', 'recovery', 'list'],
                ['/fixture/cli', '-json', 'application', 'recovery', 'export', identifier]])
            self.assertEqual(command.call_args.kwargs['env']['XDG_CONFIG_HOME'], str(state / 'config'))
            self.assertEqual(command.call_args.kwargs['env']['XDG_CACHE_HOME'], str(state / 'cache'))
            self.assertEqual(ledger.read_bytes(), acceptance.LEDGER_EDITED)
            self.assertTrue((Path(directory) / 'evidence/recovery-export.json').is_file())

    def test_failed_manual_export_publishes_no_repair_authority_or_changes_to_live_data(self):
        for failure in (False, subprocess.CalledProcessError(1, 'fixture export')):
            with self.subTest(failure=str(failure)), tempfile.TemporaryDirectory() as directory:
                state = Path(directory)
                cache = acceptance.cache_directory(state)
                cache.mkdir(parents=True)
                (cache / acceptance.LEDGER_NAME).write_bytes(acceptance.LEDGER_EDITED)
                identifier = 'b' * 64
                responses = ['garminctl v1.0.0\n', json.dumps({'verified': False, 'packages': [
                    {'cacheId': identifier, 'tag': 'v1.0.0-rc.1'}]}), failure if failure else json.dumps({'verified': False})]
                with mock.patch.object(acceptance.subprocess, 'check_output', side_effect=responses):
                    with self.assertRaises((AssertionError, subprocess.CalledProcessError)):
                        acceptance.export_repair_package(state / 'missing-desktop', Path('/fixture/cli'), state,
                                                         state / 'evidence', 'v1.0.0', 'v1.0.0-rc.1')
                self.assertFalse((state / 'evidence/recovery-export.json').exists())
                self.assertEqual((cache / acceptance.LEDGER_NAME).read_bytes(), acceptance.LEDGER_EDITED)

    def test_compatible_rollback_requires_both_confirmation_steps(self):
        driver = mock.Mock()
        final = acceptance.confirm_transition(driver, 'v1.0.0-rc.1', True)
        self.assertEqual(final, ('Keep current data and install older version', None))
        self.assertEqual(driver.method_calls, [
            mock.call.activate(r'(?:◉\s*)?Application'), mock.call.toggle('Include prereleases'),
            mock.call.toggle(r'Keep current data \(requires compatibility check\)', True),
            mock.call.activate('Prepare rollback review…'),
            mock.call.wait_text(r'Prepare\ rollback\ to\ v1\.0\.0\-rc\.1\?'),
            mock.call.activate('Prepare review'),
            mock.call.wait_text('Review the data replacements below. Installation has not started.'),
            mock.call.toggle(r'I agree to install the older version while keeping current data\..*'),
        ])
        driver.activate.assert_any_call('Prepare review')
        self.assertNotIn(mock.call.activate(final[0]), driver.method_calls)

    def test_restore_review_reveals_exact_changes_without_giving_consent(self):
        driver = mock.Mock()
        target, safety = 'a' * 64, 'b' * 64
        driver.wait_text.side_effect = [None, None, f'Restore backup: {target}. Newer data saved in backup: {safety}.', None]
        button, review = acceptance.confirm_transition(driver, 'v1.0.0-rc.1', True, restore=True)
        self.assertEqual(button, 'Restore backup and install older version')
        self.assertEqual(review, {'target': target, 'safety': safety})
        driver.toggle.assert_any_call(r'Keep current data \(requires compatibility check\)', False)
        driver.reveal.assert_called_once_with(r'[0-9]+ file changes')
        driver.wait_text.assert_any_call(r'Replace cache/uploaded-4242\.json \([0-9]+ bytes\)')
        self.assertNotIn(mock.call.activate(button), driver.method_calls)
        self.assertNotIn(mock.call.toggle(r'I agree to replace or remove the listed data\..*'), driver.method_calls)

    def test_restore_review_verifies_both_snapshots_with_the_existing_cli(self):
        for bad_safety in (False, True):
            with self.subTest(bad_safety=bad_safety), tempfile.TemporaryDirectory() as directory:
                state = Path(directory)
                cache = acceptance.cache_directory(state)
                cache.mkdir(parents=True)
                (cache / acceptance.LEDGER_NAME).write_bytes(acceptance.LEDGER_UNSUPPORTED)
                target, safety = 'a' * 64, 'b' * 64
                def snapshot(identifier, current, update, data):
                    return json.dumps({'verified': True, 'verification': 'local_manifest', 'dataCompatibility': 'not_checked',
                        'snapshot': {'snapshotId': identifier, 'from': current, 'to': update},
                        'files': [{'area': 'cache', 'path': acceptance.LEDGER_NAME, 'bytes': len(data),
                                   'sha256': acceptance.hashlib.sha256(data).hexdigest()}]})
                responses = [snapshot(target, 'v1.0.0-rc.1', 'v1.0.0', acceptance.LEDGER_EDITED),
                             snapshot(safety, 'v1.0.0', 'v1.0.0-rc.1', b'wrong safety' if bad_safety else acceptance.LEDGER_UNSUPPORTED)]
                with mock.patch.object(acceptance.subprocess, 'check_output', side_effect=responses) as command:
                    if bad_safety:
                        with self.assertRaisesRegex(AssertionError, 'safety snapshot'):
                            acceptance.verify_restore_review(Path('/fixture/cli'), state, 'v1.0.0', 'v1.0.0-rc.1', {'target': target, 'safety': safety})
                    else:
                        restored, view = acceptance.verify_restore_review(Path('/fixture/cli'), state, 'v1.0.0', 'v1.0.0-rc.1', {'target': target, 'safety': safety})
                        self.assertEqual(restored, acceptance.LEDGER_EDITED)
                        self.assertEqual(view['safetySHA256'], acceptance.hashlib.sha256(acceptance.LEDGER_UNSUPPORTED).hexdigest())
                    self.assertEqual([call.args[0] for call in command.call_args_list], [
                        ['/fixture/cli', '-json', 'application', 'recovery', 'inspect', target],
                        ['/fixture/cli', '-json', 'application', 'recovery', 'inspect', safety]])
                    self.assertEqual(command.call_args.kwargs['env']['XDG_CACHE_HOME'], str(state / 'cache'))
                    self.assertEqual((cache / acceptance.LEDGER_NAME).read_bytes(), acceptance.LEDGER_UNSUPPORTED)

    def test_refused_keep_data_preserves_backend_bytes_and_no_installer_operation(self):
        with tempfile.TemporaryDirectory() as directory:
            state = Path(directory)
            cache = acceptance.cache_directory(state)
            cache.mkdir(parents=True)
            (cache / acceptance.LEDGER_NAME).write_bytes(acceptance.LEDGER_UNSUPPORTED)
            app, driver = mock.Mock(), mock.Mock()
            app.poll.return_value = None
            with mock.patch.object(acceptance, 'request', return_value=({'version': 'v1.0.0'}, {})), \
                 mock.patch.object(acceptance.subprocess, 'check_output', return_value='garminctl v1.0.0\n'):
                acceptance.reject_keep_current(driver, app, Path('/fixture/cli'), state, 'http://127.0.0.1:12345',
                                               cache, set(), 'v1.0.0', 'v1.0.0-rc.1')
            self.assertNotIn(mock.call.activate('Restore backup and install older version'), driver.method_calls)
            self.assertEqual((cache / acceptance.LEDGER_NAME).read_bytes(), acceptance.LEDGER_UNSUPPORTED)
            self.assertFalse((cache / 'application-installing').exists())

    @contextlib.contextmanager
    def transition(self, root, outcome='installed', lost_reply=False, wrong_dialog=False, restore=False):
        state, work = root / 'state', root / 'evidence'
        app = mock.Mock(pid=123)
        app.poll.return_value = None
        app.wait.return_value = 0
        driver, restarted = mock.Mock(), mock.Mock()
        base = 'http://127.0.0.1:12345'
        cache = acceptance.cache_directory(state)
        current, target = ('v1.0.0', 'v1.0.0-rc.1') if restore else ('v1.0.0-rc.1', 'v1.0.0')
        target_id, safety_id = 'a' * 64, 'b' * 64
        if restore:
            cache.mkdir(parents=True)
            (cache / acceptance.LEDGER_NAME).write_bytes(acceptance.LEDGER_UNSUPPORTED)
            driver.wait_text.side_effect = lambda pattern: f'Restore backup: {target_id}. Newer data saved in backup: {safety_id}.' if pattern.startswith('Restore backup:') else None
        def start(*_args, **options):
            options['stdout'].write(b'desktop window ready\n')
            options['stdout'].flush()
            return app
        def click(pattern):
            if pattern != ('Restore backup and install older version' if restore else 'Install and restart'):
                return
            if lost_reply:
                raise RuntimeError('lost UI reply')
            operation = cache / 'application-installing' / 'stage-current'
            operation.mkdir(parents=True)
            if restore:
                (cache / acceptance.LEDGER_NAME).write_bytes(acceptance.LEDGER_EDITED)
            result = {'operation': 'test-operation', 'tag': target, 'status': outcome}
            startup = {'operation': 'test-operation', 'desktopVersion': target, 'backendOwned': True,
                       'backendURL': base, 'token': 'do-not-export-ticket'}
            (operation / 'install-result.json').write_text(json.dumps(result))
            (operation / 'startup-ready.json').write_text(json.dumps(startup))
        driver.activate.side_effect = click
        if wrong_dialog:
            driver.wait_text.side_effect = AssertionError('wrong selected version')
        repo = Path(__file__).resolve().parents[2]
        responses = ['garminctl ' + current + '\n', 'garminctl ' + target + '\n']
        metadata = [({'application': 'garminctl', 'version': current}, {}),
                    ({'application': 'garminctl', 'version': target}, {})]
        if restore:
            def snapshot(identifier, old, new, data):
                return json.dumps({'verified': True, 'verification': 'local_manifest', 'dataCompatibility': 'not_checked',
                    'snapshot': {'snapshotId': identifier, 'from': old, 'to': new},
                    'files': [{'area': 'cache', 'path': acceptance.LEDGER_NAME, 'bytes': len(data),
                               'sha256': acceptance.hashlib.sha256(data).hexdigest()}]})
            safety = snapshot(safety_id, current, target, acceptance.LEDGER_UNSUPPORTED)
            responses = ['garminctl ' + current + '\n', 'garminctl ' + current + '\n',
                         snapshot(target_id, target, current, acceptance.LEDGER_EDITED), safety,
                         'garminctl ' + target + '\n', safety]
            metadata.insert(1, metadata[0])
        with contextlib.ExitStack() as stack:
            commands = stack.enter_context(mock.patch.object(acceptance.subprocess, 'check_output', side_effect=responses))
            if restore:
                def consent(pattern, checked=True):
                    if pattern == r'I agree to replace or remove the listed data\..*':
                        inspections = [call.args[0][-1] for call in commands.call_args_list if 'inspect' in call.args[0]]
                        self.assertEqual(inspections, [target_id, safety_id], 'Restore consent preceded backup verification')
                driver.toggle.side_effect = consent
            stack.enter_context(mock.patch.object(acceptance.subprocess, 'Popen', side_effect=start))
            stack.enter_context(mock.patch.object(acceptance, 'driver_for', side_effect=[driver, restarted]))
            find = stack.enter_context(mock.patch.object(acceptance, 'find_application', return_value=456))
            stack.enter_context(mock.patch.object(acceptance, 'request', side_effect=metadata))
            stack.enter_context(mock.patch.object(acceptance, 'check_mcp', return_value=33))
            stack.enter_context(mock.patch.dict(acceptance.runner, free_port=lambda: 12345))
            stack.enter_context(mock.patch.dict(acceptance.runner, stop_app=mock.Mock()))
            stack.enter_context(contextlib.redirect_stdout(io.StringIO()))
            yield (lambda: acceptance.run_transition(Path('/fixture/desktop'), Path('/fixture/cli'), repo,
                   state, work, current, target, restore, restore)), driver, restarted, find, acceptance.runner['stop_app'], work

    def test_success_checks_restart_and_closes_it_before_joining_test_children(self):
        with tempfile.TemporaryDirectory() as directory, self.transition(Path(directory)) as fixture:
            run, driver, restarted, find, stop, work = fixture
            result = run()
            self.assertEqual((result['desktop'], result['cli'], result['backend'], result['mcpTools']),
                             ('v1.0.0', 'v1.0.0', 'v1.0.0', 33))
            self.assertEqual(driver.activate.call_args_list.count(mock.call('Install and restart')), 1)
            restarted.close.assert_called_once()
            restarted.wait_closed.assert_called_once()
            find.assert_called_once_with(Path('/fixture/desktop'), 123)
            stop.assert_called_once()
            self.assertNotIn('do-not-export-ticket', (work / 'installer-observations.json').read_text())

    def test_unknown_failed_or_lost_commit_never_kills_children_or_replays(self):
        for outcome, lost in [('unknown', False), ('failed', False), ('installed', True)]:
            with self.subTest(outcome=outcome, lost=lost), tempfile.TemporaryDirectory() as directory, \
                 self.transition(Path(directory), outcome, lost_reply=lost) as fixture:
                run, driver, restarted, find, stop, work = fixture
                with self.assertRaises((AssertionError, RuntimeError)):
                    run()
                stop.assert_not_called()
                find.assert_not_called()
                restarted.close.assert_not_called()
                self.assertEqual(driver.activate.call_args_list.count(mock.call('Install and restart')), 1)
                self.assertTrue((work / 'installer-observations.json').is_file())

    def test_restore_transition_verifies_data_before_consent_and_checks_safety_after_restart(self):
        with tempfile.TemporaryDirectory() as directory, self.transition(Path(directory), restore=True) as fixture:
            run, driver, restarted, find, stop, work = fixture
            result = run()
            self.assertEqual(result['to'], 'v1.0.0-rc.1')
            self.assertEqual(result['dataReview']['safetySHA256'], acceptance.hashlib.sha256(acceptance.LEDGER_UNSUPPORTED).hexdigest())
            self.assertEqual(driver.activate.call_args_list.count(mock.call('Restore backup and install older version')), 1)
            self.assertNotIn(mock.call('Keep current data and install older version'), driver.activate.call_args_list)
            driver.toggle.assert_any_call(r'I agree to replace or remove the listed data\..*')
            restarted.close.assert_called_once()
            stop.assert_called_once()

    def test_wrong_confirmation_target_stops_before_the_install_click(self):
        with tempfile.TemporaryDirectory() as directory, self.transition(Path(directory), wrong_dialog=True) as fixture:
            run, driver, restarted, find, stop, work = fixture
            with self.assertRaisesRegex(AssertionError, 'wrong selected version'):
                run()
            self.assertNotIn(mock.call('Install and restart'), driver.activate.call_args_list)
            stop.assert_called_once()
            find.assert_not_called()

    def test_disposable_polkit_rule_only_authorizes_its_test_user_and_package_tools(self):
        rule = Path(__file__).with_name('fixtures') / 'native-package.rules'
        program = """
const fs = require('fs'), vm = require('vm'), assert = require('assert/strict');
let rule;
vm.runInNewContext(fs.readFileSync(process.argv[1], 'utf8'), {
  polkit: {Result: {YES: 'yes'}, addRule: value => {rule = value;}}
});
for (const program of ['/usr/bin/garminctl', '/usr/bin/apt-get', '/usr/bin/dnf']) {
  assert.equal(rule({id:'org.freedesktop.policykit.exec', lookup:()=>program}, {user:'garmin-smoke'}), 'yes');
  assert.equal(rule({id:'org.freedesktop.policykit.exec', lookup:()=>program}, {user:'other'}), undefined);
}
assert.equal(rule({id:'org.freedesktop.policykit.exec', lookup:()=>'/bin/bash'}, {user:'garmin-smoke'}), undefined);
assert.equal(rule({id:'other', lookup:()=>'/usr/bin/apt-get'}, {user:'garmin-smoke'}), undefined);
"""
        subprocess.run(['node', '-e', program, str(rule)], check=True, capture_output=True, timeout=10)

    def test_accessibility_driver_uses_a_visible_enabled_action_and_sets_checkbox_once(self):
        states = {'showing', 'sensitive'}
        button = mock.MagicMock()
        button.name = 'Install and restart'
        button.getState.return_value.contains.side_effect = lambda value: value in states
        button.queryAction.return_value.nActions = 1
        button.queryAction.return_value.doAction.return_value = True
        hidden = mock.MagicMock()
        hidden.name = button.name
        hidden.getState.return_value.contains.side_effect = lambda value: False
        root = mock.MagicMock()
        root.name = 'garminctl'
        root.__iter__.side_effect = lambda: iter([hidden, button])
        driver = object.__new__(LinuxUI)
        driver.atspi = SimpleNamespace(STATE_SHOWING='showing', STATE_SENSITIVE='sensitive', STATE_CHECKED='checked')
        driver.dialog = mock.Mock(return_value=root)
        driver.activate('Install and restart')
        button.queryAction.return_value.doAction.assert_called_once_with(0)
        hidden.queryAction.assert_not_called()
        button.queryAction.return_value.doAction.reset_mock()
        button.name = 'Include prereleases'
        def toggle(_):
            states.add('checked')
            return True
        button.queryAction.return_value.doAction.side_effect = toggle
        driver.toggle('Include prereleases', True)
        driver.toggle('Include prereleases', True)
        button.queryAction.return_value.doAction.assert_called_once_with(0)

    def test_accessibility_reads_visible_paragraph_text_without_an_accessible_name(self):
        paragraph = mock.MagicMock()
        paragraph.name = ''
        paragraph.getState.return_value.contains.side_effect = lambda value: value == 'showing'
        paragraph.queryText.return_value.getText.return_value = 'Review the data replacements below. Installation has not started.'
        driver = object.__new__(LinuxUI)
        driver.atspi = SimpleNamespace(STATE_SHOWING='showing', STATE_SENSITIVE='sensitive')
        driver.dialog = mock.Mock(return_value=paragraph)
        def read_once(read):
            result = read()
            self.assertIsNotNone(result, 'Visible AT-SPI Text was ignored because its accessible name is empty')
            return result
        with mock.patch.object(native_ui, 'wait_for', side_effect=read_once):
            self.assertEqual(driver.wait_text(r'Review the data replacements below\. Installation has not started\.'),
                             paragraph.queryText.return_value.getText.return_value)


if __name__ == '__main__':
    unittest.main()

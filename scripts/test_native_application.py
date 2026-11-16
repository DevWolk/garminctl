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
from native_ui import LinuxUI


class NativeApplicationTests(unittest.TestCase):
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

    def test_compatible_rollback_requires_both_confirmation_steps(self):
        driver = mock.Mock()
        final = acceptance.confirm_transition(driver, 'v1.0.0-rc.1', True)
        self.assertEqual(final, 'Keep current data and install older version')
        self.assertEqual(driver.method_calls, [
            mock.call.activate(r'(?:◉\s*)?Application'), mock.call.toggle('Include prereleases'),
            mock.call.toggle(r'Keep current data \(requires compatibility check\)'),
            mock.call.activate('Prepare rollback review…'),
            mock.call.wait_text(r'Prepare\ rollback\ to\ v1\.0\.0\-rc\.1\?'),
            mock.call.activate('Prepare review'),
            mock.call.wait_text('Review the data replacements below. Installation has not started.'),
            mock.call.toggle(r'I agree to install the older version while keeping current data\..*'),
        ])
        driver.activate.assert_any_call('Prepare review')
        self.assertNotIn(mock.call.activate(final), driver.method_calls)

    @contextlib.contextmanager
    def transition(self, root, outcome='installed', lost_reply=False, wrong_dialog=False):
        state, work = root / 'state', root / 'evidence'
        app = mock.Mock(pid=123)
        app.poll.return_value = None
        app.wait.return_value = 0
        driver, restarted = mock.Mock(), mock.Mock()
        base = 'http://127.0.0.1:12345'
        cache = acceptance.cache_directory(state)
        def start(*_args, **options):
            options['stdout'].write(b'desktop window ready\n')
            options['stdout'].flush()
            return app
        def click(pattern):
            if pattern != 'Install and restart':
                return
            if lost_reply:
                raise RuntimeError('lost UI reply')
            operation = cache / 'application-installing' / 'stage-current'
            operation.mkdir(parents=True)
            result = {'operation': 'test-operation', 'tag': 'v1.0.0', 'status': outcome}
            startup = {'operation': 'test-operation', 'desktopVersion': 'v1.0.0', 'backendOwned': True,
                       'backendURL': base, 'token': 'do-not-export-ticket'}
            (operation / 'install-result.json').write_text(json.dumps(result))
            (operation / 'startup-ready.json').write_text(json.dumps(startup))
        driver.activate.side_effect = click
        if wrong_dialog:
            driver.wait_text.side_effect = AssertionError('wrong selected version')
        repo = Path(__file__).resolve().parent.parent
        with contextlib.ExitStack() as stack:
            stack.enter_context(mock.patch.object(acceptance.subprocess, 'check_output', side_effect=[
                'garminctl v1.0.0-rc.1\n', 'garminctl v1.0.0\n']))
            stack.enter_context(mock.patch.object(acceptance.subprocess, 'Popen', side_effect=start))
            stack.enter_context(mock.patch.object(acceptance, 'driver_for', side_effect=[driver, restarted]))
            find = stack.enter_context(mock.patch.object(acceptance, 'find_application', return_value=456))
            stack.enter_context(mock.patch.object(acceptance, 'request', side_effect=[
                ({'application': 'garminctl', 'version': 'v1.0.0-rc.1'}, {}),
                ({'application': 'garminctl', 'version': 'v1.0.0'}, {})]))
            stack.enter_context(mock.patch.object(acceptance, 'check_mcp', return_value=33))
            stack.enter_context(mock.patch.dict(acceptance.runner, free_port=lambda: 12345))
            stack.enter_context(mock.patch.dict(acceptance.runner, stop_app=mock.Mock()))
            stack.enter_context(contextlib.redirect_stdout(io.StringIO()))
            yield (lambda: acceptance.run_transition(Path('/fixture/desktop'), Path('/fixture/cli'), repo,
                   state, work, 'v1.0.0-rc.1', 'v1.0.0')), driver, restarted, find, acceptance.runner['stop_app'], work

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
for (const program of ['/usr/bin/apt-get', '/usr/bin/dnf']) {
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


if __name__ == '__main__':
    unittest.main()

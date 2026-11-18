"""Keep native security failures visible while collecting independent UI evidence."""

import contextlib
import io
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest import mock

import native_acceptance
from native_fixture import Commands


class NativeAcceptanceTests(unittest.TestCase):
    def test_application_gate_requires_matching_roles_and_restores_stable_channel(self):
        observation = {'facts': {'Desktop': 'v0.0.1', 'CLI': 'v0.0.1', 'Backend (reused)': 'v0.0.1',
                                 'Installation': 'Windows Setup' if os.name == 'nt' else 'DEB',
                                 'Platform': 'windows / amd64' if os.name == 'nt' else 'linux / amd64'},
                       'includePrerelease': False, 'downloadDisabled': True}
        commands = mock.Mock()
        commands.call.side_effect = [observation, dict(observation, includePrerelease=True), observation]
        result = {'version': 'v0.0.1'}
        with mock.patch.dict(os.environ, PACKAGE_FORMAT='deb'):
            native_acceptance.application_scenario(commands, result)
        self.assertEqual(commands.call.call_args_list, [mock.call('application-open'),
                         mock.call('application-channel', includePrerelease=True),
                         mock.call('application-channel', includePrerelease=False)])
        self.assertEqual(len(result['application']), 3)

        mixed = dict(observation, facts=dict(observation['facts'], CLI='v0.0.0'))
        commands.call.side_effect = None
        commands.call.return_value = mixed
        commands.call.reset_mock()
        with self.assertRaises(AssertionError):
            native_acceptance.application_scenario(commands, {'version': 'v0.0.1'})
        commands.call.assert_called_once_with('application-open')

    def test_visible_confirmation_requires_an_explicit_no(self):
        for choice in ('escape', 'close'):
            with self.subTest(choice=choice), tempfile.TemporaryDirectory() as directory:
                target = Path(directory) / 'export.xml'
                commands = mock.Mock()
                commands.call.side_effect = [{'id': 'export'}, {'done': False}]
                driver = mock.Mock()
                observation = {'confirmation': 'remained-open', 'choice': choice}
                driver.replace.side_effect = [observation, None]

                def finished(identifier):
                    self.assertEqual(identifier, 'export')
                    self.assertEqual(driver.replace.call_args_list, [mock.call(choice), mock.call('no')])

                result = native_acceptance.replace_export(commands, driver, target, b'new bytes', choice, finished)
                self.assertEqual(result, observation)
                self.assertEqual(target.read_bytes(), b'keep existing export')

    def test_visible_confirmation_cannot_have_already_overwritten_the_file(self):
        with tempfile.TemporaryDirectory() as directory:
            target = Path(directory) / 'export.xml'
            commands = mock.Mock()
            commands.call.return_value = {'id': 'export'}
            driver = mock.Mock()
            def wrong_replacement(_):
                target.write_bytes(b'wrong overwrite')
                return {'confirmation': 'remained-open'}
            driver.replace.side_effect = wrong_replacement
            finished = mock.Mock()
            with self.assertRaisesRegex(AssertionError, 'Unanswered confirmation'):
                native_acceptance.replace_export(commands, driver, target, b'new bytes', 'escape', finished)
            driver.replace.assert_called_once_with('escape')
            finished.assert_not_called()

    def test_visible_confirmation_cannot_lose_its_pending_download(self):
        with tempfile.TemporaryDirectory() as directory:
            commands = mock.Mock()
            commands.call.side_effect = [{'id': 'export'}, {'done': True}]
            driver = mock.Mock()
            driver.replace.return_value = {'confirmation': 'remained-open'}
            finished = mock.Mock()
            with self.assertRaisesRegex(AssertionError, 'lost its pending download'):
                native_acceptance.replace_export(commands, driver, Path(directory) / 'export.xml', b'new bytes', 'close', finished)
            driver.replace.assert_called_once_with('close')
            finished.assert_not_called()

    def test_a_dismissed_dialog_with_a_pending_download_still_fails(self):
        with tempfile.TemporaryDirectory() as directory:
            commands = mock.Mock()
            commands.call.return_value = {'id': 'export'}
            driver = mock.Mock()
            driver.replace.return_value = None
            finished = mock.Mock(side_effect=TimeoutError('Download stayed pending'))
            with self.assertRaisesRegex(TimeoutError, 'stayed pending'):
                native_acceptance.replace_export(commands, driver, Path(directory) / 'export.xml', b'new bytes', 'escape', finished)
            driver.replace.assert_called_once_with('escape')

    def test_origin_failure_does_not_prevent_a_fresh_ui_window_or_pass_the_gate(self):
        processes = []

        def start(_arguments, **options):
            process = mock.Mock(pid=100 + len(processes))
            process.poll.return_value = None
            options['stdout'].write(b'desktop window ready\n')
            options['stdout'].flush()
            processes.append(process)
            return process

        def commands():
            channel = Commands()
            channel.boots.append('test-page')
            return channel

        response = mock.MagicMock()
        response.__enter__.return_value.status = 200
        with tempfile.TemporaryDirectory() as directory, contextlib.ExitStack() as stack:
            work = Path(directory)
            stack.enter_context(mock.patch.dict(os.environ, NATIVE_RESULTS_DIR=str(work / 'evidence')))
            stack.enter_context(mock.patch.object(native_acceptance, 'Commands', side_effect=commands))
            stack.enter_context(mock.patch.object(native_acceptance, 'missing_bus', return_value={}))
            stack.enter_context(mock.patch.object(native_acceptance.subprocess, 'Popen', side_effect=start))
            stack.enter_context(mock.patch.object(native_acceptance.urllib.request, 'urlopen', return_value=response))
            proxy = stack.enter_context(mock.patch.object(native_acceptance, 'fixture_proxy'))
            proxy.return_value.__enter__.return_value = 12345
            stack.enter_context(mock.patch.object(native_acceptance, 'driver_for', return_value=object()))
            runtime = stack.enter_context(mock.patch.object(native_acceptance, 'runtime_scenarios',
                                                          side_effect=AssertionError('synthetic origin regression')))
            ui = stack.enter_context(mock.patch.object(native_acceptance, 'ui_scenarios'))
            stop = mock.Mock()
            with contextlib.redirect_stdout(io.StringIO()), self.assertRaisesRegex(AssertionError, 'origin regression'):
                native_acceptance.run_native_acceptance('desktop', 'cli', work, {}, work, lambda: 12344, stop, 'v0.0.1')

            runtime.assert_called_once()
            ui.assert_called_once()
            self.assertIsNot(runtime.call_args.args[0], ui.call_args.args[0], 'Suites shared a command channel')
            self.assertIsNot(runtime.call_args.args[2], ui.call_args.args[2], 'Suites shared a native window')
            self.assertEqual(len(processes), 3, 'Expected one reused backend and two isolated windows')
            environments = [call.kwargs['env'] for call in native_acceptance.subprocess.Popen.call_args_list]
            for key in ('APPDATA', 'LOCALAPPDATA', 'XDG_CONFIG_HOME', 'XDG_CACHE_HOME'):
                self.assertNotEqual(environments[1][key], environments[2][key],
                                    'Independent native suites reused the same browser profile')
            self.assertCountEqual([call.args[0] for call in stop.call_args_list], processes)
            receipt = json.loads((work / 'evidence/native-acceptance.json').read_text())
            self.assertEqual(receipt['status'], 'failed')
            self.assertEqual(len(receipt['failures']), 1)
            self.assertIn('origin regression', receipt['failures'][0]['error'])


if __name__ == '__main__':
    unittest.main()

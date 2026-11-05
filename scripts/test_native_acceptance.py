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
                native_acceptance.run_native_acceptance('desktop', 'cli', work, {}, work, lambda: 12344, stop)

            runtime.assert_called_once()
            ui.assert_called_once()
            self.assertIsNot(runtime.call_args.args[0], ui.call_args.args[0], 'Suites shared a command channel')
            self.assertIsNot(runtime.call_args.args[2], ui.call_args.args[2], 'Suites shared a native window')
            self.assertEqual(len(processes), 3, 'Expected one reused backend and two isolated windows')
            self.assertCountEqual([call.args[0] for call in stop.call_args_list], processes)
            receipt = json.loads((work / 'evidence/native-acceptance.json').read_text())
            self.assertEqual(receipt['status'], 'failed')
            self.assertEqual(len(receipt['failures']), 1)
            self.assertIn('origin regression', receipt['failures'][0]['error'])


if __name__ == '__main__':
    unittest.main()

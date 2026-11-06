"""Native UI drivers must exercise window events rather than process termination."""

import unittest
from unittest import mock
from types import SimpleNamespace

import native_ui


class NativeLinuxInputTests(unittest.TestCase):
    def replacement_driver(self, message):
        def element(name, children=()):
            item = mock.Mock()
            item.name = name
            item.__iter__ = mock.Mock(side_effect=lambda: iter(children))
            return item

        label = element(message)
        dialog = element('Question', [label])
        application = element('garminctl', [dialog])
        application.get_process_id.return_value = 321
        driver = object.__new__(native_ui.LinuxUI)
        driver.pid = 321
        driver.atspi = SimpleNamespace(Registry=SimpleNamespace(getDesktop=lambda _: [application]))
        driver.click = mock.Mock()
        driver.focus = mock.Mock(return_value=12345)
        return driver, dialog

    def read_once(self, read, **_):
        value = read()
        if value is None:
            raise TimeoutError('Dialog missing')
        return value

    def test_replacement_uses_the_actual_gtk_window_title(self):
        for choice in ('no', 'yes', 'escape', 'close'):
            with self.subTest(choice=choice):
                driver, dialog = self.replacement_driver('native-export.xml already exists. Replace it?')
                with mock.patch.object(native_ui, 'wait_for', side_effect=self.read_once), \
                     mock.patch.object(native_ui.subprocess, 'run') as run, \
                     mock.patch.object(native_ui, 'send_delete_window') as close:
                    driver.replace(choice)
                if choice in ('no', 'yes'):
                    driver.click.assert_called_once_with(dialog, 'Cancel' if choice == 'no' else 'Replace')
                else:
                    driver.focus.assert_called_once_with('Question')
                    if choice == 'escape':
                        self.assertIn('Escape', run.call_args.args[0])
                    else:
                        close.assert_called_once_with(12345)

    def test_unrelated_gtk_question_is_not_a_replacement_dialog(self):
        driver, _ = self.replacement_driver('Unrelated question')
        with mock.patch.object(native_ui, 'wait_for', side_effect=self.read_once):
            with self.assertRaisesRegex(AssertionError, 'replacement question'):
                driver.replace('yes')
        driver.click.assert_not_called()

    def test_save_replaces_the_entire_filename_including_its_extension(self):
        driver = object.__new__(native_ui.LinuxUI)
        driver.focus = mock.Mock()
        state = {'selected': False, 'text': 'GarminDevice.xml'}
        entry = mock.Mock()
        entry.__iter__ = mock.Mock(side_effect=lambda: iter([]))
        entry.queryText.return_value.getText.side_effect = lambda *_: state['text']
        dialog = mock.Mock()
        dialog.__iter__ = mock.Mock(side_effect=lambda: iter([entry]))
        dialog.queryText.side_effect = NotImplementedError
        driver.dialog = mock.Mock(return_value=dialog)
        driver.click = mock.Mock()

        def input_event(command, **_):
            if command[:3] == ['xdotool', 'key', '--clearmodifiers'] and command[-1] == 'ctrl+a':
                state['selected'] = True
            if command[:2] == ['xdotool', 'type']:
                state['text'] = command[-1] + ('' if state['selected'] else '.xml')

        with mock.patch.object(native_ui.subprocess, 'run', side_effect=input_event):
            driver.save('/tmp/native-export.xml')
        self.assertEqual(state['text'], '/tmp/native-export.xml')
        driver.click.assert_called_once_with(dialog, 'Save')

    def test_save_activates_the_save_button_for_the_entered_location(self):
        driver = object.__new__(native_ui.LinuxUI)
        driver.focus = mock.Mock()
        entry = mock.Mock()
        entry.queryText.return_value.getText.return_value = '/tmp/native-export.xml'
        dialog = mock.Mock()
        dialog.__iter__ = mock.Mock(return_value=iter([entry]))
        dialog.queryText.side_effect = NotImplementedError
        driver.dialog = mock.Mock(return_value=dialog)
        driver.click = mock.Mock()
        with mock.patch.object(native_ui.subprocess, 'run') as run:
            driver.save('/tmp/native-export.xml')
        driver.click.assert_called_once_with(dialog, 'Save')
        self.assertFalse(any('Return' in call.args[0] for call in run.call_args_list))

    def test_close_sends_the_window_manager_request(self):
        driver = object.__new__(native_ui.LinuxUI)
        driver.pid = 321
        driver.focus = mock.Mock(return_value=12345)
        with mock.patch.object(native_ui, 'send_delete_window') as close, mock.patch.object(native_ui.os, 'kill') as kill:
            driver.close()
        driver.focus.assert_called_once_with('garminctl')
        close.assert_called_once_with(12345)
        kill.assert_not_called()


if __name__ == '__main__':
    unittest.main()

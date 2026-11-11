"""Actual OpenSSL/PFX regression checks using disposable signing identities."""

import base64
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import textwrap
import unittest


REPOSITORY = Path(__file__).resolve().parent.parent
PAYLOADS = (
    'garminctl_v1.2.0_windows_amd64_setup.exe',
    'garminctl_1.2.0_linux_amd64.deb',
    'garminctl_1.2.0_linux_amd64.rpm',
    'garminctl_1.2.0_linux_amd64.tar.gz',
    'garminctl_1.2.0_linux_arm64.tar.gz',
    'garminctl_1.2.0_windows_amd64.zip',
    'garminctl_1.2.0_windows_arm64.zip',
)


class UpdateSigningTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.identity = tempfile.TemporaryDirectory()
        cls.addClassCleanup(cls.identity.cleanup)
        cls.identity_path = Path(cls.identity.name)
        (cls.identity_path / 'password').write_text('synthetic-password')
        commands = [
            ['openssl', 'req', '-x509', '-newkey', 'rsa:3072', '-nodes',
             '-sha256', '-days', '1', '-subj', '/CN=garminctl synthetic signing test',
             '-addext', 'extendedKeyUsage=codeSigning',
             '-keyout', 'key.pem', '-out', 'certificate.pem'],
            ['openssl', 'pkcs12', '-export', '-inkey', 'key.pem',
             '-in', 'certificate.pem', '-passout', 'file:password', '-out', 'signing.pfx'],
        ]
        for command in commands:
            subprocess.run(command, cwd=cls.identity_path, check=True,
                           stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=30)
        cls.public_der = subprocess.check_output(
            ['openssl', 'pkey', '-in', str(cls.identity_path / 'key.pem'),
             '-pubout', '-outform', 'DER'], timeout=10)
        (cls.identity_path / 'key.pem').unlink()

    def setUp(self):
        directory = tempfile.TemporaryDirectory(prefix='garmin signing ')
        self.addCleanup(directory.cleanup)
        self.root = Path(directory.name)
        (self.root / 'scripts').mkdir()
        (self.root / 'build').mkdir()
        for filename in ['sign-update-packages.sh', 'sign-windows.sh']:
            shutil.copyfile(REPOSITORY / 'scripts' / filename, self.root / 'scripts' / filename)
        (self.root / 'build' / 'update-public-key.b64').write_text(
            base64.b64encode(self.public_der).decode() + '\n')
        self.assets = self.root / 'assets'
        self.assets.mkdir()
        for filename in PAYLOADS:
            (self.assets / filename).write_bytes(b'synthetic package; never executed: ' + filename.encode())
        self.signing = self.root / 'signing'
        shutil.copytree(self.identity_path, self.signing)
        self.environment = os.environ.copy()
        for name in ['WINDOWS_SIGNING_PFX_BASE64', 'WINDOWS_SIGNING_PASSWORD']:
            self.environment.pop(name, None)
        self.environment.update(WINDOWS_SIGNING_MODE='certificate', WINDOWS_SIGNING_DIR=str(self.signing))

    def run_signer(self, operation='sign'):
        return subprocess.run(
            ['bash', str(self.root / 'scripts' / 'sign-update-packages.sh'), operation, str(self.assets)],
            cwd=self.root, env=self.environment, capture_output=True, text=True, timeout=30)

    def sign_successfully(self):
        result = self.run_signer()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(len(list(self.assets.glob('*.sig'))), len(PAYLOADS))
        for filename in PAYLOADS:
            self.assertEqual((self.assets / (filename + '.sig')).stat().st_size, 384)

    def test_same_pfx_signs_all_payloads_and_public_only_verification_works(self):
        self.sign_successfully()
        shutil.rmtree(self.signing)
        self.environment['WINDOWS_SIGNING_MODE'] = 'none'
        result = self.run_signer('verify')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(list(self.assets.glob('.updater-sign-*')), [])

    def test_certificate_mode_is_required_without_generating_an_identity(self):
        for mode in ['none', 'self-signed']:
            with self.subTest(mode=mode):
                self.environment['WINDOWS_SIGNING_MODE'] = mode
                result = self.run_signer()
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(list(self.assets.glob('*.sig')), [])

    def test_wrong_existing_certificate_is_rejected_before_publishing(self):
        shutil.copyfile(REPOSITORY / 'build' / 'update-public-key.b64',
                        self.root / 'build' / 'update-public-key.b64')
        result = self.run_signer()
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(list(self.assets.glob('*.sig')), [])

    def test_missing_pfx_or_password_cannot_fall_back_to_a_new_key(self):
        (self.signing / 'signing.pfx').unlink()
        result = self.run_signer()
        self.assertNotEqual(result.returncode, 0)
        self.assertFalse((self.signing / 'signing.pfx').exists())
        self.assertEqual(list(self.assets.glob('*.sig')), [])

    def test_missing_or_extra_payloads_are_rejected(self):
        (self.assets / PAYLOADS[0]).unlink()
        result = self.run_signer()
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(list(self.assets.glob('*.sig')), [])
        (self.assets / PAYLOADS[0]).write_bytes(b'synthetic')
        (self.assets / 'unexpected-payload.bin').write_bytes(b'synthetic')
        self.assertNotEqual(self.run_signer().returncode, 0)

    def test_zero_and_oversized_payloads_are_rejected(self):
        target = self.assets / PAYLOADS[0]
        for size in [0, (512 << 20) + 1]:
            with self.subTest(size=size):
                with target.open('wb') as output:
                    output.truncate(size)
                self.assertNotEqual(self.run_signer().returncode, 0)
                self.assertEqual(list(self.assets.glob('*.sig')), [])

    def test_symlinked_payload_is_rejected_without_signing(self):
        target = self.assets / PAYLOADS[0]
        target.unlink()
        target.symlink_to(self.signing / 'certificate.pem')
        self.assertNotEqual(self.run_signer().returncode, 0)
        self.assertEqual(list(self.assets.glob('*.sig')), [])

    def test_payload_tampering_and_invalid_signature_length_fail_offline(self):
        self.sign_successfully()
        target = self.assets / PAYLOADS[0]
        original = target.read_bytes()
        target.write_bytes(bytes([original[0] ^ 1]) + original[1:])
        self.assertNotEqual(self.run_signer('verify').returncode, 0)
        target.write_bytes(original)
        (self.assets / (PAYLOADS[0] + '.sig')).write_bytes(bytes(64))
        self.assertNotEqual(self.run_signer('verify').returncode, 0)

    def test_missing_signature_and_asset_supplied_certificate_do_not_grant_trust(self):
        self.assertNotEqual(self.run_signer('verify').returncode, 0)
        self.sign_successfully()
        shutil.copyfile(self.signing / 'certificate.pem', self.assets / 'windows-signing-certificate.pem')
        shutil.copyfile(REPOSITORY / 'build' / 'update-public-key.b64',
                        self.root / 'build' / 'update-public-key.b64')
        self.assertNotEqual(self.run_signer('verify').returncode, 0)

    def test_mixed_versions_cannot_be_signed_as_one_release(self):
        archive = self.assets / PAYLOADS[3]
        archive.rename(self.assets / archive.name.replace('1.2.0', '1.3.0'))
        self.assertNotEqual(self.run_signer().returncode, 0)
        self.assertEqual(list(self.assets.glob('*.sig')), [])

    def test_late_signing_failure_publishes_no_signatures(self):
        wrappers = self.root / 'wrappers'
        wrappers.mkdir()
        actual = shutil.which('openssl')
        self.assertIsNotNone(actual)
        wrapper = wrappers / 'openssl'
        wrapper.write_text(textwrap.dedent('''\
            #!/usr/bin/env python3
            import os,sys
            from pathlib import Path
            if '-sign' in sys.argv and sys.argv[-1].endswith('.rpm'):
                print('synthetic late signing failure', file=sys.stderr)
                sys.exit(89)
            os.execv(os.environ['SYNTHETIC_REAL_OPENSSL'], ['openssl']+sys.argv[1:])
        '''))
        wrapper.chmod(0o700)
        self.environment['SYNTHETIC_REAL_OPENSSL'] = actual
        self.environment['PATH'] = str(wrappers) + os.pathsep + self.environment['PATH']
        result = self.run_signer()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('synthetic late signing failure', result.stderr)
        self.assertEqual(list(self.assets.glob('*.sig')), [])
        self.assertEqual(list(self.assets.glob('.updater-sign-*')), [])


if __name__ == '__main__':
    unittest.main()

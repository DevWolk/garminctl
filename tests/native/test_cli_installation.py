"""Standalone CLI updates use private prefixes, never a neighboring native install."""

import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import tarfile
import tempfile
import unittest
import zipfile


REPOSITORY = Path(__file__).resolve().parents[2]
MODULE = 'github.com/DevWolk/garminctl'
VERSIONS = ('v0.0.0-cli-test.1', 'v0.0.0-cli-test.2')


class StandaloneCLIInstallationTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.temporary = tempfile.TemporaryDirectory(prefix='garmin standalone CLI ')
        cls.addClassCleanup(cls.temporary.cleanup)
        cls.root = Path(cls.temporary.name)
        cls.go = shutil.which('go')
        if not cls.go:
            raise RuntimeError('the Docker Go toolchain is required')
        original_cache = Path(subprocess.check_output(
            [cls.go, 'env', 'GOMODCACHE'], text=True, timeout=10).strip())
        cls.proxy = cls.root / 'proxy'
        escaped = ''.join('!' + c.lower() if c.isupper() else c for c in MODULE)
        versions = cls.proxy / escaped / '@v'
        versions.mkdir(parents=True)
        # The real production trees include their embedded assets. Scope the
        # fixture to these roots so private prompts/build outputs are excluded.
        source = [REPOSITORY / 'go.mod', REPOSITORY / 'go.sum']
        for directory in ['cmd', 'internal']:
            source.extend((REPOSITORY / directory).rglob('*'))
        files = sorted(str(p.relative_to(REPOSITORY)) for p in source
                       if p.is_file() and not p.is_symlink())
        for version in VERSIONS:
            (versions / (version + '.info')).write_text(json.dumps({
                'Version': version, 'Time': '2026-11-15T00:00:00Z'}))
            shutil.copyfile(REPOSITORY / 'go.mod', versions / (version + '.mod'))
            with zipfile.ZipFile(versions / (version + '.zip'), 'w',
                                 compression=zipfile.ZIP_DEFLATED, compresslevel=1) as archive:
                for name in files:
                    archive.write(REPOSITORY / name, MODULE + '@' + version + '/' + name)
        (versions / 'list').write_text('\n'.join(VERSIONS) + '\n')
        cls.environment = os.environ.copy()
        cls.environment.update(
            CGO_ENABLED='0', GOWORK='off', GOTOOLCHAIN='local',
            GOPATH=str(cls.root / 'gopath'), GOMODCACHE=str(cls.root / 'modules'),
            # Disposable versions come only from this fixture. Dependencies come
            # from the existing pinned download cache; no public proxy is used.
            GOPROXY=cls.proxy.as_uri() + ',' + (original_cache / 'cache/download').as_uri(),
            GOSUMDB='off', GOFLAGS='-p=2',
            XDG_CONFIG_HOME=str(cls.root / 'config'),
            XDG_CACHE_HOME=str(cls.root / 'cache'))
        cls.built = {}
        for version in VERSIONS:
            target = cls.root / version
            target.mkdir()
            cls.install(version, target)
            cls.built[version] = target / 'garminctl'

    @classmethod
    def install(cls, version, directory, check=True):
        environment = dict(cls.environment, GOBIN=str(directory))
        return subprocess.run(
            [cls.go, 'install', '-ldflags', '-X main.version=' + version,
             MODULE + '/cmd/garminctl@' + version], cwd=cls.root, env=environment,
            capture_output=True, text=True, check=check, timeout=180)

    def setUp(self):
        self.case = Path(tempfile.mkdtemp(dir=self.root, prefix='case '))
        self.managed = self.case / 'native installation'
        self.managed.mkdir()
        for name in ['garminctl', 'garmin-desktop', 'registration', 'account.json']:
            (self.managed / name).write_bytes(b'untouched native installation: ' + name.encode())
        (self.managed / 'garminctl').chmod(0o755)
        self.original = {p.name: p.read_bytes() for p in self.managed.iterdir()}
        self.data = Path(self.environment['XDG_CONFIG_HOME']) / 'garminctl'
        self.data.mkdir(parents=True, exist_ok=True)
        for name in ['garmin-connect.json', 'garmin-connect.json.mfa', 'uploaded-42.json']:
            (self.data / name).write_bytes(b'preserved standalone test data: ' + name.encode())
        self.data_before = {p.name: p.read_bytes() for p in self.data.iterdir()}

    def assert_managed_untouched(self):
        self.assertEqual({p.name: p.read_bytes() for p in self.managed.iterdir()}, self.original)
        self.assertEqual({p.name: p.read_bytes() for p in self.data.iterdir()}, self.data_before)

    def assert_version(self, path, version):
        output = subprocess.check_output(
            [str(path), 'version'], env=self.environment, text=True, timeout=5)
        self.assertEqual(output.strip(), 'garminctl ' + version)
        build = subprocess.check_output(
            [self.go, 'version', '-m', str(path)], text=True, timeout=5)
        self.assertIn('\tmod\t' + MODULE + '\t' + version + '\t', build)

    def test_go_install_replaces_only_explicit_gobin_and_retains_previous(self):
        destination = self.case / 'source CLI bin'
        destination.mkdir()
        old, new = VERSIONS
        shutil.copy2(self.built[old], destination / 'garminctl')
        shutil.copy2(destination / 'garminctl', destination / 'garminctl.previous')
        # A different CLI earlier on PATH must not become the installation target.
        original_path = self.environment['PATH']
        self.environment['PATH'] = str(self.managed) + os.pathsep + original_path
        try:
            self.assertEqual(Path(shutil.which('garminctl', path=self.environment['PATH'])),
                             self.managed / 'garminctl')
            self.install(new, destination)
        finally:
            self.environment['PATH'] = original_path
        self.assert_version(destination / 'garminctl', new)
        self.assert_version(destination / 'garminctl.previous', old)
        self.assert_managed_untouched()

    def test_failed_go_install_keeps_existing_cli(self):
        destination = self.case / 'source CLI bin'
        destination.mkdir()
        target = destination / 'garminctl'
        shutil.copy2(self.built[VERSIONS[0]], target)
        before = hashlib.sha256(target.read_bytes()).digest()
        failed = self.install('v0.0.0-cli-missing', destination, check=False)
        self.assertNotEqual(failed.returncode, 0)
        self.assertEqual(hashlib.sha256(target.read_bytes()).digest(), before)
        self.assert_version(target, VERSIONS[0])
        self.assert_managed_untouched()

    def test_portable_archive_replacement_and_return_to_previous(self):
        destination = self.case / 'portable CLI bin'
        destination.mkdir()
        old, new = VERSIONS
        target = destination / 'garminctl'
        shutil.copy2(self.built[old], target)
        package = self.case / 'portable.tar.gz'
        with tarfile.open(package, 'w:gz') as archive:
            archive.add(self.built[new], arcname='garminctl')
        unpacked = self.case / 'unpacked'
        unpacked.mkdir()
        with tarfile.open(package) as archive:
            archive.extractall(unpacked, filter='data')
        candidate = destination / 'garminctl.new'
        subprocess.run(['install', '-m', '0755', str(unpacked / 'garminctl'),
                        str(candidate)], check=True, timeout=5)
        self.assert_version(candidate, new)
        previous = destination / 'garminctl.previous'
        shutil.copy2(target, previous)
        subprocess.run(['mv', '-f', str(candidate), str(target)], check=True, timeout=5)
        self.assert_version(target, new)
        self.assert_version(previous, old)
        shutil.copy2(previous, candidate)
        subprocess.run(['mv', '-f', str(candidate), str(target)], check=True, timeout=5)
        self.assert_version(target, old)
        self.assert_managed_untouched()


if __name__ == '__main__':
    unittest.main()

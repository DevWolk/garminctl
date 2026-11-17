# Building and publishing releases

[`release.yml`](../.github/workflows/release.yml) publishes the exact assets
that pass [`packages.yml`](../.github/workflows/packages.yml). Local package
builds and manual workflow runs produce artifacts without publishing a release.

## Build packages

Follow [development setup](../CONTRIBUTING.md#development-setup), then set the
chosen version in the command, for example:

```sh
make check
make package VERSION=v1.2.3
```

[`package-windows.sh`](../scripts/package-windows.sh) creates an amd64 Setup
with the CLI, desktop app and WebView2 bootstrapper. It downloads the
bootstrapper from Microsoft and packages it only when its Authenticode
signature chains to the Microsoft root in `build/windows` and names Microsoft
Corporation; the check fetches Microsoft's revocation list, so the first
build after a download needs internet access. When the signing certificate
has expired, it is checked at the time the file's countersignature states;
the build tool cannot verify that time.
[`package-linux.sh`](../scripts/package-linux.sh) creates amd64 DEB/RPM
packages with the desktop app, native libusb CLI and udev rule.
The outputs go to `dist/`; builds without `VERSION` use `git describe`, which
can include a commit ID and `dirty`.

CI also builds portable CLI archives using [GoReleaser](../.goreleaser.yml):
Windows ZIP and Linux tar.gz archives for amd64 and arm64. These CLIs have CGO
disabled; native Linux USB support is supplied by the DEB/RPM packages.

## Configure Windows signing

Application update packages reuse this Windows signing identity. No additional
updater signing secret is required. `build/update-public-key.b64` contains only
the existing certificate's public RSA key, compiled into CLI and desktop builds.
It is not a private key or an extra certificate to install.

The release workflow enables `sign_updates` and requires `certificate` mode.
After Authenticode and packaging, it signs every Setup/DEB/RPM/archive with the
same PFX and verifies each raw `.sig` against the compiled public key. Checksums
include those signatures. Publication verifies them again without the PFX.
Ordinary PR package checks leave `sign_updates` disabled and receive no signing
secrets. Signed package preparation does not install or roll back the application.
Releases without the required `.sig` assets are rejected by package preparation;
the Releases page remains available for manual downloads.

[`sign-windows.sh`](../scripts/sign-windows.sh) defaults to
`WINDOWS_SIGNING_MODE=none`. Its supported modes are:

| Mode | Signing material |
| --- | --- |
| `none` | No signature. |
| `certificate` | Existing `signing.pfx` and `password` files in `WINDOWS_SIGNING_DIR`, or both signing secrets below. Missing material fails the build. |
| `self-signed` | Local builds only: create or reuse an identity in `WINDOWS_SIGNING_DIR`. |

The default signing directory is `.cache/windows-signing`. For local certificate
builds, place the PFX and password there, then set the mode in the ignored
`.make/options.local.mk`:

```makefile
WINDOWS_SIGNING_MODE := certificate
```

Use `WINDOWS_SIGNING_DIR` to select another container-visible directory. The
signer sets directory permissions to 700 and private-file permissions to 600.
It retains local signing material for reuse. Optional `WINDOWS_TIMESTAMP_URL`
adds an RFC 3161 timestamp request; signed CI builds set it, and signing fails
when the timestamp service does not answer.

For CI, set the Actions variable `WINDOWS_SIGNING_MODE` to `certificate` and
configure these Actions secrets:

| Secret | Value |
| --- | --- |
| `WINDOWS_SIGNING_PFX_BASE64` | Base64-encoded PFX. |
| `WINDOWS_SIGNING_PASSWORD` | PFX password. |

The reusable workflow accepts only `none` or `certificate`. Pull request package
checks use `none` and receive no signing secrets. Release builds remove the
default signing staging files in an `always()` cleanup step.

The package script signs the application and CLI before packaging, the NSIS
hook signs the generated uninstaller, and the script signs Setup last.
The [GoReleaser hook](../scripts/sign-release-binary.sh) signs Windows CLIs
before archiving. Each signing operation verifies the result with
`osslsigncode` and fails on error. Signed assets include the public certificate;
the private PFX and password are excluded. Signature verification in the
builder does not establish acceptance by another Windows host's policy.

## Publish a tag

Merge and validate the release commit on `main`, then create and push an
annotated `v*` tag. Replace the example version:

```sh
release_version=v1.2.3
git switch main
git pull --ff-only
git tag -a "$release_version" -m "Release $release_version"
git push origin "$release_version"
```

The workflow requires a version matching `v?major.minor.patch` with an optional
prerelease suffix. The tag trigger requires the `v` prefix. It then:

1. Requires the tagged commit to be the tip of `origin/main`. A tag on an
   older commit is refused, and so is a rerun after `main` has moved.
2. Builds the toolchain, runs `make check`, builds installers and runs
   GoReleaser with `--skip=publish`.
3. Assembles `dist/assets/`, calculates `checksums.txt` after signing, and
   uploads the `garminctl-packages` artifact with seven-day retention.
4. Installs and tests the packages on Windows, Ubuntu 24.04 and Fedora 44.
   Tests launch the native window and call HTTP MCP against repository fixtures,
   then uninstall. Signed Windows builds also check shipped signatures.
5. After those jobs pass, verifies the checksums and publishes those assets
   with `gh release create`. Only this job has `contents: write`.

A manual Release run is accepted from `main` only; it uses the package
pipeline in snapshot mode and skips publication. Fixture checks cover installed-app behavior; physical USB
compatibility requires a separate hardware check.

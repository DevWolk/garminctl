# Windows signing and releases

Status checked on 2026-10-24. The desktop release workflow described here has
been prepared locally; it takes effect on GitHub when the corresponding commit
is pushed. No desktop release has been published yet.

## Personal Windows builds

Windows Setup and the desktop executable can be built and distributed without
code signing. Builds default to unsigned unless signing is explicitly
configured. A paid signing subscription is not a prerequisite for producing a
personal release. The [recorded self-signing tests](windows-selfsign-test.md)
installed and launched garminctl successfully on this machine with SAC enabled.

The blocker observed on this machine is specifically **Smart App Control**.
The logged policy ID, `{0283ac0f-fff1-49ae-ada1-8a933130cad6}`, identifies
Windows's built-in `VerifiedAndReputableDesktop` policy. A read-only check
confirmed `VerifiedAndReputablePolicyState = 1` and that policy's presence.
It is not evidence that this is a company-managed computer. See Microsoft's
[built-in policy list](https://learn.microsoft.com/en-us/windows/security/application-security/application-control/app-control-for-business/operations/inbox-appcontrol-policies).

Smart App Control has two acceptance paths: code recognized as safe by
Microsoft's app intelligence, and publicly trusted signing when reputation
does not establish trust. Therefore, public signing is not a universal
requirement for an application to run with SAC enabled. See Microsoft's
[SAC overview](https://learn.microsoft.com/en-us/windows/apps/develop/smart-app-control/overview).

| Route | Requirements | Effect on this machine |
| --- | --- | --- |
| Unsigned personal build | No certificate or signing account. | Known-safe code can run with SAC enabled. The current garmin-desktop build received a block; that does not prove a paid certificate is necessary for every personal build. |
| Self-signed certificate | A private signing certificate; no publicly trusted signing identity is established. | Does not itself satisfy the public-signature path. Such a binary may still be accepted through reputation; self-signing is not a guaranteed fix for garmin-desktop. |
| Publicly trusted signing | A supported code signing certificate or signing service, identity validation, and access to its signing mechanism. | The supported signing route for an unknown app with Smart App Control enabled; verify the final signed build actually launches. |

Smart App Control's signing requirements are documented in
[Microsoft's developer guide](https://learn.microsoft.com/en-us/windows/apps/develop/smart-app-control/code-signing-for-smart-app-control).

### Comparison with UsbTreeView

The user-provided UsbTreeView 4.8.0 executable was inspected and launched on
2026-10-24. Its window opened while `VerifiedAndReputablePolicyState = 1`.
Its Authenticode signer and issuer are both Uwe Sieber; Windows reports
`UnknownError`, with chain status `UntrustedRoot`. It is self-signed, not an
example of a publicly trusted code signing certificate. The Sectigo timestamp
on the signature does not change that signer-chain result.

The user also reports that this executable runs without an internet connection.
Windows's ISG mechanism caches file trust information locally; offline launch
alone therefore does not identify which trust path allowed a file. See
[Microsoft's ISG description](https://learn.microsoft.com/en-us/windows/security/application-security/application-control/app-control-for-business/design/use-appcontrol-with-intelligent-security-graph).
The exact SAC acceptance reason for this UsbTreeView file was not established.

A read-only EA comparison found `$KERNEL.PURGE.ESBCACHE` on UsbTreeView and no
extended attributes on the tested garmin-desktop file. The contents of that
cache were not decoded, so this is an observed difference, not proof of an
allow decision. The garmin-desktop file in the temporary test directory has
the same SHA-256 as the checkout's current Windows desktop build, and the
Code Integrity log records its 3077/3033 block. A normal installed-app run
was later verified successfully after self-signing Setup. No Windows protection
or network settings were changed during these comparisons.

A subsequent [normal-install attempt](windows-install-check.md) confirmed that
Windows also rejects the current Setup before installation. Its event 3077
flat hash matches the actual installer; the older successful installer test
used a different artifact. The subsequent [self-signing experiment](windows-selfsign-test.md)
resolved execution for the tested build, and its installed native app's MCP
successfully read the connected watch.

Turning off SAC is an owner-controlled alternative, not a mandatory step for
unsigned builds. Its UI is Windows Security → App & browser control → Smart
App Control settings → Off. This changes protection for the
computer, not just garminctl; there is no per-app exception. Microsoft says
recent Windows updates support re-enabling it without reinstalling Windows;
check availability on the installed Windows version. These settings have not
been changed by garminctl or its installer. See the
[Smart App Control FAQ](https://support.microsoft.com/en-us/windows/security/threat-malware-protection/smart-app-control-frequently-asked-questions).

SmartScreen download warnings are a separate mechanism. Publishing a GitHub
release, signing a Git tag, or supplying SHA-256 checksums does not add a Windows
executable signature.

## Free signing and the Let's Encrypt comparison

The installer can be Authenticode-signed after building. Signing an unsigned
file adds a signature; it does not borrow another application's identity.
The signed build route signs application/CLI binaries before packaging, the
NSIS-generated uninstaller, and the resulting Setup. Portable Windows CLI
archives are signed before archiving and checksum calculation.

Let's Encrypt certificates cannot supply the Windows code signing identity:
they are TLS certificates, not code signing certificates. Let's Encrypt states
this explicitly in its [FAQ](https://letsencrypt.org/docs/faq/#does-lets-encrypt-issue-certificates-for-anything-other-than-ssltls-for-websites).

A self-signed code signing certificate is free to create and can sign our own
files. That creates an integrity signature but not public trust or the trust
history of another application. Installing a private certificate in a local
certificate store must not be presented as a guaranteed SAC exemption.

[SignPath Foundation](https://signpath.org/) offers free publicly trusted code
signing for accepted open-source projects. Its [conditions](https://signpath.org/terms.html)
require a maintained, released, documented OSS project, a verifiable build,
defined signing roles, MFA, and manual approval of releases for signing.
The certificate is issued to the Foundation; it does not hand out an unrestricted
private key for arbitrary files. Admission is reviewed and not guaranteed.

GitHub repository metadata was checked on 2026-10-24: `DevWolk/garminctl` is
private. Free OSS signing is therefore an option to assess against the project's
distribution plans and the Foundation's admission requirements, not an account
that is already available. No repository visibility was changed or application
submitted. The current release workflow has no SignPath integration.

## If public signing is chosen

Microsoft Artifact Signing is one option. Its published Basic price is
US$9.99 per month for 5,000 signatures. Public Trust currently supports
individual developers in the US/Canada and organizations in supported regions,
including the EU. Do not assume an individual account outside those countries
is eligible. See [pricing](https://learn.microsoft.com/en-us/azure/artifact-signing/how-to-change-sku)
and [eligibility/setup](https://learn.microsoft.com/en-us/azure/artifact-signing/quickstart).
Another option is a publicly trusted code signing certificate from a provider;
the certificate must support the chosen local/CI signing mechanism.

For Artifact Signing, the setup is: Azure subscription and tenant, a signing
account, identity validation, a Public Trust certificate profile, and CI access
to that profile. Signing keys belong in the provider's protected signing
mechanism, not in the repository.

The release implementation must then sign all shipped Windows binaries before
embedding them in Setup, sign the resulting installer and generated uninstaller,
verify signatures, and calculate checksums after signing. Signing the Setup
alone leaves its installed executable unsigned. CLI binaries assembled by
GoReleaser also need the signing step before archive/checksum creation.
These signing steps are implemented by `scripts/sign-windows.sh`, the NSIS
uninstaller hook, and the GoReleaser post-build hook. The build fails if signing
or signature verification fails. Signing remains optional for unsigned builds.

## Configure personal signing

Use a persistent code signing certificate rather than generating a new identity
for each CI run. A Linux-generated unrelated self-signed identity was blocked
on the test machine; the persistent Windows-generated personal identity,
exported as an encrypted PFX and used by the Linux builder, installed
successfully. See the [exact results and limitations](windows-selfsign-test.md).
Self-signing is not a promise of public trust or acceptance on another machine.

For local builds, put `signing.pfx` and its password in a file named `password`
inside a private, Git-ignored directory. Protect the directory with mode 700
and both files with mode 600. For this checkout, `.make/options.local.mk` can
select the existing personal identity without putting secrets in Make arguments:

```makefile
WINDOWS_SIGNING_MODE := certificate
WINDOWS_SIGNING_DIR := /src/.cache/windows-release-signing
```

Then run `make package-windows VERSION=v0.5.0-test`. The `/src` path is the
builder container's checkout path. Without these settings, `make package`
builds unsigned binaries. `WINDOWS_SIGNING_MODE=self-signed` explicitly creates
or reuses a local identity under `.cache/windows-signing`; this is useful for
signature experiments, not a verified acceptance route on the user's Windows.

For GitHub Releases, configure two repository **Actions secrets**:

| Secret | Value |
| --- | --- |
| `WINDOWS_SIGNING_PFX_BASE64` | Base64 encoding of the encrypted PFX. |
| `WINDOWS_SIGNING_PASSWORD` | Its password. |

Set the repository **Actions variable** `WINDOWS_SIGNING_MODE` to `certificate`.
The Release workflow uses the secrets only in the package-building job. Missing
credentials fail the signed build; it does not silently fall back to unsigned.
PR package checks always use unsigned mode and do not receive signing secrets.
Only the public certificate is included in signed release assets; the build
removes its temporary private signing files even after failure.

With GitHub CLI, send credentials through stdin rather than command arguments:

```sh
base64 -w 0 .cache/windows-release-signing/signing.pfx | gh secret set WINDOWS_SIGNING_PFX_BASE64 --repo DevWolk/garminctl
gh secret set WINDOWS_SIGNING_PASSWORD --repo DevWolk/garminctl < .cache/windows-release-signing/password
gh variable set WINDOWS_SIGNING_MODE --repo DevWolk/garminctl --body certificate
```

The current personal signatures have no timestamp. The personal certificate
expires on 2031-10-24; renew it and verify later builds before then. An optional
`WINDOWS_TIMESTAMP_URL` can configure a supported RFC 3161 timestamp service;
timestamping has not been validated with the current builder. No timestamp
service is necessary for the successful personal-build test above.

## Current CI and release flow

| Event | Current behavior |
| --- | --- |
| Pull request or push to `main` | Linux/Windows tests, quality gate, installers and portable archives, then Windows/Ubuntu/Fedora installed-app checks. Downloadable `garminctl-packages` artifact; no release publication. |
| Push to another branch without a PR | These branch/PR workflows do not run. |
| Weekly security schedule | Vulnerability checks; CodeQL is conditional on repository visibility. |
| Push of a tag matching `v*` | The Release workflow runs the sequence below. |
| Manual Release workflow run | The same package pipeline as a snapshot, with configured signing and downloadable artifacts; no tag/release is published. |

The tag workflow in [release.yml](../.github/workflows/release.yml) performs:

1. Check out the tagged commit and set up Go.
2. Build the Docker toolchain with `make image-build`.
3. Run `make check` (lint, race tests, reachable-vulnerability analysis).
4. Run `make package VERSION=<tag>` to build Windows Setup and Linux DEB/RPM,
   signing all shipped Windows executables when configured.
5. Run GoReleaser with `--skip=publish` to build portable archives, signing
   their Windows CLIs before packaging.
6. Assemble the final public assets and `checksums.txt`, then upload the
   `garminctl-packages` Actions artifact (retained for seven days).
7. Install, launch a real native window, call HTTP MCP against test fixtures,
   and uninstall on Windows and in clean Ubuntu 24.04/Fedora 44 userspaces
   under Xvfb. Windows also checks a normal window/server shutdown and, for
   signed releases, the signer of Setup, installed binaries and portable CLIs.
8. After every package job succeeds, verify checksums and publish those exact
   assets as a GitHub Release. No rebuild occurs between testing and publishing.

Only the publication job has `contents: write`; it uses GitHub's provided
token. Unsigned releases require no signing credentials. Signed releases use
the configured PFX secrets. Neither route deploys the app onto this computer.
CI fixture tests do not verify physical USB compatibility or SAC acceptance
on a user's Windows installation; those need separate hardware/host checks.

The tag filter does not require the tagged commit to be on `main` or wait for
all separate branch CI workflows to finish. Tag a reviewed commit whose branch
checks have passed; the Release job then repeats its own `make check`.
The updated package pipeline must be exercised on GitHub before a final tag
is published; [validation status](desktop-validation.md) tracks that evidence.

For example, after committing, pushing, and checking the chosen release commit:

```sh
git tag -a v0.5.0 -m "Release v0.5.0"
git push origin v0.5.0
```

`v0.5.0` is an example version, not a version already selected or published.
Creating a local tag alone does not start Actions. Ordinary branch pushes do
not publish a release. The application currently has no desktop self-updater;
install a newer package to update it.

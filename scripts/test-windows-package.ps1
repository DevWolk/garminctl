param(
  [Parameter(Mandatory=$true)][string]$Setup,
  [Parameter(Mandatory=$true)][string]$Version,
  [string]$PreviousSetup,
  [string]$PreviousVersion,
  [switch]$RequireSignature
)
$ErrorActionPreference = 'Stop'
$paired = [bool]$PreviousSetup
if ($paired -ne [bool]$PreviousVersion) { throw 'PreviousSetup and PreviousVersion must be supplied together.' }
if ($paired -and ($env:GITHUB_ACTIONS -ne 'true' -or $PreviousVersion -eq $Version)) { throw 'Two distinct package versions require an isolated CI runner.' }
$catalog = Join-Path $PSScriptRoot '../dist/native-package-catalog/setup'
if ($paired -and !(Test-Path -LiteralPath $catalog)) { throw 'Paired acceptance requires the retained native catalog artifact.' }
$installed = Join-Path $env:LOCALAPPDATA 'Programs\garminctl'
if ($paired) {
  $installed = Join-Path $env:LOCALAPPDATA 'Programs\garminctl acceptance'
  if (Test-Path 'HKCU:\Software\garminctl') { throw 'Use a clean Windows test system without an existing garminctl registration.' }
}
if (Test-Path $installed) { throw 'Use a clean Windows test system; do not replace an existing user installation.' }
$setupFile = (Resolve-Path -LiteralPath $Setup).Path
$setupSignature = Get-AuthenticodeSignature -LiteralPath $setupFile
if ($RequireSignature -and (!$setupSignature.SignerCertificate -or $setupSignature.Status -eq 'HashMismatch')) { throw 'Setup signature is missing or damaged.' }
function Invoke-Setup([string]$Path, [string]$Arguments = '/S') {
  $installer = Start-Process -FilePath $Path -ArgumentList $Arguments -PassThru
  if (!$installer.WaitForExit(120000)) { throw 'Setup outcome is unknown; do not retry automatically.' }
  if ($installer.ExitCode -ne 0) { throw "Setup exited $($installer.ExitCode)." }
}
if ($paired) {
  $previousFile = (Resolve-Path -LiteralPath $PreviousSetup).Path
  $previousSignature = Get-AuthenticodeSignature -LiteralPath $previousFile
  if ($RequireSignature -and (!$previousSignature.SignerCertificate -or $previousSignature.Status -eq 'HashMismatch' -or $previousSignature.SignerCertificate.Thumbprint -ne $setupSignature.SignerCertificate.Thumbprint)) { throw 'Previous Setup must have the same intact signing identity.' }
  # NSIS requires /D last, with the complete directory following it unquoted.
  Invoke-Setup $previousFile "/S /D=$installed"
} else {
  Invoke-Setup $setupFile
}
$thumbprint = $setupSignature.SignerCertificate.Thumbprint
if ($RequireSignature) {
  $portable = Join-Path $env:TEMP ('garminctl-portable-' + [guid]::NewGuid())
  foreach ($archive in Get-ChildItem (Join-Path (Split-Path $setupFile) '*_windows_*.zip')) {
    $destination = Join-Path $portable $archive.BaseName
    Expand-Archive -LiteralPath $archive.FullName -DestinationPath $destination
    $binary = Get-ChildItem $destination -Filter garminctl.exe -Recurse | Select-Object -First 1
    if (!$binary) { throw "Missing CLI in $($archive.Name)" }
    $signature = Get-AuthenticodeSignature -LiteralPath $binary.FullName
    if ($signature.SignerCertificate.Thumbprint -ne $thumbprint -or $signature.Status -eq 'HashMismatch') { throw "Incorrect portable CLI signature: $($archive.Name)" }
  }
}
$state = Join-Path $env:TEMP ('garminctl-package-state-' + [guid]::NewGuid())
$sentinel = Join-Path $state 'appdata\garminctl\package-acceptance.txt'
New-Item -ItemType Directory -Path (Split-Path $sentinel) -Force | Out-Null
[IO.File]::WriteAllText($sentinel, 'preserve synthetic package state')
$evidence = $env:NATIVE_RESULTS_DIR
function Invoke-PackageCheck([string]$Expected, [string]$Phase, [bool]$NativeUI) {
  if ((Get-ItemPropertyValue 'HKCU:\Software\garminctl' 'InstallDir') -ne $installed) { throw 'Setup changed the selected InstallDir.' }
  if ([IO.File]::ReadAllText($sentinel) -ne 'preserve synthetic package state') { throw 'Package transaction changed synthetic application state.' }
  if ($evidence) {
    $env:NATIVE_RESULTS_DIR = $evidence
    if ($paired) { $env:NATIVE_RESULTS_DIR = Join-Path $evidence $Phase }
  }
  foreach ($name in @('garmin-desktop.exe','garminctl.exe','uninstall.exe')) {
    $file = Join-Path $installed $name
    if (!(Test-Path -LiteralPath $file)) { throw "Missing installed file: $name" }
    $signature = Get-AuthenticodeSignature -LiteralPath $file
    if ($RequireSignature -and ($signature.SignerCertificate.Thumbprint -ne $thumbprint -or $signature.Status -eq 'HashMismatch')) {
      throw "Incorrect signature: $name"
    }
  }
  $arguments = @('scripts/test-package.py', '--repo', '.', '--version', $Expected, '--state', $state, '--desktop', (Join-Path $installed 'garmin-desktop.exe'), '--cli', (Join-Path $installed 'garminctl.exe'))
  if ($NativeUI) { $arguments += '--native-ui' }
  python @arguments
  if ($LASTEXITCODE -ne 0) { throw 'Installed native application/MCP check failed.' }
  if ([IO.File]::ReadAllText($sentinel) -ne 'preserve synthetic package state') { throw 'Native app changed the preservation sentinel.' }
}
function Invoke-ApplicationTransition([string]$Current, [string]$Target, [string]$Phase, [bool]$Rollback, [switch]$Seed, [switch]$Restore, [switch]$RepairExport) {
  $work = Join-Path $state "application-$Phase"
  if ($evidence) { $work = Join-Path $evidence "application-$Phase" }
  $arguments = @((Join-Path $PSScriptRoot 'native_application.py'), '--repo', '.', '--state', $state,
    '--desktop', (Join-Path $installed 'garmin-desktop.exe'), '--cli', (Join-Path $installed 'garminctl.exe'),
    '--work', $work, '--current', $Current, '--target', $Target)
  if ($Rollback) {
    $arguments += '--rollback'
  } elseif ($Seed) {
    $arguments += @('--catalog', $catalog, '--assets', (Split-Path $setupFile), '--assets', (Split-Path $previousFile))
  }
  if ($Restore) { $arguments += '--restore' }
  if ($RepairExport) { $arguments += '--repair-export' }
  python @arguments
  if ($LASTEXITCODE -ne 0) { throw 'Application native transition failed; do not replay the installer.' }
}
$sequencePassed = $false
try {
  $parseErrors = $null
  [void][System.Management.Automation.Language.Parser]::ParseFile((Join-Path $PSScriptRoot 'native-ui-windows.ps1'), [ref]$null, [ref]$parseErrors)
  if ($parseErrors.Count) { throw ($parseErrors | Out-String) }
  if ($paired) {
    Invoke-PackageCheck $PreviousVersion 'previous' $false
    Invoke-ApplicationTransition $PreviousVersion $Version 'upgrade' $false -Seed
    Invoke-PackageCheck $Version 'upgrade' $true
    Invoke-ApplicationTransition $Version $PreviousVersion 'rollback' $true
    Invoke-PackageCheck $PreviousVersion 'rollback' $false
    Invoke-ApplicationTransition $PreviousVersion $Version 'restore-upgrade' $false
    Invoke-PackageCheck $Version 'restore-upgrade' $false
    $newerCLI = Join-Path $state 'newer-cli.exe'
    Copy-Item -LiteralPath (Join-Path $installed 'garminctl.exe') -Destination $newerCLI
    Invoke-ApplicationTransition $Version $PreviousVersion 'restore-rollback' $true -Restore
    Invoke-PackageCheck $PreviousVersion 'restore-rollback' $false
    # Disposable fault fixture only; production never replaces managed files this way.
    $ledger = Join-Path $state 'localappdata/garminctl/uploaded-4242.json'
    $ledgerHash = (Get-FileHash -LiteralPath $ledger -Algorithm SHA256).Hash
    Move-Item -LiteralPath (Join-Path $installed 'garmin-desktop.exe') -Destination (Join-Path $state 'unavailable-desktop.exe')
    Copy-Item -LiteralPath $newerCLI -Destination (Join-Path $installed 'garminctl.exe') -Force
    Invoke-ApplicationTransition $Version $PreviousVersion 'manual-repair' $false -RepairExport
    $repairWork = Join-Path $state 'application-manual-repair'
    if ($evidence) { $repairWork = Join-Path $evidence 'application-manual-repair' }
    $repairExport = Get-Content -LiteralPath (Join-Path $repairWork 'recovery-export.json') -Raw | ConvertFrom-Json
    $repairDirectory = Join-Path $state 'manual-repair-installer'
    New-Item -ItemType Directory -Path $repairDirectory | Out-Null
    $repairSetup = Join-Path $repairDirectory 'repair-setup.exe'
    Copy-Item -LiteralPath $repairExport.path -Destination $repairSetup
    if ((Get-FileHash -LiteralPath $repairSetup -Algorithm SHA256).Hash -ne (Get-FileHash -LiteralPath $previousFile -Algorithm SHA256).Hash) { throw 'Exported repair package differs from the signed reference.' }
    Invoke-Setup $repairSetup "/S /D=$installed"
    if ((Get-FileHash -LiteralPath $ledger -Algorithm SHA256).Hash -ne $ledgerHash) { throw 'Manual repair changed current delivery data.' }
    Invoke-PackageCheck $PreviousVersion 'manual-repair' $false
  } else {
    Invoke-PackageCheck $Version 'current' $true
  }
  $sequencePassed = $true
} finally {
  if ($paired -and !$sequencePassed) {
    Write-Warning 'Package sequence failed; leaving the disposable installation for diagnosis without replay or automatic uninstall.'
  } else {
    $uninstaller = Join-Path $installed 'uninstall.exe'
    $process = Start-Process -FilePath $uninstaller -ArgumentList '/S' -PassThru
    if (!$process.WaitForExit(30000)) { throw 'Uninstaller timed out.' }
    # NSIS runs a temporary copy; the original process can exit before removal.
    $deadline = (Get-Date).AddSeconds(30)
    while ((Test-Path (Join-Path $installed 'garmin-desktop.exe')) -and (Get-Date) -lt $deadline) { Start-Sleep -Milliseconds 200 }
    if (Test-Path (Join-Path $installed 'garmin-desktop.exe')) { throw 'Uninstall left the application installed.' }
  }
}

param(
  [Parameter(Mandatory=$true)][string]$Setup,
  [Parameter(Mandatory=$true)][string]$Version,
  [switch]$RequireSignature
)
$ErrorActionPreference = 'Stop'
$installed = Join-Path $env:LOCALAPPDATA 'Programs\garminctl'
if (Test-Path $installed) { throw 'Use a clean Windows test system; do not replace an existing user installation.' }
$setupFile = (Resolve-Path -LiteralPath $Setup).Path
$setupSignature = Get-AuthenticodeSignature -LiteralPath $setupFile
if ($RequireSignature -and (!$setupSignature.SignerCertificate -or $setupSignature.Status -eq 'HashMismatch')) { throw 'Setup signature is missing or damaged.' }
$process = Start-Process -FilePath $setupFile -ArgumentList '/S' -PassThru
if (!$process.WaitForExit(120000)) { throw 'Setup timed out.' }
if ($process.ExitCode -ne 0) { throw "Setup exited $($process.ExitCode)." }
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
foreach ($name in @('garmin-desktop.exe','garminctl.exe','uninstall.exe')) {
  $file = Join-Path $installed $name
  if (!(Test-Path -LiteralPath $file)) { throw "Missing installed file: $name" }
  $signature = Get-AuthenticodeSignature -LiteralPath $file
  if ($RequireSignature -and ($signature.SignerCertificate.Thumbprint -ne $thumbprint -or $signature.Status -eq 'HashMismatch')) {
    throw "Incorrect signature: $name"
  }
}
try {
  $parseErrors = $null
  [void][System.Management.Automation.Language.Parser]::ParseFile((Join-Path $PSScriptRoot 'native-ui-windows.ps1'), [ref]$null, [ref]$parseErrors)
  if ($parseErrors.Count) { throw ($parseErrors | Out-String) }
  python scripts/test-package.py --repo . --version $Version --desktop (Join-Path $installed 'garmin-desktop.exe') --cli (Join-Path $installed 'garminctl.exe') --native-ui
  if ($LASTEXITCODE -ne 0) { throw 'Installed native application/MCP check failed.' }
} finally {
  $uninstaller = Join-Path $installed 'uninstall.exe'
  $process = Start-Process -FilePath $uninstaller -ArgumentList '/S' -PassThru
  if (!$process.WaitForExit(30000)) { throw 'Uninstaller timed out.' }
  # NSIS runs a temporary copy; the original process can exit before removal.
  $deadline = (Get-Date).AddSeconds(30)
  while ((Test-Path (Join-Path $installed 'garmin-desktop.exe')) -and (Get-Date) -lt $deadline) { Start-Sleep -Milliseconds 200 }
  if (Test-Path (Join-Path $installed 'garmin-desktop.exe')) { throw 'Uninstall left the application installed.' }
}

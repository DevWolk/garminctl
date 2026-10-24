param(
  [Parameter(Mandatory=$true)][string]$Root,
  [string]$CertificateThumbprint = ''
)
$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = New-Object Text.UTF8Encoding($false)
$certificate = $null
if ($CertificateThumbprint) { $certificate = Get-Item ("Cert:\CurrentUser\My\" + $CertificateThumbprint) }
$cases = @(Import-Csv -LiteralPath (Join-Path $Root 'bin\windows-tests\manifest.tsv') -Delimiter "`t" -Header Package,Binary)
if (!$cases.Count) { throw 'No compiled Windows tests. Run make test-windows.' }
$failed = @()
foreach ($case in $cases) {
  $binary = Join-Path $Root $case.Binary
  if ($certificate) {
    $signature = Set-AuthenticodeSignature -LiteralPath $binary -Certificate $certificate -HashAlgorithm SHA256 -IncludeChain Signer
    if ($signature.SignerCertificate.Thumbprint -ne $CertificateThumbprint -or $signature.Status -eq 'HashMismatch') { throw "Signing failed: $($case.Package)" }
  }
  $start = New-Object Diagnostics.ProcessStartInfo
  $start.FileName = $binary
  $start.WorkingDirectory = Join-Path $Root $case.Package
  $start.Arguments = '-test.v -test.shuffle=on -test.timeout=2m'
  $start.UseShellExecute = $false
  $start.CreateNoWindow = $true
  $start.RedirectStandardOutput = $true
  $start.RedirectStandardError = $true
  $process = New-Object Diagnostics.Process
  $process.StartInfo = $start
  Write-Output ("Testing on Windows: " + $case.Package)
  try {
    if (!$process.Start()) { throw "Cannot start $($case.Package)" }
  } catch {
    Write-Output ("Cannot execute " + $case.Package + ': ' + $_.Exception.Message)
    $failed += $case.Package
    $process.Dispose()
    continue
  }
  $stdout = $process.StandardOutput.ReadToEndAsync()
  $stderr = $process.StandardError.ReadToEndAsync()
  if (!$process.WaitForExit(150000)) {
    $process.Kill()
    throw "Windows test timed out: $($case.Package)"
  }
  Write-Output $stdout.GetAwaiter().GetResult()
  Write-Output $stderr.GetAwaiter().GetResult()
  if ($process.ExitCode -ne 0) { $failed += $case.Package }
  $process.Dispose()
}
if ($failed.Count) { throw ("Windows tests failed: " + ($failed -join ', ')) }
Write-Output ("All " + $cases.Count + " Windows test packages passed.")

# Installs relevo-host on Windows from the latest GitHub release.
#   irm https://raw.githubusercontent.com/azratul/relevo/main/install.ps1 | iex
# Options: $env:RELEVO_VERSION = "v0.1.0" for a specific release.
$ErrorActionPreference = 'Stop'
$repo = if ($env:RELEVO_REPO) { $env:RELEVO_REPO } else { 'azratul/relevo' }
$version = if ($env:RELEVO_VERSION) { $env:RELEVO_VERSION } else { 'latest' }
$arch = if ($env:PROCESSOR_ARCHITECTURE -eq 'ARM64') { 'arm64' } else { 'amd64' }
$base = if ($version -eq 'latest') { "https://github.com/$repo/releases/latest/download" } else { "https://github.com/$repo/releases/download/$version" }
$asset = "relevo-host_windows_$arch.zip"
$dir = Join-Path $env:LOCALAPPDATA 'relevo'
$tmp = Join-Path ([IO.Path]::GetTempPath()) ([Guid]::NewGuid())
New-Item -ItemType Directory -Force -Path $tmp, $dir | Out-Null
try {
  Write-Host "Downloading $asset ($version)..."
  Invoke-WebRequest "$base/$asset" -OutFile (Join-Path $tmp $asset) -UseBasicParsing
  Invoke-WebRequest "$base/checksums.txt" -OutFile (Join-Path $tmp 'checksums.txt') -UseBasicParsing
  $line = Get-Content (Join-Path $tmp 'checksums.txt') | Where-Object { $_ -match " $([regex]::Escape($asset))$" }
  if (-not $line) { throw "$asset is not listed in checksums.txt" }
  $want = ($line -split ' ')[0]
  $got = (Get-FileHash (Join-Path $tmp $asset) -Algorithm SHA256).Hash.ToLower()
  if ($got -ne $want) { throw "checksum mismatch for $asset" }
  Expand-Archive -Force (Join-Path $tmp $asset) -DestinationPath $dir
} finally { Remove-Item -Recurse -Force $tmp }
$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if (($userPath -split ';') -notcontains $dir) {
  [Environment]::SetEnvironmentVariable('Path', "$userPath;$dir", 'User')
  Write-Host "Added $dir to your PATH (open a new terminal)."
}
Write-Host "Installed $(& (Join-Path $dir 'relevo-host.exe') version) to $dir"

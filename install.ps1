# queqiao installer for Windows:
#   irm https://raw.githubusercontent.com/weiping/queqiao/queqiao/install.ps1 | iex
#
# Downloads the terminal build from the latest GitHub Release, checks its
# SHA-256 against the release's checksums.txt, and puts queqiao.exe in
# ~\.local\bin ($env:USERPROFILE\.local\bin) by default.
#
# Options:
#   irm ... | iex  with variables set before, or run the file directly:
#   .\install.ps1 -Version qq-v0.1.0     install this release, not the latest
#   .\install.ps1 -BinDir C:\bin         install somewhere else
param(
  [string] $Version = "",
  [string] $BinDir = ""
)

$ErrorActionPreference = "Stop"
$repo = "weiping/queqiao"

# TLS 1.2 for the Windows PowerShell that irm runs in
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

switch ($env:PROCESSOR_ARCHITECTURE) {
  "AMD64" { $arch = "amd64" }
  "ARM64" { $arch = "arm64" }
  default { Write-Error "queqiao: no prebuilt binary for $env:PROCESSOR_ARCHITECTURE; build from source: make cli"; exit 1 }
}

if (-not $Version) {
  $rel = Invoke-RestMethod "https://api.github.com/repos/$repo/releases/latest"
  $Version = $rel.tag_name
  if (-not $Version) { Write-Error "queqiao: could not learn the latest release (rate limited? use -Version qq-v<x.y.z>)"; exit 1 }
}

$asset = "queqiao-cli-windows-$arch.exe"
$base = "https://github.com/$repo/releases/download/$Version"
Write-Host "  queqiao $Version (windows/$arch)"

if (-not $BinDir) { $BinDir = Join-Path $env:USERPROFILE ".local\bin" }
$tmp = New-Item -ItemType Directory -Force -Path (Join-Path $env:TEMP ([IO.Path]::GetRandomFileName()))

try {
  Invoke-WebRequest "$base/$asset" -OutFile "$tmp\$asset"
  Invoke-WebRequest "$base/checksums.txt" -OutFile "$tmp\checksums.txt"

  # trust the download only when its SHA-256 is the release's
  $line = Select-String -Path "$tmp\checksums.txt" -Pattern "^([0-9a-f]{64})  $([regex]::Escape($asset))`$"
  if (-not $line) { Write-Error "queqiao: checksums.txt in $Version has no line for $asset"; exit 1 }
  $want = $line.Matches[0].Groups[1].Value
  $got = (Get-FileHash "$tmp\$asset" -Algorithm SHA256).Hash.ToLower()
  if ($want -ne $got) { Write-Error "queqiao: SHA-256 mismatch for $asset (want $want, got $got): the download is refused"; exit 1 }

  New-Item -ItemType Directory -Force -Path $BinDir | Out-Null
  Copy-Item "$tmp\$asset" (Join-Path $BinDir "queqiao.exe") -Force
  Write-Host "  installed to $BinDir\queqiao.exe"

  if ($env:PATH -notlike "*$BinDir*") {
    Write-Host "  note: $BinDir is not on PATH; add it for this session:"
    Write-Host "        `$env:PATH = `"$BinDir;`$env:PATH`""
    Write-Host "        (or set it permanently: [Environment]::SetEnvironmentVariable('Path', `"$BinDir;`" + `$env:Path, 'User'))"
  }

  Write-Host ""
  Write-Host "  next:"
  Write-Host "    queqiao router init --preset cn    # routing groups + router.json (frontier/anthropic also exist)"
  Write-Host "    queqiao serve                      # the gateway on 127.0.0.1:3425"
  Write-Host "    queqiao router status              # config, mapping, recent decisions"
} finally {
  Remove-Item $tmp -Recurse -Force -ErrorAction SilentlyContinue
}

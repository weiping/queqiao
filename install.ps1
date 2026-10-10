# mbridge installer for Windows:
#   irm https://raw.githubusercontent.com/weiping/magpie-bridge/main/install.ps1 | iex
#
# mbridge runs beside official magpie (https://github.com/yetone/magpie):
# install magpie first. This downloads mbridge from the latest GitHub
# Release, checks its SHA-256 against the release's checksums.txt, puts
# mbridge.exe in ~\.local\bin and runs mbridge at logon
# (mbridge service install). mbridge listens on 127.0.0.1:3426.
#
#   .\install.ps1 -Version v0.2.0     install this release, not the latest
#   .\install.ps1 -BinDir C:\bin         install somewhere else
#   .\install.ps1 -NoService             don't run mbridge at logon
param(
  [string] $Version = $env:MBRIDGE_VERSION,
  [string] $BinDir = $env:MBRIDGE_BIN_DIR,
  [switch] $NoService
)

$ErrorActionPreference = "Stop"
$repo = "weiping/magpie-bridge"
function Fail($msg) { [Console]::Error.WriteLine("mbridge: $msg"); exit 1 }

if (-not (Get-Command magpie -ErrorAction SilentlyContinue)) {
  Fail "mbridge needs official magpie: install it first from https://github.com/yetone/magpie, then run this again"
}

[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

switch ($env:PROCESSOR_ARCHITECTURE) {
  "AMD64" { $arch = "amd64" }
  "ARM64" { $arch = "arm64" }
  default { Fail "no prebuilt binary for $env:PROCESSOR_ARCHITECTURE; build from source: make build" }
}

if (-not $Version) {
  $rel = Invoke-RestMethod "https://api.github.com/repos/$repo/releases/latest"
  $Version = $rel.tag_name
  if (-not $Version) { Fail "could not learn the latest release (rate limited? use -Version v<x.y.z>)" }
}

$asset = "mbridge-windows-$arch.exe"
$base = if ($env:MBRIDGE_DOWNLOAD_BASE) { $env:MBRIDGE_DOWNLOAD_BASE } else { "https://github.com/$repo/releases/download/$Version" }
Write-Host "  mbridge $Version (windows/$arch)"

if (-not $BinDir) { $BinDir = Join-Path $env:USERPROFILE ".local\bin" }
$tmp = New-Item -ItemType Directory -Force -Path (Join-Path $env:TEMP ([IO.Path]::GetRandomFileName()))

function Fetch($url, $out) {
  if ($url -like "file://*") { Copy-Item ([Uri]$url).LocalPath $out } else { Invoke-WebRequest $url -OutFile $out -UseBasicParsing }
}

try {
  try { Fetch "$base/$asset" "$tmp\$asset" } catch { Fail "no $asset in release $Version" }
  try { Fetch "$base/checksums.txt" "$tmp\checksums.txt" } catch { Fail "no checksums.txt in release $Version" }

  # trust the download only when its SHA-256 is the release's
  $line = Select-String -Path "$tmp\checksums.txt" -Pattern "^([0-9a-f]{64})  $([regex]::Escape($asset))`$"
  if (-not $line) { Fail "checksums.txt in $Version has no line for $asset" }
  $want = $line.Matches[0].Groups[1].Value
  $got = (Get-FileHash "$tmp\$asset" -Algorithm SHA256).Hash.ToLower()
  if ($want -ne $got) { Fail "SHA-256 mismatch for $asset (want $want, got $got): the download is refused" }

  New-Item -ItemType Directory -Force -Path $BinDir | Out-Null
  $exe = Join-Path $BinDir "mbridge.exe"
  Copy-Item "$tmp\$asset" $exe -Force
  Write-Host "  installed to $exe"

  if ($env:PATH -notlike "*$BinDir*") {
    Write-Host "  note: $BinDir is not on PATH; add it:"
    Write-Host "        [Environment]::SetEnvironmentVariable('Path', `"$BinDir;`" + `$env:Path, 'User')"
  }

  if (-not $NoService) {
    & $exe service install
    if ($LASTEXITCODE -ne 0) { Write-Host "  mbridge service install failed; run mbridge yourself: mbridge serve" }
  }

  Write-Host ""
  Write-Host "  next:"
  Write-Host "    mbridge router init --preset cn    # tier groups in magpie + router.json"
  Write-Host "    mbridge status                     # magpie, mbridge, groups, recent decisions"
  Write-Host "    codex -p mbridge                   # Codex through mbridge"
} finally {
  Remove-Item $tmp -Recurse -Force -ErrorAction SilentlyContinue
}

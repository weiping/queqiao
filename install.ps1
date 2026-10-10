# queqiao installer for Windows:
#   irm https://raw.githubusercontent.com/weiping/queqiao/queqiao/install.ps1 | iex
#
# queqiao runs beside official magpie (https://github.com/yetone/magpie):
# install magpie first. This downloads queqiao from the latest GitHub
# Release, checks its SHA-256 against the release's checksums.txt, puts
# queqiao.exe in ~\.local\bin and runs queqiaod at logon
# (queqiao service install). queqiaod listens on 127.0.0.1:3426.
#
#   .\install.ps1 -Version qq-v0.2.0     install this release, not the latest
#   .\install.ps1 -BinDir C:\bin         install somewhere else
#   .\install.ps1 -NoService             don't run queqiaod at logon
param(
  [string] $Version = $env:QUEQIAO_VERSION,
  [string] $BinDir = $env:QUEQIAO_BIN_DIR,
  [switch] $NoService
)

$ErrorActionPreference = "Stop"
$repo = "weiping/queqiao"
function Fail($msg) { [Console]::Error.WriteLine("queqiao: $msg"); exit 1 }

if (-not (Get-Command magpie -ErrorAction SilentlyContinue)) {
  Fail "queqiao needs official magpie: install it first from https://github.com/yetone/magpie, then run this again"
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
  if (-not $Version) { Fail "could not learn the latest release (rate limited? use -Version qq-v<x.y.z>)" }
}

$asset = "queqiao-windows-$arch.exe"
$base = if ($env:QUEQIAO_DOWNLOAD_BASE) { $env:QUEQIAO_DOWNLOAD_BASE } else { "https://github.com/$repo/releases/download/$Version" }
Write-Host "  queqiao $Version (windows/$arch)"

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
  $exe = Join-Path $BinDir "queqiao.exe"
  Copy-Item "$tmp\$asset" $exe -Force
  Write-Host "  installed to $exe"

  if ($env:PATH -notlike "*$BinDir*") {
    Write-Host "  note: $BinDir is not on PATH; add it:"
    Write-Host "        [Environment]::SetEnvironmentVariable('Path', `"$BinDir;`" + `$env:Path, 'User')"
  }

  if (-not $NoService) {
    & $exe service install
    if ($LASTEXITCODE -ne 0) { Write-Host "  queqiao service install failed; run queqiaod yourself: queqiao serve" }
  }

  $cfg = if ($env:XDG_CONFIG_HOME) { $env:XDG_CONFIG_HOME } else { Join-Path $env:USERPROFILE ".config" }
  if (Test-Path (Join-Path $cfg "queqiao\providers.json")) {
    Write-Host ""
    Write-Host "  qq-v0.1.x data found in $cfg\queqiao: hand it to official magpie with"
    Write-Host "    queqiao migrate --dry-run     # see what moves"
    Write-Host "    queqiao migrate               # do it (queqiao migrate restore undoes it)"
  }

  Write-Host ""
  Write-Host "  next:"
  Write-Host "    queqiao router init --preset cn    # tier groups in magpie + router.json"
  Write-Host "    queqiao status                     # magpie, queqiaod, groups, recent decisions"
  Write-Host "    codex -p queqiao                   # Codex through queqiaod"
} finally {
  Remove-Item $tmp -Recurse -Force -ErrorAction SilentlyContinue
}

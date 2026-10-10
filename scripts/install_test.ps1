# Tests install.ps1 against a fake release (file:// URLs) and fake commands.
#   pwsh scripts/install_test.ps1
$ErrorActionPreference = "Stop"
$root = Split-Path $PSScriptRoot -Parent
$script:fail = 0
function Ok($m) { Write-Host "ok   $m" }
function Bad($m) { Write-Host "FAIL $m"; $script:fail = 1 }

function Setup {
  $t = New-Item -ItemType Directory -Force -Path (Join-Path $env:TEMP ([IO.Path]::GetRandomFileName()))
  foreach ($d in "rel", "home", "fakebin") { New-Item -ItemType Directory -Force -Path (Join-Path $t $d) | Out-Null }
  $arch = if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") { "arm64" } else { "amd64" }
  $asset = "queqiao-windows-$arch.exe"
  # the "release binary": cmd.exe copied, so it runs and exits 0
  Copy-Item "$env:SystemRoot\System32\cmd.exe" (Join-Path $t "rel\$asset")
  $h = (Get-FileHash (Join-Path $t "rel\$asset") -Algorithm SHA256).Hash.ToLower()
  Set-Content -NoNewline -Path (Join-Path $t "rel\checksums.txt") -Value "$h  $asset`n"
  Set-Content -Path (Join-Path $t "fakebin\magpie.cmd") -Value "@echo magpie v0.1.1000"
  return @{ T = $t; Asset = $asset }
}

function Run($s, $path) {
  $env:USERPROFILE = Join-Path $s.T "home"
  $env:XDG_CONFIG_HOME = $null
  $env:QUEQIAO_VERSION = "qq-v0.2.0"
  $env:QUEQIAO_DOWNLOAD_BASE = "file:///" + ((Join-Path $s.T "rel") -replace '\\', '/')
  $env:QUEQIAO_BIN_DIR = Join-Path $s.T "home\.local\bin"
  $old = $env:PATH; $env:PATH = $path
  try { $out = & powershell -NoProfile -ExecutionPolicy Bypass -File "$root\install.ps1" -NoService 2>&1 | Out-String; $code = $LASTEXITCODE }
  finally { $env:PATH = $old }
  return @{ Out = $out; Code = $code }
}

$sys = "$env:SystemRoot\System32;$env:SystemRoot;$env:SystemRoot\System32\WindowsPowerShell\v1.0"

$s = Setup
$r = Run $s $sys
if ($r.Code -eq 0) { Bad "no magpie: install went ahead" }
elseif ($r.Out -match "https://github.com/yetone/magpie") { Ok "no magpie: refuses and names magpie" }
else { Bad "no magpie: message lacks magpie's address: $($r.Out)" }

$s = Setup
$r = Run $s "$(Join-Path $s.T 'fakebin');$sys"
if ($r.Code -eq 0 -and (Test-Path (Join-Path $s.T "home\.local\bin\queqiao.exe"))) { Ok "with magpie: installed" } else { Bad "with magpie: $($r.Out)" }

$s = Setup
New-Item -ItemType Directory -Force -Path (Join-Path $s.T "home\.config\queqiao") | Out-Null
Set-Content -Path (Join-Path $s.T "home\.config\queqiao\providers.json") -Value "{}"
$r = Run $s "$(Join-Path $s.T 'fakebin');$sys"
if ($r.Out -match "migrate") { Bad "old data: install still talks of migrating: $($r.Out)" } else { Ok "old data: no migrate hint" }

$s = Setup
Set-Content -NoNewline -Path (Join-Path $s.T "rel\checksums.txt") -Value ("0" * 64 + "  $($s.Asset)`n")
$r = Run $s "$(Join-Path $s.T 'fakebin');$sys"
if ($r.Code -ne 0 -and $r.Out -match "SHA-256 mismatch") { Ok "bad checksum: refused" } else { Bad "bad checksum: $($r.Out)" }

exit $script:fail

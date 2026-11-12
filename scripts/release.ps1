<#
.SYNOPSIS
  Builds a Lightspeak release into dist/release/<version>/.

.DESCRIPTION
  Runs the checks, cross-compiles the server (linux amd64/arm64, windows
  amd64), builds the browser client, the Windows installer (NSIS) and writes
  SHA256SUMS. With -Publish the result is uploaded as a GitHub release.
  The version comes from client/src-tauri/tauri.conf.json; package.json and
  Cargo.toml must carry the same version.

  Output:
    lightspeak-server-linux-amd64, lightspeak-server-linux-arm64,
    lightspeak-server-windows-amd64.exe, lightspeak-web.tar.gz (browser
    client, installed next to the server binary), Lightspeak-setup.exe,
    install.sh, SHA256SUMS and – when client/src-tauri/updater.pub exists –
    Lightspeak-setup.exe.sig + latest.json for the app's auto-updater and
    SHA256SUMS.sig for install.sh --version / lightspeak-admin update.

  The apps look for updates at
    https://github.com/<Repo>/releases/latest/download/latest.json
  so a release is live for everyone as soon as it is published and marked
  as the latest one.

  Signing: the private updater key is read from TAURI_SIGNING_PRIVATE_KEY
  (path or content) or %USERPROFILE%\.lightspeak\updater.key; the password
  from TAURI_SIGNING_PRIVATE_KEY_PASSWORD or a prompt. Nothing is stored.

.PARAMETER Repo
  GitHub repository (owner/name) the release belongs to. A fork passes its
  own and builds the app with LIGHTSPEAK_UPDATE_URL pointing at it.

.PARAMETER Publish
  Creates the GitHub release v<version> with the GitHub CLI (gh) and uploads
  all files. The release notes come from -Notes or the matching section of
  CHANGELOG.md. Only complete, signed builds can be published.

.EXAMPLE
  ./scripts/release.ps1                 # build everything into dist/release/<version>
  ./scripts/release.ps1 -Publish        # build and publish on GitHub
  ./scripts/release.ps1 -SkipClient     # server only
  ./scripts/release.ps1 -SkipChecks     # no tests/lint (not for real releases)
  ./scripts/release.ps1 -Notes "Bug fixes"   # text shown with the update
#>
param(
  [string]$Notes = '',
  [string]$Repo = 'syntstar/lightspeak',
  [switch]$Publish,
  [switch]$SkipChecks,
  [switch]$SkipClient,
  [switch]$SkipServer
)
$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$savedKey = $env:TAURI_SIGNING_PRIVATE_KEY; $savedPw = $env:TAURI_SIGNING_PRIVATE_KEY_PASSWORD
$client = Join-Path $root 'client'
$server = Join-Path $root 'server'

function Step($text) { Write-Host "`n== $text" -ForegroundColor Cyan }
function Run([string]$dir, [scriptblock]$block) {
  Push-Location $dir
  try {
    & $block
    if ($LASTEXITCODE -ne 0) { throw "Command failed in ${dir} (exit $LASTEXITCODE)" }
  } finally { Pop-Location }
}

# ---------- version ----------
$ver = (Get-Content (Join-Path $client 'src-tauri/tauri.conf.json') -Raw | ConvertFrom-Json).version
$pkgVer = (Get-Content (Join-Path $client 'package.json') -Raw | ConvertFrom-Json).version
$cargoVer = (Select-String -Path (Join-Path $client 'src-tauri/Cargo.toml') -Pattern '^version\s*=\s*"([^"]+)"' | Select-Object -First 1).Matches[0].Groups[1].Value
if ($ver -notmatch '^\d+\.\d+\.\d+$') { throw "Invalid version '$ver' in tauri.conf.json" }
if ($pkgVer -ne $ver -or $cargoVer -ne $ver) {
  throw "Version mismatch: tauri.conf.json $ver, package.json $pkgVer, Cargo.toml $cargoVer"
}
# install.sh trusts this key for downloaded releases.
$pubFile = Join-Path $client 'src-tauri/updater.pub'
if (Test-Path $pubFile) {
  $pubKey = (Get-Content $pubFile -Raw).Trim()
  if (-not (Select-String -Path (Join-Path $root 'deploy/install.sh') -SimpleMatch "RELEASE_PUBKEY_DEFAULT=$pubKey" -Quiet)) {
    throw 'RELEASE_PUBKEY_DEFAULT in deploy/install.sh differs from client/src-tauri/updater.pub'
  }
}
if ($Repo -notmatch '^[\w.-]+/[\w.-]+$') { throw "Invalid -Repo '$Repo' (expected owner/name)" }
if ($Publish) {
  if ($SkipChecks -or $SkipClient -or $SkipServer) { throw '-Publish needs a complete, checked build (no -Skip switches).' }
  if (-not (Test-Path $pubFile)) { throw '-Publish needs a signed build (client/src-tauri/updater.pub).' }
  if (-not (Get-Command gh -ErrorAction SilentlyContinue)) { throw '-Publish needs the GitHub CLI (gh).' }
}
# Release notes: -Notes, otherwise the section of this version in CHANGELOG.md.
if (-not $Notes) {
  $changelog = Join-Path $root 'CHANGELOG.md'
  if (Test-Path $changelog) {
    $m = [regex]::Match((Get-Content $changelog -Raw), "(?ms)^## \[?$([regex]::Escape($ver))\]?[^\n]*\n(.*?)(?=^## |\z)")
    if ($m.Success) { $Notes = $m.Groups[1].Value.Trim() }
  }
}
$out = Join-Path $root "dist/release/$ver"
New-Item -ItemType Directory -Force $out | Out-Null
Write-Host "Lightspeak $ver -> $out"

$goBin = Join-Path $env:USERPROFILE 'go/bin'
if (-not (Get-Command go -ErrorAction SilentlyContinue)) { $env:Path += ';C:\Program Files\Go\bin' }
if (Test-Path $goBin) { $env:Path += ";$goBin" }

# ---------- checks ----------
if (-not $SkipChecks) {
  if (-not $SkipServer) {
    Step 'Server checks (vet, test, staticcheck, govulncheck)'
    Run $server { go vet ./... }
    Run $server { go test -count=1 ./... }
    Run $server { go run honnef.co/go/tools/cmd/staticcheck@v0.8.2 ./... }
    Run $server { go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./... }
  }
  if (-not $SkipClient) {
    Step 'Dependency security checks'
    Run $client { npm ci }
    Run $client { npm audit --omit=dev }
    # The documented low-severity deepmerge build-tool warning remains visible.
    Run $client { npm audit --audit-level=moderate }
    if (-not (Get-Command cargo-audit -ErrorAction SilentlyContinue)) {
      throw 'Install cargo-audit 0.22.2: cargo install cargo-audit --version 0.22.2 --locked'
    }
    if ((cargo audit --version) -notmatch '\b0\.22\.2$') { throw 'cargo-audit 0.22.2 is required.' }
    Run (Join-Path $client 'src-tauri') { cargo audit }
    # Tauri's test build embeds frontend assets, including on a fresh checkout.
    Run $client { npm run build }
    Run (Join-Path $client 'src-tauri') { cargo test --locked }
    Run (Join-Path $client 'src-tauri') { cargo clippy --locked --all-targets -- -D warnings }
    Step 'Client checks (types, lint, unit tests)'
    Run $client { npm run check }
    Run $client { npm run lint }
    Run $client { npm test }
  }
}

# ---------- server ----------
if (-not $SkipServer) {
  Step 'Server binaries'
  $targets = @(
    @{ os = 'linux'; arch = 'amd64'; name = 'lightspeak-server-linux-amd64' },
    @{ os = 'linux'; arch = 'arm64'; name = 'lightspeak-server-linux-arm64' },
    @{ os = 'windows'; arch = 'amd64'; name = 'lightspeak-server-windows-amd64.exe' }
  )
  $saved = @{ GOOS = $env:GOOS; GOARCH = $env:GOARCH; CGO_ENABLED = $env:CGO_ENABLED }
  try {
    foreach ($t in $targets) {
      $env:GOOS = $t.os; $env:GOARCH = $t.arch; $env:CGO_ENABLED = '0'
      $dest = Join-Path $out $t.name
      Run $server { go build -trimpath -ldflags "-s -w -X main.version=$ver" -o $dest ./cmd/lightspeak-server }
      Write-Host ("  {0,-38} {1,6:N1} MB" -f $t.name, ((Get-Item $dest).Length / 1MB))
    }
  } finally {
    foreach ($k in $saved.Keys) {
      if ($saved[$k]) { Set-Item "env:$k" $saved[$k] } else { Remove-Item "env:$k" -ErrorAction SilentlyContinue }
    }
  }
}

# ---------- browser client ----------
# Part of the server release: it is unpacked next to the binary, so the
# browser app always matches the server (and rolls back with it).
if (-not $SkipServer) {
  Step 'Browser client'
  Run $client { npm run build:web }
  $webTar = Join-Path $out 'lightspeak-web.tar.gz'
  Remove-Item $webTar -ErrorAction SilentlyContinue
  # Windows' bsdtar; --format ustar keeps it readable for GNU tar on the server.
  Run $client { & (Join-Path $env:SystemRoot 'System32\tar.exe') --format ustar -czf $webTar -C dist-web . }
  Write-Host ("  {0,-38} {1,6:N1} MB" -f 'lightspeak-web.tar.gz', ((Get-Item $webTar).Length / 1MB))
}

# ---------- client ----------
if (-not $SkipClient) {
  Step 'Windows installer (NSIS)'
  $signed = Test-Path (Join-Path $client 'src-tauri/updater.pub')
  try {
    if ($signed) {
      # build.rs embeds updater.pub; a fresh mtime forces it to re-run, so a
      # key compiled in by an earlier (e.g. test) build can never ship.
      (Get-Item (Join-Path $client 'src-tauri/updater.pub')).LastWriteTime = Get-Date
      if (-not $env:TAURI_SIGNING_PRIVATE_KEY) {
        $keyFile = Join-Path $env:USERPROFILE '.lightspeak/updater.key'
        if (-not (Test-Path $keyFile)) { throw "updater.pub exists but no private key: set TAURI_SIGNING_PRIVATE_KEY or create $keyFile (CONTRIBUTING.md)" }
        $env:TAURI_SIGNING_PRIVATE_KEY = $keyFile
      }
      if ($null -eq $env:TAURI_SIGNING_PRIVATE_KEY_PASSWORD) {
        $sec = Read-Host 'Updater key password' -AsSecureString
        $env:TAURI_SIGNING_PRIVATE_KEY_PASSWORD = [Runtime.InteropServices.Marshal]::PtrToStringUni(
          [Runtime.InteropServices.Marshal]::SecureStringToGlobalAllocUnicode($sec))
      }
      # Merged into tauri.conf.json for this build only (JSON merge patch).
      $pub = (Get-Content (Join-Path $client 'src-tauri/updater.pub') -Raw).Trim()
      $override = Join-Path ([IO.Path]::GetTempPath()) "lightspeak-release-$PID.json"
      $conf = @{ bundle = @{ createUpdaterArtifacts = $true }; plugins = @{ updater = @{ pubkey = $pub } } }
      [IO.File]::WriteAllText($override, ($conf | ConvertTo-Json -Depth 5))
      try { Run $client { npm run tauri build -- --config $override } }
      finally { Remove-Item $override -ErrorAction SilentlyContinue }
    } else {
      Write-Warning 'No client/src-tauri/updater.pub – building without auto-update.'
      Run $client { npm run tauri build }
    }
  } finally {
    Remove-Item (Join-Path $out 'SHA256SUMS.sig') -ErrorAction SilentlyContinue
  }
  $nsis = Join-Path $client "src-tauri/target/release/bundle/nsis/Lightspeak_${ver}_x64-setup.exe"
  if (-not (Test-Path $nsis)) { throw "Installer not found: $nsis" }
  Copy-Item $nsis (Join-Path $out 'Lightspeak-setup.exe') -Force
  Write-Host ("  Lightspeak-setup.exe {0:N1} MB" -f ((Get-Item $nsis).Length / 1MB))
  Remove-Item (Join-Path $out 'Lightspeak-setup.exe.sig'), (Join-Path $out 'latest.json') -ErrorAction SilentlyContinue
  if ($signed) {
    if (-not (Test-Path "$nsis.sig")) { throw "Signature not found: $nsis.sig" }
    Copy-Item "$nsis.sig" (Join-Path $out 'Lightspeak-setup.exe.sig') -Force
    # Update manifest for the app. The URL names this exact release, so the
    # manifest and the installer it points to can never drift apart.
    $latest = [ordered]@{
      version   = $ver
      notes     = $Notes
      pub_date  = (Get-Date).ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ssZ')
      platforms = [ordered]@{
        'windows-x86_64' = [ordered]@{
          signature = (Get-Content "$nsis.sig" -Raw).Trim()
          url       = "https://github.com/$Repo/releases/download/v$ver/Lightspeak-setup.exe"
        }
      }
    }
    [IO.File]::WriteAllText((Join-Path $out 'latest.json'), ($latest | ConvertTo-Json -Depth 5) + "`n")
    Write-Host '  signed for auto-update'
  }
}

# ---------- checksums ----------
Step 'SHA256SUMS'
# LF only (.gitattributes), so it runs on the server as it is.
Copy-Item (Join-Path $root 'deploy/install.sh') (Join-Path $out 'install.sh') -Force
Remove-Item (Join-Path $out 'SHA256SUMS.sig') -ErrorAction SilentlyContinue
$lines = Get-ChildItem $out -File | Where-Object Name -ne 'SHA256SUMS' | Sort-Object Name | ForEach-Object {
  '{0}  {1}' -f (Get-FileHash $_.FullName -Algorithm SHA256).Hash.ToLowerInvariant(), $_.Name
}
# LF line endings so `sha256sum -c` works on Linux.
[IO.File]::WriteAllText((Join-Path $out 'SHA256SUMS'), (($lines -join "`n") + "`n"))
$lines | ForEach-Object { Write-Host "  $_" }

# Signed with the updater key: install.sh checks this file before it trusts
# any download. Needs the key, so a server-only build stays unsigned.
try {
  if (-not $SkipClient -and $signed) {
    Step 'Signing SHA256SUMS'
    $keyArg = if (Test-Path $env:TAURI_SIGNING_PRIVATE_KEY) { @('--private-key-path', $env:TAURI_SIGNING_PRIVATE_KEY) } else { @() }
    $envKey = $env:TAURI_SIGNING_PRIVATE_KEY
    if ($keyArg) { Remove-Item env:TAURI_SIGNING_PRIVATE_KEY }
    try { Run $client { npm run tauri signer sign -- @keyArg (Join-Path $out 'SHA256SUMS') } }
    finally { $env:TAURI_SIGNING_PRIVATE_KEY = $envKey }
    if (-not (Test-Path (Join-Path $out 'SHA256SUMS.sig'))) { throw 'SHA256SUMS.sig was not created' }
  } elseif (-not $SkipClient) {
    Write-Warning 'SHA256SUMS is not signed: install.sh --version will refuse this release.'
  }
} finally {
  $env:TAURI_SIGNING_PRIVATE_KEY = $savedKey; $env:TAURI_SIGNING_PRIVATE_KEY_PASSWORD = $savedPw
}

Write-Host "`nDone: $out" -ForegroundColor Green

# ---------- GitHub release ----------
if ($Publish) {
  Step "Publishing v$ver to github.com/$Repo"
  $notesFile = Join-Path ([IO.Path]::GetTempPath()) "lightspeak-notes-$PID.md"
  [IO.File]::WriteAllText($notesFile, $(if ($Notes) { $Notes } else { "Lightspeak $ver" }) + "`n")
  try {
    $files = Get-ChildItem $out -File | ForEach-Object FullName
    # Uploaded as a draft first: the apps only see the release (and its
    # latest.json) once every file is there.
    & gh release create "v$ver" @files --repo $Repo --title "Lightspeak $ver" --notes-file $notesFile --draft
    if ($LASTEXITCODE -ne 0) { throw "gh release create failed (exit $LASTEXITCODE)" }
    & gh release edit "v$ver" --repo $Repo --draft=false --latest
    if ($LASTEXITCODE -ne 0) { throw "gh release edit failed (exit $LASTEXITCODE)" }
  } finally { Remove-Item $notesFile -ErrorAction SilentlyContinue }
  Write-Host "Published: https://github.com/$Repo/releases/tag/v$ver" -ForegroundColor Green
} else {
  Write-Host 'Publish on GitHub: ./scripts/release.ps1 -Publish'
}

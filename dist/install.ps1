# blueprint installer for Windows (PowerShell 5.1+ / pwsh).
#
# Usage:
#   powershell -ExecutionPolicy Bypass -File install.ps1
#
# Environment overrides:
#   GITHUB_REPO         owner/repo slug of the release repo
#   BLUEPRINT_VERSION   version to install (e.g. 1.2.3); default: latest release
#
# Installs to $env:LOCALAPPDATA\Programs\blueprint and adds that directory to
# the *user* PATH (no admin rights needed). Verifies sha256 against the release
# checksums file. Idempotent: re-running with the same version is a no-op.
# Blueprint is Windows-clean: no symlinks are created anywhere.

$ErrorActionPreference = 'Stop'

$Repo = if ($env:GITHUB_REPO) { $env:GITHUB_REPO } else { 'akhilmukkamala/blueprint' }
$Version = if ($env:BLUEPRINT_VERSION) { $env:BLUEPRINT_VERSION } else { 'latest' }

$Arch = if ([Environment]::Is64BitOperatingSystem) { 'amd64' } else {
    throw 'blueprint requires a 64-bit Windows (amd64).'
}

# --- resolve version ---------------------------------------------------------
if ($Version -eq 'latest') {
    $resp = Invoke-WebRequest -Uri "https://github.com/$Repo/releases/latest" `
        -MaximumRedirection 0 -ErrorAction SilentlyContinue -UseBasicParsing
    $location = $resp.Headers.Location
    if (-not $location) {
        # PowerShell 7 throws on 3xx with MaximumRedirection 0; retry catching it.
        try {
            Invoke-WebRequest -Uri "https://github.com/$Repo/releases/latest" `
                -MaximumRedirection 0 -UseBasicParsing | Out-Null
        } catch {
            $location = $_.Exception.Response.Headers.Location.AbsoluteUri
        }
    }
    if ($location -match '/tag/v([^/]+)$') { $Version = $Matches[1] }
    else { throw "Could not resolve the latest release of $Repo. Set BLUEPRINT_VERSION=x.y.z and re-run." }
}
Write-Host "Installing blueprint v$Version (windows/$Arch) from $Repo"

# --- install dir + idempotency ----------------------------------------------
$InstallDir = Join-Path $env:LOCALAPPDATA 'Programs\blueprint'
$Dest = Join-Path $InstallDir 'blueprint.exe'
if (Test-Path $Dest) {
    $installed = & $Dest --version 2>$null
    if ($installed -match [regex]::Escape($Version)) {
        Write-Host "blueprint v$Version already installed at $Dest - nothing to do"
        exit 0
    }
}
New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null

# --- download + verify -------------------------------------------------------
$Asset = "blueprint-$Version-windows-$Arch.zip"
$Base = "https://github.com/$Repo/releases/download/v$Version"
$Tmp = Join-Path ([System.IO.Path]::GetTempPath()) ("blueprint-install-" + [guid]::NewGuid())
New-Item -ItemType Directory -Path $Tmp | Out-Null
try {
    Write-Host "Downloading $Asset ..."
    Invoke-WebRequest -Uri "$Base/$Asset" -OutFile (Join-Path $Tmp $Asset) -UseBasicParsing
    Invoke-WebRequest -Uri "$Base/checksums.txt" -OutFile (Join-Path $Tmp 'checksums.txt') -UseBasicParsing

    $line = Select-String -Path (Join-Path $Tmp 'checksums.txt') -Pattern ([regex]::Escape($Asset) + '$') |
        Select-Object -First 1
    if (-not $line) { throw "checksums.txt has no entry for $Asset; refusing to install." }
    $expected = ($line.Line -split '\s+')[0].ToLowerInvariant()
    $actual = (Get-FileHash -Algorithm SHA256 -Path (Join-Path $Tmp $Asset)).Hash.ToLowerInvariant()
    if ($expected -ne $actual) {
        throw "sha256 mismatch for $Asset (expected $expected, got $actual); download corrupted or tampered - not installing."
    }
    Write-Host 'sha256 verified'

    Expand-Archive -Path (Join-Path $Tmp $Asset) -DestinationPath $Tmp -Force
    $bin = Join-Path $Tmp 'blueprint.exe'
    if (-not (Test-Path $bin)) { throw "zip did not contain blueprint.exe" }
    Move-Item -Force -Path $bin -Destination $Dest
    Write-Host "Installed $Dest"
} finally {
    Remove-Item -Recurse -Force -Path $Tmp -ErrorAction SilentlyContinue
}

# --- user PATH ---------------------------------------------------------------
$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if (-not $userPath) { $userPath = '' }
$onPath = ($userPath -split ';' | Where-Object { $_ -eq $InstallDir }).Count -gt 0
if (-not $onPath) {
    $newPath = if ($userPath) { "$userPath;$InstallDir" } else { $InstallDir }
    [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
    Write-Host "Added $InstallDir to your user PATH (open a new terminal to pick it up)."
} else {
    Write-Host "$InstallDir is already on your user PATH."
}

Write-Host "Done - run 'blueprint init' in a repo to get started (see INSTALL.md)."

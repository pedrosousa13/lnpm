# lnpm installer for Windows
# Usage: irm https://raw.githubusercontent.com/pedrosousa13/lnpm/main/install.ps1 | iex

$ErrorActionPreference = "Stop"

$Repo = "pedrosousa13/lnpm"
$Binary = "lnpm"
$InstallDir = if ($env:LNPM_INSTALL_DIR) { $env:LNPM_INSTALL_DIR } else { "$env:LOCALAPPDATA\lnpm" }

function Write-Info { param($msg) Write-Host "[INFO] $msg" -ForegroundColor Blue }
function Write-Ok { param($msg) Write-Host "[OK] $msg" -ForegroundColor Green }
function Write-Warn { param($msg) Write-Host "[WARN] $msg" -ForegroundColor Yellow }
function Write-Err { param($msg) Write-Host "[ERROR] $msg" -ForegroundColor Red; exit 1 }

# Detect architecture
function Get-Arch {
    switch ($env:PROCESSOR_ARCHITECTURE) {
        "AMD64" { return "amd64" }
        "ARM64" { return "arm64" }
        default { Write-Err "Unsupported architecture: $env:PROCESSOR_ARCHITECTURE" }
    }
}

# Get latest release version
function Get-LatestVersion {
    try {
        $release = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/latest" -UseBasicParsing
        return $release.tag_name -replace '^v', ''
    } catch {
        Write-Err "Failed to get latest version: $_"
    }
}

Write-Host ""
Write-Host "  _                       "
Write-Host " | |_ __  _ __  _ __ ___  "
Write-Host " | | '_ \| '_ \| '_ `` _ \ "
Write-Host " | | | | | |_) | | | | | |"
Write-Host " |_|_| |_| .__/|_| |_| |_|"
Write-Host "         |_|              "
Write-Host ""
Write-Host " Fast local npm package development"
Write-Host ""

Write-Info "Installing lnpm..."

$Arch = Get-Arch
$Version = Get-LatestVersion

Write-Info "OS: windows, Arch: $Arch, Version: $Version"

$Filename = "${Binary}_${Version}_windows_${Arch}.zip"
$Url = "https://github.com/$Repo/releases/download/v$Version/$Filename"

# Download to temp
$TmpDir = Join-Path ([System.IO.Path]::GetTempPath()) ([System.Guid]::NewGuid().ToString())
New-Item -ItemType Directory -Path $TmpDir -Force | Out-Null

try {
    $ZipPath = Join-Path $TmpDir $Filename
    Write-Info "Downloading from $Url..."
    Invoke-WebRequest -Uri $Url -OutFile $ZipPath -UseBasicParsing

    Write-Info "Extracting..."
    Expand-Archive -Path $ZipPath -DestinationPath $TmpDir -Force

    # Install
    New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
    Copy-Item -Path (Join-Path $TmpDir "$Binary.exe") -Destination $InstallDir -Force

    Write-Ok "Installed lnpm v$Version to $InstallDir\$Binary.exe"

    # Check PATH
    $UserPath = [Environment]::GetEnvironmentVariable("Path", "User")
    if ($UserPath -notlike "*$InstallDir*") {
        Write-Host ""
        Write-Warn "$InstallDir is not in your PATH"
        Write-Host ""
        Write-Host "To add it, run:"
        Write-Host ""
        Write-Host "  `$path = [Environment]::GetEnvironmentVariable('Path', 'User')"
        Write-Host "  [Environment]::SetEnvironmentVariable('Path', `"`$path;$InstallDir`", 'User')"
        Write-Host ""
        Write-Host "Then restart your terminal."
    }

    Write-Host ""
    Write-Ok "Installation complete!"
    Write-Host ""
    Write-Host "Run 'lnpm --help' to get started"
} finally {
    Remove-Item -Path $TmpDir -Recurse -Force -ErrorAction SilentlyContinue
}

# sshtool build script (PowerShell)
#
#   .\build.ps1                  # build for current platform
#   .\build.ps1 -All             # cross build windows/linux/darwin
#   .\build.ps1 -Version v1.0.0  # set version
#   .\build.ps1 -Run             # run after build

param(
    [string]$Version = "dev",
    [switch]$All,
    [switch]$Run
)

$ErrorActionPreference = "Stop"

if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    $candidate = "D:\Projects\.tools\go1.26.7\bin"
    if (Test-Path (Join-Path $candidate "go.exe")) {
        $env:PATH = "$candidate;$env:PATH"
    }
    else {
        throw "go not found. Install Go or add go.exe to PATH."
    }
}

$root = Split-Path -Parent $MyInvocation.MyCommand.Path
Push-Location $root
try {
    $outDir = Join-Path $root "dist"
    New-Item -ItemType Directory -Force -Path $outDir | Out-Null

    if ($All) {
        $targets = @(
            @{ GOOS = "windows"; GOARCH = "amd64"; Ext = ".exe" },
            @{ GOOS = "linux";   GOARCH = "amd64"; Ext = "" },
            @{ GOOS = "darwin";  GOARCH = "arm64"; Ext = "" }
        )
    }
    else {
        $ext = ".exe"
        if ($env:GOOS -eq "linux" -or $env:GOOS -eq "darwin") { $ext = "" }
        $arch = "amd64"
        if ($env:GOARCH) { $arch = $env:GOARCH }
        $targets = @( @{ GOOS = ""; GOARCH = $arch; Ext = $ext } )
    }

    $ldflags = "-s -w -X main.version=$Version"

    foreach ($t in $targets) {
        $goos = $t.GOOS
        $goarch = $t.GOARCH
        if ($goos) { $name = "sshtool-$goos-$goarch" } else { $name = "sshtool" }
        $bin = Join-Path $outDir ($name + $t.Ext)

        $env:GOOS = $goos
        $env:GOARCH = $goarch
        $env:CGO_ENABLED = "0"

        Write-Host "building $name ..." -ForegroundColor Cyan
        & go build -trimpath -ldflags $ldflags -o $bin .
        if ($LASTEXITCODE -ne 0) { throw "build failed: $name" }

        $size = [math]::Round((Get-Item $bin).Length / 1MB, 2)
        Write-Host "  -> $bin ($size MB)" -ForegroundColor Green
    }

    if ($Run) {
        $bin = Join-Path $outDir "sshtool.exe"
        if (-not (Test-Path $bin)) { $bin = Join-Path $outDir "sshtool" }
        Write-Host "starting $bin" -ForegroundColor Cyan
        & $bin
    }
}
finally {
    $env:GOOS = ""
    $env:GOARCH = ""
    Pop-Location
}

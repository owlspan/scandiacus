# Builds the Windows installer for the Scandiacus assembly backend.
#
#     powershell -ExecutionPolicy Bypass -File installer\build.ps1
#
# Two steps, which is the whole point of this backend: build
# scandiacus.exe, then compile the Inno Setup script. There is no toolchain
# to stage. The Go backend's installer ships 177 MB of trimmed Go
# because that backend compiles by handing generated code to the Go
# compiler; this one writes the executable itself, so the payload is one
# small exe.
#
# It does check that the exe it just built actually compiles and runs a
# program before spending time on compression, because an installer
# around a broken compiler is the failure worth catching early.

$ErrorActionPreference = 'Stop'

$repo = Split-Path -Parent $PSScriptRoot
$dist = Join-Path $repo 'dist'   # matches OutputDir in scandiacus.iss

Push-Location $repo
try {
    Write-Host '==> building scandiacus.exe' -ForegroundColor Cyan
    & go build -o scandiacus.exe ./compiler
    if ($LASTEXITCODE -ne 0) { throw 'go build failed' }

    $exe = Join-Path $repo 'scandiacus.exe'
    $version = (& $exe version)
    if ($LASTEXITCODE -ne 0) { throw 'scandiacus version failed' }
    Write-Host "    $version" -ForegroundColor Green

    # The version in the .iss has to match the one compiled into the
    # exe, or the installer names a release that does not exist. Read it
    # from the exe and check, rather than trusting two files to be
    # edited together.
    $iss = Join-Path $PSScriptRoot 'scandiacus.iss'
    $want = ([regex]'scandiacus (\S+)').Match($version).Groups[1].Value
    $have = ([regex]'#define AppVersion "([^"]+)"').Match((Get-Content $iss -Raw)).Groups[1].Value
    if ($want -ne $have) {
        throw "version mismatch: scandiacus.exe says $want, scandiacus.iss says $have. " +
              "Update AppVersion in installer\scandiacus.iss."
    }
    $ext = ([regex]'#define ExtVersion "([^"]+)"').Match((Get-Content $iss -Raw)).Groups[1].Value
    $pkg = (Get-Content (Join-Path $repo '..\editors\vscode\package.json') -Raw | ConvertFrom-Json).version
    if ($ext -ne $want -or $pkg -ne $want) {
        throw "version mismatch: scandiacus.exe says $want, ExtVersion in scandiacus.iss says $ext, " +
              "editors\vscode\package.json says $pkg. All three have to agree."
    }

    # Prove it compiles something. PATH is stripped to System32 first so
    # this cannot accidentally succeed by finding a Go, an assembler or
    # a linker that the installed copy will not have.
    Write-Host '==> checking the compiler builds a program' -ForegroundColor Cyan
    $probe = Join-Path $env:TEMP ("scandiacus-probe-" + [guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Force $probe | Out-Null
    try {
        $src = Join-Path $probe 'probe.scd'
        Set-Content -Path $src -Encoding utf8 -Value 'print("installer probe {2 + 2}")'

        $savedPath = $env:PATH
        $env:PATH = "$env:SystemRoot\System32"
        try {
            $out = & $exe run $src
            if ($LASTEXITCODE -ne 0) { throw "scandiacus could not compile the probe" }
            if ($out -notmatch 'installer probe 4') { throw "unexpected probe output: $out" }
        } finally {
            $env:PATH = $savedPath
        }
        Write-Host '    compiles and runs with nothing on PATH' -ForegroundColor Green
    } finally {
        Remove-Item -Recurse -Force $probe -ErrorAction SilentlyContinue
    }

    if (-not (Test-Path $dist)) { New-Item -ItemType Directory -Force $dist | Out-Null }

    $iscc = @(
        "$env:LOCALAPPDATA\Programs\Inno Setup 6\ISCC.exe",
        "${env:ProgramFiles(x86)}\Inno Setup 6\ISCC.exe",
        "$env:ProgramFiles\Inno Setup 6\ISCC.exe"
    ) | Where-Object { Test-Path $_ } | Select-Object -First 1

    if (-not $iscc) {
        throw "ISCC.exe not found. Install Inno Setup: winget install JRSoftware.InnoSetup"
    }

    Write-Host '==> compiling the installer' -ForegroundColor Cyan
    & $iscc $iss
    if ($LASTEXITCODE -ne 0) { throw "iscc failed with $LASTEXITCODE" }

    Get-ChildItem (Join-Path $dist 'scandiacus-*-setup.exe') | ForEach-Object {
        Write-Host ("==> {0}  ({1:N1} MB)" -f $_.Name, ($_.Length / 1MB)) -ForegroundColor Green
    }
} finally {
    Pop-Location
}

# Per-example generated-code size: assembly line count and executable bytes.
#
#     .\scripts\metrics.ps1                    use ..\scandiacus.exe
#     .\scripts\metrics.ps1 -Compiler X.exe    use another build
#
# Examples are copied to a temp directory before building, so nothing lands
# beside the sources and multi-file examples keep their imports working.
# Lines counted exclude comments, labels and directives, so the number moves
# when the code gets better, not when it gets commented.
param([string]$Compiler = (Join-Path $PSScriptRoot "..\scandiacus.exe"))

$ErrorActionPreference = "Stop"
$examples = Join-Path $PSScriptRoot "..\examples"
$tmp = Join-Path ([System.IO.Path]::GetTempPath()) (
    "scandiacus-metrics-" + [guid]::NewGuid().ToString("N").Substring(0, 8))
New-Item -ItemType Directory $tmp | Out-Null
# The whole tree, not just top-level files: an example may import from a
# subfolder next to it (imports.scd does).
Copy-Item $examples\* $tmp -Recurse

try {
    $totalLines = 0
    $totalBytes = 0
    foreach ($f in Get-ChildItem (Join-Path $examples "*.scd") | Sort-Object Name) {
        $asm = & $Compiler asm $f.FullName
        $lines = @($asm | Where-Object {
            $_ -match '\S' -and $_ -notmatch '^\s*#' -and $_ -notmatch '^\s*\.' -and $_ -notmatch ':$'
        }).Count

        & $Compiler build (Join-Path $tmp $f.Name) | Out-Null
        $exe = Join-Path $tmp ($f.BaseName + ".exe")
        $size = 0
        if (Test-Path $exe) { $size = (Get-Item $exe).Length }

        $totalLines += $lines
        $totalBytes += $size
        "{0,-12} {1,7} lines {2,10} bytes" -f $f.BaseName, $lines, $size
    }
    "{0,-12} {1,7} lines {2,10} bytes" -f "TOTAL", $totalLines, $totalBytes
}
finally {
    Remove-Item -Recurse -Force $tmp
}

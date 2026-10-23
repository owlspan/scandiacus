@echo off
rem Double-click this to build the scandiacus installer.
rem
rem It exists because Windows will not run a .ps1 on double-click. That
rem is a deliberate security default, not something to work around in
rem the script itself. A .bat does run, so this one just calls the
rem PowerShell script with the flags it needs.
rem
rem This builds only. It does not commit, tag, push, or install
rem anything: the result is a setup .exe in asm-src\dist that you then
rem run yourself.

setlocal
rem The scripts live in asm-src\scripts, and build.ps1 expects to be
rem run from asm-src itself.
cd /d "%~dp0.."

echo Building the scandiacus installer.
echo.
echo This one is quick. There is no Go toolchain to bundle: scandiacus
echo assembles and links by itself, so the installer is one small exe.
echo.

powershell -NoProfile -ExecutionPolicy Bypass -File "installer\build.ps1"
set BUILD_EXIT=%ERRORLEVEL%

echo.
if not "%BUILD_EXIT%"=="0" (
    echo Build FAILED with exit code %BUILD_EXIT%.
    echo.
    echo If it said ISCC.exe was not found, install Inno Setup:
    echo     winget install JRSoftware.InnoSetup
    echo.
    echo If it complained that a file is in use, that is usually the
    echo virus scanner holding the output open. Run it again.
) else (
    echo Done. The installer is in the dist folder:
    echo.
    dir /b dist\scandiacus-*-setup.exe
)

echo.
pause

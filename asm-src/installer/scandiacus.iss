; Inno Setup script for Scandiacus.
;
; One exe, a PATH entry, and the file associations. There is no
; toolchain to bundle: scandiacus.exe encodes, links and writes the PE by
; itself, which is why this installer is 5 MB and the old backend's was
; 90.
;
; Build it with installer\build.ps1 rather than calling iscc directly,
; so scandiacus.exe is built and stamped first:
;
;     powershell -ExecutionPolicy Bypass -File installer\build.ps1

#define AppName "Scandiacus"
#define ShortName "scandiacus"
#define AppVersion "2.7.0"
#define AppPublisher "Scandiacus"
#define AppExeName "scandiacus.exe"
; Has to match editors/vscode/package.json, which is the version VS
; Code records for the extension. build.ps1 checks both against scandiacus.exe.
#define ExtVersion "2.7.0"

[Setup]
AppId={{B7D42A19-3E6C-4F80-A5D3-91C7E20B4F68}
AppName={#AppName}
AppVersion={#AppVersion}
AppPublisher={#AppPublisher}
DefaultDirName={autopf}\Scandiacus
DefaultGroupName=Scandiacus
DisableProgramGroupPage=yes
OutputDir=..\dist
OutputBaseFilename=scandiacus-{#AppVersion}-setup
; Without this Inno does not broadcast WM_SETTINGCHANGE after editing
; the environment, so Explorer keeps handing every new terminal the old
; PATH until the next sign-out. The entry was written correctly and
; still looked like it had done nothing, which is exactly how this was
; reported.
ChangesEnvironment=yes
; The shell caches file associations, so a new .scd icon and the verbs
; below do not appear until it is told. Same class of problem.
ChangesAssociations=yes
Compression=lzma2/max
SolidCompression=yes
WizardStyle=modern
SetupIconFile=..\..\icons\scandiacus.ico
UninstallDisplayIcon={app}\{#AppExeName}
AppSupportURL=https://github.com/owlspan/scandiacus
AppUpdatesURL=https://github.com/owlspan/scandiacus/releases
; Per-user by default, so no administrator prompt for the common case.
; PrivilegesRequired defaults to "admin", so without this line every
; install would open with a UAC prompt it does not need.
PrivilegesRequired=lowest
PrivilegesRequiredOverridesAllowed=dialog
ArchitecturesInstallIn64BitMode=x64compatible
; The whole payload is one small exe and some text, so the wizard never
; sits there looking frozen and there is nothing to warn about.

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"

[Components]
Name: "core"; Description: "The Scandiacus compiler"; Types: full compact custom; Flags: fixed
Name: "docs"; Description: "Examples and documentation"; Types: full custom

; Building Linux and macOS executables from Windows. It is zig's C
; toolchain, downloaded at install time (about 80 MB, 300 MB unpacked)
; rather than carried in this installer; `scandiacus cross install` does the
; same later if this is left unticked or the download fails.
Name: "cross"; Description: "Cross compiling (build for Linux and macOS; downloads about 80 MB)";     Types: full custom; ExtraDiskSpaceRequired: 320000000

; Editor syntax highlighting. Each writes into that editor's own config
; folder, so ticking one for an editor you do not have is harmless: the
; folder is created and simply never read.
Name: "editors"; Description: "Editor syntax highlighting"; Types: full custom
Name: "editors\vscode"; Description: "Visual Studio Code"; Types: full custom
Name: "editors\npp"; Description: "Notepad++"; Types: full custom
Name: "editors\sublime"; Description: "Sublime Text"; Types: custom
Name: "editors\vim"; Description: "Vim and Neovim"; Types: custom

[Tasks]
Name: "addtopath"; Description: "Add Scandiacus to PATH so 'scandiacus' works in any terminal"; \
    GroupDescription: "Setup:"
Name: "associate"; Description: "Associate .scd files, with an icon and right-click Run and Compile"; \
    GroupDescription: "Setup:"
Name: "prompt"; Description: "Add a ""Scandiacus prompt"" shortcut (a terminal with Scandiacus already on PATH)"; \
    GroupDescription: "Setup:"
Name: "desktopicon"; Description: "Put that shortcut on the Desktop too"; \
    GroupDescription: "Setup:"; Flags: unchecked

[Files]
Source: "..\scandiacus.exe";           DestDir: "{app}"; Components: core; Flags: ignoreversion
Source: "..\..\icons\scandiacus.ico";     DestDir: "{app}"; Components: core; Flags: ignoreversion
Source: "..\..\README.md";          DestDir: "{app}"; Components: core; Flags: ignoreversion
Source: "..\..\LICENSE";            DestDir: "{app}"; Components: core; Flags: ignoreversion

Source: "..\..\docs\SYNTAX.md";   DestDir: "{app}"; Components: docs; Flags: ignoreversion
Source: "..\..\docs\TUTORIAL.md"; DestDir: "{app}"; Components: docs; Flags: ignoreversion
Source: "..\examples\*.scd";       DestDir: "{app}\examples"; Components: docs; Flags: ignoreversion
Source: "..\examples\net\*.scd";   DestDir: "{app}\examples\net"; Components: docs; Flags: ignoreversion
Source: "..\examples\gui\*.scd";   DestDir: "{app}\examples\gui"; Components: docs; Flags: ignoreversion
Source: "..\examples\mod\*.scd";   DestDir: "{app}\examples\mod"; Components: docs; Flags: ignoreversion
Source: "..\examples\ffi\*.scd";   DestDir: "{app}\examples\ffi"; Components: docs; Flags: ignoreversion

; The VS Code extension goes in beside scandiacus.exe, and [Run] below has
; scandiacus install it with VS Code's own command line. Copying the folder
; into .vscode\extensions, as this used to, stopped working: VS Code
; keeps a registry (extensions.json) and does not load a folder that is
; not listed in it, so highlighting silently vanished for anyone who had
; any other extension installed. scandiacus vscode packs the folder as a .vsix
; and runs code --install-extension, which registers it.
Source: "..\..\editors\vscode\*"; DestDir: "{app}\editors\vscode"; \
    Components: editors\vscode; Flags: ignoreversion recursesubdirs createallsubdirs

; Notepad++ reads every XML in userDefineLangs at startup.
Source: "..\..\editors\notepad++\scandiacus.xml"; \
    DestDir: "{userappdata}\Notepad++\userDefineLangs"; \
    Components: editors\npp; Flags: ignoreversion

; Sublime scans Packages\User. The build file makes Ctrl+B build the
; open .scd file, and Ctrl+Shift+B offer Run.
Source: "..\..\editors\sublime\Scandiacus.sublime-syntax"; \
    DestDir: "{userappdata}\Sublime Text\Packages\User"; \
    Components: editors\sublime; Flags: ignoreversion
Source: "..\..\editors\sublime\Scandiacus.sublime-build"; \
    DestDir: "{userappdata}\Sublime Text\Packages\User"; \
    Components: editors\sublime; Flags: ignoreversion

; Vim and Neovim look in different places, so both get a copy rather
; than the installer trying to guess which one is in use.
Source: "..\..\editors\vim\syntax\scandiacus.vim"; DestDir: "{userdocs}\vimfiles\syntax"; \
    Components: editors\vim; Flags: ignoreversion
Source: "..\..\editors\vim\ftdetect\scandiacus.vim"; DestDir: "{userdocs}\vimfiles\ftdetect"; \
    Components: editors\vim; Flags: ignoreversion
Source: "..\..\editors\vim\syntax\scandiacus.vim"; DestDir: "{localappdata}\nvim\syntax"; \
    Components: editors\vim; Flags: ignoreversion
Source: "..\..\editors\vim\ftdetect\scandiacus.vim"; DestDir: "{localappdata}\nvim\ftdetect"; \
    Components: editors\vim; Flags: ignoreversion
Source: "..\..\editors\vim\ftplugin\scandiacus.vim"; DestDir: "{userdocs}\vimfiles\ftplugin"; \
    Components: editors\vim; Flags: ignoreversion
Source: "..\..\editors\vim\ftplugin\scandiacus.vim"; DestDir: "{localappdata}\nvim\ftplugin"; \
    Components: editors\vim; Flags: ignoreversion

[Icons]
; A terminal with Scandiacus on PATH whether or not the PATH task was
; ticked, so the install is usable without signing out and back in.
Name: "{group}\Scandiacus prompt"; Filename: "{cmd}"; \
    Parameters: "/K ""set PATH={app};%PATH% && echo Scandiacus {#AppVersion} - try: scandiacus run examples\collatz.scd && cd /d {app}"""; \
    IconFilename: "{app}\scandiacus.ico"; Tasks: prompt
Name: "{autodesktop}\Scandiacus prompt"; Filename: "{cmd}"; \
    Parameters: "/K ""set PATH={app};%PATH% && echo Scandiacus {#AppVersion} - try: scandiacus run examples\collatz.scd && cd /d {app}"""; \
    IconFilename: "{app}\scandiacus.ico"; Tasks: desktopicon

Name: "{group}\Examples"; Filename: "{app}\examples"; Components: docs
Name: "{group}\Scandiacus language reference"; Filename: "{app}\SYNTAX.md"; Components: docs
Name: "{group}\Learn Scandiacus (tutorial)"; Filename: "{app}\TUTORIAL.md"; Components: docs
Name: "{group}\Uninstall Scandiacus"; Filename: "{uninstallexe}"

[Registry]
; PATH. The two hives keep it in completely different places and HKA
; cannot paper over that: the per-user one is HKCU\Environment, the
; machine one is under Session Manager. There is no HKLM\Environment,
; and asking for one fails with "code 87, the parameter is incorrect",
; which names the API call rather than the mistake.
Root: HKCU; Subkey: "Environment"; ValueType: expandsz; ValueName: "Path"; \
    ValueData: "{olddata};{app}"; Tasks: addtopath; \
    Check: not IsAdminInstallMode and NeedsPathEntry(ExpandConstant('{app}'))
Root: HKLM; Subkey: "SYSTEM\CurrentControlSet\Control\Session Manager\Environment"; \
    ValueType: expandsz; ValueName: "Path"; \
    ValueData: "{olddata};{app}"; Tasks: addtopath; \
    Check: IsAdminInstallMode and NeedsPathEntry(ExpandConstant('{app}'))

; The .scd file type.
;
; This used to hang its verbs off a Scandiacus.Source class it never created,
; on the reasoning that the Go backend's installer owned the extension
; and two installers should not fight over it. That backend is on its
; own branch now and this is the compiler, so it claims the type - and
; the old arrangement was broken anyway: with nothing creating
; Scandiacus.Source, the verbs were attached to a class that did not exist
; and no Scandiacus entry appeared on the menu at all.
Root: HKA; Subkey: "Software\Classes\.scd"; ValueType: string; ValueName: ""; \
    ValueData: "Scandiacus.Source"; Flags: uninsdeletevalue; Tasks: associate
Root: HKA; Subkey: "Software\Classes\Scandiacus.Source"; ValueType: string; ValueName: ""; \
    ValueData: "Scandiacus source file"; Flags: uninsdeletekey; Tasks: associate
Root: HKA; Subkey: "Software\Classes\Scandiacus.Source\DefaultIcon"; ValueType: string; ValueName: ""; \
    ValueData: "{app}\scandiacus.ico"; Tasks: associate

; Double-clicking runs it. Through cmd /K so the console stays up: a
; program that prints and exits is otherwise a window that opens and
; closes faster than anyone can read.
Root: HKA; Subkey: "Software\Classes\Scandiacus.Source\shell"; ValueType: string; ValueName: ""; \
    ValueData: "run"; Tasks: associate

Root: HKA; Subkey: "Software\Classes\Scandiacus.Source\shell\run"; ValueType: string; ValueName: ""; \
    ValueData: "Run with Scandiacus"; Tasks: associate
Root: HKA; Subkey: "Software\Classes\Scandiacus.Source\shell\run"; ValueType: string; ValueName: "Icon"; \
    ValueData: "{app}\scandiacus.ico"; Tasks: associate
Root: HKA; Subkey: "Software\Classes\Scandiacus.Source\shell\run\command"; ValueType: string; ValueName: ""; \
    ValueData: "{cmd} /K """"{app}\{#AppExeName}"" run ""%1"""""; Tasks: associate

Root: HKA; Subkey: "Software\Classes\Scandiacus.Source\shell\build"; ValueType: string; ValueName: ""; \
    ValueData: "Compile to .exe"; Tasks: associate
Root: HKA; Subkey: "Software\Classes\Scandiacus.Source\shell\build"; ValueType: string; ValueName: "Icon"; \
    ValueData: "{app}\scandiacus.ico"; Tasks: associate
Root: HKA; Subkey: "Software\Classes\Scandiacus.Source\shell\build\command"; ValueType: string; ValueName: ""; \
    ValueData: "{cmd} /K """"{app}\{#AppExeName}"" build ""%1"""""; Tasks: associate

; The rest go under one cascading "Scandiacus" entry rather than scattering
; four more verbs across the menu. Windows builds the submenu from a
; second key named by ExtendedSubCommandsKey, and the parent
; deliberately has no command of its own.
Root: HKA; Subkey: "Software\Classes\Scandiacus.Source\shell\scandiacus"; ValueType: string; ValueName: "MUIVerb"; \
    ValueData: "Scandiacus"; Tasks: associate
Root: HKA; Subkey: "Software\Classes\Scandiacus.Source\shell\scandiacus"; ValueType: string; ValueName: "Icon"; \
    ValueData: "{app}\scandiacus.ico"; Tasks: associate
Root: HKA; Subkey: "Software\Classes\Scandiacus.Source\shell\scandiacus"; ValueType: string; \
    ValueName: "ExtendedSubCommandsKey"; \
    ValueData: "Scandiacus.Source\shell\scandiacus\sub"; Tasks: associate

Root: HKA; Subkey: "Software\Classes\Scandiacus.Source\shell\scandiacus\sub\shell\01asm"; ValueType: string; ValueName: "MUIVerb"; \
    ValueData: "Show the generated assembly"; Tasks: associate
Root: HKA; Subkey: "Software\Classes\Scandiacus.Source\shell\scandiacus\sub\shell\01asm\command"; ValueType: string; ValueName: ""; \
    ValueData: "{cmd} /K """"{app}\{#AppExeName}"" asm ""%1"""""; Tasks: associate

Root: HKA; Subkey: "Software\Classes\Scandiacus.Source\shell\scandiacus\sub\shell\02ir"; ValueType: string; ValueName: "MUIVerb"; \
    ValueData: "Show the IR"; Tasks: associate
Root: HKA; Subkey: "Software\Classes\Scandiacus.Source\shell\scandiacus\sub\shell\02ir\command"; ValueType: string; ValueName: ""; \
    ValueData: "{cmd} /K """"{app}\{#AppExeName}"" ir ""%1"""""; Tasks: associate

Root: HKA; Subkey: "Software\Classes\Scandiacus.Source\shell\scandiacus\sub\shell\03prompt"; ValueType: string; ValueName: "MUIVerb"; \
    ValueData: "Open a Scandiacus prompt here"; Tasks: associate
Root: HKA; Subkey: "Software\Classes\Scandiacus.Source\shell\scandiacus\sub\shell\03prompt\command"; ValueType: string; ValueName: ""; \
    ValueData: "{cmd} /K ""set PATH={app};%PATH% && cd /d ""%~dp1"""""; Tasks: associate

; Right-clicking a folder, or its empty background, opens a console
; already sitting in that directory. This is the one people reach for
; most and it costs two keys.
Root: HKA; Subkey: "Software\Classes\Directory\shell\scandiacusprompt"; ValueType: string; ValueName: "MUIVerb"; \
    ValueData: "Open Scandiacus prompt here"; Flags: uninsdeletekey; Tasks: associate
Root: HKA; Subkey: "Software\Classes\Directory\shell\scandiacusprompt"; ValueType: string; ValueName: "Icon"; \
    ValueData: "{app}\scandiacus.ico"; Tasks: associate
Root: HKA; Subkey: "Software\Classes\Directory\shell\scandiacusprompt\command"; ValueType: string; ValueName: ""; \
    ValueData: "{cmd} /K ""set PATH={app};%PATH% && cd /d ""%1"""""; Tasks: associate
Root: HKA; Subkey: "Software\Classes\Directory\Background\shell\scandiacusprompt"; ValueType: string; ValueName: "MUIVerb"; \
    ValueData: "Open Scandiacus prompt here"; Flags: uninsdeletekey; Tasks: associate
Root: HKA; Subkey: "Software\Classes\Directory\Background\shell\scandiacusprompt"; ValueType: string; ValueName: "Icon"; \
    ValueData: "{app}\scandiacus.ico"; Tasks: associate
Root: HKA; Subkey: "Software\Classes\Directory\Background\shell\scandiacusprompt\command"; ValueType: string; ValueName: ""; \
    ValueData: "{cmd} /K ""set PATH={app};%PATH% && cd /d ""%V"""""; Tasks: associate

[Run]
; As the user who ran setup, not the administrator it elevated to, since
; VS Code's extensions live in that user's profile.
Filename: "{app}\{#AppExeName}"; Parameters: "vscode"; \
    StatusMsg: "Installing the Scandiacus extension into VS Code..."; \
    Components: editors\vscode; Flags: runhidden runasoriginaluser waituntilterminated

Filename: "{app}\{#AppExeName}"; Parameters: "cross install --dir ""{app}\zig""";     StatusMsg: "Downloading the cross compiling toolchain (about 80 MB)...";     Components: cross; Flags: runhidden waituntilterminated

[UninstallRun]
Filename: "{app}\{#AppExeName}"; Parameters: "vscode --uninstall"; \
    Components: editors\vscode; Flags: runhidden waituntilterminated; RunOnceId: "ScandiacusVSCode"

[UninstallDelete]
Type: filesandordirs; Name: "{app}\zig"
Type: dirifempty; Name: "{app}\editors\vscode\syntaxes"
Type: dirifempty; Name: "{app}\editors\vscode\icons"
Type: dirifempty; Name: "{app}\editors\vscode"
Type: dirifempty; Name: "{app}\editors"
Type: dirifempty; Name: "{app}\examples\net"
Type: dirifempty; Name: "{app}\examples\gui"
Type: dirifempty; Name: "{app}\examples"
Type: dirifempty; Name: "{app}"

[Code]
{ Where PATH lives depends on how setup was run. The check and the
  removal both have to agree with the [Registry] section, or setup
  edits one hive and uninstall looks in the other. }
function PathHive: Integer;
begin
  if IsAdminInstallMode then
    Result := HKEY_LOCAL_MACHINE
  else
    Result := HKEY_CURRENT_USER;
end;

function PathKey: string;
begin
  if IsAdminInstallMode then
    Result := 'SYSTEM\CurrentControlSet\Control\Session Manager\Environment'
  else
    Result := 'Environment';
end;

{ Only append if it is not already there, so reinstalling does not grow
  PATH without bound. }
function NeedsPathEntry(Dir: string): Boolean;
var
  Existing: string;
begin
  if not RegQueryStringValue(PathHive, PathKey, 'Path', Existing) then
  begin
    Result := True;
    exit;
  end;
  Result := Pos(';' + Uppercase(Dir) + ';', ';' + Uppercase(Existing) + ';') = 0;
end;

{ Uninstalling has to take the PATH entry back out. Inno deletes values
  it created outright, but this is an edit to a value that already
  existed and belongs to the user, so removing it means rewriting the
  string without our segment. Leaving it would mean every install and
  remove cycle grows PATH by one dead directory. }
procedure RemoveFromPath(Dir: string);
var
  Existing, Rebuilt, Segment: string;
  P: Integer;
begin
  if not RegQueryStringValue(PathHive, PathKey, 'Path', Existing) then
    exit;

  Rebuilt := '';
  Existing := Existing + ';';
  repeat
    P := Pos(';', Existing);
    Segment := Trim(Copy(Existing, 1, P - 1));
    Delete(Existing, 1, P);
    if (Segment <> '') and (CompareText(Segment, Dir) <> 0) then
    begin
      if Rebuilt <> '' then
        Rebuilt := Rebuilt + ';';
      Rebuilt := Rebuilt + Segment;
    end;
  until Existing = '';

  if Rebuilt = '' then
    RegDeleteValue(PathHive, PathKey, 'Path')
  else
    RegWriteExpandStringValue(PathHive, PathKey, 'Path', Rebuilt);
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
begin
  if CurUninstallStep = usPostUninstall then
    RemoveFromPath(ExpandConstant('{app}'));
end;

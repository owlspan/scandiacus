; Inno Setup script for Veyl.
;
; One exe, a PATH entry, and the file associations. There is no
; toolchain to bundle: veyl.exe encodes, links and writes the PE by
; itself, which is why this installer is 5 MB and the old backend's was
; 90.
;
; Build it with installer\build.ps1 rather than calling iscc directly,
; so veyl.exe is built and stamped first:
;
;     powershell -ExecutionPolicy Bypass -File installer\build.ps1

#define AppName "Veyl"
#define ShortName "veyl"
#define AppVersion "0.48.0"
#define AppPublisher "Veyl"
#define AppExeName "veyl.exe"
; Has to match editors/vscode/package.json, because the folder name VS
; Code expects embeds it. build.ps1 checks both against veyl.exe.
#define ExtVersion "0.48.0"

[Setup]
AppId={{B7D42A19-3E6C-4F80-A5D3-91C7E20B4F68}
AppName={#AppName}
AppVersion={#AppVersion}
AppPublisher={#AppPublisher}
DefaultDirName={autopf}\Veyl
DefaultGroupName=Veyl
DisableProgramGroupPage=yes
OutputDir=..\dist
OutputBaseFilename=veyl-{#AppVersion}-setup
; Without this Inno does not broadcast WM_SETTINGCHANGE after editing
; the environment, so Explorer keeps handing every new terminal the old
; PATH until the next sign-out. The entry was written correctly and
; still looked like it had done nothing, which is exactly how this was
; reported.
ChangesEnvironment=yes
; The shell caches file associations, so a new .vl icon and the verbs
; below do not appear until it is told. Same class of problem.
ChangesAssociations=yes
Compression=lzma2/max
SolidCompression=yes
WizardStyle=modern
SetupIconFile=..\..\icons\veyl.ico
UninstallDisplayIcon={app}\{#AppExeName}
AppSupportURL=https://github.com/owlspan/veyl
AppUpdatesURL=https://github.com/owlspan/veyl/releases
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
Name: "core"; Description: "The Veyl compiler"; Types: full compact custom; Flags: fixed
Name: "docs"; Description: "Examples and documentation"; Types: full custom

; Editor syntax highlighting. Each writes into that editor's own config
; folder, so ticking one for an editor you do not have is harmless: the
; folder is created and simply never read.
Name: "editors"; Description: "Editor syntax highlighting"; Types: full custom
Name: "editors\vscode"; Description: "Visual Studio Code"; Types: full custom
Name: "editors\npp"; Description: "Notepad++"; Types: full custom
Name: "editors\sublime"; Description: "Sublime Text"; Types: custom
Name: "editors\vim"; Description: "Vim and Neovim"; Types: custom

[Tasks]
Name: "addtopath"; Description: "Add Veyl to PATH so 'veyl' works in any terminal"; \
    GroupDescription: "Setup:"
Name: "associate"; Description: "Associate .vl files, with an icon and right-click Run and Compile"; \
    GroupDescription: "Setup:"
Name: "prompt"; Description: "Add a ""Veyl prompt"" shortcut (a terminal with Veyl already on PATH)"; \
    GroupDescription: "Setup:"
Name: "desktopicon"; Description: "Put that shortcut on the Desktop too"; \
    GroupDescription: "Setup:"; Flags: unchecked

[Files]
Source: "..\veyl.exe";           DestDir: "{app}"; Components: core; Flags: ignoreversion
Source: "..\..\icons\veyl.ico";     DestDir: "{app}"; Components: core; Flags: ignoreversion
Source: "..\..\README.md";          DestDir: "{app}"; Components: core; Flags: ignoreversion

Source: "..\..\docs\SYNTAX.md";   DestDir: "{app}"; Components: docs; Flags: ignoreversion
Source: "..\..\docs\TUTORIAL.md"; DestDir: "{app}"; Components: docs; Flags: ignoreversion
Source: "..\examples\*.vl";       DestDir: "{app}\examples"; Components: docs; Flags: ignoreversion
Source: "..\examples\net\*.vl";   DestDir: "{app}\examples\net"; Components: docs; Flags: ignoreversion
Source: "..\examples\gui\*.vl";   DestDir: "{app}\examples\gui"; Components: docs; Flags: ignoreversion
Source: "..\examples\mod\*.vl";   DestDir: "{app}\examples\mod"; Components: docs; Flags: ignoreversion
Source: "..\examples\ffi\*.vl";   DestDir: "{app}\examples\ffi"; Components: docs; Flags: ignoreversion

; Dropped straight into the extensions folder. VS Code scans that
; directory at startup, so there is no marketplace step and nothing to
; sideload by hand.
;
; The folder name is not decoration. VS Code expects
; publisher.name-version, the same shape it gives its own installs. A
; plain "veyl-lang" is treated as something it did not put there, and
; the id it derives can end up in the .obsolete file beside the
; extensions folder - anything named there is ignored outright, with no
; highlighting and no error to say why.
Source: "..\..\editors\vscode\*"; \
    DestDir: "{%USERPROFILE}\.vscode\extensions\veyl.veyl-lang-{#ExtVersion}"; \
    Components: editors\vscode; Flags: ignoreversion recursesubdirs createallsubdirs

; Notepad++ reads every XML in userDefineLangs at startup.
Source: "..\..\editors\notepad++\veyl.xml"; \
    DestDir: "{userappdata}\Notepad++\userDefineLangs"; \
    Components: editors\npp; Flags: ignoreversion

; Sublime scans Packages\User.
Source: "..\..\editors\sublime\Veyl.sublime-syntax"; \
    DestDir: "{userappdata}\Sublime Text\Packages\User"; \
    Components: editors\sublime; Flags: ignoreversion

; Vim and Neovim look in different places, so both get a copy rather
; than the installer trying to guess which one is in use.
Source: "..\..\editors\vim\syntax\veyl.vim"; DestDir: "{userdocs}\vimfiles\syntax"; \
    Components: editors\vim; Flags: ignoreversion
Source: "..\..\editors\vim\ftdetect\veyl.vim"; DestDir: "{userdocs}\vimfiles\ftdetect"; \
    Components: editors\vim; Flags: ignoreversion
Source: "..\..\editors\vim\syntax\veyl.vim"; DestDir: "{localappdata}\nvim\syntax"; \
    Components: editors\vim; Flags: ignoreversion
Source: "..\..\editors\vim\ftdetect\veyl.vim"; DestDir: "{localappdata}\nvim\ftdetect"; \
    Components: editors\vim; Flags: ignoreversion

[Icons]
; A terminal with Veyl on PATH whether or not the PATH task was
; ticked, so the install is usable without signing out and back in.
Name: "{group}\Veyl prompt"; Filename: "{cmd}"; \
    Parameters: "/K ""set PATH={app};%PATH% && echo Veyl {#AppVersion} - try: veyl run examples\collatz.vl && cd /d {app}"""; \
    IconFilename: "{app}\veyl.ico"; Tasks: prompt
Name: "{autodesktop}\Veyl prompt"; Filename: "{cmd}"; \
    Parameters: "/K ""set PATH={app};%PATH% && echo Veyl {#AppVersion} - try: veyl run examples\collatz.vl && cd /d {app}"""; \
    IconFilename: "{app}\veyl.ico"; Tasks: desktopicon

Name: "{group}\Examples"; Filename: "{app}\examples"; Components: docs
Name: "{group}\Veyl language reference"; Filename: "{app}\SYNTAX.md"; Components: docs
Name: "{group}\Learn Veyl (tutorial)"; Filename: "{app}\TUTORIAL.md"; Components: docs
Name: "{group}\Uninstall Veyl"; Filename: "{uninstallexe}"

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

; The .vl file type.
;
; This used to hang its verbs off a Veyl.Source class it never created,
; on the reasoning that the Go backend's installer owned the extension
; and two installers should not fight over it. That backend is on its
; own branch now and this is the compiler, so it claims the type - and
; the old arrangement was broken anyway: with nothing creating
; Veyl.Source, the verbs were attached to a class that did not exist
; and no Veyl entry appeared on the menu at all.
Root: HKA; Subkey: "Software\Classes\.vl"; ValueType: string; ValueName: ""; \
    ValueData: "Veyl.Source"; Flags: uninsdeletevalue; Tasks: associate
Root: HKA; Subkey: "Software\Classes\Veyl.Source"; ValueType: string; ValueName: ""; \
    ValueData: "Veyl source file"; Flags: uninsdeletekey; Tasks: associate
Root: HKA; Subkey: "Software\Classes\Veyl.Source\DefaultIcon"; ValueType: string; ValueName: ""; \
    ValueData: "{app}\veyl.ico"; Tasks: associate

; Double-clicking runs it. Through cmd /K so the console stays up: a
; program that prints and exits is otherwise a window that opens and
; closes faster than anyone can read.
Root: HKA; Subkey: "Software\Classes\Veyl.Source\shell"; ValueType: string; ValueName: ""; \
    ValueData: "run"; Tasks: associate

Root: HKA; Subkey: "Software\Classes\Veyl.Source\shell\run"; ValueType: string; ValueName: ""; \
    ValueData: "Run with Veyl"; Tasks: associate
Root: HKA; Subkey: "Software\Classes\Veyl.Source\shell\run"; ValueType: string; ValueName: "Icon"; \
    ValueData: "{app}\veyl.ico"; Tasks: associate
Root: HKA; Subkey: "Software\Classes\Veyl.Source\shell\run\command"; ValueType: string; ValueName: ""; \
    ValueData: "{cmd} /K """"{app}\{#AppExeName}"" run ""%1"""""; Tasks: associate

Root: HKA; Subkey: "Software\Classes\Veyl.Source\shell\build"; ValueType: string; ValueName: ""; \
    ValueData: "Compile to .exe"; Tasks: associate
Root: HKA; Subkey: "Software\Classes\Veyl.Source\shell\build"; ValueType: string; ValueName: "Icon"; \
    ValueData: "{app}\veyl.ico"; Tasks: associate
Root: HKA; Subkey: "Software\Classes\Veyl.Source\shell\build\command"; ValueType: string; ValueName: ""; \
    ValueData: "{cmd} /K """"{app}\{#AppExeName}"" build ""%1"""""; Tasks: associate

; The rest go under one cascading "Veyl" entry rather than scattering
; four more verbs across the menu. Windows builds the submenu from a
; second key named by ExtendedSubCommandsKey, and the parent
; deliberately has no command of its own.
Root: HKA; Subkey: "Software\Classes\Veyl.Source\shell\veyl"; ValueType: string; ValueName: "MUIVerb"; \
    ValueData: "Veyl"; Tasks: associate
Root: HKA; Subkey: "Software\Classes\Veyl.Source\shell\veyl"; ValueType: string; ValueName: "Icon"; \
    ValueData: "{app}\veyl.ico"; Tasks: associate
Root: HKA; Subkey: "Software\Classes\Veyl.Source\shell\veyl"; ValueType: string; \
    ValueName: "ExtendedSubCommandsKey"; \
    ValueData: "Veyl.Source\shell\veyl\sub"; Tasks: associate

Root: HKA; Subkey: "Software\Classes\Veyl.Source\shell\veyl\sub\shell\01asm"; ValueType: string; ValueName: "MUIVerb"; \
    ValueData: "Show the generated assembly"; Tasks: associate
Root: HKA; Subkey: "Software\Classes\Veyl.Source\shell\veyl\sub\shell\01asm\command"; ValueType: string; ValueName: ""; \
    ValueData: "{cmd} /K """"{app}\{#AppExeName}"" asm ""%1"""""; Tasks: associate

Root: HKA; Subkey: "Software\Classes\Veyl.Source\shell\veyl\sub\shell\02ir"; ValueType: string; ValueName: "MUIVerb"; \
    ValueData: "Show the IR"; Tasks: associate
Root: HKA; Subkey: "Software\Classes\Veyl.Source\shell\veyl\sub\shell\02ir\command"; ValueType: string; ValueName: ""; \
    ValueData: "{cmd} /K """"{app}\{#AppExeName}"" ir ""%1"""""; Tasks: associate

Root: HKA; Subkey: "Software\Classes\Veyl.Source\shell\veyl\sub\shell\03prompt"; ValueType: string; ValueName: "MUIVerb"; \
    ValueData: "Open a Veyl prompt here"; Tasks: associate
Root: HKA; Subkey: "Software\Classes\Veyl.Source\shell\veyl\sub\shell\03prompt\command"; ValueType: string; ValueName: ""; \
    ValueData: "{cmd} /K ""set PATH={app};%PATH% && cd /d ""%~dp1"""""; Tasks: associate

; Right-clicking a folder, or its empty background, opens a console
; already sitting in that directory. This is the one people reach for
; most and it costs two keys.
Root: HKA; Subkey: "Software\Classes\Directory\shell\veylprompt"; ValueType: string; ValueName: "MUIVerb"; \
    ValueData: "Open Veyl prompt here"; Flags: uninsdeletekey; Tasks: associate
Root: HKA; Subkey: "Software\Classes\Directory\shell\veylprompt"; ValueType: string; ValueName: "Icon"; \
    ValueData: "{app}\veyl.ico"; Tasks: associate
Root: HKA; Subkey: "Software\Classes\Directory\shell\veylprompt\command"; ValueType: string; ValueName: ""; \
    ValueData: "{cmd} /K ""set PATH={app};%PATH% && cd /d ""%1"""""; Tasks: associate
Root: HKA; Subkey: "Software\Classes\Directory\Background\shell\veylprompt"; ValueType: string; ValueName: "MUIVerb"; \
    ValueData: "Open Veyl prompt here"; Flags: uninsdeletekey; Tasks: associate
Root: HKA; Subkey: "Software\Classes\Directory\Background\shell\veylprompt"; ValueType: string; ValueName: "Icon"; \
    ValueData: "{app}\veyl.ico"; Tasks: associate
Root: HKA; Subkey: "Software\Classes\Directory\Background\shell\veylprompt\command"; ValueType: string; ValueName: ""; \
    ValueData: "{cmd} /K ""set PATH={app};%PATH% && cd /d ""%V"""""; Tasks: associate

[UninstallDelete]
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

; ---------------------------------------------------------------------------
;  Inno Setup script for git-persona.
;
;  Produces a per-user installer: it writes to the user's own program folder
;  and to HKEY_CURRENT_USER, so no UAC elevation and no administrator is
;  needed. That also means PATH changes affect only the installing user,
;  which is the correct scope for a personal developer tool.
;
;  Build:  iscc build\windows\installer.iss
;  Expects git-persona.exe to already exist at the repository root:
;      go build -o git-persona.exe .\cmd\git-persona
; ---------------------------------------------------------------------------

#define AppName        "Git Persona"
#define AppShortName   "git-persona"
#define AppPublisher   "Git Persona"
#define AppURL         "https://github.com/Dkavila/git-persona"
#define AppExeName     "git-persona.exe"
#define AliasName      "gitp.cmd"

; --- Overridable from the command line -------------------------------------
;
; CI passes these so one script serves both a local build and the release
; pipeline. Every define has a default, so a bare
;   iscc build\windows\installer.iss
; still works against a binary compiled into the repository root.
;
;   iscc /DBinaryDir=C:\path\to\dist\installer-input ^
;        /DAppVersion=1.2.3 ^
;        /DOutputDir=C:\path\to\dist\installer ^
;        /DOutputBaseFilename=GitPersona_Installer ^
;        build\windows\installer.iss
;
; BinaryDir is the one that matters: GoReleaser does not write the binary to
; the repository root, it writes it to dist\<id>_windows_amd64_<goamd64>\.
; ---------------------------------------------------------------------------

#ifndef BinaryDir
  #define BinaryDir "..\.."
#endif

#ifndef AppVersion
  #define AppVersion "0.0.0-dev"
#endif

#ifndef OutputDir
  #define OutputDir "..\..\dist\installer"
#endif

#ifndef OutputBaseFilename
  #define OutputBaseFilename "GitPersona_Installer"
#endif

[Setup]
; AppId uniquely identifies this application for upgrades and uninstall.
; Never change it once released, or upgrades will install side by side.
AppId={{7C3A9F14-2B58-4D6E-9A07-51E8C2D4B36F}
AppName={#AppName}
AppVersion={#AppVersion}
AppVerName={#AppName} {#AppVersion}
AppPublisher={#AppPublisher}
AppPublisherURL={#AppURL}
AppSupportURL={#AppURL}/issues
AppUpdatesURL={#AppURL}/releases

; lowest = never request elevation. Combined with {autopf} this resolves to
; %LOCALAPPDATA%\Programs, a directory the user already owns.
PrivilegesRequired=lowest
PrivilegesRequiredOverridesAllowed=dialog

DefaultDirName={autopf}\{#AppName}
DefaultGroupName={#AppName}
; A command line tool has nothing useful to put in the Start menu.
DisableProgramGroupPage=yes
DisableDirPage=no
AllowNoIcons=yes

; Tells Inno to broadcast WM_SETTINGCHANGE after install, so Explorer and any
; newly launched shell pick up the PATH change without a reboot.
ChangesEnvironment=yes

OutputDir={#OutputDir}
OutputBaseFilename={#OutputBaseFilename}
Compression=lzma2/max
SolidCompression=yes
WizardStyle=modern
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
UninstallDisplayName={#AppName}
UninstallDisplayIcon={app}\{#AppExeName}

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"
Name: "brazilianportuguese"; MessagesFile: "compiler:Languages\BrazilianPortuguese.isl"

[Files]
; Paths are relative to this .iss file, which lives in build\windows.
Source: "{#BinaryDir}\{#AppExeName}"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#AliasName}";        DestDir: "{app}"; Flags: ignoreversion
Source: "..\..\README.md";     DestDir: "{app}"; Flags: ignoreversion isreadme

[Registry]
; Append {app} to the per-user PATH.
;
; Two mutually exclusive entries avoid the classic bug of writing a leading
; semicolon when the user has no PATH value of their own: the first handles an
; existing non-empty PATH, the second an absent or empty one. Both are gated on
; NeedsAddPath so re-running the installer cannot duplicate the entry.
;
; expandsz (REG_EXPAND_SZ) is required: PATH commonly contains %USERPROFILE%
; and similar, and rewriting it as a plain string would break those entries for
; every other program.
Root: HKCU; Subkey: "Environment"; ValueType: expandsz; ValueName: "Path"; \
    ValueData: "{olddata};{app}"; \
    Check: NeedsAddPath(ExpandConstant('{app}')) and HasExistingPath

Root: HKCU; Subkey: "Environment"; ValueType: expandsz; ValueName: "Path"; \
    ValueData: "{app}"; \
    Check: NeedsAddPath(ExpandConstant('{app}')) and not HasExistingPath

[Code]

const
  EnvironmentKey = 'Environment';

{ ReadUserPath returns the raw, unexpanded per-user PATH. It must stay
  unexpanded so that writing it back preserves any %VARIABLES% it contains. }
function ReadUserPath(var Value: string): Boolean;
begin
  Result := RegQueryStringValue(HKEY_CURRENT_USER, EnvironmentKey, 'Path', Value);
  if not Result then
    Value := '';
end;

{ HasExistingPath reports whether the user already has a non-empty PATH value.
  Used to pick between the two [Registry] entries above. }
function HasExistingPath: Boolean;
var
  Path: string;
begin
  ReadUserPath(Path);
  Result := Trim(Path) <> '';
end;

{ NeedsAddPath reports whether Param is absent from the per-user PATH.

  The comparison wraps both the needle and the haystack in semicolons, so that
  "C:\Tools\Git Persona" is never considered present merely because
  "C:\Tools\Git Persona Extra" is. Case is folded because Windows paths are
  case insensitive, and a trailing backslash is normalised away because
  "C:\X" and "C:\X\" denote the same directory. }
function NeedsAddPath(Param: string): Boolean;
var
  Path: string;
  Needle: string;
begin
  ReadUserPath(Path);

  Needle := Uppercase(Trim(Param));
  if (Needle <> '') and (Needle[Length(Needle)] = '\') then
    Delete(Needle, Length(Needle), 1);

  Result := Pos(';' + Needle + ';', ';' + Uppercase(Path) + ';') = 0;
end;

{ RemoveFromPath strips Param from the per-user PATH, leaving every other
  entry untouched. Rebuilding the list entry by entry is deliberate: a naive
  string replace would corrupt neighbouring entries that merely contain the
  same substring. }
procedure RemoveFromPath(Param: string);
var
  Path: string;
  Rebuilt: string;
  Entry: string;
  Needle: string;
  P: Integer;
begin
  if not ReadUserPath(Path) then
    exit;

  Needle := Uppercase(Trim(Param));
  if (Needle <> '') and (Needle[Length(Needle)] = '\') then
    Delete(Needle, Length(Needle), 1);

  Rebuilt := '';
  Path := Path + ';';

  repeat
    P := Pos(';', Path);
    Entry := Trim(Copy(Path, 1, P - 1));
    Delete(Path, 1, P);

    if Entry <> '' then
    begin
      if (Entry[Length(Entry)] = '\') and (Length(Entry) > 1) then
        Delete(Entry, Length(Entry), 1);

      if Uppercase(Entry) <> Needle then
      begin
        if Rebuilt <> '' then
          Rebuilt := Rebuilt + ';';
        Rebuilt := Rebuilt + Entry;
      end;
    end;
  until Path = '';

  if Rebuilt = '' then
    RegDeleteValue(HKEY_CURRENT_USER, EnvironmentKey, 'Path')
  else
    RegWriteExpandStringValue(HKEY_CURRENT_USER, EnvironmentKey, 'Path', Rebuilt);
end;

{ Uninstall must leave PATH as it found it. Without this the machine collects
  dead PATH entries with every install and uninstall cycle. }
procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
begin
  if CurUninstallStep = usPostUninstall then
    RemoveFromPath(ExpandConstant('{app}'));
end;

[Run]
Filename: "{app}\{#AppExeName}"; Parameters: "--version"; \
    Description: "Verify the installation"; \
    Flags: postinstall runhidden skipifsilent

[Messages]
FinishedLabel=Setup has installed [name] on your computer.%n%nOpen a NEW terminal and run "git-persona --help" or the short alias "gitp --help". An already open terminal will not see the updated PATH.

; Bokkie for Windows — the step-by-step installer.
;
; Built by `make installer` (makensis runs on macOS and Linux too). It wraps the
; one Windows binary; everything the program itself knows — the old service from
; 2.6, the old data folder — it is asked through `bokkie installer-hook`.
;
; What it installs, and what it must never install:
;   - the program in Program Files, a Start menu entry, optionally a desktop
;     shortcut, an Add/Remove Programs entry and an inbound firewall rule for
;     the program on private and domain networks;
;   - NOTHING that starts Bokkie without a person: no service, no Run key, no
;     Startup shortcut, no scheduled task. See D1 in
;     docs/plans/02.7-window-is-the-server.md, which still holds.
;
; Defines expected from the command line:
;   VERSION     the release tag, e.g. v0.3.0
;   VIVERSION   the same as four numbers, e.g. 0.3.0.0
;   SOURCE      path to bokkie-win-amd64.exe
;   OUTFILE     where to write the installer

Unicode true
ManifestDPIAware true
SetCompressor /SOLID lzma

!ifndef VERSION
  !define VERSION "dev"
!endif
!ifndef VIVERSION
  !define VIVERSION "0.0.0.0"
!endif
!ifndef SOURCE
  !error "SOURCE must name the Windows binary"
!endif
!ifndef OUTFILE
  !define OUTFILE "BokkieSetup.exe"
!endif

!define APP        "Bokkie"
!define PUBLISHER  "TLS Palay and Corn"
!define ARP        "Software\Microsoft\Windows\CurrentVersion\Uninstall\Bokkie"
!define WINCLASS   "BokkieStationWindow"
!define FWRULE     "Bokkie LMS"
!define DEFAULTURL "http://bokkie.local:8080"

!include "MUI2.nsh"
!include "LogicLib.nsh"
!include "nsDialogs.nsh"
!include "x64.nsh"
!include "FileFunc.nsh"

Name "${APP}"
Caption "${APP} ${VERSION} Setup"
OutFile "${OUTFILE}"
InstallDir "$PROGRAMFILES64\Bokkie"
InstallDirRegKey HKLM "${ARP}" "InstallLocation"
RequestExecutionLevel admin
BrandingText "${APP} ${VERSION}"

VIProductVersion "${VIVERSION}"
VIAddVersionKey "ProductName" "${APP}"
VIAddVersionKey "ProductVersion" "${VERSION}"
VIAddVersionKey "FileVersion" "${VERSION}"
VIAddVersionKey "CompanyName" "${PUBLISHER}"
VIAddVersionKey "FileDescription" "${APP} Setup"
VIAddVersionKey "LegalCopyright" "${PUBLISHER}"

!define MUI_ICON   "bokkie.ico"
!define MUI_UNICON "bokkie.ico"
!define MUI_ABORTWARNING
!define MUI_COMPONENTSPAGE_SMALLDESC

; ---------------------------------------------------------------- pages

Var FinishText
Var CounterAddress
Var CounterField
Var WasProgram
Var WasCounter

!define MUI_WELCOMEPAGE_TITLE "Install Bokkie ${VERSION}"
!define MUI_WELCOMEPAGE_TEXT "This will set up Bokkie, the TLS Palay and Corn lending system, on this computer.$\r$\n$\r$\n\
On the STATION computer — the one that keeps the records — choose $\"Station computer$\". Bokkie runs while its window is open, and closing the window stops it for every computer at the station. It never starts by itself.$\r$\n$\r$\n\
On a COUNTER computer choose $\"Counter PC$\". That only puts a shortcut to the station on the desktop; nothing else is installed.$\r$\n$\r$\n\
If Bokkie is already installed, this updates it. Your records are kept exactly where they are."
!insertmacro MUI_PAGE_WELCOME

!insertmacro MUI_PAGE_COMPONENTS

!define MUI_PAGE_CUSTOMFUNCTION_PRE SkipUnlessProgram
!insertmacro MUI_PAGE_DIRECTORY

Page custom CounterPage CounterPageLeave

!insertmacro MUI_PAGE_INSTFILES

!define MUI_FINISHPAGE_TITLE "Bokkie is installed"
!define MUI_FINISHPAGE_TEXT "$FinishText"
!define MUI_FINISHPAGE_RUN
!define MUI_FINISHPAGE_RUN_TEXT "Open Bokkie now"
!define MUI_FINISHPAGE_RUN_FUNCTION OpenBokkie
!define MUI_PAGE_CUSTOMFUNCTION_SHOW FinishShow
!insertmacro MUI_PAGE_FINISH

!define MUI_UNCONFIRMPAGE_TEXT_TOP "Bokkie will be removed from this computer.$\r$\n$\r$\n\
The records and every backup are NOT removed — they stay in the data folder (usually %LOCALAPPDATA%\Bokkie), so installing Bokkie again opens them as they were."
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES

!insertmacro MUI_LANGUAGE "English"

; ---------------------------------------------------------------- types and sections

InstType "Station computer (keeps the records and runs Bokkie)"
InstType "Counter PC (a shortcut to the station only)"

Section "Bokkie program" SecProgram
  SectionIn 1
  SetShellVarContext all

  ; The older Bokkie registered a Windows service that holds bokkie.exe open
  ; and starts the station on every restart. It has to go before the file can
  ; be replaced, and it is asked of the new program, which knows its name.
  DetailPrint "Looking for an older Bokkie service…"
  InitPluginsDir
  SetOutPath "$PLUGINSDIR"
  File "/oname=bokkie-new.exe" "${SOURCE}"
  nsExec::ExecToLog '"$PLUGINSDIR\bokkie-new.exe" installer-hook prepare'
  Pop $0
  ${If} $0 != 0
    MessageBox MB_OK|MB_ICONEXCLAMATION "An older Bokkie service could not be removed, so this computer may still start Bokkie by itself after a restart.$\r$\n$\r$\nThe installation will continue. Send the developer the list of details on the next screen."
  ${EndIf}

  SetOutPath "$INSTDIR"
  SetOverwrite on
  ClearErrors
  File "/oname=bokkie.exe" "${SOURCE}"
  ${If} ${Errors}
    Abort "Could not replace $INSTDIR\bokkie.exe. Close Bokkie and run this installer again."
  ${EndIf}
  WriteUninstaller "$INSTDIR\Uninstall.exe"

  ; Start menu. SW_SHOWMINIMIZED because bokkie.exe is a console program as
  ; well as a window: the console it is given is hidden at once, and starting
  ; it minimised means it is never seen at all.
  CreateShortcut "$SMPROGRAMS\Bokkie.lnk" "$INSTDIR\bokkie.exe" "serve" "$INSTDIR\bokkie.exe" 0 SW_SHOWMINIMIZED "" "Open the Bokkie station"

  ; Let the counter PCs in. By program rather than by port, so a station moved
  ; to another port in its settings is still reachable, and port 80's redirect
  ; with it. Private and domain only: never a public network.
  DetailPrint "Allowing the counter computers through Windows Firewall…"
  nsExec::ExecToLog 'netsh advfirewall firewall delete rule name="${FWRULE}"'
  Pop $0
  nsExec::ExecToLog 'netsh advfirewall firewall add rule name="${FWRULE}" dir=in action=allow program="$INSTDIR\bokkie.exe" enable=yes profile=private,domain'
  Pop $0
  ${If} $0 != 0
    DetailPrint "The firewall rule could not be added; Windows will ask the first time Bokkie opens."
  ${EndIf}

  ; Records an older Bokkie left in C:\ProgramData\Bokkie were made for a
  ; service account; give the people who use this computer access to them.
  nsExec::ExecToLog '"$INSTDIR\bokkie.exe" installer-hook finish'
  Pop $0

  ; Add/Remove Programs.
  WriteRegStr HKLM "${ARP}" "DisplayName" "${APP}"
  WriteRegStr HKLM "${ARP}" "DisplayVersion" "${VERSION}"
  WriteRegStr HKLM "${ARP}" "Publisher" "${PUBLISHER}"
  WriteRegStr HKLM "${ARP}" "InstallLocation" "$INSTDIR"
  WriteRegStr HKLM "${ARP}" "DisplayIcon" "$INSTDIR\bokkie.exe"
  WriteRegStr HKLM "${ARP}" "UninstallString" '"$INSTDIR\Uninstall.exe"'
  WriteRegStr HKLM "${ARP}" "QuietUninstallString" '"$INSTDIR\Uninstall.exe" /S'
  WriteRegDWORD HKLM "${ARP}" "NoModify" 1
  WriteRegDWORD HKLM "${ARP}" "NoRepair" 1
  ${GetSize} "$INSTDIR" "/S=0K" $0 $1 $2
  IntFmt $0 "0x%08X" $0
  WriteRegDWORD HKLM "${ARP}" "EstimatedSize" "$0"
SectionEnd

Section "Desktop shortcut" SecDesktop
  SectionIn 1
  SetShellVarContext all
  CreateShortcut "$DESKTOP\Bokkie.lnk" "$INSTDIR\bokkie.exe" "serve" "$INSTDIR\bokkie.exe" 0 SW_SHOWMINIMIZED "" "Open the Bokkie station"
SectionEnd

Section /o "Counter PC shortcut" SecCounter
  SectionIn 2
  SetShellVarContext all
  ; Edge in application mode — no address bar, no tabs — as the counter PCs
  ; have always had. A plain web link when there is no Edge.
  StrCpy $0 "$PROGRAMFILES32\Microsoft\Edge\Application\msedge.exe"
  ${IfNot} ${FileExists} "$0"
    StrCpy $0 "$PROGRAMFILES64\Microsoft\Edge\Application\msedge.exe"
  ${EndIf}
  ${If} ${FileExists} "$0"
    CreateShortcut "$DESKTOP\Bokkie.lnk" "$0" "--app=$CounterAddress" "$0" 0 SW_SHOWNORMAL "" "Bokkie at the station"
  ${Else}
    WriteINIStr "$DESKTOP\Bokkie.url" "InternetShortcut" "URL" "$CounterAddress"
  ${EndIf}
  DetailPrint "Desktop shortcut to $CounterAddress"
SectionEnd

LangString DESC_Program ${LANG_ENGLISH} "The Bokkie program, a Start menu entry, and a firewall rule so the counter computers can reach it. For the station computer."
LangString DESC_Desktop ${LANG_ENGLISH} "A Bokkie icon on the desktop that opens the station."
LangString DESC_Counter ${LANG_ENGLISH} "Only a desktop shortcut to the station's address, for a counter computer. Installs no program."
!insertmacro MUI_FUNCTION_DESCRIPTION_BEGIN
  !insertmacro MUI_DESCRIPTION_TEXT ${SecProgram} $(DESC_Program)
  !insertmacro MUI_DESCRIPTION_TEXT ${SecDesktop} $(DESC_Desktop)
  !insertmacro MUI_DESCRIPTION_TEXT ${SecCounter} $(DESC_Counter)
!insertmacro MUI_FUNCTION_DESCRIPTION_END

; ---------------------------------------------------------------- functions

!macro WaitForStationClosed
  ; Replacing or removing bokkie.exe while the station is open would fail on
  ; a locked file at best. Closing it is the person's call: it stops the
  ; station for every counter.
  ${Do}
    FindWindow $0 "${WINCLASS}"
    ${If} $0 == 0
      ${Break}
    ${EndIf}
    MessageBox MB_RETRYCANCEL|MB_ICONEXCLAMATION "Bokkie is open on this computer.$\r$\n$\r$\nClose the Bokkie window first (this stops it for the counter computers too), then press Retry." IDRETRY +2
    Abort
  ${Loop}
!macroend

Function .onInit
  ${IfNot} ${RunningX64}
    MessageBox MB_OK|MB_ICONSTOP "Bokkie needs a 64-bit Windows."
    Abort
  ${EndIf}
  SetRegView 64
  StrCpy $CounterAddress "${DEFAULTURL}"
  StrCpy $WasProgram 1
  StrCpy $WasCounter 0
  !insertmacro WaitForStationClosed
FunctionEnd

; The station and the counter shortcut are two different machines. Choosing
; one clears the other, so a counter PC cannot become a second station by a
; stray tick — a second station has its own, empty, ledger.
Function .onSelChange
  SectionGetFlags ${SecProgram} $0
  IntOp $0 $0 & ${SF_SELECTED}
  SectionGetFlags ${SecCounter} $1
  IntOp $1 $1 & ${SF_SELECTED}

  ${If} $1 <> 0
  ${AndIf} $WasCounter = 0
    !insertmacro UnselectSection ${SecProgram}
    !insertmacro UnselectSection ${SecDesktop}
    StrCpy $0 0
  ${ElseIf} $0 <> 0
  ${AndIf} $WasProgram = 0
    !insertmacro UnselectSection ${SecCounter}
    StrCpy $1 0
  ${EndIf}

  ; The desktop shortcut opens the program, so it means nothing without it.
  ${If} $0 = 0
    !insertmacro UnselectSection ${SecDesktop}
  ${EndIf}
  StrCpy $WasProgram $0
  StrCpy $WasCounter $1
FunctionEnd

Function SkipUnlessProgram
  ${IfNot} ${SectionIsSelected} ${SecProgram}
    Abort
  ${EndIf}
FunctionEnd

Function CounterPage
  ${IfNot} ${SectionIsSelected} ${SecCounter}
    Abort
  ${EndIf}
  !insertmacro MUI_HEADER_TEXT "The station's address" "Where this counter computer finds Bokkie."
  nsDialogs::Create 1018
  Pop $0
  ${NSD_CreateLabel} 0 0 100% 48u "Type the address shown in the Bokkie window on the station computer, under $\"Other computers$\".$\r$\n$\r$\nIf the name does not work on this computer, use the number address shown below it instead."
  Pop $0
  ${NSD_CreateText} 0 56u 100% 13u "$CounterAddress"
  Pop $CounterField
  nsDialogs::Show
FunctionEnd

Function CounterPageLeave
  ${NSD_GetText} $CounterField $CounterAddress
  StrCpy $0 $CounterAddress 7
  StrCpy $1 $CounterAddress 8
  ${If} $0 != "http://"
  ${AndIf} $1 != "https://"
    StrCpy $CounterAddress "http://$CounterAddress"
  ${EndIf}
  ${If} $CounterAddress == "http://"
    MessageBox MB_OK|MB_ICONEXCLAMATION "Type the station's address."
    Abort
  ${EndIf}
FunctionEnd

Function FinishShow
  ${If} ${SectionIsSelected} ${SecProgram}
    StrCpy $FinishText "Open Bokkie from the Start menu or the desktop. A window shows whether it is running and the address the counter computers use.$\r$\n$\r$\nClosing that window stops Bokkie. After a restart nothing opens it by itself — open it again each morning."
  ${Else}
    StrCpy $FinishText "The Bokkie shortcut is on the desktop. It opens $CounterAddress.$\r$\n$\r$\nIf it says the page cannot be reached, Bokkie is not open on the station computer, or this computer is not on the station's network."
    ShowWindow $mui.FinishPage.Run 0
    ${NSD_Uncheck} $mui.FinishPage.Run
  ${EndIf}
  ${NSD_SetText} $mui.FinishPage.Text "$FinishText"
FunctionEnd

Function OpenBokkie
  ${If} ${SectionIsSelected} ${SecProgram}
    ; Through Explorer, so Bokkie opens as the person and not as the
    ; administrator who approved this installer: the records belong to that
    ; person's account.
    Exec '"$WINDIR\explorer.exe" "$INSTDIR\bokkie.exe"'
  ${EndIf}
FunctionEnd

; ---------------------------------------------------------------- uninstall

Function un.onInit
  SetRegView 64
  !insertmacro WaitForStationClosed
FunctionEnd

Section "Uninstall"
  SetShellVarContext all
  nsExec::ExecToLog 'netsh advfirewall firewall delete rule name="${FWRULE}"'
  Pop $0

  Delete "$SMPROGRAMS\Bokkie.lnk"
  Delete "$DESKTOP\Bokkie.lnk"
  Delete "$INSTDIR\bokkie.exe"
  ; What `bokkie update` leaves beside the program while it swaps it.
  Delete "$INSTDIR\bokkie.exe.old"
  Delete "$INSTDIR\bokkie.exe.new"
  Delete "$INSTDIR\Uninstall.exe"
  RMDir "$INSTDIR"

  DeleteRegKey HKLM "${ARP}"
  DetailPrint "The records and backups were left in place."
SectionEnd

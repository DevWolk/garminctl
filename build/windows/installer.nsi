Unicode true
!include "MUI2.nsh"
!include "x64.nsh"
!include "WinVer.nsh"
!include "LogicLib.nsh"

Name "garminctl"
VIProductVersion "${PRODUCT_VERSION}"
VIAddVersionKey "ProductName" "garminctl"
VIAddVersionKey "FileDescription" "garminctl Setup"
VIAddVersionKey "CompanyName" "DevWolk"
VIAddVersionKey "FileVersion" "${VERSION}"
VIAddVersionKey "ProductVersion" "${VERSION}"
VIAddVersionKey "LegalCopyright" "garminctl contributors"
!uninstfinalize 'bash "../../scripts/sign-windows.sh" "%1"' = 0
OutFile "../../dist/garminctl_${VERSION}_windows_amd64_setup.exe"
InstallDir "$LOCALAPPDATA\Programs\garminctl"
InstallDirRegKey HKCU "Software\garminctl" "InstallDir"
RequestExecutionLevel user
SetCompressor /SOLID lzma
Icon "../../bin/garminctl.ico"
UninstallIcon "../../bin/garminctl.ico"
!define MUI_ABORTWARNING
!define MUI_FINISHPAGE_RUN "$INSTDIR\garmin-desktop.exe"
!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "English"

Function .onInit
  SetRegView 64
  ${IfNot} ${AtLeastWin10}
    MessageBox MB_ICONSTOP "garminctl requires Windows 10 or newer."
    Abort
  ${EndIf}
  ${IfNot} ${IsNativeAMD64}
    MessageBox MB_ICONSTOP "This installer requires 64-bit Intel/AMD Windows."
    Abort
  ${EndIf}
FunctionEnd

Section "garminctl"
  SetShellVarContext current
  SetRegView 64
  ReadRegStr $0 HKLM "SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}" "pv"
  ReadRegStr $1 HKCU "Software\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}" "pv"
  ${If} $0 == ""
  ${AndIf} $1 == ""
    InitPluginsDir
    SetOutPath "$PLUGINSDIR"
    File "../../.cache/MicrosoftEdgeWebview2Setup.exe"
    DetailPrint "Installing Microsoft WebView2 Runtime (internet connection required)..."
    ExecWait '"$PLUGINSDIR\MicrosoftEdgeWebview2Setup.exe" /silent /install' $0
    ${If} $0 != 0
      MessageBox MB_ICONSTOP "WebView2 installation failed (code $0). Check your internet connection and run setup again."
      Abort
    ${EndIf}
  ${EndIf}
  SetOutPath "$INSTDIR"
  File "../../bin/garmin-desktop.exe"
  File "../../bin/garminctl.exe"
  File "../../LICENSE"
  WriteUninstaller "$INSTDIR\uninstall.exe"
  WriteRegStr HKCU "Software\garminctl" "InstallDir" "$INSTDIR"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\garminctl-desktop" "DisplayName" "garminctl Desktop"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\garminctl-desktop" "DisplayVersion" "${VERSION}"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\garminctl-desktop" "Publisher" "DevWolk"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\garminctl-desktop" "DisplayIcon" "$INSTDIR\garmin-desktop.exe"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\garminctl-desktop" "UninstallString" '$\"$INSTDIR\uninstall.exe$\"'
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\garminctl-desktop" "QuietUninstallString" '$\"$INSTDIR\uninstall.exe$\" /S'
  CreateShortcut "$SMPROGRAMS\garminctl.lnk" "$INSTDIR\garmin-desktop.exe"
  CreateShortcut "$DESKTOP\garminctl.lnk" "$INSTDIR\garmin-desktop.exe"
SectionEnd

Section "Uninstall"
  SetShellVarContext current
  SetRegView 64
  Delete "$SMPROGRAMS\garminctl.lnk"
  Delete "$DESKTOP\garminctl.lnk"
  ; "garminctl autostart on" adds a sign-in shortcut and its own CLI copy.
  ; Caches in the same directory stay.
  Delete "$SMSTARTUP\garminctl.lnk"
  Delete "$LOCALAPPDATA\garminctl\garminctl.exe"
  Delete "$INSTDIR\garmin-desktop.exe"
  Delete "$INSTDIR\garminctl.exe"
  Delete "$INSTDIR\LICENSE"
  Delete "$INSTDIR\uninstall.exe"
  RMDir "$INSTDIR"
  DeleteRegKey HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\garminctl-desktop"
  DeleteRegKey HKCU "Software\garminctl"
SectionEnd

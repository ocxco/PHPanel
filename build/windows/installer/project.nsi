Unicode true
RequestExecutionLevel user
ManifestDPIAware true
SetCompressor /SOLID lzma
!include "MUI2.nsh"
!include "x64.nsh"
!include "WinVer.nsh"
!include "LogicLib.nsh"

!ifndef PACKAGE_DIR
!error "PACKAGE_DIR is required"
!endif
!ifndef GUARD_EXE
!error "GUARD_EXE is required"
!endif
!ifndef WEBVIEW_INSTALLER
!error "WEBVIEW_INSTALLER is required"
!endif
!ifndef OUTPUT_FILE
!error "OUTPUT_FILE is required"
!endif
!ifndef PRODUCT_VERSION
!define PRODUCT_VERSION "0.1.0"
!endif

Name "PHPanel"
OutFile "${OUTPUT_FILE}"
InstallDir "$LOCALAPPDATA\Programs\PHPanel"
InstallDirRegKey HKCU "Software\PHPanel\Installer" "InstallDir"
VIProductVersion "${PRODUCT_VERSION}.0"
VIAddVersionKey "ProductName" "PHPanel"
VIAddVersionKey "CompanyName" "PHPanel"
VIAddVersionKey "FileDescription" "PHPanel Windows Installer"
VIAddVersionKey "FileVersion" "${PRODUCT_VERSION}"
VIAddVersionKey "LegalCopyright" ""
!define MUI_ICON "..\icon.ico"
!define MUI_UNICON "..\icon.ico"
!define MUI_ABORTWARNING
!define MUI_FINISHPAGE_NOAUTOCLOSE
!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "SimpChinese"
!insertmacro MUI_LANGUAGE "English"

Function .onInit
  SetShellVarContext current
  SetRegView 64
  ${IfNot} ${AtLeastWin10}
    MessageBox MB_ICONSTOP "PHPanel 需要 Windows 10/11 x64。"
    Abort
  ${EndIf}
  ${IfNot} ${IsNativeAMD64}
    MessageBox MB_ICONSTOP "此安装包仅支持 Windows x64。"
    Abort
  ${EndIf}
FunctionEnd

Function CheckInstalledFiles
  InitPluginsDir
  SetOutPath "$PLUGINSDIR"
  File /oname=package-guard.exe "${GUARD_EXE}"
  nsExec::ExecToStack '"$PLUGINSDIR\package-guard.exe" --root "$INSTDIR" --mode update'
  Pop $0
  Pop $1
  ${If} $0 != 0
    MessageBox MB_ICONSTOP "$1"
    Abort
  ${EndIf}
FunctionEnd

Function InstallWebView2
  SetRegView 32
  ReadRegStr $0 HKLM "Software\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}" "pv"
  ${If} $0 == ""
    ReadRegStr $0 HKCU "Software\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}" "pv"
  ${EndIf}
  SetRegView 64
  ${If} $0 != ""
    Return
  ${EndIf}
  InitPluginsDir
  SetOutPath "$PLUGINSDIR"
  File /oname=WebView2Setup.exe "${WEBVIEW_INSTALLER}"
  DetailPrint "正在安装 Microsoft WebView2 Runtime..."
  ExecWait '"$PLUGINSDIR\WebView2Setup.exe" /silent /install' $0
  ${If} $0 != 0
  ${AndIf} $0 != 3010
    MessageBox MB_ICONSTOP "WebView2 安装失败（错误 $0）。请检查网络或安装 WebView2 后重新运行。"
    Abort
  ${EndIf}
FunctionEnd

Section "PHPanel"
  Call CheckInstalledFiles
  Call InstallWebView2
  SetOutPath "$INSTDIR"
  File "${PACKAGE_DIR}\PHPanel.exe"
  File "${PACKAGE_DIR}\phpanel-cli.exe"
  File "${PACKAGE_DIR}\phpanel-service.exe"
  File "${PACKAGE_DIR}\README.md"
  File /r "${PACKAGE_DIR}\docs"
  ; Never copy the developer's state, packages, instances or databases.
  Delete "$INSTDIR\portable.flag"
  CreateDirectory "$SMPROGRAMS\PHPanel"
  CreateShortcut "$SMPROGRAMS\PHPanel\PHPanel.lnk" "$INSTDIR\PHPanel.exe"
  CreateShortcut "$DESKTOP\PHPanel.lnk" "$INSTDIR\PHPanel.exe"
  WriteUninstaller "$INSTDIR\uninstall.exe"
  WriteRegStr HKCU "Software\PHPanel\Installer" "InstallDir" "$INSTDIR"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\PHPanel" "DisplayName" "PHPanel"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\PHPanel" "DisplayVersion" "${PRODUCT_VERSION}"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\PHPanel" "Publisher" "PHPanel"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\PHPanel" "DisplayIcon" "$INSTDIR\PHPanel.exe"
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\PHPanel" "UninstallString" '$\"$INSTDIR\uninstall.exe$\"'
  WriteRegDWORD HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\PHPanel" "NoModify" 1
  WriteRegDWORD HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\PHPanel" "NoRepair" 1
SectionEnd

Function un.onInit
  SetShellVarContext current
  SetRegView 64
FunctionEnd

Section "Uninstall"
  InitPluginsDir
  SetOutPath "$PLUGINSDIR"
  File /oname=package-guard.exe "${GUARD_EXE}"
  nsExec::ExecToStack '"$PLUGINSDIR\package-guard.exe" --root "$INSTDIR" --mode uninstall --cleanup'
  Pop $0
  Pop $1
  ${If} $0 != 0
    MessageBox MB_ICONSTOP "$1"
    Abort
  ${EndIf}
  ; Delete only named application files. Retain environment and database data.
  Delete "$INSTDIR\PHPanel.exe"
  Delete "$INSTDIR\phpanel-cli.exe"
  Delete "$INSTDIR\phpanel-service.exe"
  Delete "$INSTDIR\README.md"
  Delete "$INSTDIR\uninstall.exe"
  Delete "$SMPROGRAMS\PHPanel\PHPanel.lnk"
  RMDir "$SMPROGRAMS\PHPanel"
  Delete "$DESKTOP\PHPanel.lnk"
  DeleteRegKey HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\PHPanel"
  DeleteRegKey HKCU "Software\PHPanel\Installer"
  RMDir "$INSTDIR"
SectionEnd

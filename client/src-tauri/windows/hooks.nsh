; Lightspeak NSIS hooks (Tauri 2). The app registers its per-user entries
; itself at runtime (URL scheme, toast AppUserModelID, optional autostart);
; the uninstaller removes them again so nothing is left behind in HKCU.

!macro NSIS_HOOK_POSTINSTALL
  ; Agent status was removed: drop its helper left by older versions.
  Delete "$INSTDIR\lightspeak-hook.exe"
!macroend

!macro NSIS_HOOK_PREUNINSTALL
  ; Helper of the removed agent status, in case an update kept it;
  ; removed first so the install folder can go away.
  Delete "$INSTDIR\lightspeak-hook.exe"
!macroend

!macro NSIS_HOOK_POSTUNINSTALL
  DeleteRegKey HKCU "Software\Classes\lightspeak"
  DeleteRegKey HKCU "Software\Classes\AppUserModelId\app.lightspeak.desktop"
  DeleteRegValue HKCU "Software\Microsoft\Windows\CurrentVersion\Run" "Lightspeak"
  ; "Delete app data" checkbox: settings, logs and WebView2 cache live in
  ; %APPDATA%\Lightspeak (sessions/keys are in the Credential Manager).
  ${If} $DeleteAppDataCheckboxState = 1
    RMDir /r "$APPDATA\Lightspeak"
  ${EndIf}
!macroend

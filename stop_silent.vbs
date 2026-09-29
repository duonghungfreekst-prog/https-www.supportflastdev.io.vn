' ==============================================================================
' SUPPORTFLAST MONOLITH PLATFORM - SILENT STOP SUPERVISOR
' Tat ngam an toan toan dien he thong: Tuyet doi khong hien bat ky cua so CMD nao
' File: stop_silent.vbs
' ==============================================================================
Option Explicit

Dim WshShell, fso, scriptDir, batPath, ps1Path, runCmd, q

Set WshShell = CreateObject("WScript.Shell")
Set fso = CreateObject("Scripting.FileSystemObject")
q = Chr(34)

scriptDir = fso.GetParentFolderName(WScript.ScriptFullName)
batPath = scriptDir & "\STOP_FULL_SYSTEM.bat"
ps1Path = scriptDir & "\tools\system_supervisor.ps1"

If fso.FileExists(ps1Path) Then
    runCmd = "powershell.exe -NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -File " & q & ps1Path & q & " -Action Stop -All -Silent"
    WshShell.Run runCmd, 0, True
ElseIf fso.FileExists(batPath) Then
    runCmd = "cmd.exe /c " & q & q & batPath & q & " /all /silent" & q
    WshShell.Run runCmd, 0, True
End If

Set WshShell = Nothing
Set fso = Nothing

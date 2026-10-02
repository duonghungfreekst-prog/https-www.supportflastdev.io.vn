' ==============================================================================
' SUPPORTFLAST MONOLITH PLATFORM - SILENT AUTO-START SUPERVISOR
' Khoi chay ngam toan dien he thong: Tuyet doi khong hien bat ky cua so CMD nao
' File: start_silent.vbs
' ==============================================================================
Option Explicit

Dim WshShell, fso, scriptDir, batPath, ps1Path, runCmd, q

Set WshShell = CreateObject("WScript.Shell")
Set fso = CreateObject("Scripting.FileSystemObject")
q = Chr(34)

scriptDir = fso.GetParentFolderName(WScript.ScriptFullName)
batPath = scriptDir & "\RUN_FULL_SYSTEM.bat"
ps1Path = scriptDir & "\tools\system_supervisor.ps1"

' Khoi chay hoan toan khong co bat ky popup nao voi WindowStyle=0
If fso.FileExists(ps1Path) Then
    runCmd = "powershell.exe -NoProfile -ExecutionPolicy Bypass -WindowStyle Hidden -File " & q & ps1Path & q & " -Action Start -Silent"
    WshShell.Run runCmd, 0, False
ElseIf fso.FileExists(batPath) Then
    runCmd = "cmd.exe /c " & q & q & batPath & q & " /silent" & q
    WshShell.Run runCmd, 0, False
End If

Set WshShell = Nothing
Set fso = Nothing

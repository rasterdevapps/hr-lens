@echo off
setlocal
rem Standard prompt: current user. Elevated prompt: all users (IT managed).
"%~dp0hrlens-agent.exe" install
if errorlevel 1 exit /b 1
echo Installation complete. Register as the Windows user being monitored.
echo See README.txt for registration, recording dependency and browser setup.

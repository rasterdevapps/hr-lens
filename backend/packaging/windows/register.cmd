@echo off
setlocal
rem Run as yourself, without elevation. Credentials are requested by the agent.
set /p "SERVER=HR Lens HTTPS server address: "
set /p "EMAIL=Your HR Lens email: "
if "%SERVER%"=="" exit /b 1
if "%EMAIL%"=="" exit /b 1
"%~dp0hrlens-agent.exe" register -server "%SERVER%" -email "%EMAIL%"
if errorlevel 1 exit /b 1
echo Registration saved. Sign out and back in to start the installed agent.
echo You can also start the installed hrlens-agentw.exe with the run argument.

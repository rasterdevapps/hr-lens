HR Lens Windows x64 desktop agent
================================
Runs in the signed-in user's interactive desktop session. Windows agents use the
existing HR Lens server; this package does not install a Windows server.

INSTALL
Extract the complete ZIP. Use a standard Command Prompt for a per-user install:
  hrlens-agent.exe install
The destination is %LOCALAPPDATA%\Programs\HR Lens.
An elevated Command Prompt installs for all users in %ProgramFiles%\HR Lens.
Each user must register separately, WITHOUT elevation:
  hrlens-agent.exe register -server https://hr.example.com -email you@example.com
Start the installed background program, or sign out and in:
  start "" "%LOCALAPPDATA%\Programs\HR Lens\hrlens-agentw.exe" run
For a machine install, use "%ProgramFiles%\HR Lens\hrlens-agentw.exe" instead.

A visible tray icon shows monitoring status and offers a graceful Quit action.
Screenshots, live frames and aggregate input counts pause on locked/secure desktops.
No typed text is recorded. VP8/WebM recordings require a separately managed FFmpeg
installation with the libvpx encoder. See docs/WINDOWS.md in the source repository
for configuration, browser extension setup, validation and recovery instructions.

UPDATES / REMOVE
Per-user release builds accept only updates signed by the existing release key.
Machine-wide installs require IT-managed package upgrades. Quit running sessions
before replacing the package. Keep the preceding signed ZIP for rollback.
  hrlens-agent.exe status
  hrlens-agent.exe uninstall
Use the tray Quit action before deleting installed files. Configuration and pending
data remain in %APPDATA%\hrlens-agent; preserve them for recovery or remove them
only when the owner's retention policy allows it. Never share config.json (token).

DEVELOPMENT / SIGNING
CI packages are unsigned development builds and do not update themselves.
Production packages require Authenticode signing before release checksums/signatures.
Verify publisher and package provenance with IT. Do not bypass security warnings
for an unverified build. Signing does not guarantee absence of SmartScreen prompts.

Desktop acceptance (DPI, multi-monitor, lock/UAC, logout, sleep and RDP) must be run
on the target Windows fleet before rollout. Hosted CI is not that acceptance test.

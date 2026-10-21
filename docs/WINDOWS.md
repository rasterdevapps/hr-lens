# Windows agent deployment and acceptance

Target: x64 Windows 10/11 interactive user sessions. The server and PostgreSQL can
remain on Linux. This change does not install the backend as a Windows service.
Deploy the server migration/capability API before rolling out updated agents.

## Install and registration

1. Obtain a verified Windows agent ZIP and extract the whole directory. CI produces
   **unsigned development builds**; production packaging requires a signing tool.
2. Run `hrlens-agent.exe install` in a normal Command Prompt for a per-user install
   at `%LOCALAPPDATA%\Programs\HR Lens`. An elevated prompt installs under
   `%ProgramFiles%\HR Lens` for everyone. Each Windows user registers independently.
3. As the user being monitored (without elevation), run:
   ```bat
   hrlens-agent.exe register -server https://hr.example.com -email you@example.com
   ```
   The password is requested interactively. Never distribute `config.json`.
4. Sign out and in, or start the installed background executable:
   ```bat
   start "" "%LOCALAPPDATA%\Programs\HR Lens\hrlens-agentw.exe" run
   ```
   For a machine installation, substitute `%ProgramFiles%\HR Lens`.
5. Verify the monitoring tray icon, status, account and live-screen capability in
   HR Lens. Use the tray's Quit action for a graceful stop before maintenance.

Get employees' informed authorization and use the organization's established
monitoring policy. Input activity is aggregate counts; typed characters are never
saved. Locked/secure desktops suspend collection. A UAC prompt must not create
attendance lock/unlock events.

## Screen recording dependency

Screenshots and live frames use native Windows capture. VP8/WebM recording uses a
**separately installed and managed FFmpeg** with the `libvpx` encoder. IT controls
its provenance, version, installation and updates. It is not bundled with or
replaced by the HR Lens updater.

Set `HRLENS_FFMPEG` to its absolute executable path for the user, then restart the
agent. In PowerShell, for example:

```powershell
[Environment]::SetEnvironmentVariable('HRLENS_FFMPEG', 'C:\Program Files\FFmpeg\bin\ffmpeg.exe', 'User')
```

The runtime checks the encoder before advertising recording support. Without a
usable encoder, recordings are unavailable; other available capabilities remain
independent. CI uses synthetic frames with a checksum-pinned FFmpeg build and
checks the generated WebM using ffprobe; it does not record a person's desktop.

## Chrome and Edge domains

Installation registers `com.hrlens.agent` as a native messaging host for Chrome
and Edge in HKCU (per-user) or HKLM (machine-wide). It points at the installed GUI
agent, so the bridge follows signed agent updates. The extension communicates
through a local named pipe restricted by user ACL and verified Windows session.
The Windows extension never falls back to the machine-wide Linux TCP port.

For development/unmanaged testing, use Chrome/Edge's developer mode **Load
unpacked** to load `backend/browser-extension/chromium`. The checked-in public key
fixes the extension ID to `jobmeopcpnhamneamceglchglicfhdlk`. Install the host first,
restart the browser, and confirm front-tab domains reach the correct account.
The extension reports only tab title and hostname, not full URL or page contents.

For production, publish the extension through the browser's supported store or
use your organization's eligible managed extension policy with the matching ID.
Do not force an unsupported self-hosted installation on unmanaged Windows PCs.
Adding a native host does not itself install an extension. Chrome and Edge store
identities must match the native host allowlist; a different store identity needs
an explicit allowlist change and package rebuild.

References: [Chrome native messaging](https://developer.chrome.com/docs/extensions/develop/concepts/native-messaging),
[Chrome Windows extension policy](https://support.google.com/chrome/a/answer/7532015).

## Build, signing and deployment

From `backend`, development packaging requires no release key:

```sh
go run ./cmd/release -dev -out ../artifacts/windows-validation
```

The output directory must not already exist. Both OS packages, installer source
files and `SHA256SUMS.json` are built from tracked sources. `DEVELOPMENT.txt`
identifies unsigned packages, and their agent version stays `dev` (updates off).

For a production release, use the **existing** `agent-release.key` matching the
checked-in public key. Do not regenerate the release key for this migration.
Pass a signer executable that accepts one binary path, applies Authenticode and
verifies the signature before returning success:

```sh
go run ./cmd/release -version X.Y.Z -out /new/private/staging/X.Y.Z -windows-signer /path/to/sign-and-verify
```

The signer handles certificate credentials outside the repository. Both Windows
executables are signed; the GUI executable's Ed25519 checksum/signature is
calculated afterward. Review and promote staging as a separate deployment step.
Signing does not guarantee removal of every SmartScreen warning. Never instruct
employees to bypass a warning for an unverified build.

Set `HRLENS_RELEASE_DIR` to the completed staging directory when invoking
`backend/deploy/build-bundle.sh`; the bundle includes both Linux and Windows
archives. The backend serves the Windows ZIP beside `AGENT_DIR`. No production
release is signed, published or deployed by the CI workflow.

## Updates and rollback

Per-user installations accept only updates verified by the existing Ed25519 key
and SHA-256 checksum. Machine-wide installations remain administrator managed.
The background executable is updated automatically; installation/registration CLI
changes are delivered by reinstalling a complete new package. FFmpeg remains
separately managed. Preserve the previous verified ZIP before an upgrade.

For rollback, quit HR Lens in each affected session, stop automatic startup with
`hrlens-agent.exe uninstall`, reinstall the previous verified package and register
only if needed. A server advertising a newer release can update it again: IT must
also withdraw that release or use the approved rollback deployment procedure.
Do not delete user configuration or pending spool files as part of binary rollback.
If an update leaves a backup executable, retain it while its process is running;
remove retired backups only after the corresponding process has stopped.

Logs live under `%APPDATA%\hrlens-agent`. Share redacted diagnostics only; tokens,
real screenshots, window titles and employee records do not belong in issues.

## Acceptance gate before rollout

Hosted CI covers native Windows/Linux tests, process shutdown, updater/file
semantics, package contents, synthetic video encoding, capability integration,
and frontend builds. A successful CI run is **not** desktop fleet acceptance.
Use a dedicated Windows test machine with synthetic accounts and screen content:

| Area | Required checks |
|---|---|
| Install | Standard/elevated installs; Unicode/spaces in paths; sign-in startup; visible tray; graceful Quit/uninstall |
| Displays | One/multiple monitors; negative coordinates; mixed 100/150/200% DPI; connect/disconnect display; JPEG geometry |
| Recording | ffprobe shows VP8/WebM, normal and early stop play, encoder missing/hung behavior is bounded |
| Sessions | Lock/unlock, UAC (no false attendance lock), sleep/resume, logout/reboot, RDP reconnect, fast user switching |
| Browser | Two simultaneous sessions report domains to their own account; missing extension is handled honestly |
| Network | Offline restart preserves durable queue; reconnect cleans uploaded files; disk bounds; cleanup failure retry |
| Upgrade | Two successive upgrades in one login, running EXE locks, invalid signature/checksum, interrupted replacement, rollback |

Record Windows version, display configuration, agent commit, encoder version and
observed results. Do not mark these rows passed from compilation or synthetic
encoder tests alone. Pilot on a small group after the interactive matrix passes.

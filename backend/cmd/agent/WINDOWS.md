# Windows agent: what is built and what is left

The Windows agent is the same Go program as the Linux one. Everything specific to Windows is
in `screen_windows.go`, `install_windows.go` and `log_windows.go`.

## Built

- Front application and window title (`activeWindow`: GetForegroundWindow + process image name)
- Screen lock detection (`locked`: the input desktop is not "Default")
- One agent per sign-in session (`singleInstance`: named mutex `Local\HRLensAgent`)
- Register / status / install / uninstall, starting at sign-in through the registry Run key
  (HKLM for a computer-wide install from an administrator prompt, HKCU otherwise)
- Heartbeat, activity upload, presence, signed self-update (per-user install), crash watchdog
- Two programs from one source: `hrlens-agent.exe` (console: register/status/install) and
  `hrlens-agentw.exe` (built with `-H=windowsgui`, runs in the background, logs to
  `%APPDATA%\hrlens-agent\agent.log`). `go run ./cmd/release -version X` builds both and
  `dist/hrlens-agent-windows.zip` (with `install.cmd`, `register.cmd`, `README.txt`).

## Left for a Windows developer

Implement these in `screen_windows.go` and set `canCapture = true`. The rest of the agent
(spooling, upload, retries, live view, metadata) already works with them, as on Linux.

| Function | Contract |
|---|---|
| `screenshot(ctx, path, maxWidth, quality)` | Write the whole desktop (all monitors) as a JPEG at `path`, scaled down to `maxWidth` px wide when it is not 0, JPEG quality `quality` (1-100). Return an error and leave no file on failure. Called every 5 minutes (quality 60) and every 2 seconds during live view (1280 px, quality 50). |
| `record(ctx, path, length, stop)` | Record the desktop to a WebM (VP8) file at `path`: 1 frame per second, about 1280 px wide, for `length` (10 minutes), stopping early when `ctx` ends or `stop()` returns true (checked about every second; it means the screen got locked). The file must be playable when it returns, even when stopped early. |
| `watchInput(ctx, key, click)` | Call `key()` once per key press and `click()` once per mouse click until `ctx` ends. Counts only: never record which key. Return `errors.ErrUnsupported` to turn the feature off. |
| `sessionStart()` | Optional: the time this user signed in to Windows (e.g. WTS session info), or `false`. |

Notes for whoever builds it:

- The agent runs in the user's own session (not as a service), so it can see the desktop and
  does not need session-0 workarounds.
- High DPI: make the process per-monitor DPI aware, or captures come out blurred / cropped.
- Builds use `CGO_ENABLED=0`. Pure Go (syscalls to GDI / DXGI / Windows.Graphics.Capture) keeps
  that; bundling a tool such as ffmpeg for WebM is fine too, shipped in the zip next to the exe.
- Show employees that monitoring is on (for example a tray icon), and get the programs
  code-signed: unsigned monitoring software is often quarantined by antivirus.
- The Live Screen page shows Windows computers as "Screen view is not available" based on the
  device OS (`screenCaptureOS` in `internal/api/captures.go`); change it there when capture works.

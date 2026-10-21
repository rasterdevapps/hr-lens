# Windows agent implementation

The x64 Windows adapter runs in the signed-in interactive session. The Linux
server remains supported. See [Windows deployment and acceptance](../../../docs/WINDOWS.md)
for installation, managed FFmpeg, browser setup, signing and required desktop tests.

- `screen_windows.go`: foreground windows, WTS session state and sign-in time,
  visible tray status, Quit, session/power/shutdown notifications.
- `capture_gdi_windows.go` and `capture_pixels.go`: bounded all-monitor capture,
  negative coordinates, DPI awareness and pixel conversion.
- Windows recording uses separately managed FFmpeg (`libvpx`, VP8/WebM); missing
  dependencies disable that capability independently.
- Input collection records only aggregate key-press and click counts.
- `browser_windows.go`: Chrome/Edge native messaging over user/session-scoped pipes.
- `snapshot.go` and `spool_recovery.go`: bounded durable queues, atomic state writes
  and recovery of abandoned sessions.
- `install_windows.go`, `update.go` and `supervise.go`: startup registration,
  verified per-user updates, repeated running-EXE replacement and bounded shutdown.

Capabilities are reported by the installed agent. Old partial Windows agents
continue to report screen view unavailable. Compilation and hosted synthetic tests
are not evidence that an interactive desktop scenario has passed; follow the
acceptance matrix before rollout.

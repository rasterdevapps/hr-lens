# Automated Windows and Linux validation

The `Linux and Windows CI` workflow runs on pull requests, pushes to `main`, and manual dispatches. It needs no repository secrets or employee credentials.

It runs Go unit tests on native Ubuntu and Windows runners, Linux race checks, native agent/server/release builds with CGO disabled, and the Windows GUI agent build. The Windows development ZIP is unpacked and its files are hash-checked. Angular browser tests and production builds run on both operating systems. A separate Linux job initializes a disposable PostgreSQL 16 service with synthetic fixtures and runs the existing integration tests; skipped integration tests fail that job.

Each run retains test logs and development binaries for seven days. These are unsigned `dev` builds: they do not self-update and are not production installers. The workflow does not run the desktop agent, capture screens, publish releases, change registry startup settings, use production databases, or access the private release key.

## Desktop acceptance remains separate

Hosted CI cannot prove the missing Windows capture features work. The current Windows adapter still disables screenshots, recording, live view and input counts. Before deployment, implement the Windows audit plan and test on a dedicated interactive Windows desktop:

- standard-user and machine-wide install, login startup, graceful stop and uninstall;
- multiple monitors, mixed DPI and negative display coordinates;
- lock/unlock, UAC, sign-out, sleep/resume and RDP transitions;
- offline capture queues and successful upload deletion;
- two consecutive updates during one login, interrupted updates and rollback;
- correct browser attribution for concurrent user sessions.

A self-hosted runner can coordinate these checks when started in a signed-in desktop session, with an explicit desktop-test label and manual dispatch. Do not route arbitrary pull-request code to that machine. No desktop runner is configured by this workflow.

## Test changes included

- Browser handler tests use an ephemeral HTTP test server instead of the live agent's fixed port.
- Update tests check executable mode bits only on platforms that implement POSIX permissions.
- The Angular starter test now checks the actual router outlet and supplies router dependencies.

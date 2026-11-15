# reManager on-device: design

Status: draft, 2026-11-14. Nothing here has been run on a tablet yet.

This fork turns reManager from a desktop app that drives a reMarkable over SSH
into an app that runs on the tablet itself. Upstream is
[rmitchellscott/reManager](https://github.com/rmitchellscott/reManager)
(GPL-3.0, Wails: Go backend, React frontend). The fork stays GPL-3.0.

## 1. Decisions

These are settled. Sections 4 to 7 follow from them and are open to change.

| # | Decision |
|---|----------|
| D1 | Primary target is the reMarkable Paper Pro Move (64-bit ARM). |
| D2 | Code is written so rM1, rM2, Paper Pro and Paper Pure can be added later. They are not tested and not promised to work. |
| D3 | Milestone 1 is an app that launches on the tablet and can list and install Vellum packages. |
| D4 | The Go backend is reused. A new `CommandExecutor` (`internal/executor/executor.go`) runs commands locally instead of over SSH. |
| D5 | The Wails UI is not used on the tablet, because it needs GTK and WebKitGTK. The UI is option A in section 3: a QML frontend loaded by AppLoad, with the Go backend as its backend process. |
| D6 | There is a written plan for recovering when the app breaks the stock UI or itself (section 5). |
| D7 | Desktop-only features are dropped or reworked: saved SSH logins for several devices, file transfer between computer and tablet, and probably the built-in terminal (section 6). |

## 2. What the research found

Sources are listed in section 9. "Read" means I read the source or package
definition. Nothing was tested on hardware.

### The Move

- 7.3 inch colour e-paper, 1696 x 954, 264 ppi, dual-core Cortex-A55, 2 GB RAM.
- Vellum calls it `rmppm`; reManager's code calls it `rmppmove`, codename
  `chiappa` (`internal/vellum/metadata.go`, `internal/swupdate/versions.go`).
- SSH needs developer mode, which factory-resets the tablet when enabled.

### How third-party apps reach the screen

There is one established route on the Paper Pro line, and it has three layers:

1. **xovi** loads extensions into the stock UI process (`xochitl`). It is
   tethered: a reboot always returns the tablet to stock, and xovi has to be
   started again with `xovi/start`. Its README says not to make it start at
   boot, because that can cause a boot loop.
2. **AppLoad** is a xovi extension that adds a launcher inside the stock UI. It
   runs apps full screen or in a window. It has an explicit `move` screen
   setting. The Vellum package is `appload` 0.6.0, for `aarch64` and `armv7`,
   and it currently requires reMarkable OS 3.28 or 3.29.
3. **tripletap** is a small systemd service that starts xovi when the power
   button is pressed three times. It lists the Move as supported and is a
   Vellum package. Without it, starting xovi after a reboot needs SSH.

AppLoad supports two kinds of app:

- **QML app with an optional backend.** The UI is QML loaded into `xochitl`,
  so the stock UI does the e-ink drawing and touch handling. The backend is a
  separate executable in any language. AppLoad starts it with a Unix socket
  path as its first argument.
- **External app with qtfb.** A separate process draws raw pixels into a
  shared-memory buffer that AppLoad shows. Client libraries exist for Rust and
  C++ only.

The backend protocol, read from AppLoad's source (`src/protocol.h`,
`src/management.cpp`): a `SOCK_SEQPACKET` Unix socket at `/tmp/<app id>.sock`;
each message is an 8-byte header packet (`int` type, `int` length, native byte
order) and a UTF-8 contents packet. Types -1, -2 and -3 are AppLoad's own:
terminate, frontend attached, frontend detached. The stated limit is 10 MiB,
but a message must fit in one packet of the socket's send buffer, so UI
messages are kept to tens of kilobytes and long lists are sent in pages.
`internal/appload` is a Go client for this, with tests.

The tablet runs OS 3.27.3.0. The AppLoad release Vellum offers for 3.26 and
3.27 is 0.5.3, so that is most likely what is installed; this is inferred, not
read from the tablet. The protocol files are identical in 0.5.3 and 0.6.0.
The `aspectRatio` and `width` keys for QML apps were added in 0.6.0, so the
UI cannot rely on them and has to fit whatever size it is given.

reTaskable is a working precedent: a QML frontend plus a compiled backend,
built for the Move, shipped as a Vellum package that depends on `appload` and
installs into `/home/root/xovi/exthome/appload/retaskable/`.

### What the existing Go code allows

- `CommandExecutor` has three methods and three consumers: `vellum.Client`,
  `vellum.HashtabChecker` and `installer.Installer`. The only implementation
  is `wailsExecutor` in `shared.go`, which is tied to the Wails app and its
  SSH session.
- `internal/vellum`, `internal/installer` and `internal/executor` compile for
  `linux/arm64` with `CGO_ENABLED=0` and do not pull in Wails. Checked with
  `go build`.
- The root package (`packagesservice.go`, `connectionservice.go` and the
  rest) is the Wails app. It cannot be imported. Logic the on-device app
  needs from it has to be moved into `internal/` first. I have not yet read
  `packagesservice.go` closely enough to say how much that is.
- Device detection in `remarkable-go` already takes `filesystem.FS` and
  `executor.Executor` interfaces, so local implementations should be enough.
- The package catalogue is fetched over HTTPS from
  `packages.vellum.delivery`, so listing packages needs Wi-Fi on the tablet.

## 3. UI and launcher options

### Option A: AppLoad QML frontend with a Go backend (chosen)

The UI is QML inside the stock UI. The reused Go backend runs as the AppLoad
backend process and talks to the UI in JSON over the AppLoad socket.

- For: the stock UI handles refresh, touch, pen and the on-screen keyboard.
  It runs beside the stock UI by design. The Go backend is reused whole. A
  backend crash does not take the stock UI down. It ships as an ordinary
  Vellum package. reTaskable shows the shape works on the Move.
- Against: the UI is a rewrite in QML; none of the React code carries over.
  Bad QML runs inside `xochitl`. The app only exists while xovi is running,
  and AppLoad is pinned to specific OS versions (see section 4).

### Option B: AppLoad external app drawing through qtfb

A Go process draws its own pixels.

- For: all the code is Go and none of it runs inside `xochitl`.
- Against: needs a Go qtfb client and a UI toolkit (text, lists, buttons,
  scrolling, keyboard), all written from scratch. Refresh quality is in our
  hands. Same xovi and AppLoad dependency as option A, for much more work.

### Option C: take over the display with the stock UI stopped

- Against: it does not run alongside the stock UI, which D5 asks for. I found
  no documented open display path for the Move. It has the highest risk of
  leaving the tablet without a working UI. Rejected.

### Option D: no on-tablet UI, serve the existing React UI over HTTP

Wails v3 has a server build (`-tags server`) that serves the frontend to a
browser with no GTK. The tablet would run the backend and a phone or computer
would show the UI.

- For: most of the existing UI is reused, and it does not need xovi.
- Against: it is not an e-ink UI and it needs a second device, which is most
  of what this fork is trying to remove. Worth remembering as a rescue
  interface for when xovi is down. Not planned.

### Not evaluated

Oxide is a second launcher that Vellum packages for `aarch64`. I did not
check whether it works on the Move.

### Choice

Option A, launched from AppLoad. tripletap is the suggested way to start xovi
without a computer.

## 4. Consequences of option A

These follow from the choice and shape the design.

1. **The app cannot install itself from nothing.** It needs Vellum, xovi,
   qt-resource-rebuilder and AppLoad on the tablet first. First-time setup
   still goes over SSH, with desktop reManager or a script. Milestone 1
   assumes that setup is done.
2. **The app is missing when it is most wanted.** After a reboot it is gone
   until xovi is started. After an OS update AppLoad may refuse to install
   until it supports the new version. "Guided recovery after OS updates"
   therefore cannot live only in this app.
3. **Installing mods can restart the UI the app is drawn in.** Many packages
   need xovi restarted. AppLoad starts the backend as a child process of
   `xochitl` (a `QProcess`, read from `src/library.cpp`), so a restart of the
   stock UI is expected to take the backend with it. An install therefore
   runs in its own systemd unit (`systemd-run`), writes a log and a result
   file under `/home/root`, and the UI reads that state when it comes back.
   Whether `systemd-run` is present on the Move is not checked yet.
4. **Two UIs, one backend.** The Go packages stay shared with upstream so
   upstream fixes can be merged. On-device code goes in new directories and
   the Wails app keeps building.

## 5. Recovery plan

Ordered from least to most drastic. The first three need no computer.

| Failure | Recovery |
|---------|----------|
| The app's UI hangs or misdraws | Close it with AppLoad's gesture (drag from top centre to the centre of the screen). |
| The app's backend crashes | The stock UI is unaffected. Reopen the app. The install log shows what was in progress. |
| A mod, AppLoad or the app's QML breaks the stock UI | Reboot. xovi is tethered, so the tablet comes up stock. |
| The stock UI is fine but xovi will not start cleanly | Over SSH: `vellum del` the offending package, or remove `/home/root/xovi/exthome/appload/remanager/`. Desktop reManager can do the same. |
| The tablet boots but the UI never appears | SSH over USB still works if nothing below was broken. Fix from there. |
| Nothing responds | reMarkable's official software recovery over USB. Recovery mode is entered from the boot ROM with the power button (hold 25 to 30 seconds, release, press once), so it does not depend on any software on the tablet. It needs reMarkable's desktop app or recovery command-line tool on a computer. It is also how developer mode is left. I have not confirmed what it does to documents. |

Rules the app follows so those steps keep working:

- Never make xovi start at boot, and never edit `xochitl`'s systemd units.
- Never write to the root partition in milestone 1. Everything goes under
  `/home/root`.
- Never touch SSH, USB networking, Wi-Fi configuration or developer mode.
- No OS install or OS switching until the recovery steps have been rehearsed
  on the real tablet.
- Show the plan (Vellum's simulate output) and ask before any change, and say
  so when a change will restart the stock UI.
- Use the existing start-limit guard (`executor.WithXochitlStartReset`): on
  the Paper Pro line a fifth start of `xochitl` in ten minutes reboots the
  tablet.
- One Vellum process at a time. The lock in `internal/vellum/client.go` only
  covers one process, so the on-device app also needs a lock file.

Before the first on-tablet test: confirm SSH over USB works, back up
documents, and rehearse "reboot returns to stock" once with a harmless mod.

Candidate for later: a watchdog that runs `xovi/stock` if the stock UI does
not come back within a set time after an install.

## 6. Feature triage

| Upstream feature | On-device | Notes |
|------------------|-----------|-------|
| Browse and install mods | Milestone 1 | The goal. |
| Upgrade and uninstall mods | Next | Same Vellum client. |
| Saved SSH logins for several devices | Drop | One device, local, no credentials. |
| Built-in terminal | Drop | No keyboard worth using; SSH remains for this. |
| File transfer to and from a computer | Drop | No computer in the picture. |
| Document import (PDF, EPUB, rmdoc) | Drop | It imports from the computer. |
| Backup and restore | Drop for now | A backup kept on the same tablet is not a backup. |
| Settings sync, support bundle upload, demo mode | Drop | Desktop concerns. |
| App self-update | Rework | Through Vellum, as a package. |
| Status checks and one-click fixes | Later | Limited by consequence 2 above. |
| Auto-update toggle, timezone, start and stop mods, mod commands | Later | Small once the local executor exists. |
| Sleep screen from a PNG | Later, reworked | Pick from files already on the tablet. |
| Local file browser, configuration editor | Later, maybe | Editing text on e-ink is poor. |
| OS manager | Deferred | Highest risk. See the recovery rules. |

## 7. Code layout

```
internal/executor/local.go      LocalExecutor: CommandExecutor over /bin/sh -c
internal/appload/               Go client for the AppLoad backend protocol
internal/ondevice/              device profile, operation runner, UI messages
cmd/remanager-ondevice/         backend entry point (no Wails)
ondevice/ui/                    QML, built into resources.rcc
ondevice/manifest.json          AppLoad manifest
```

- `LocalExecutor` runs each command through `/bin/sh -c` with the same `PATH`
  prefix as `executor.WithDevicePath`, and streams output line by line. It
  drops `LD_PRELOAD` (AppLoad already removes it for backends; this covers
  other ways of starting the binary). It refuses commands marked `RequiresPTY`; nothing in the code sets
  that flag today. Written, with tests.
- Other models (D2): everything model-specific goes behind one device profile
  (model name, Vellum virtual package, screen size, architecture). Only the
  Move's profile is filled in. The backend builds for `arm64` only until
  another model is taken on.
- Build: `CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build ./cmd/remanager-ondevice`,
  plus Qt's `rcc` for the QML.
- Development loop: AppLoad has a PC emulator for the QML, and the backend
  can run on a PC against a fake `vellum` script.
- Delivery: copy to `/home/root/xovi/exthome/appload/remanager/` during
  development; a Vellum package that depends on `appload` later.

## 8. Milestone 1 and open questions

Milestone 1, in order:

1. `LocalExecutor` with tests. Done.
2. Go AppLoad client with tests against a local socket. Done.
3. Backend: list catalogue, list installed, simulate, install.
4. QML: package list, package detail, confirm, progress.
5. First run on the Move, after the rehearsal in section 5.

Open questions:

- Which AppLoad version is installed? (`vellum info appload`)
- Is `systemd-run` available on the Move?
- How much of `packagesservice.go` has to move into `internal/`?
- What does official software recovery do to documents on the Move?

## 9. Sources

- AppLoad: <https://github.com/asivery/rm-appload> (README, wiki, Rust backend
  client, examples; cloned and read)
- xovi extensions and xovi's boot behaviour:
  <https://github.com/asivery/rm-xovi-extensions>
- qtfb (archived, merged into AppLoad): <https://github.com/asivery/qtfb>
- tripletap: <https://github.com/rmitchellscott/xovi-tripletap>
- Vellum packages (`appload`, `xovi`, `tripletap`, `retaskable`, `koreader`;
  cloned and read): <https://github.com/vellum-dev/vellum>
- reTaskable: <https://github.com/jdkruzr/reTaskable>
- Wails v3 server build: <https://v3.wails.io/guides/server-build>
- Move specifications:
  <https://remarkable.com/products/remarkable-paper/pro-move/details/features>
- Recovery mode: <https://developer.remarkable.com/documentation/recovery-mode>
- Official recovery: <https://support.remarkable.com/s/article/Software-recovery>
  (search summary only)
- Developer mode: <https://remarkable.guide/tech/developer-mode.html>
- KOReader install wiki, which describes the same xovi and AppLoad route for
  the Move: <https://github.com/koreader/koreader/wiki/Installation-on-ReMarkable>

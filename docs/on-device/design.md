# reManager on-device: design

Status: milestone 1 reached on 2026-11-15. On a Paper Pro Move with OS
3.27.3.0 and AppLoad 0.5.3-r3, the app launches from AppLoad, lists the
catalogue and installed a package (`retaskable`) through the systemd worker.
One tablet, one install; the failure paths in section 5 have not been
rehearsed.

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

The tablet runs OS 3.27.3.0 and has AppLoad 0.5.3-r3 installed (read from
the tablet). The protocol files are identical in 0.5.3 and 0.6.0.
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
  rest) is the Wails app. It cannot be imported. For milestone 1 nothing had
  to move out of it: the catalogue, the solver calls and the installer are
  already in `internal/vellum` and `internal/installer`, and the root package
  only wires them to Wails. The on-device service does its own, smaller
  wiring.
- One shared-code fix was needed: `vellum.Client.GetOSVersionState` treated a
  missing stored-version file as a failure unless the error was an SSH exit
  error. It now accepts a local exit error too.
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
   `systemd-run` is present on the Move and the worker has completed one
   install there. An install that restarts the stock UI partway has not been
   tried.
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
| Upgrade and uninstall mods | Built | On the Move: a single-mod update (`hide-dev-mode-icon`) worked. Removal of `retaskable` first failed (see below) and the fix is not yet tried there. |
| Saved SSH logins for several devices | Drop | One device, local, no credentials. |
| Built-in terminal | Drop | No keyboard worth using; SSH remains for this. |
| File transfer to and from a computer | Drop | No computer in the picture. |
| Document import (PDF, EPUB, rmdoc) | Drop | It imports from the computer. |
| Backup and restore | Drop for now | A backup kept on the same tablet is not a backup. |
| Settings sync, support bundle upload, demo mode | Drop | Desktop concerns. |
| App self-update | Rework | Through Vellum, as a package. |
| Status checks and one-click fixes | Built, partly | See "Status checks" below. |
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

### Backend as built

- `internal/ondevice.Service` lists the catalogue in pages of at most 25,
  shows one package, plans an install with Vellum's simulate, and installs.
- Post-install steps that packages ask for (today: rebuilding the hashtable)
  are not run. They stop the stock UI, so they wait until that path is
  designed. The install result names each step it skipped.
- Removal is deliberately narrower than upstream's. It removes what was
  asked for plus whatever Vellum purges with it, in one transaction, and
  refuses in four cases: the package is one the app runs on (`vellum`,
  `xovi`, `qt-resource-rebuilder`, `appload`); the removal would take one of
  those with it; other mods still need the package (it names them and does
  not remove them for you); or Vellum would leave the package installed.
- Found on the Move: removing `retaskable` failed because KOReader needs
  "a launcher" and, once `retaskable` no longer named AppLoad, Vellum would
  not choose between AppLoad and Oxide by itself, though AppLoad was
  installed. When a removal fails that way and exactly one installed
  package provides the virtual, the plan now says so and the removal first
  runs `vellum add <provider>`. That only records the provider as installed
  on purpose; it changes no package. If no installed provider, or more than
  one, matches, the removal is refused with Vellum's message.
- Network failures (no DNS, no route, apk's "network error") reach the
  screen led by a plain "the tablet seems to be offline" message. Found on
  the Move: KOReader can turn Wi-Fi on while the reMarkable settings say
  airplane mode, and restarting the reMarkable UI turns it off again, so
  the app loses the network after a restart.
- Restart and hashtable rebuild (System tab, and offered after an install
  that asks for a rebuild) also run in the worker, because both stop the
  stock UI and with it this app's backend. Restart runs `xovi/start` after
  `systemctl reset-failed xochitl`. The rebuild reuses desktop reManager's
  `RebuildHashtableDialog`: it goes through `launcherctl` when a launcher
  manages the UI. Otherwise it runs `xovi/rebuild_hashtable` and then starts
  the UI with mods, even if the rebuild failed, since the rebuild leaves the
  UI stopped. The result is shown the next time the app opens, within ten
  minutes. Restart has worked on the Move.
- The first rebuild on the Move (2026-11-15) failed. During the rebuild the
  screen shows a temporary reMarkable UI with no mods, which looks finished.
  A triple-tap then ran `xovi/start`, whose `systemctl daemon-reload` and
  restart stopped the job 45 s in, before the hashtable was saved. The
  worker treated the stop as a cancel and did not start the UI again. Since
  then:
  - `xovi-tripletap` is stopped for the rebuild if it was running, and
    started again afterwards. A marker in `/run` records the pause, so a
    reboot forgets it.
  - The worker brings the UI back when told to stop, using an executor with
    a context of its own (`SetRescueExecutor`). systemd also runs
    `worker restore-ui` as the job's `ExecStopPost`, for when the worker
    itself is killed. Restoring starts the UI only if it is not already
    active or activating, and resumes triple-tap.
  - A rebuild has a five-minute limit (`RuntimeMaxSec=300`).
  - The confirm screen says the screen restarts twice, that the UI in
    between is temporary, and to wait for the second restart.
  - Nothing can be drawn on the temporary UI: no mods are loaded while the
    hashtable is built.
  - Not yet known: whether a `daemon-reload` during a job also discards the
    job's `ExecStopPost`. The worker's own restore does not depend on it.
  - With these changes, a rebuild started from the app worked on the Move.
- The second rebuild on the Move worked and restored the hashtable. The app
  then could not start: the rebuild had recorded about 3,000 lines of the
  temporary UI's log, and the reply to `job` carried all of them. That is
  far more than one packet on AppLoad's socket (208 KiB by default), so the
  send failed and the frontend waited. Now `job` and the replay to a newly
  attached frontend send a summary (start, latest progress, result), and
  the full record stays on disk. A reply that cannot be sent is replaced by
  a short error reply. Colour codes are stripped from recorded output, and
  progress is flushed to disk at most once a second. Confirmed on the Move:
  the app opens normally after the fix. If the UI does not come
  back, restarting the tablet brings back the stock UI (section 5).
- Upgrades come from Vellum's own solver (`upgrade --simulate`) after an
  index refresh, for everything or for named packages. Upgrading the
  packages the app runs on is allowed; they take effect when xovi restarts.
  No post-upgrade steps are run, as upstream.
- A job started from the frontend (install, remove or upgrade) runs in a
  worker: the same binary, started by `systemd-run` as the unit
  `remanager-ondevice-job`. It holds a lock for the whole job and appends
  each step to `/home/root/.local/state/remanager-ondevice/job.jsonl`. A
  backend that starts later replays that file to the frontend.
- Frontend and backend exchange JSON: type 1 is a request (`device`,
  `refresh`, `list`, `detail`, `plan`, `planRemove`, `upgrades`,
  `planUpgrade`, `install`, `remove`, `upgrade`, `job`), type 2 its reply,
  type 3 a job event, type 4 "backend ready".
- The binary also runs from a shell, so the backend can be tried over SSH.
  `list`, `installed`, `info`, `plan`, `plan-remove`, `plan-upgrade` and
  `job` change nothing on the tablet. `upgrades` refreshes Vellum's package
  index and changes nothing else. `install`, `remove` and `upgrade` do the
  real thing.
- It refuses to run on a model other than the Move unless given
  `--allow-untested`.

### Frontend as built

- `ondevice/ui/Main.qml` has four screens: list (all, installed or updates,
  eight per page, with Previous and Next instead of scrolling), detail, plan
  and progress. Plan and progress serve install, remove and update alike. Mods that
  cannot be installed on this tablet and OS are hidden from "All mods"
  unless shown with a toggle; installed ones are always listed. The filter
  is applied by the backend, so page counts stay right. It is black on white with no animation, and every size is a
  fraction of the width so it fits a window as well as the full screen.
- It uses only `QtQuick` and AppLoad's endpoint, not Qt Quick Controls.
- AppLoad drops messages sent while the backend is not connected. The
  frontend first relied on a single "ready" message (type 4); on the Move,
  every second launch missed it and showed "the backend did not start",
  though requests then worked. The cause inside AppLoad is not known. The
  frontend now also asks the backend for the device every 0.7 s until it
  answers. It treats "ready" as a hint to ask at once, and it gives up on a
  request with no reply after 30 s (3 min for index refreshes and solves)
  instead of staying busy.
- Search (All mods and Installed) matches names and descriptions. AppLoad's
  on-screen keyboard only feeds external qtfb apps, so the app has its own:
  digits, letters, hyphen, space and backspace. While typing, it shows how
  many mods match, asking the backend after a 0.6 s pause.
- Closing the app stops the backend. A running install is in its own worker
  and carries on.
- The icon (`ondevice/icon.svg`, rendered to `icon.png`) is the desktop
  icon without its background, black on transparent. AppLoad's Reload does
  not pick up a changed icon; restarting the reMarkable UI does.
- Pipelines (2026-11-15): this repo no longer runs upstream's workflows,
  which built, signed and published the desktop app and its guide. It has
  one release pipeline, `.github/workflows/release.yml`: a tag `v<pkgver>`
  builds the Vellum package with vbuild, signs it and the index with the
  key in the `ONDEVICE_SIGNING_KEY` secret (checked against
  `packaging/keys/remanager-ondevice.rsa.pub`), and publishes them with the
  public key on GitHub Pages. `check.yml` runs the Go tests, the tablet
  build and the screen renderer on every push. `upstream.yml` checks
  upstream's latest release weekly against `.github/upstream-release`
  (v2.0.1 when this was set up) and opens an issue, or fails if issues are
  off. Nothing from upstream is merged automatically.
- Packaging: `packaging/vellum/remanager-ondevice/VELBUILD` builds the app
  from a tagged source archive in Vellum's `ghcr.io/toltec-dev/golang:v4.0`
  image, installing Qt's `rcc` from Debian and letting Go fetch the version
  `go.mod` names. Its `build` and `package` steps were run by hand in that
  image (vbuild itself was not available in the session that wrote it) and
  produced the app folder plus licence files, with the binary stamped
  `0.1.0-r0`. Before submitting: fill in the maintainer, tag `v0.1.0`, and
  run Vellum's `update-checksums.sh`. Vellum accepts pull requests only from
  people, not agents, and no commit there may credit an assistant.
- The app's own package (`remanager-ondevice`) is protected from removal
  from inside the app, like the packages it runs on. Updating it is allowed;
  the new version loads the next time the app opens.
- Status checks (System tab), each left out when it cannot be read:
  hashtable missing or built for another OS version (fix: rebuild);
  tripletap not installed (fix: install it through the usual plan) or left
  paused by a rebuild (fix: turn it on); automatic OS updates on; OS changed
  since Vellum last ran or `vellum reenable` needed; Vellum missing core
  packages. The last three are reported only: fixing them writes to the root
  partition, which this app does not do. Desktop checks left out: xovi and
  launcher state (this app only runs under xovi), timezone (compared against
  a computer's saved setting), and mods incompatible with the next OS
  release (needs the OS release feed; a candidate for later).
- Released: v0.1.0 (2026-11-15) built, signed and published by the
  release workflow, and installed on the Move with `vellum add` from the
  repository at `https://nolsen311.github.io/reManager-ondevice`.
- Packages Vellum can install from repositories other than its own, such as
  this app's, are not in the catalogue the lists come from. The backend
  adds them from `vellum list --available` (newest version of each name)
  and `vellum info -d`, cached for a minute. A name in the catalogue for
  any device is not added; `-doc`, `-dev` and hidden packages are left out.
- The tablet's font lacks some symbols: the check mark showed as an empty
  box on the Move. The screens use only plain text and ordinary punctuation
  ("…", "·", "•", quotes), and draw marks and the text cursor as shapes.
- `ondevice/build.sh` builds the app folder (manifest, icon, `resources.rcc`,
  `backend/entry`) into `ondevice/output/remanager`.

## 8. Milestone 1 and open questions

Milestone 1, in order:

1. `LocalExecutor` with tests. Done.
2. Go AppLoad client with tests against a local socket. Done.
3. Backend: list catalogue, list installed, simulate, install. Done, tested
   against a fake tablet. On the Move (2026-11-15, OS 3.27.3.0): model
   detection, `installed` and `plan` of an already-installed package work.
   Not yet run there: a plan that would install something, `install`, and
   the systemd worker.
4. QML: package list, package detail, confirm, progress. Done; rendered on
   a PC against a mock backend (`ondevice/test/render.sh`) and used on the
   Move.
5. First run on the Move. Done: browsing, a plan, and an install of
   `retaskable` worked, and packages for other OS versions show as
   incompatible. The rehearsal in section 5 was not done first.

Open questions:

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

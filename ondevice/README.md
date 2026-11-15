# reManager on the tablet

An AppLoad app for browsing, installing, updating and removing Vellum mods on
the reMarkable itself, without a computer. It is a fork of
[reManager](https://github.com/rmitchellscott/reManager), the desktop app,
reusing its Go backend with a new e-ink interface.

Only tried on the **reMarkable Paper Pro Move**. Other models are refused
unless the backend is started with `--allow-untested`.

## What it does

- **All mods**: the Vellum catalogue, eight to a page. Mods that cannot be
  installed on this tablet and OS version are hidden behind a toggle.
- **Installed**: what is on the tablet now.
- **Updates**: what Vellum would upgrade, one mod at a time or all at once.
- **System**: restart the reMarkable UI with mods, and rebuild the hashtable
  that UI mods need after an OS update.

Every change shows Vellum's plan first and asks before doing anything.

## What it will not do

- Remove the packages it runs on (Vellum, xovi, qt-resource-rebuilder,
  AppLoad, or this app), or anything whose removal would take one of them
  with it. Do that from a computer.
- Remove a mod that other mods still need. It names them instead.
- Change the OS, SSH, networking or developer mode.

## Needs

- Vellum, xovi and AppLoad already set up, which still takes a computer.
- Wi-Fi for anything that downloads. If an app such as KOReader turned Wi-Fi
  on under airplane mode, restarting the reMarkable UI turns it off again.

## If something goes wrong

xovi never starts at boot. Holding the power button to restart the tablet
always brings back the stock reMarkable UI; start xovi again afterwards, for
example with tripletap.

Installs, removals, updates, restarts and rebuilds run in their own systemd
unit (`remanager-ondevice-job`), so closing the app does not stop them. The
record of the last one is in
`/home/root/.local/state/remanager-ondevice/job.jsonl`.

During a hashtable rebuild the screen restarts twice. The UI between the two
restarts is a temporary one without mods. Leave the tablet alone until the
second restart; triple-tap is turned off until then.

## Building

```sh
ondevice/build.sh
```

Needs Go and Qt 6's `rcc`. The app folder is written to
`ondevice/output/remanager`; copy it to `/home/root/xovi/exthome/appload/` on
the tablet. The design notes are in `docs/on-device/design.md`.

## Licence

GPL-3.0, as reManager.

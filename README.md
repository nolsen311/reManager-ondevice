# reManager on-device

<img src="ondevice/icon.png" alt="" width="96" align="right">

An AppLoad app for browsing, installing, updating and removing
[Vellum](https://github.com/vellum-dev/vellum) mods on the reMarkable
itself, without a computer.

Only tried on the **reMarkable Paper Pro Move**.

**Install:** <https://nolsen311.github.io/reManager-ondevice/> has the
commands for Linux, macOS and Windows, and the signing key with its
fingerprint.

## What it does

- Browse Vellum's catalogue, search it, and install mods. Each change shows
  Vellum's plan first.
- Update mods, including this app, from an Updates tab.
- Remove mods, refusing anything the app itself depends on.
- Restart the reMarkable UI with mods, and rebuild the hashtable.
- Check the tablet: the hashtable, triple-tap, automatic OS updates, and
  whether Vellum needs attention after an OS update.

More in [ondevice/README.md](ondevice/README.md). The design notes are in
[docs/on-device/design.md](docs/on-device/design.md).

## Where it comes from

This is a fork of [reManager](https://github.com/rmitchellscott/reManager)
by Mitchell Scott, the desktop app for managing mods over SSH. It reuses that
app's Go backend (`internal/`) with an e-ink interface in QML
(`ondevice/ui/`) and a backend that runs on the tablet
(`cmd/remanager-ondevice`, `internal/ondevice`). The desktop app's code is
still in the tree but is not built or released here. A weekly workflow
reports new upstream releases so shared fixes can be considered.

Not part of Vellum's catalogue, and not supported by reMarkable.

## Building

```sh
ondevice/build.sh
```

Needs Go and Qt 6's `rcc`. Releases are built by
`.github/workflows/release.yml` from a `v<version>` tag, signed, and
published to the package repository above.

## Licence

GPL-3.0, as reManager.

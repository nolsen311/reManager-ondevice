# Package signing key

`remanager-ondevice.rsa.pub` is the public half of the key that signs this
repository's Vellum packages and their index. Tablets trust it by having a
copy at `/home/root/.vellum/etc/apk/keys/remanager-ondevice.rsa.pub`; `apk`
finds it by that file name, so keep the name.

The private half is never committed. It lives in the repository secret
`ONDEVICE_SIGNING_KEY`, which `.github/workflows/ondevice-package.yml` checks
against this file before it signs anything.

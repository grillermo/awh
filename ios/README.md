# Installing AWH

AWH installs straight from Safari over an `itms-services://` link — no AltStore,
no cable, no Mac awake. Tap a bookmark on the device and the newest release
installs itself.

## On the device, once

1. Open **https://files.chiq.me/files/awh-install.html** in Safari.
2. Share → **Add to Home Screen**.

That bookmark never goes stale: every `./deploy` overwrites the page and the
manifest it links to, in place, at the same URLs. One tap installs whatever the
current release is, over the top of the installed app.

## Releasing

`./deploy` runs on the **macmini**: it publishes by copying into
`~/c/file_server/files/`, the directory files.chiq.me serves, so no upload token
is involved and large .ipa files never cross the Cloudflare tunnel.

```sh
./deploy                         # patch bump (1.0.0 -> 1.0.1)
./deploy minor --summary "Pick OCR samples" --note "Drag across the circles to select"
./deploy 1.4.2 --yes             # explicit version, no confirmation prompt
```

It bumps `MARKETING_VERSION` and `CURRENT_PROJECT_VERSION` in
`ios/AWH/project.yml`, regenerates the Xcode project with XcodeGen, archives and
exports an ad hoc .ipa, publishes the .ipa, icon, manifest and install page, then
commits the bump, tags `awh-ota-vX.Y.Z` and pushes. Every check (clean tree,
`main`, unused tag, publish dir, codesign) runs before anything is touched, and a
failed run puts `project.yml` back. `errors.sqlite3` is the server's live
database and is ignored by the clean-tree check.

`--summary` is the install page's headline and `--note` (repeatable) its
bullets; without them the notes come from commit subjects since the last tag.

Setup, both gitignored:

- `.env` at the repo root: `AWH_UPLOAD_BASE=https://files.chiq.me`. Optional:
  `AWH_PUBLISH_DIR` (defaults to `../file_server/files`), `AWH_TEAM_ID`.
- `ios/AWH/Config/Local.xcconfig` (optional): overrides `DEVELOPMENT_TEAM` or
  `AWH_BUNDLE_ID` from the committed `ios/AWH/Config/AWH.xcconfig`.

`AWH_UNSIGNED=1 ./deploy` builds an unsigned `Payload/` zip instead, for when
signing is broken and you still need a binary.

### Install Latest quick action

Builds from `./deploy` carry the manifest URL (`AWHOTAManifestURL` in
Info.plist, passed as `AWH_OTA_MANIFEST_URL` on the xcodebuild command line,
since an xcconfig reads `//` as a comment). Long-press the app icon →
**Install Latest** starts the install straight away; the app quits itself 1.5 s
later so iOS can replace it. Local Xcode builds have no URL and no quick action.

## Signing

The `.ipa` is ad-hoc-signed with the paid Apple Developer Program team in
`ios/AWH/Config/AWH.xcconfig` (`DEVELOPMENT_TEAM`). The signature lasts a year.
Over SSH the login keychain is locked; the deploy's codesign probe unlocks it
with `~/c/agents-configs/bin/unlock-codesign-keychain.sh`.

Two things this requires:

- **`com.awh.app` must exist as an Identifier on developer.apple.com.**
  Automatic signing creates certificates and profiles on its own, but it cannot
  register App IDs — the export fails with "No profiles for 'com.awh.app' were
  found" until it exists.
- **Every device must be registered on the portal before the build.** An ad-hoc
  profile embeds the UDIDs known at build time, so adding a device means
  registering it and re-running `./deploy`.

`AWH_UNSIGNED=1` falls back to an unsigned archive. That build cannot be
installed from Safari; it exists so a broken signing setup does not block
producing a binary.

## Device checklist after a release

- [ ] The install page shows the version you just shipped.
- [ ] Tapping Install replaces the installed app; it launches on the live page.
- [ ] The status bar is hidden and the page runs edge to edge.
- [ ] Pulling down reloads.
- [ ] A reset on the live page toggles the system music player. (Start something
      playing in Music first — this is the one behaviour the Simulator cannot
      show.)
- [ ] Turn wifi off: the app retries rather than showing an error page, and
      recovers on its own when wifi returns.
- [ ] Leave it running 30 minutes: the screen never sleeps.
- [ ] Background the app for 30 minutes: the device does sleep.
- [ ] Settings.app → AWH → Home URL edits where it points; blank falls back to
      `https://awh.chiq.me/live`.

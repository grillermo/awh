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

```sh
export AWH_UPLOAD_TOKEN=...      # file_to_s3's AUTH_TOKEN
./deploy                         # patch bump
./deploy minor --summary "Faster reconnect" --note "Retry ramps 2/5/10s"
```

## How it hangs together

```
Home Screen bookmark
  -> https://files.chiq.me/files/awh-install.html      (pinned, overwritten)
     -> itms-services://?action=download-manifest&url=
        https://files.chiq.me/files/awh-manifest.plist (pinned, overwritten)
        -> https://files.chiq.me/files/AWH-1.2.3.ipa   (pinned, one per release)
           https://files.chiq.me/files/awh-icon.png    (pinned, overwritten)
```

Only the install page has to keep its URL — it is what gets bookmarked. The
manifest is pinned because the page embeds its URL. The `.ipa` is pinned under a
versioned name for a different reason: `xcodebuild -exportArchive` bakes the
download URL into the manifest it emits, so that URL has to be known *before*
the build.

`files.chiq.me` is the `file_to_s3` service; pinned uploads are `POST
/upload?name=<name>` and carry `cache-control: no-cache` so Cloudflare cannot
serve a previous release's manifest.

## Signing

The `.ipa` is ad-hoc-signed with the paid Apple Developer Program team in
`ios/AWH/project.yml` (`DEVELOPMENT_TEAM`). The signature lasts a year.

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

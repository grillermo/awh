# AWH iOS kiosk — design

Date: 2026-09-06

## Summary

A standalone iOS app whose entire UI is one full-bleed `WKWebView` pinned to
`https://awh.chiq.me/live`, plus a Ruby release pipeline that builds a signed
.ipa and publishes it over the air through `files.chiq.me` (the `file_to_s3`
service). The web-viewer code is extracted from `patatatube/ios`, which loses
its in-app browser in the process.

Three repos change:

| Repo | Change |
|---|---|
| `awh` | gains `ios/` (the app) and `deploy` (the pipeline) |
| `file_to_s3` | gains pinned filenames and `HEAD` support |
| `patatatube` | loses the web bridge |

## Motivation

`awh` is a monitor: `templates/live.html` renders a camera frame with OCR and
auto-reset logic, and calls
`window.webkit.messageHandlers.soundBridge.postMessage("playSound")`
(`templates/live.html:296`) to make a noise when a reset fires. That bridge only
exists inside PatataTube, so watching the awh display today means opening
PatataTube — a video app — and tapping through to its in-app browser.

The page deserves its own app. Extracting it also removes a feature from
PatataTube that never belonged there.

## Non-goals

- No address bar, history, tabs, or bookmarks. This is a kiosk.
- No AltStore source, no `apps.json`, no GitHub Releases, no GitHub Pages.
- No DevLog, Sentry, or bitdrift Capture instrumentation.
- No offline caching. The page is live data; stale frames are worse than none.

---

## Part 1 — The app

### Structure

```
ios/
  AWH/
    project.yml                    # xcodegen spec; source of truth for version + team
    Sources/
      AWHApp.swift                 # @main; idle timer follows scene phase
      KioskView.swift              # root view
      KioskWebView.swift           # UIViewRepresentable + Coordinator
      HomeURL.swift                # UserDefaults "home_url" -> URL, with fallback
      ReloadPolicy.swift           # retry delays; pure, no WebKit
      Info.plist
      LaunchScreen.storyboard
      Settings.bundle/Root.plist   # "Home URL" field in Settings.app
      Assets.xcassets/AppIcon.appiconset/icon-1024.png
    Tests/
      HomeURLTests.swift
      ReloadPolicyTests.swift
      SoundBridgeTests.swift
  ipa_builder.rb
  install.md
  .gitignore                       # AWH.xcodeproj/, xcuserdata/
deploy                             # repo root, Ruby
apple-touch-icon.png               # OTA display image
```

One application target and one test target. No SwiftPM package, no third-party
dependencies. `PatataTubeKit`, Sentry, Capture, Clocks, and ViewInspector are
all left behind; the app is small enough that a package would be ceremony.

`WebAddress.swift` and `WebHistoryStore.swift` are **not** ported. They serve an
address bar and a history menu, neither of which exists here.

### Components

**`HomeURL`** — resolves the page to load.

```swift
enum HomeURL {
    static let fallback = URL(string: "https://awh.chiq.me/live")!
    static func resolve(from defaults: UserDefaults = .standard) -> URL
}
```

Reads the `home_url` key, trims whitespace, and requires the result to parse as
a `URL` with a non-empty host. Anything else — empty string, `"cat videos"`,
whitespace — yields `fallback`. The `UserDefaults` parameter is injected so
tests can drive it with a throwaway suite.

**`ReloadPolicy`** — how long to wait before retrying a failed load.

```swift
struct ReloadPolicy {
    static func delay(afterFailures n: Int) -> TimeInterval  // 2, 5, 10, 10, ...
}
```

A pure function, so the retry schedule is tested without WebKit or a timer. The
ramp recovers a wifi blip in two seconds while settling a genuinely-down server
into a ten-second poll.

**`KioskWebView`** — a `UIViewRepresentable` wrapping `WKWebView`.

Configuration: a `WKUserContentController` with the `soundBridge` handler,
`allowsInlineMediaPlayback = true`, opaque black background, and a
`UIRefreshControl` on the web view's `scrollView`. The live page is
`overflow: hidden`, so pull-to-refresh is the only thing that scroll view does.

`dismantleUIView` removes the script message handler. Without it the user
content controller retains the coordinator and neither it nor the web view ever
deallocates — the same fix `WebBridgeView` carries today.

`Coordinator` conforms to two protocols:

- `WKScriptMessageHandler` — accepts only `name == "soundBridge"` with body
  `"playSound"`, then toggles `MPMusicPlayerController.systemMusicPlayer`
  between `.playing` and paused. Every other message is ignored. The accept/
  reject decision is a static predicate so it can be unit-tested without a
  live `WKScriptMessage`.
- `WKNavigationDelegate` — `didFinish` ends the refresh control and zeroes the
  failure count; `didFail` and `didFailProvisionalNavigation` increment it and
  schedule a reload after `ReloadPolicy.delay(afterFailures:)`. Scheduling
  replaces any pending retry, so a burst of failures cannot stack timers.

**`AWHApp`** — `@main`, a single `WindowGroup` containing `KioskView`.

Sets `UIApplication.shared.isIdleTimerDisabled = true` when the scene becomes
active and `false` when it backgrounds, so a backgrounded app never holds the
assertion.

### Info.plist

| Key | Value | Why |
|---|---|---|
| `NSAppleMusicUsageDescription` | "AWH controls the system music player when the live page asks it to." | Required; the `MPMusicPlayerController` call fails without it |
| `NSAppTransportSecurity.NSAllowsLocalNetworking` | `true` | Lets a `http://192.168.x.x:3060/live` override work |
| `UIStatusBarHidden` | `true` | Full-bleed display |
| `UIViewControllerBasedStatusBarAppearance` | `false` | Makes the above take effect |
| `UILaunchStoryboardName` | `LaunchScreen` | |
| `UISupportedInterfaceOrientations` (both idioms) | all four | Wall mounts go either way |

No `UIBackgroundModes`. The app never plays audio itself — it toggles the
system music player, which owns its own background audio.

### Build settings (`project.yml`)

```
PRODUCT_BUNDLE_IDENTIFIER  com.awh.app
DEVELOPMENT_TEAM           Q3WS4MWCW3
CODE_SIGN_STYLE            Automatic
deploymentTarget iOS       18.0
TARGETED_DEVICE_FAMILY     "1,2"
SWIFT_VERSION              "6.0"
MARKETING_VERSION          "1.0.0"
CURRENT_PROJECT_VERSION    "1"
```

`MARKETING_VERSION` and `CURRENT_PROJECT_VERSION` are what `deploy` rewrites;
`project.yml` is the single source of truth for both the version and the signing
team, so a build and its manifest can never disagree.

### Settings.bundle

`Root.plist` declares one `PSTextFieldSpecifier` with identifier `home_url`,
title "Home URL", `DefaultValue` `https://awh.chiq.me/live`, autocapitalization
and autocorrection off, and keyboard type `URL`. It surfaces in Settings.app
under AWH.

`AWHApp` calls `UserDefaults.standard.register(defaults:)` on launch so the
field shows the default before it has ever been edited.

### Icon

`awh-icon.svg` in the repo, rendered to `icon-1024.png` for the asset catalog
and `apple-touch-icon.png` at the repo root for the OTA display image and the
install page. A placeholder to start with; replaceable by dropping in new files
and re-running `deploy`.

### Testing

The three testable units are deliberately separated from the WebKit plumbing so
the suite needs no UI automation:

- `HomeURLTests` — the fallback, a valid override, a hostless string, an empty
  string, leading/trailing whitespace.
- `ReloadPolicyTests` — the 2/5/10 ramp and that it plateaus rather than growing.
- `SoundBridgeTests` — the message predicate accepts `("soundBridge",
  "playSound")` and rejects a wrong name, a wrong body, and a non-`String` body.

Run with `xcodebuild test -project AWH.xcodeproj -scheme AWH -destination
'platform=iOS Simulator,name=iPhone 17 Pro'`.

Manual checks, on device, recorded in `ios/install.md`:

1. Launch with no override: the live page loads and fills the screen, no status bar.
2. Trigger a reset on the page: the system music player toggles.
3. Pull down: the page reloads.
4. Stop the awh server: the app retries rather than showing WebKit's error page,
   and recovers on its own when the server returns.
5. Leave it running 30 minutes: the screen never sleeps.
6. Background the app for 30 minutes: the device *does* sleep.
7. Set Settings.app → AWH → Home URL to a LAN address: it loads after relaunch.
8. Clear the field: it falls back to `awh.chiq.me/live`.

---

## Part 2 — The release pipeline

### Delivery chain

```
Home Screen bookmark
  -> https://files.chiq.me/files/awh-install.html        (stable, overwritten)
     -> itms-services://?action=download-manifest&url=
        https://files.chiq.me/files/awh-manifest.plist   (stable, overwritten)
        -> https://files.chiq.me/files/AWH-1.2.3.ipa     (one per release)
           https://files.chiq.me/files/awh-icon.png      (stable, overwritten)
```

Only the install page must never move — it is what gets bookmarked. The manifest
is pinned too because the page embeds its URL, and pinning both means the page's
contents never change either.

The .ipa is pinned as well, under a name carrying its version. This is forced by
`ipa_builder`: the export bakes the .ipa's final URL into the manifest, so that
URL must be known *before* the build, while `file_to_s3` normally assigns URLs
at upload time. A version-derived name is predictable before the build and,
because the version is in it, still never overwrites a past release. So one
`?name=` mechanism serves all four uploads, and the guard against clobbering a
release is the existing "refuse a version that already has a tag" check.

`itms-services://` requires the manifest be served over HTTPS with a certificate
iOS trusts; `files.chiq.me` is behind Cloudflare with a valid cert, serves
HTTP/2, and advertises `accept-ranges: bytes`. Verified 2026-09-06.

### `ios/ipa_builder.rb`

Ported from `patatatube/ios/ipa_builder.rb` with the names changed
(`APP_NAME = "AWH"`, `AWH_UNSIGNED`, `AWH_TEAM_ID`) and the `instrumented:`
parameter deleted along with DevLog.

Everything else carries over unchanged, and for the reasons the original
documents:

- `DEVELOPER_DIR` resolution, because `xcodebuild` needs a full Xcode rather
  than the Command Line Tools.
- `xcodegen generate` before every build, so the checked-in `project.yml` is
  always what gets built.
- `xcodebuild archive` at `-configuration Release`, `-destination
  'generic/platform=iOS'`, `-allowProvisioningUpdates`.
- `-exportArchive` with `method: release-testing` (Xcode 15.3+'s name for
  ad hoc), `signingStyle: automatic`, `thinning: <none>` — a thinned export
  produces per-device variants that one download URL cannot serve.
- The `manifest` dict in `ExportOptions.plist`. This is the reason to export at
  all rather than zipping a `Payload/` by hand: it makes `xcodebuild` emit the
  OTA `manifest.plist`, so the manifest can never describe a different binary
  than the one shipped. Its `appURL` must be the .ipa's final public URL, which
  the caller must therefore know *before* the build.
- `AWH_UNSIGNED=1` falls back to a hand-zipped unsigned archive, for when
  signing breaks and something still has to go out.

`deploy` therefore passes `manifest: {app_url:, icon_url:}` built from the
version it is about to release — `https://files.chiq.me/files/AWH-X.Y.Z.ipa` —
and the upload in step 5 pins that exact name. See "Delivery chain" above.

`refresh-ipa.rb` is **not** ported. It exists to drop an .ipa in iCloud for a
manual AltStore sideload, and there is no AltStore in this design.

### `deploy`

Ruby, at the repo root, roughly 180 lines. Usage mirrors PatataTube's:

```
./deploy                    # bump patch
./deploy patch|minor|major
./deploy 1.4.2              # explicit version
./deploy --yes              # skip the confirmation prompt
./deploy minor --summary "Faster reconnect" --note "Retry ramps 2/5/10s"
```

Steps:

1. Require `AWH_UPLOAD_TOKEN` in the environment; die with instructions if unset.
2. Refuse to run on a non-default branch, and refuse a version that already has
   a tag.
3. Read `MARKETING_VERSION` from `project.yml`, compute the next version and
   build number, write both back.
4. Build the .ipa via `IpaBuilder.build(manifest: {app_url:, icon_url:})`.
5. Upload the .ipa to `files.chiq.me`; upload `awh-icon.png` pinned.
6. Rewrite the exported `manifest.plist` with the real .ipa URL, bundle id,
   version, and title; write a copy to `ios/manifest.plist` for the record and
   upload it pinned as `awh-manifest.plist`.
7. Generate `awh-install.html` — an HTML page with the `itms-services://` link,
   the version, and the build date — and upload it pinned.
8. Commit the version bump and `ios/manifest.plist`, tag `vX.Y.Z`, push.
9. Print the stable install URL.

Release notes come from `--summary` and repeated `--note`, falling back to
commit subjects since the last `v*` tag with merge/release noise filtered out.
They land in the install page, so the person tapping the bookmark can see what
changed.

The push tolerates a push the `git-sync` post-commit hook already made: on
failure, ask the remote what it holds, and continue if it matches local `HEAD`.
Without this the script can die *after* publishing a manifest, leaving the
install page advertising a build whose commit was never pushed.

No `gh`, no GitHub Release, no GitHub Pages, no `apps.json`.

### Uploading

```
POST https://files.chiq.me/upload
Authorization: Bearer $AWH_UPLOAD_TOKEN
multipart/form-data; file=@<path>
```

Returns `200 text/plain` with the file's URL. `deploy` uses `curl` via
`IO.popen` rather than adding an HTTP gem, and dies on any non-200.

---

## Part 3 — `file_to_s3` changes

Two changes to `app.rb`, both small, both independently useful.

### Pinned filenames

`build_local_filename` currently always prepends `SecureRandom.uuid`, so no
uploaded file has a stable URL. Add an opt-in:

- `POST /upload?name=awh-manifest.plist` stores the file at exactly that name,
  overwriting any existing file, and returns
  `https://files.chiq.me/files/awh-manifest.plist`.
- The name is run through the existing `sanitize_filename` (`File.basename` plus
  a `[^\w.\-]` scrub), so it cannot escape `files/`.
- Without `?name=`, behaviour is unchanged: UUID prefix, no overwrite.

Pinned responses set `cache-control: no-cache` so Cloudflare and iOS revalidate
rather than serving a previous release's manifest.

### `HEAD` support

`case [req.request_method, req.path_info]` matches only `"GET"` for `/files/`,
so a `HEAD` falls through to `not_found`. Verified 2026-09-06: `GET
/files/…Install_PatataTube.html` returns 200 while `HEAD` on the same URL
returns 404. (`HEAD` on a `.apk` returns 200 only because Cloudflare caches that
extension and answers from its own cache.)

iOS's install daemon issues `HEAD` and `Range` requests for both the manifest
and the .ipa, so this must work. Change the pattern to `in ["GET" | "HEAD",
path]` and return the same status and headers with an empty body for `HEAD`.

### Notes

- `app.rb` currently has uncommitted local changes. These edits land on top of
  them; review before committing.
- The README documents a live `AUTH_TOKEN` value in plaintext. Out of scope
  here, but worth rotating.
- The README's "uploads larger than 25 MB are rejected" is not enforced anywhere
  in `app.rb`. Irrelevant at this size — the .ipa will be 2–5 MB, against
  PatataTube's 4.4 MB with far more code — but the claim is stale.

---

## Part 4 — Removing the web bridge from patatatube

One commit in that repo, verified by `xcodebuild test -project
PatataTube.xcodeproj -scheme PatataTube`.

| File | Change |
|---|---|
| `ios/PatataTube/Sources/WebBridgeView.swift` | delete |
| `ios/PatataTubeKit/Sources/PatataTubeKit/WebAddress.swift` | delete |
| `ios/PatataTubeKit/Sources/PatataTubeKit/WebHistoryStore.swift` | delete |
| `VideoGridView.swift` | remove the live-page toolbar button (~:177), `showWebBridge` (:180), the `onChange` (:364), the `fullScreenCover` (:367–369) |
| `AppModel.swift` | remove `webBridgeRequests` and the `.openWeb` quick-action branch (:110, :120, :234) |
| `QuickActions.swift` | remove `case openWeb` (:7) |
| `PatataTube/Sources/Info.plist` | remove the `com.patatatube.openWeb` shortcut item (:45) |
| `project.yml` | remove the same shortcut item (:51) and `NSAppleMusicUsageDescription` (:79) |
| `PatataTube/Sources/Info.plist` | remove `NSAppleMusicUsageDescription` (:28) |
| `ios/README.md` | remove the "Web bridge address bar" section |

`NSAppleMusicUsageDescription` goes because the sound bridge was its only user;
PatataTube touches `MPMusicPlayerController` nowhere else.

`AppIconSmall.imageset` becomes unreferenced — `WebBridgeView` was its only
consumer — but stays. It is a plausible thing to want back, and it costs 30 KB.

No test files reference the web bridge, so the test target needs no changes.

PatataTube keeps its own `deploy`, `ipa_builder.rb`, `refresh-ipa.rb`,
`apps.json`, and `manifest.plist`. It still ships as an app.

---

## One-time setup

Owner actions, outside this work, both confirmed done or accepted:

1. `com.awh.app` registered as an Identifier on developer.apple.com. Automatic
   signing creates certificates and profiles but cannot create App IDs, so the
   export fails with "No profiles for 'com.awh.app' were found" without it.
2. The kiosk device's UDID registered on the portal. An ad-hoc profile only
   covers devices registered at build time; adding a device means re-running
   `./deploy`.
3. `AWH_UPLOAD_TOKEN` exported in the shell that runs `./deploy`, matching
   `file_to_s3`'s `AUTH_TOKEN`.

## Order of work

1. `file_to_s3`: pinned names and `HEAD`, with tests, deployed to
   `files.chiq.me`. Everything downstream depends on it.
2. `awh/ios`: the app, built and running in the Simulator against a local
   `./serve`.
3. `awh/ios/ipa_builder.rb` and `awh/deploy`: first real release, installed on
   the device from the bookmark.
4. `patatatube`: remove the web bridge.

Steps 2 and 4 are independent of each other; 4 should not land until 3 has put a
working app on the device.

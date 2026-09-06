# AWH iOS Kiosk Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship a standalone iOS app whose entire UI is one full-bleed `WKWebView` pinned to `https://awh.chiq.me/live`, installable over the air from a permanent bookmark.

**Architecture:** A single-target SwiftUI app with no third-party dependencies, extracted from PatataTube's in-app web bridge. A Ruby pipeline builds an ad-hoc-signed `.ipa` and publishes it — plus an OTA manifest and an install page — to `files.chiq.me` (the `file_to_s3` Rack service), which first gains the ability to serve stable, overwritable filenames.

**Tech Stack:** Swift 6 / SwiftUI / WebKit / MediaPlayer, XcodeGen, `xcodebuild`, Ruby 3.2 (stdlib only, `curl` for HTTP), Rack 3.1, Minitest, Swift Testing.

**Spec:** `docs/superpowers/specs/2026-09-06-awh-ios-kiosk-design.md`

## Global Constraints

- Three repos change: `/Users/grillermo/c/awh`, `/Users/grillermo/c/file_to_s3`, `/Users/grillermo/c/patatatube`. Every task names its repo. Never commit across repos in one commit.
- App identity is fixed: display name `AWH`, bundle identifier `com.awh.app`, `DEVELOPMENT_TEAM` `Q3WS4MWCW3`, deployment target iOS `18.0`, `TARGETED_DEVICE_FAMILY` `"1,2"`, `SWIFT_VERSION` `"6.0"`.
- Default page is exactly `https://awh.chiq.me/live`.
- The app takes **no third-party dependencies**. No SwiftPM package, no Sentry, no bitdrift Capture, no ViewInspector, no DevLog.
- `file_to_s3` takes **no new gems**. Tests use Minitest (Ruby stdlib) and `Rack::MockRequest` (already a dependency via `rack ~> 3.1`).
- `deploy` and `ipa_builder.rb` use the Ruby stdlib plus `curl`. No HTTP gems, and no `gh` CLI.
- iOS test framework is **Swift Testing** (`import Testing`, `@Test`, `#expect`), matching PatataTube. Not XCTest.
- Swift 6 strict concurrency: `WKNavigationDelegate` / `WKScriptMessageHandler` conformances live on a **non-isolated** `NSObject` coordinator that hops to the main actor with `MainActor.assumeIsolated { }`. Marking the coordinator `@MainActor` will not compile against these protocols.
- Pinned upload names are exactly: `awh-install.html`, `awh-manifest.plist`, `awh-icon.png`, and `AWH-<version>.ipa`.

---

## File Structure

**`/Users/grillermo/c/file_to_s3`**

| File | Responsibility |
|---|---|
| `app.rb` (modify) | Add `HEAD` support, `?name=` pinned uploads, `FILES_DIR` override |
| `test/app_test.rb` (create) | In-process Rack tests; no running server, no network |
| `Rakefile` (create) | `rake test` entry point |

**`/Users/grillermo/c/awh`**

| File | Responsibility |
|---|---|
| `ios/AWH/project.yml` | XcodeGen spec; source of truth for version, team, bundle id |
| `ios/AWH/Sources/HomeURL.swift` | Resolve the page to load from `UserDefaults`, with fallback |
| `ios/AWH/Sources/ReloadPolicy.swift` | Retry delay schedule; pure function |
| `ios/AWH/Sources/SoundBridgeMessage.swift` | Which JS bridge messages are accepted |
| `ios/AWH/Sources/KioskController.swift` | Main-actor state: retry counter, retry task, web view handle |
| `ios/AWH/Sources/KioskWebView.swift` | `UIViewRepresentable` + non-isolated coordinator |
| `ios/AWH/Sources/KioskView.swift` | Root view |
| `ios/AWH/Sources/AWHApp.swift` | `@main`; defaults registration, idle timer |
| `ios/AWH/Sources/Info.plist` | Usage strings, ATS, status bar, orientations |
| `ios/AWH/Sources/Settings.bundle/Root.plist` | "Home URL" field in Settings.app |
| `ios/AWH/Tests/*.swift` | Swift Testing suites for the three pure units |
| `ios/ipa_builder.rb` | Build + ad-hoc-sign + export an `.ipa` and its OTA manifest |
| `ios/uploader.rb` | Pinned uploads to `files.chiq.me` via `curl` |
| `ios/install.md` | How the OTA route works; manual device checklist |
| `deploy` | Version bump, build, upload, commit, tag, push |
| `apple-touch-icon.png` | OTA display image and install-page icon |

**`/Users/grillermo/c/patatatube`** — deletions only; see Task 10.

---

## Task 1: `file_to_s3` — test harness and `HEAD` support

**Repo:** `/Users/grillermo/c/file_to_s3`

**Files:**
- Create: `test/app_test.rb`
- Create: `Rakefile`
- Modify: `app.rb` (the `call` dispatch table, `serve_file`, `files_dir`)

**Interfaces:**
- Consumes: nothing.
- Produces: `rake test` runs the suite. `FileToS3App#call` answers `HEAD /files/:name` with the same status and headers as `GET` and an empty body. `files_dir` honours `ENV["FILES_DIR"]`.

**Context:** `app.rb:19` dispatches on `case [req.request_method, req.path_info]` and matches only `"GET"` for `/files/`, so every `HEAD` falls through to `not_found`. Verified against production 2026-09-06: `GET /files/…Install_PatataTube.html` → 200, `HEAD` on the same URL → 404. iOS's install daemon issues `HEAD` for both the manifest and the `.ipa`, so this blocks the whole OTA route.

`app.rb` has uncommitted local changes on disk. Leave them alone; these edits sit on top.

- [ ] **Step 1: Add the Rakefile**

```ruby
# Rakefile
require "rake/testtask"

Rake::TestTask.new(:test) do |t|
  t.libs << "test"
  t.test_files = FileList["test/*_test.rb"]
  t.warning = false
end

task default: :test
```

- [ ] **Step 2: Write the failing tests**

Create `test/app_test.rb`. `Rack::MockRequest.env_for` builds a real multipart body when a param is a `Rack::Multipart::UploadedFile`, so no gem beyond `rack` is needed.

```ruby
# frozen_string_literal: true

ENV["AUTH_TOKEN"] = "test-token"

require "minitest/autorun"
require "fileutils"
require "rack"
require "rack/mock_request"
require "tmpdir"
require_relative "../app"

class AppTest < Minitest::Test
  def setup
    @dir = Dir.mktmpdir("file-to-s3-test-")
    ENV["FILES_DIR"] = @dir
    @app = FileToS3App.new
  end

  def teardown
    ENV.delete("FILES_DIR")
    FileUtils.remove_entry(@dir)
  end

  # --- helpers ---------------------------------------------------------

  def write_temp(name, content)
    path = File.join(@dir, "..", "src-#{name}")
    File.write(path, content)
    path
  end

  def upload(path, filename, query: "")
    file = Rack::Multipart::UploadedFile.new(
      path, "application/octet-stream", true, filename: filename
    )
    env = Rack::MockRequest.env_for(
      "http://files.example/upload#{query}",
      method: "POST",
      params: { "file" => file },
      "HTTP_AUTHORIZATION" => "Bearer test-token"
    )
    @app.call(env)
  end

  def get(path)
    @app.call(Rack::MockRequest.env_for("http://files.example#{path}", method: "GET"))
  end

  def head(path)
    @app.call(Rack::MockRequest.env_for("http://files.example#{path}", method: "HEAD"))
  end

  def stored(name, content)
    File.write(File.join(@dir, name), content)
  end

  # --- files_dir override ----------------------------------------------

  def test_uploads_land_in_the_files_dir_override
    src = write_temp("a.txt", "hello")
    status, _, body = upload(src, "a.txt")

    assert_equal 200, status
    name = body.join.split("/files/").last
    assert_path_exists File.join(@dir, name)
  end

  # --- HEAD -------------------------------------------------------------

  def test_head_on_an_existing_file_matches_get
    stored("thing.plist", "<plist/>")

    get_status, get_headers, = get("/files/thing.plist")
    head_status, head_headers, head_body = head("/files/thing.plist")

    assert_equal 200, get_status
    assert_equal get_status, head_status
    assert_equal get_headers["content-type"], head_headers["content-type"]
    assert_equal "", head_body.to_a.join
  end

  def test_head_reports_the_length_without_a_body
    stored("thing.ipa", "0123456789")

    _, headers, body = head("/files/thing.ipa")

    assert_equal "10", headers["content-length"]
    assert_equal "", body.to_a.join
  end

  def test_head_on_a_missing_file_is_404
    status, = head("/files/nope.txt")

    assert_equal 404, status
  end
end
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `cd /Users/grillermo/c/file_to_s3 && rake test`

Expected: `test_head_on_an_existing_file_matches_get` and `test_head_reports_the_length_without_a_body` FAIL (they get 404), and `test_uploads_land_in_the_files_dir_override` FAILS because uploads still go to the repo's real `files/`. `test_head_on_a_missing_file_is_404` passes for the wrong reason — that is fine, it is a regression guard.

- [ ] **Step 4: Make `files_dir` overridable**

In `app.rb`, replace:

```ruby
  def files_dir
    File.join(__dir__, "files")
  end
```

with:

```ruby
  # Overridable so tests can point at a scratch directory instead of the
  # repo's real files/.
  def files_dir
    ENV.fetch("FILES_DIR") { File.join(__dir__, "files") }
  end
```

- [ ] **Step 5: Accept `HEAD` in the dispatch table**

In `app.rb`, replace:

```ruby
    in ["GET", path] if path.start_with?("/files/")
      serve_file(path.delete_prefix("/files/"))
```

with:

```ruby
    in ["GET" | "HEAD", path] if path.start_with?("/files/")
      serve_file(path.delete_prefix("/files/"), head: req.request_method == "HEAD")
```

- [ ] **Step 6: Answer `HEAD` with headers but no body**

In `app.rb`, replace `serve_file` with:

```ruby
  # iOS's install daemon issues HEAD (and Range) for the OTA manifest and the
  # .ipa before it downloads either, so HEAD must answer with the same status
  # and headers as GET — just without the body.
  def serve_file(filename, head: false)
    safe_name = File.basename(filename)
    path = File.join(files_dir, safe_name)

    return not_found unless File.file?(path)

    content_type = Rack::Mime.mime_type(File.extname(safe_name), "application/octet-stream")
    headers = {
      "content-type" => content_type,
      "content-length" => File.size(path).to_s
    }

    return [200, headers, []] if head

    [200, headers, [File.binread(path)]]
  end
```

- [ ] **Step 7: Run the tests to verify they pass**

Run: `cd /Users/grillermo/c/file_to_s3 && rake test`

Expected: 4 runs, 0 failures, 0 errors.

- [ ] **Step 8: Confirm the repo's real `files/` was not touched**

Run: `cd /Users/grillermo/c/file_to_s3 && git status --porcelain files/`

Expected: no output. If any file appears, the `FILES_DIR` override is not being honoured — fix before committing.

- [ ] **Step 9: Commit**

```bash
cd /Users/grillermo/c/file_to_s3
git add Rakefile test/app_test.rb app.rb
git commit -m "Serve HEAD for /files/ and allow a FILES_DIR override

iOS's install daemon issues HEAD for an OTA manifest and .ipa before
downloading either; the dispatch table matched only GET, so every HEAD
404'd. FILES_DIR lets the new tests run against a scratch directory."
```

---

## Task 2: `file_to_s3` — pinned filenames

**Repo:** `/Users/grillermo/c/file_to_s3`

**Files:**
- Modify: `app.rb` (`handle_upload`, `build_local_filename`, `text_response`)
- Modify: `test/app_test.rb` (append cases)
- Modify: `README.md` (document `?name=`)

**Interfaces:**
- Consumes: Task 1's `FILES_DIR` override and test harness.
- Produces: `POST /upload?name=<name>` stores at exactly `<name>`, overwrites in place, returns `https://<host>/files/<name>`, and sets `cache-control: no-cache`. Without `?name=`, behaviour is unchanged (UUID prefix, never overwrites).

**Context:** `build_local_filename` always prepends `SecureRandom.uuid`, so nothing has a stable URL. The OTA route needs three: the install page (it gets bookmarked), the manifest (the page embeds its URL), and the icon. The `.ipa` needs a *predictable* name for a different reason — `xcodebuild -exportArchive` bakes the download URL into the manifest it emits, so the URL must be known before the build.

`cache-control: no-cache` matters because `files.chiq.me` sits behind Cloudflare. Without it an overwritten manifest can be served from the edge cache and install the previous release.

- [ ] **Step 1: Write the failing tests**

Append to `test/app_test.rb`, inside `class AppTest`:

```ruby
  # --- pinned uploads ---------------------------------------------------

  def test_pinned_upload_uses_the_exact_name
    src = write_temp("m.plist", "<plist/>")

    status, _, body = upload(src, "ignored.plist", query: "?name=awh-manifest.plist")

    assert_equal 200, status
    assert_equal "http://files.example/files/awh-manifest.plist", body.join
    assert_path_exists File.join(@dir, "awh-manifest.plist")
  end

  def test_pinned_upload_overwrites_in_place
    first = write_temp("v1.plist", "one")
    second = write_temp("v2.plist", "two")

    _, _, first_body = upload(first, "x", query: "?name=awh-manifest.plist")
    _, _, second_body = upload(second, "x", query: "?name=awh-manifest.plist")

    assert_equal first_body.join, second_body.join
    assert_equal "two", File.read(File.join(@dir, "awh-manifest.plist"))
    assert_equal 1, Dir.children(@dir).size
  end

  def test_pinned_upload_sets_no_cache
    src = write_temp("m.plist", "<plist/>")

    _, headers, = upload(src, "x", query: "?name=awh-manifest.plist")

    assert_equal "no-cache", headers["cache-control"]
  end

  def test_pinned_name_cannot_escape_the_files_dir
    src = write_temp("m.plist", "pwned")

    status, _, body = upload(src, "x", query: "?name=../../etc/awh.plist")

    assert_equal 200, status
    assert_equal "http://files.example/files/awh.plist", body.join
    assert_path_exists File.join(@dir, "awh.plist")
  end

  def test_unpinned_upload_still_gets_a_uuid_prefix
    src = write_temp("a.txt", "hello")

    _, headers, body = upload(src, "a.txt")

    name = body.join.split("/files/").last
    assert_match(/\A[0-9a-f-]{36}-a\.txt\z/, name)
    assert_nil headers["cache-control"]
  end

  def test_a_blank_name_falls_back_to_the_uuid_prefix
    src = write_temp("a.txt", "hello")

    _, _, body = upload(src, "a.txt", query: "?name=")

    name = body.join.split("/files/").last
    assert_match(/\A[0-9a-f-]{36}-a\.txt\z/, name)
  end
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd /Users/grillermo/c/file_to_s3 && rake test`

Expected: the five pinned-upload tests FAIL (names come back UUID-prefixed, no `cache-control` header). `test_unpinned_upload_still_gets_a_uuid_prefix` PASSES already — it is the regression guard for the existing behaviour.

- [ ] **Step 3: Implement pinned names**

In `app.rb`, replace `handle_upload` with:

```ruby
  def handle_upload(req)
    uploaded = extract_uploaded_file(req)
    return uploaded unless uploaded.is_a?(Hash)

    pinned = pinned_name(req)
    filename = pinned || build_local_filename(uploaded[:filename])
    FileUtils.mkdir_p(files_dir)

    uploaded[:tempfile].rewind
    File.open(File.join(files_dir, filename), "wb") do |file|
      IO.copy_stream(uploaded[:tempfile], file)
    end

    # A pinned file is overwritten in place, so its URL is stable and every
    # cache in front of it — Cloudflare especially — must revalidate rather
    # than serve the previous release.
    headers = pinned ? { "cache-control" => "no-cache" } : {}
    text_response(200, file_url(req, filename), headers)
  end
```

Add, in the private section next to `build_local_filename`:

```ruby
  # ?name=awh-manifest.plist stores the upload under exactly that name,
  # overwriting any previous one, so the URL never changes. Runs through the
  # same sanitizer as an uploaded filename, so it cannot escape files_dir.
  def pinned_name(req)
    requested = req.params["name"]
    return nil if requested.to_s.strip.empty?

    sanitize_filename(requested)
  end
```

- [ ] **Step 4: Let `text_response` carry extra headers**

In `app.rb`, replace:

```ruby
  def text_response(status, body)
    [status, { "content-type" => "text/plain; charset=utf-8" }, [body]]
  end
```

with:

```ruby
  def text_response(status, body, extra_headers = {})
    headers = { "content-type" => "text/plain; charset=utf-8" }.merge(extra_headers)
    [status, headers, [body]]
  end
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `cd /Users/grillermo/c/file_to_s3 && rake test`

Expected: 10 runs, 0 failures, 0 errors.

Note on `test_pinned_name_cannot_escape_the_files_dir`: `sanitize_filename` does `File.basename` then scrubs `[^\w.\-]`, so `../../etc/awh.plist` becomes `awh.plist`. Confirm the assertion passes rather than adjusting it — if it does not, the sanitizer is the bug.

- [ ] **Step 6: Document it in the README**

Append to the "API" section of `README.md`:

````markdown
To store the file under a stable name that overwrites any previous upload —
so the URL never changes — pass `?name=`:

```sh
curl -X POST "https://files.chiq.me/upload?name=awh-manifest.plist" \
  -H "Authorization: Bearer $AUTH_TOKEN" \
  -F "file=@ios/manifest.plist"
```

The response is always `https://files.chiq.me/files/awh-manifest.plist`, and
pinned responses carry `cache-control: no-cache` so caches in front of the
service revalidate instead of serving a stale copy. Without `?name=`, uploads
keep their UUID prefix and never overwrite anything.
````

- [ ] **Step 7: Commit**

```bash
cd /Users/grillermo/c/file_to_s3
git add app.rb test/app_test.rb README.md
git commit -m "Add ?name= pinned uploads with a stable, overwritable URL

The OTA install route needs URLs that never move: the page that gets
bookmarked, the manifest it links to, and the icon. Pinned responses send
cache-control: no-cache so Cloudflare cannot serve a previous release."
```

- [ ] **Step 8: Deploy to `files.chiq.me` and verify against production**

Deploy by whatever means this service is normally updated (pull and restart on the host). Then verify — this is the gate for every later task:

```bash
TOKEN=<the AUTH_TOKEN from the server>
echo "<plist/>" > /tmp/awh-probe.plist

curl -sS -X POST "https://files.chiq.me/upload?name=awh-probe.plist" \
  -H "Authorization: Bearer $TOKEN" -F "file=@/tmp/awh-probe.plist"
# expect exactly: https://files.chiq.me/files/awh-probe.plist

curl -sS -o /dev/null -w "GET=%{http_code}\n" https://files.chiq.me/files/awh-probe.plist
curl -sS -o /dev/null -w "HEAD=%{http_code}\n" -I https://files.chiq.me/files/awh-probe.plist
# expect GET=200 and HEAD=200
```

Both must pass before Task 9. Do not commit anything for this step.

---

## Task 3: `awh/ios` — project scaffolding and `HomeURL`

**Repo:** `/Users/grillermo/c/awh`

**Files:**
- Create: `ios/AWH/project.yml`
- Create: `ios/AWH/Sources/HomeURL.swift`
- Create: `ios/AWH/Sources/Info.plist`
- Create: `ios/AWH/Sources/LaunchScreen.storyboard`
- Create: `ios/AWH/Sources/Assets.xcassets/Contents.json`
- Create: `ios/AWH/Sources/Assets.xcassets/AppIcon.appiconset/Contents.json`
- Create: `ios/AWH/Sources/Assets.xcassets/AppIcon.appiconset/icon-1024.png`
- Create: `ios/AWH/Sources/AWHApp.swift` (placeholder root, replaced in Task 5)
- Create: `ios/AWH/Tests/HomeURLTests.swift`
- Create: `ios/.gitignore`
- Create: `apple-touch-icon.png`

**Interfaces:**
- Consumes: nothing.
- Produces: `HomeURL.key: String` (`"home_url"`), `HomeURL.fallback: URL`, `HomeURL.resolve(from: UserDefaults = .standard) -> URL`. An `AWH` scheme that builds and a `AWHTests` target that runs.

**Prerequisite:** `brew install xcodegen` (already present at `/opt/homebrew/bin/xcodegen`).

- [ ] **Step 1: Create the gitignore**

`ios/.gitignore`:

```
AWH/AWH.xcodeproj/
xcuserdata/
*.xcworkspace/xcuserdata/
```

`project.pbxproj` is generated from `project.yml` on every build, so it is never committed.

- [ ] **Step 2: Create `project.yml`**

```yaml
name: AWH
options:
  bundleIdPrefix: com.awh
  deploymentTarget:
    iOS: "18.0"
targets:
  AWH:
    type: application
    platform: iOS
    deploymentTarget: "18.0"
    sources:
      - Sources
    info:
      path: Sources/Info.plist
      properties:
        CFBundleShortVersionString: $(MARKETING_VERSION)
        CFBundleVersion: $(CURRENT_PROJECT_VERSION)
        UILaunchStoryboardName: LaunchScreen
        UIStatusBarHidden: true
        UIViewControllerBasedStatusBarAppearance: false
        NSAppleMusicUsageDescription: AWH controls the system music player when the live page asks it to.
        NSAppTransportSecurity:
          NSAllowsLocalNetworking: true
        "UISupportedInterfaceOrientations~iphone":
          - UIInterfaceOrientationPortrait
          - UIInterfaceOrientationLandscapeLeft
          - UIInterfaceOrientationLandscapeRight
        "UISupportedInterfaceOrientations~ipad":
          - UIInterfaceOrientationPortrait
          - UIInterfaceOrientationPortraitUpsideDown
          - UIInterfaceOrientationLandscapeLeft
          - UIInterfaceOrientationLandscapeRight
    settings:
      base:
        DEVELOPMENT_TEAM: Q3WS4MWCW3
        CODE_SIGN_STYLE: Automatic
        PRODUCT_BUNDLE_IDENTIFIER: com.awh.app
        ASSETCATALOG_COMPILER_APPICON_NAME: AppIcon
        MARKETING_VERSION: "1.0.0"
        CURRENT_PROJECT_VERSION: "1"
        TARGETED_DEVICE_FAMILY: "1,2"
        SWIFT_VERSION: "6.0"
  AWHTests:
    type: bundle.unit-test
    platform: iOS
    deploymentTarget: "18.0"
    sources:
      - Tests
    dependencies:
      - target: AWH
    settings:
      base:
        PRODUCT_BUNDLE_IDENTIFIER: com.awh.tests
        GENERATE_INFOPLIST_FILE: YES
        SWIFT_VERSION: "6.0"
schemes:
  AWH:
    build:
      targets:
        AWH: all
    run:
      config: Debug
    test:
      targets:
        - AWHTests
    archive:
      config: Release
```

- [ ] **Step 3: Create `Info.plist`**

`ios/AWH/Sources/Info.plist` — XcodeGen merges the `properties` above into this file, so it only needs the skeleton:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleDevelopmentRegion</key>
	<string>en</string>
	<key>CFBundleDisplayName</key>
	<string>AWH</string>
	<key>CFBundleExecutable</key>
	<string>$(EXECUTABLE_NAME)</string>
	<key>CFBundleIdentifier</key>
	<string>$(PRODUCT_BUNDLE_IDENTIFIER)</string>
	<key>CFBundleName</key>
	<string>$(PRODUCT_NAME)</string>
	<key>CFBundlePackageType</key>
	<string>APPL</string>
</dict>
</plist>
```

- [ ] **Step 4: Create the launch screen and asset catalog**

`ios/AWH/Sources/LaunchScreen.storyboard` — a plain black screen:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<document type="com.apple.InterfaceBuilder3.CocoaTouch.Storyboard.XIB" version="3.0" toolsVersion="22505" targetRuntime="iOS.CocoaTouch" propertyAccessControl="none" useAutolayout="YES" launchScreen="YES" useTraitCollections="YES" useSafeAreas="YES" colorMatched="YES" initialViewController="01J-lp-oVM">
    <dependencies>
        <plugIn identifier="com.apple.InterfaceBuilder.IBCocoaTouchPlugin" version="22504"/>
    </dependencies>
    <scenes>
        <scene sceneID="EHf-IW-A2E">
            <objects>
                <viewController id="01J-lp-oVM" sceneMemberID="viewController">
                    <view key="view" contentMode="scaleToFill" id="Ze5-6b-2t3">
                        <rect key="frame" x="0.0" y="0.0" width="393" height="852"/>
                        <autoresizingMask key="autoresizingMask" widthSizable="YES" heightSizable="YES"/>
                        <color key="backgroundColor" red="0.0" green="0.0" blue="0.0" alpha="1" colorSpace="custom" customColorSpace="sRGB"/>
                    </view>
                </viewController>
                <placeholder placeholderIdentifier="IBFirstResponder" id="iYj-Kq-Ea1" sceneMemberID="firstResponder"/>
            </objects>
        </scene>
    </scenes>
</document>
```

`ios/AWH/Sources/Assets.xcassets/Contents.json`:

```json
{ "info" : { "author" : "xcode", "version" : 1 } }
```

`ios/AWH/Sources/Assets.xcassets/AppIcon.appiconset/Contents.json`:

```json
{
  "images" : [
    { "filename" : "icon-1024.png", "idiom" : "universal", "platform" : "ios", "size" : "1024x1024" }
  ],
  "info" : { "author" : "xcode", "version" : 1 }
}
```

- [ ] **Step 5: Generate the icon**

Write `ios/AWH/awh-icon.svg` — a placeholder to be replaced later:

```xml
<svg xmlns="http://www.w3.org/2000/svg" width="1024" height="1024" viewBox="0 0 1024 1024">
  <rect width="1024" height="1024" fill="#111111"/>
  <rect x="152" y="232" width="720" height="450" rx="24" fill="#000000" stroke="#1E88E5" stroke-width="16"/>
  <circle cx="512" cy="457" r="96" fill="#1E88E5"/>
  <text x="512" y="840" font-family="Helvetica,Arial,sans-serif" font-size="150"
        font-weight="bold" fill="#f2f2f2" text-anchor="middle">AWH</text>
</svg>
```

Render it to both required PNGs. `rsvg-convert` (`brew install librsvg`) or `qlmanage` both work; `sips` cannot read SVG.

```bash
cd /Users/grillermo/c/awh
rsvg-convert -w 1024 -h 1024 ios/AWH/awh-icon.svg -o ios/AWH/Sources/Assets.xcassets/AppIcon.appiconset/icon-1024.png
cp ios/AWH/Sources/Assets.xcassets/AppIcon.appiconset/icon-1024.png apple-touch-icon.png
```

Verify both are 1024×1024 PNGs: `file apple-touch-icon.png`.

- [ ] **Step 6: Write the failing test**

`ios/AWH/Tests/HomeURLTests.swift`:

```swift
import Foundation
import Testing
@testable import AWH

struct HomeURLTests {
    /// A throwaway suite per test, so nothing leaks into the real defaults.
    private func makeDefaults() -> UserDefaults {
        let name = "awh.tests.\(UUID().uuidString)"
        let defaults = UserDefaults(suiteName: name)!
        defaults.removePersistentDomain(forName: name)
        return defaults
    }

    @Test func fallsBackWhenUnset() {
        #expect(HomeURL.resolve(from: makeDefaults()) == HomeURL.fallback)
    }

    @Test func fallbackIsTheLivePage() {
        #expect(HomeURL.fallback.absoluteString == "https://awh.chiq.me/live")
    }

    @Test func usesAnExplicitOverride() {
        let defaults = makeDefaults()
        defaults.set("https://example.com/page", forKey: HomeURL.key)

        #expect(HomeURL.resolve(from: defaults).absoluteString == "https://example.com/page")
    }

    @Test func addsHTTPSWhenSchemeIsMissing() {
        let defaults = makeDefaults()
        defaults.set("awh.chiq.me/live", forKey: HomeURL.key)

        #expect(HomeURL.resolve(from: defaults).absoluteString == "https://awh.chiq.me/live")
    }

    @Test func keepsAnExplicitHTTPScheme() {
        let defaults = makeDefaults()
        defaults.set("http://192.168.1.5:3060/live", forKey: HomeURL.key)

        #expect(HomeURL.resolve(from: defaults).absoluteString == "http://192.168.1.5:3060/live")
    }

    @Test func trimsSurroundingWhitespace() {
        let defaults = makeDefaults()
        defaults.set("  https://example.com/page \n", forKey: HomeURL.key)

        #expect(HomeURL.resolve(from: defaults).absoluteString == "https://example.com/page")
    }

    @Test func fallsBackOnAnEmptyString() {
        let defaults = makeDefaults()
        defaults.set("   ", forKey: HomeURL.key)

        #expect(HomeURL.resolve(from: defaults) == HomeURL.fallback)
    }

    @Test func fallsBackOnTextThatIsNotAnAddress() {
        let defaults = makeDefaults()
        defaults.set("cat videos", forKey: HomeURL.key)

        #expect(HomeURL.resolve(from: defaults) == HomeURL.fallback)
    }
}
```

- [ ] **Step 7: Add a placeholder app entry point so the target links**

`ios/AWH/Sources/AWHApp.swift` — replaced wholesale in Task 5:

```swift
import SwiftUI

@main
struct AWHApp: App {
    var body: some Scene {
        WindowGroup {
            Text(HomeURL.resolve().absoluteString)
        }
    }
}
```

- [ ] **Step 8: Run the test to verify it fails**

```bash
cd /Users/grillermo/c/awh/ios/AWH
xcodegen generate
xcodebuild test -project AWH.xcodeproj -scheme AWH \
  -destination 'platform=iOS Simulator,name=iPhone 17 Pro' 2>&1 | tail -30
```

Expected: FAIL — `cannot find 'HomeURL' in scope`.

If the destination name is wrong, list what is available with `xcrun simctl list devices available` and use any iPhone running iOS 18 or later. Use the same destination for every later test step.

- [ ] **Step 9: Implement `HomeURL`**

`ios/AWH/Sources/HomeURL.swift`:

```swift
import Foundation

/// The page the kiosk opens on.
///
/// There is no address bar, so the only way to point the app somewhere else is
/// the "Home URL" field in Settings.app (see `Settings.bundle/Root.plist`).
/// Anything unusable there — blank, or text that is not an address — falls back
/// to the live page rather than leaving the kiosk on an error screen.
enum HomeURL {
    static let key = "home_url"
    static let fallback = URL(string: "https://awh.chiq.me/live")!

    static func resolve(from defaults: UserDefaults = .standard) -> URL {
        guard let raw = defaults.string(forKey: key) else { return fallback }

        let trimmed = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return fallback }

        // A bare host is the common case when typing on a phone; assume https.
        // An explicit http:// is kept, which is what makes a LAN address like
        // http://192.168.1.5:3060/live work (see NSAllowsLocalNetworking).
        let candidate = trimmed.contains("://") ? trimmed : "https://\(trimmed)"
        guard let url = URL(string: candidate),
              let host = url.host, !host.isEmpty else { return fallback }

        return url
    }
}
```

- [ ] **Step 10: Run the test to verify it passes**

```bash
cd /Users/grillermo/c/awh/ios/AWH
xcodebuild test -project AWH.xcodeproj -scheme AWH \
  -destination 'platform=iOS Simulator,name=iPhone 17 Pro' 2>&1 | tail -20
```

Expected: `TEST SUCCEEDED`, 8 tests passing.

- [ ] **Step 11: Commit**

```bash
cd /Users/grillermo/c/awh
git add ios/.gitignore ios/AWH/project.yml ios/AWH/awh-icon.svg ios/AWH/Sources ios/AWH/Tests apple-touch-icon.png
git commit -m "Scaffold the AWH iOS app with HomeURL resolution

XcodeGen project, placeholder icon, and the Settings-backed home URL with
a fallback to https://awh.chiq.me/live."
```

---

## Task 4: `awh/ios` — reload policy and the sound-bridge predicate

**Repo:** `/Users/grillermo/c/awh`

**Files:**
- Create: `ios/AWH/Sources/ReloadPolicy.swift`
- Create: `ios/AWH/Sources/SoundBridgeMessage.swift`
- Create: `ios/AWH/Tests/ReloadPolicyTests.swift`
- Create: `ios/AWH/Tests/SoundBridgeMessageTests.swift`

**Interfaces:**
- Consumes: the `AWH` target from Task 3.
- Produces: `ReloadPolicy.delay(afterFailures: Int) -> TimeInterval` and `SoundBridgeMessage.handlerName: String` (`"soundBridge"`), `SoundBridgeMessage.isPlaySound(name: String, body: Any) -> Bool`. Task 5 consumes both.

**Context:** Both are pure so the retry schedule and the bridge's accept/reject decision can be tested without WebKit or a live `WKScriptMessage`. The ramp is 2s → 5s → 10s → 10s…: a wifi blip recovers in two seconds, a genuinely-down server settles into a ten-second poll.

- [ ] **Step 1: Write the failing tests**

`ios/AWH/Tests/ReloadPolicyTests.swift`:

```swift
import Foundation
import Testing
@testable import AWH

struct ReloadPolicyTests {
    @Test func rampsThroughTheSchedule() {
        #expect(ReloadPolicy.delay(afterFailures: 1) == 2)
        #expect(ReloadPolicy.delay(afterFailures: 2) == 5)
        #expect(ReloadPolicy.delay(afterFailures: 3) == 10)
    }

    @Test func plateausRatherThanGrowing() {
        #expect(ReloadPolicy.delay(afterFailures: 4) == 10)
        #expect(ReloadPolicy.delay(afterFailures: 99) == 10)
    }

    @Test func treatsZeroOrNegativeAsTheFirstRetry() {
        #expect(ReloadPolicy.delay(afterFailures: 0) == 2)
        #expect(ReloadPolicy.delay(afterFailures: -1) == 2)
    }
}
```

`ios/AWH/Tests/SoundBridgeMessageTests.swift`:

```swift
import Foundation
import Testing
@testable import AWH

struct SoundBridgeMessageTests {
    @Test func acceptsThePlaySoundMessage() {
        #expect(SoundBridgeMessage.isPlaySound(name: "soundBridge", body: "playSound"))
    }

    @Test func rejectsAnotherHandlerName() {
        #expect(!SoundBridgeMessage.isPlaySound(name: "other", body: "playSound"))
    }

    @Test func rejectsAnotherBody() {
        #expect(!SoundBridgeMessage.isPlaySound(name: "soundBridge", body: "stopSound"))
    }

    @Test func rejectsANonStringBody() {
        #expect(!SoundBridgeMessage.isPlaySound(name: "soundBridge", body: 42))
        #expect(!SoundBridgeMessage.isPlaySound(name: "soundBridge", body: ["playSound"]))
    }

    @Test func handlerNameIsWhatThePagePostsTo() {
        #expect(SoundBridgeMessage.handlerName == "soundBridge")
    }
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
cd /Users/grillermo/c/awh/ios/AWH
xcodegen generate
xcodebuild test -project AWH.xcodeproj -scheme AWH \
  -destination 'platform=iOS Simulator,name=iPhone 17 Pro' 2>&1 | tail -30
```

Expected: FAIL — `cannot find 'ReloadPolicy' in scope` and `cannot find 'SoundBridgeMessage' in scope`.

- [ ] **Step 3: Implement both**

`ios/AWH/Sources/ReloadPolicy.swift`:

```swift
import Foundation

/// How long to wait before retrying a failed page load.
///
/// Ramps rather than polling at a flat interval: a dropped wifi frame is back
/// in two seconds, while a server that is genuinely down settles into a ten
/// second poll instead of hammering it. Pure, so the schedule is testable
/// without a timer.
enum ReloadPolicy {
    static let schedule: [TimeInterval] = [2, 5, 10]

    /// - Parameter afterFailures: consecutive failures so far, 1 for the first.
    static func delay(afterFailures failures: Int) -> TimeInterval {
        let index = max(0, failures - 1)
        return schedule[min(index, schedule.count - 1)]
    }
}
```

`ios/AWH/Sources/SoundBridgeMessage.swift`:

```swift
import Foundation

/// The one message the page is allowed to send the app.
///
/// `templates/live.html` posts
/// `window.webkit.messageHandlers.soundBridge.postMessage("playSound")` when a
/// reset fires. Nothing else crosses the bridge — every other name or body is
/// ignored rather than interpreted.
enum SoundBridgeMessage {
    static let handlerName = "soundBridge"

    static func isPlaySound(name: String, body: Any) -> Bool {
        name == handlerName && (body as? String) == "playSound"
    }
}
```

- [ ] **Step 4: Run the tests to verify they pass**

```bash
cd /Users/grillermo/c/awh/ios/AWH
xcodebuild test -project AWH.xcodeproj -scheme AWH \
  -destination 'platform=iOS Simulator,name=iPhone 17 Pro' 2>&1 | tail -20
```

Expected: `TEST SUCCEEDED`, 16 tests passing.

- [ ] **Step 5: Commit**

```bash
cd /Users/grillermo/c/awh
git add ios/AWH/Sources/ReloadPolicy.swift ios/AWH/Sources/SoundBridgeMessage.swift ios/AWH/Tests/ReloadPolicyTests.swift ios/AWH/Tests/SoundBridgeMessageTests.swift
git commit -m "Add the reload ramp and the sound-bridge message predicate

Both pure, so the retry schedule and the bridge's accept/reject decision
are testable without WebKit."
```

---

## Task 5: `awh/ios` — the web view and app shell

**Repo:** `/Users/grillermo/c/awh`

**Files:**
- Create: `ios/AWH/Sources/KioskController.swift`
- Create: `ios/AWH/Sources/KioskWebView.swift`
- Create: `ios/AWH/Sources/KioskView.swift`
- Modify: `ios/AWH/Sources/AWHApp.swift` (replace the Task 3 placeholder)

**Interfaces:**
- Consumes: `HomeURL.resolve(from:)`, `ReloadPolicy.delay(afterFailures:)`, `SoundBridgeMessage.handlerName`, `SoundBridgeMessage.isPlaySound(name:body:)`.
- Produces: a running app. Task 6 modifies `AWHApp` and adds `Settings.bundle`.

**Context — Swift 6 concurrency:** `WKNavigationDelegate` and `WKScriptMessageHandler` are not main-actor-isolated protocols, so a `@MainActor` coordinator will not compile against them. PatataTube solves this by keeping the coordinator a plain `NSObject` and hopping with `MainActor.assumeIsolated { }` inside each delegate callback — all of which WebKit already calls on the main thread. Mutable state lives on `KioskController`, which *is* `@MainActor`. Follow this split exactly.

**Context — the retain cycle:** `WKUserContentController` retains a registered script message handler. Without removing it in `dismantleUIView`, neither the coordinator nor the web view ever deallocates. `WebBridgeView.swift:320-326` carries the same fix and the same comment.

- [ ] **Step 1: Write `KioskController`**

`ios/AWH/Sources/KioskController.swift`:

```swift
import Foundation
import WebKit

/// Main-actor state behind the kiosk web view: which page to load, how many
/// consecutive failures have happened, and the pending retry.
///
/// Separate from the coordinator because the coordinator cannot be
/// `@MainActor` — see `KioskWebView.Coordinator`.
@MainActor
final class KioskController {
    let url: URL

    private weak var webView: WKWebView?
    private var failures = 0
    private var retry: Task<Void, Never>?

    init(url: URL) {
        self.url = url
    }

    func attach(_ webView: WKWebView) {
        self.webView = webView
        webView.load(URLRequest(url: url))
    }

    func reload() {
        webView?.reload()
    }

    /// A page committed: stop retrying and clear the ramp.
    func navigationSucceeded() {
        failures = 0
        cancelRetry()
        endRefreshing()
    }

    /// A navigation failed outright — no network, server down, bad host. Retry
    /// on the ramp instead of leaving WebKit's error page on a wall display.
    func navigationFailed() {
        endRefreshing()
        failures += 1
        let delay = ReloadPolicy.delay(afterFailures: failures)

        // Replacing rather than adding, so a burst of failures cannot stack
        // timers and turn the ramp into a flood.
        cancelRetry()
        retry = Task { [weak self] in
            try? await Task.sleep(for: .seconds(delay))
            guard !Task.isCancelled else { return }
            self?.webView?.reload()
        }
    }

    func cancelRetry() {
        retry?.cancel()
        retry = nil
    }

    private func endRefreshing() {
        webView?.scrollView.refreshControl?.endRefreshing()
    }
}
```

- [ ] **Step 2: Write `KioskWebView`**

`ios/AWH/Sources/KioskWebView.swift`:

```swift
import MediaPlayer
import SwiftUI
import WebKit

/// The whole app: one web view, pinned to `HomeURL`.
///
/// The page can drive native audio through a JS bridge — it posts
/// `window.webkit.messageHandlers.soundBridge.postMessage("playSound")` and the
/// coordinator toggles `MPMusicPlayerController.systemMusicPlayer`. Nothing
/// else crosses the bridge.
struct KioskWebView: UIViewRepresentable {
    let url: URL

    func makeCoordinator() -> Coordinator {
        Coordinator(controller: KioskController(url: url))
    }

    func makeUIView(context: Context) -> WKWebView {
        let contentController = WKUserContentController()
        contentController.add(context.coordinator, name: SoundBridgeMessage.handlerName)

        let config = WKWebViewConfiguration()
        config.userContentController = contentController
        config.allowsInlineMediaPlayback = true

        let webView = WKWebView(frame: .zero, configuration: config)
        webView.navigationDelegate = context.coordinator
        webView.backgroundColor = .black
        webView.isOpaque = false
        webView.scrollView.backgroundColor = .black

        // The live page is overflow:hidden, so pulling down is the only thing
        // this scroll view ever does — and the only way to force a reload.
        let refresh = UIRefreshControl()
        refresh.addTarget(context.coordinator,
                          action: #selector(Coordinator.handleRefresh),
                          for: .valueChanged)
        webView.scrollView.refreshControl = refresh

        MainActor.assumeIsolated { context.coordinator.controller.attach(webView) }
        return webView
    }

    func updateUIView(_ uiView: WKWebView, context: Context) {}

    static func dismantleUIView(_ uiView: WKWebView, coordinator: Coordinator) {
        // The user content controller retains the handler; drop it so the
        // coordinator and the web view can actually deallocate.
        MainActor.assumeIsolated { coordinator.controller.cancelRetry() }
        uiView.configuration.userContentController
            .removeScriptMessageHandler(forName: SoundBridgeMessage.handlerName)
        uiView.navigationDelegate = nil
    }

    /// Deliberately **not** `@MainActor`: `WKScriptMessageHandler` and
    /// `WKNavigationDelegate` are not main-actor-isolated protocols, and an
    /// isolated conformance does not compile under Swift 6. WebKit calls every
    /// method below on the main thread, so `assumeIsolated` is sound.
    final class Coordinator: NSObject, WKScriptMessageHandler, WKNavigationDelegate {
        let controller: KioskController

        init(controller: KioskController) {
            self.controller = controller
        }

        @objc func handleRefresh() {
            MainActor.assumeIsolated { controller.reload() }
        }

        func userContentController(_ userContentController: WKUserContentController,
                                   didReceive message: WKScriptMessage) {
            guard SoundBridgeMessage.isPlaySound(name: message.name, body: message.body) else {
                return
            }

            MainActor.assumeIsolated {
                let player = MPMusicPlayerController.systemMusicPlayer
                if player.playbackState == .playing {
                    player.pause()
                } else {
                    player.play()
                }
            }
        }

        func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) {
            MainActor.assumeIsolated { controller.navigationSucceeded() }
        }

        func webView(_ webView: WKWebView, didFail navigation: WKNavigation!,
                     withError error: Error) {
            MainActor.assumeIsolated { controller.navigationFailed() }
        }

        func webView(_ webView: WKWebView, didFailProvisionalNavigation navigation: WKNavigation!,
                     withError error: Error) {
            MainActor.assumeIsolated { controller.navigationFailed() }
        }
    }
}
```

- [ ] **Step 3: Write `KioskView`**

`ios/AWH/Sources/KioskView.swift`:

```swift
import SwiftUI

struct KioskView: View {
    var body: some View {
        KioskWebView(url: HomeURL.resolve())
            .ignoresSafeArea()
            .background(.black)
    }
}
```

- [ ] **Step 4: Replace the placeholder `AWHApp`**

`ios/AWH/Sources/AWHApp.swift`:

```swift
import SwiftUI

@main
struct AWHApp: App {
    var body: some Scene {
        WindowGroup {
            KioskView()
        }
    }
}
```

- [ ] **Step 5: Build and confirm the existing tests still pass**

```bash
cd /Users/grillermo/c/awh/ios/AWH
xcodegen generate
xcodebuild test -project AWH.xcodeproj -scheme AWH \
  -destination 'platform=iOS Simulator,name=iPhone 17 Pro' 2>&1 | tail -20
```

Expected: `TEST SUCCEEDED`, 16 tests still passing. If it fails with a concurrency diagnostic about isolated conformances, the coordinator has been marked `@MainActor` — remove that annotation.

- [ ] **Step 6: Run it against the real server and verify by hand**

```bash
cd /Users/grillermo/c/awh/ios/AWH
open AWH.xcodeproj
```

Run on any iPhone or iPad simulator (⌘R) and confirm:

- The live page at `awh.chiq.me/live` loads and fills the screen.
- Pulling down reloads it.
- Stopping the network (Simulator → Device → Network Link Conditioner, or just turn off wifi) makes the app retry rather than show WebKit's error page, and it recovers on its own when the network returns.

The sound bridge cannot be verified in the Simulator — `MPMusicPlayerController.systemMusicPlayer` needs a real device with the Music app. It is checked on device in Task 9.

- [ ] **Step 7: Commit**

```bash
cd /Users/grillermo/c/awh
git add ios/AWH/Sources/KioskController.swift ios/AWH/Sources/KioskWebView.swift ios/AWH/Sources/KioskView.swift ios/AWH/Sources/AWHApp.swift
git commit -m "Add the kiosk web view, sound bridge, and failure retry

One full-bleed WKWebView pinned to HomeURL, with pull-to-refresh and a
2/5/10s reload ramp so an unattended display heals itself. The coordinator
stays non-isolated and hops with assumeIsolated, because WKNavigationDelegate
conformance cannot be main-actor-isolated under Swift 6."
```

---

## Task 6: `awh/ios` — Settings.bundle and screen-awake behaviour

**Repo:** `/Users/grillermo/c/awh`

**Files:**
- Create: `ios/AWH/Sources/Settings.bundle/Root.plist`
- Modify: `ios/AWH/Sources/AWHApp.swift`
- Modify: `ios/AWH/Sources/KioskView.swift`
- Modify: `ios/AWH/project.yml` (add `Settings.bundle` as a resource)

**Interfaces:**
- Consumes: `HomeURL.key`, `HomeURL.fallback`, `KioskView`.
- Produces: nothing new for later tasks.

**Context:** `Settings.bundle` is how a kiosk with no chrome stays reachable — the home URL is editable from Settings.app → AWH without an address bar in the app. Registering the default at launch is what makes the field show `https://awh.chiq.me/live` before it has ever been edited.

`isIdleTimerDisabled` must follow the scene phase rather than being set once. Set unconditionally at launch, a backgrounded app keeps holding the assertion and the device never sleeps.

- [ ] **Step 1: Create the settings bundle**

`ios/AWH/Sources/Settings.bundle/Root.plist`:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>StringsTable</key>
	<string>Root</string>
	<key>PreferenceSpecifiers</key>
	<array>
		<dict>
			<key>Type</key>
			<string>PSGroupSpecifier</string>
			<key>Title</key>
			<string>Display</string>
			<key>FooterText</key>
			<string>The page AWH opens on. Leave blank to use https://awh.chiq.me/live.</string>
		</dict>
		<dict>
			<key>Type</key>
			<string>PSTextFieldSpecifier</string>
			<key>Title</key>
			<string>Home URL</string>
			<key>Key</key>
			<string>home_url</string>
			<key>DefaultValue</key>
			<string>https://awh.chiq.me/live</string>
			<key>IsSecure</key>
			<false/>
			<key>KeyboardType</key>
			<string>URL</string>
			<key>AutocapitalizationType</key>
			<string>None</string>
			<key>AutocorrectionType</key>
			<string>No</string>
		</dict>
	</array>
</dict>
</plist>
```

The `Key` value must stay exactly `home_url` — it is `HomeURL.key`.

- [ ] **Step 2: Make XcodeGen copy the bundle as a resource**

In `ios/AWH/project.yml`, replace the `AWH` target's `sources:` block:

```yaml
    sources:
      - Sources
```

with:

```yaml
    sources:
      - path: Sources
        excludes:
          - "Settings.bundle"
      # Copied verbatim rather than compiled, so Settings.app can read it.
      - path: Sources/Settings.bundle
        type: folder
```

- [ ] **Step 3: Register the default and follow the scene phase**

`ios/AWH/Sources/AWHApp.swift`:

```swift
import SwiftUI

@main
struct AWHApp: App {
    @Environment(\.scenePhase) private var scenePhase

    init() {
        // Without this, Settings.app shows an empty Home URL field until it is
        // edited once, even though the app is using the fallback.
        UserDefaults.standard.register(
            defaults: [HomeURL.key: HomeURL.fallback.absoluteString]
        )
    }

    var body: some Scene {
        WindowGroup {
            KioskView()
        }
        .onChange(of: scenePhase) { _, phase in
            // Tied to the scene phase, not set once at launch: a backgrounded
            // app must not keep the device awake.
            UIApplication.shared.isIdleTimerDisabled = (phase == .active)
        }
    }
}
```

- [ ] **Step 4: Hide the status bar**

`ios/AWH/Sources/KioskView.swift`:

```swift
import SwiftUI

struct KioskView: View {
    var body: some View {
        KioskWebView(url: HomeURL.resolve())
            .ignoresSafeArea()
            .background(.black)
            .statusBarHidden(true)
    }
}
```

`UIStatusBarHidden` and `UIViewControllerBasedStatusBarAppearance` are already in `project.yml` from Task 3; this covers the SwiftUI side.

- [ ] **Step 5: Build and verify the tests still pass**

```bash
cd /Users/grillermo/c/awh/ios/AWH
xcodegen generate
xcodebuild test -project AWH.xcodeproj -scheme AWH \
  -destination 'platform=iOS Simulator,name=iPhone 17 Pro' 2>&1 | tail -20
```

Expected: `TEST SUCCEEDED`, 16 tests passing.

- [ ] **Step 6: Verify the settings bundle landed in the app**

```bash
cd /Users/grillermo/c/awh/ios/AWH
BUILT=$(xcodebuild -project AWH.xcodeproj -scheme AWH -configuration Debug \
  -destination 'platform=iOS Simulator,name=iPhone 17 Pro' \
  -showBuildSettings 2>/dev/null \
  | awk -F' = ' '/ BUILT_PRODUCTS_DIR = /{print $2; exit}')
ls "$BUILT/AWH.app/Settings.bundle/Root.plist"
```

Expected: the path prints. If it is "No such file or directory", the `type: folder` source entry is wrong and Settings.app will show nothing even though the app builds.

- [ ] **Step 7: Verify by hand in the Simulator**

Run the app (⌘R), then:

- Settings.app → AWH shows a "Home URL" field pre-filled with `https://awh.chiq.me/live`.
- The status bar is gone; the page runs edge to edge.
- Set the field to a bad host, relaunch: the app retries rather than showing an error page.
- Clear the field, relaunch: it is back on the live page.

- [ ] **Step 8: Commit**

```bash
cd /Users/grillermo/c/awh
git add ios/AWH/Sources/Settings.bundle ios/AWH/Sources/AWHApp.swift ios/AWH/Sources/KioskView.swift ios/AWH/project.yml
git commit -m "Add the Home URL setting and kiosk display behaviour

Settings.app is the only way to repoint an app with no address bar. The
idle timer follows the scene phase so a backgrounded app stops holding the
device awake."
```

---

## Task 7: `awh` — the IPA builder

**Repo:** `/Users/grillermo/c/awh`

**Files:**
- Create: `ios/ipa_builder.rb`

**Interfaces:**
- Consumes: `ios/AWH/project.yml`.
- Produces: `IpaBuilder::APP_NAME` (`"AWH"`), `IpaBuilder::PROJECT_YML`, `IpaBuilder.marketing_version -> String`, `IpaBuilder.team_id -> String`, `IpaBuilder.build(manifest: nil) -> String` (path to the `.ipa`, with `manifest.plist` written as a sibling when `manifest:` is given), and the console helpers `IpaBuilder.step`, `.die`, `.run`, `.bold`, `.green`, `.red`. Tasks 8 and 9 consume all of these.

**Context:** Port `/Users/grillermo/c/patatatube/ios/ipa_builder.rb`. Read that file first — its comments explain *why* each step is shaped the way it is, and they should survive the port. In particular:

- The `manifest` dict in `ExportOptions.plist` is why the build exports at all rather than zipping a `Payload/` by hand: it makes `xcodebuild` emit the OTA `manifest.plist`, so the manifest can never describe a different binary. Its `appURL` must be the `.ipa`'s **final public URL**, which the caller must know before the build.
- `thinning: <none>` keeps it one universal `.ipa`; a thinned export produces per-device variants one URL cannot serve.
- `release-testing` is Xcode 15.3+'s name for ad hoc. It re-signs against a profile embedding the UDIDs registered on the portal.
- Automatic signing creates certificates and profiles but **cannot** create App IDs, so `com.awh.app` must already exist as an Identifier on the portal.

- [ ] **Step 1: Copy the file**

```bash
cp /Users/grillermo/c/patatatube/ios/ipa_builder.rb /Users/grillermo/c/awh/ios/ipa_builder.rb
```

- [ ] **Step 2: Apply the renames**

In `ios/ipa_builder.rb`, make exactly these substitutions:

| From | To |
|---|---|
| `APP_NAME    = "PatataTube"` | `APP_NAME    = "AWH"` |
| `SCHEME      = "PatataTube"` | `SCHEME      = "AWH"` |
| `ENV["PATATATUBE_UNSIGNED"]` | `ENV["AWH_UNSIGNED"]` |
| `ENV["PATATATUBE_TEAM_ID"]` | `ENV["AWH_TEAM_ID"]` |
| `PATATATUBE_TEAM_ID set` (in the `die` message) | `AWH_TEAM_ID set` |
| `com.patatatube.app` (in the signing comment) | `com.awh.app` |
| `ios/install.md` (in comments) | `ios/install.md` — unchanged, the file exists here too |

Rewrite the file header comment to:

```ruby
# ipa_builder.rb
#
# Builds an ad-hoc-signed AWH .ipa from source, together with the OTA
# manifest.plist that an `itms-services://` link points at. Used by ../deploy.
#
# The .ipa is signed with the paid Apple Developer Program team in
# project.yml's DEVELOPMENT_TEAM. That is what lets iOS install it straight
# from Safari over an `itms-services://` link — no AltStore, no cable, and a
# signature good for a year instead of the free tier's 7 days. The price is
# that an Ad Hoc profile only covers **devices registered on the portal**: a
# device whose UDID was added after this .ipa was built cannot install it, so
# adding a device means registering it and then re-running ../deploy.
#
# Set AWH_UNSIGNED=1 to fall back to an unsigned archive. That build cannot be
# installed from Safari; it exists so a broken signing setup does not block
# producing a binary.
```

- [ ] **Step 3: Remove the DevLog instrumentation**

AWH has no DevLog. Change the `build` signature from:

```ruby
  def build(instrumented: false, manifest: nil)
```

to:

```ruby
  def build(manifest: nil)
```

Then delete, from the body of `build`:

- the whole `if instrumented ... end` block that prints the DEVLOG warning,
- the line `devlog = instrumented ? "SWIFT_ACTIVE_COMPILATION_CONDITIONS='$(inherited) DEVLOG' " : ""`,
- the `"#{devlog}" \` line from the `xcodebuild` command string,
- `#{instrumented ? ' [DEVLOG]' : ''}` from the `step "Archiving (xcodebuild)…"` string.

Also delete the paragraphs about `DEVLOG` and `instrumented:` from the doc comment directly above `build`, keeping everything about `manifest:` and the tmp dir.

- [ ] **Step 4: Verify it loads and reads the project**

```bash
cd /Users/grillermo/c/awh
ruby -r./ios/ipa_builder -e 'puts IpaBuilder::APP_NAME, IpaBuilder.marketing_version, IpaBuilder.team_id'
```

Expected:

```
AWH
1.0.0
Q3WS4MWCW3
```

- [ ] **Step 5: Confirm no PatataTube references survived**

```bash
cd /Users/grillermo/c/awh
grep -in "patatatube\|devlog\|instrumented" ios/ipa_builder.rb
```

Expected: no output.

- [ ] **Step 6: Do a real build**

This is slow (several minutes) and needs `com.awh.app` registered on the portal.

```bash
cd /Users/grillermo/c/awh
ruby -r./ios/ipa_builder -e '
  ipa = IpaBuilder.build(manifest: {
    app_url:  "https://files.chiq.me/files/AWH-1.0.0.ipa",
    icon_url: "https://files.chiq.me/files/awh-icon.png"
  })
  puts "IPA:      #{ipa} (#{File.size(ipa)} bytes)"
  puts "MANIFEST: #{File.join(File.dirname(ipa), "manifest.plist")}"
'
```

Expected: an `AWH.ipa` of a few MB and a sibling `manifest.plist`. Print the manifest and confirm its `software-package` URL is exactly the `app_url` passed in:

```bash
plutil -p /path/from/above/manifest.plist
```

If the export fails with "No profiles for 'com.awh.app' were found", the App ID is not registered on the portal — that is the prerequisite, not a code bug.

- [ ] **Step 7: Commit**

```bash
cd /Users/grillermo/c/awh
git add ios/ipa_builder.rb
git commit -m "Add the AWH ipa builder

Ported from patatatube, minus the DevLog instrumentation path. Exports
ad-hoc-signed with the OTA manifest xcodebuild emits, so the manifest can
never describe a different binary than the one shipped."
```

---

## Task 8: `awh` — the uploader

**Repo:** `/Users/grillermo/c/awh`

**Files:**
- Create: `ios/uploader.rb`
- Create: `ios/uploader_test.rb`

**Interfaces:**
- Consumes: Task 2's `?name=` endpoint; `IpaBuilder.die` for error reporting.
- Produces: `Uploader.base_url -> String`, `Uploader.upload(path, name:) -> String` (the public URL). Task 9 consumes both.

**Context:** `deploy` uploads four files. `curl` does the HTTP so no gem is needed. The upload must fail loudly: a silent failure would leave the install page pointing at a `.ipa` that was never published.

- [ ] **Step 1: Write the failing test**

`ios/uploader_test.rb` — an integration test against a locally-run `file_to_s3`, skipped when one is not running, so it never blocks on the network:

```ruby
# frozen_string_literal: true

# Integration test for Uploader against a locally-run file_to_s3.
#
#   cd /Users/grillermo/c/file_to_s3
#   FILES_DIR=/tmp/awh-upload-test AUTH_TOKEN=test-token bin/rackup -s webrick
#
#   cd /Users/grillermo/c/awh
#   AWH_UPLOAD_BASE=http://localhost:33333 AWH_UPLOAD_TOKEN=test-token \
#     ruby ios/uploader_test.rb
#
# Skips itself when no server is listening, so it is safe to run unattended.

require "minitest/autorun"
require "net/http"
require "tempfile"
require "uri"
require_relative "uploader"

class UploaderTest < Minitest::Test
  def setup
    skip "no file_to_s3 at #{Uploader.base_url}" unless server_running?
  end

  def server_running?
    uri = URI(Uploader.base_url)
    Net::HTTP.start(uri.host, uri.port, open_timeout: 1, read_timeout: 2) { |h| h.get("/") }
    true
  rescue StandardError
    false
  end

  def with_file(content)
    Tempfile.create(["awh-upload", ".plist"]) do |f|
      f.write(content)
      f.flush
      yield f.path
    end
  end

  def test_pinned_upload_returns_the_stable_url
    url = with_file("<plist/>") { |p| Uploader.upload(p, name: "awh-test.plist") }

    assert_equal "#{Uploader.base_url}/files/awh-test.plist", url
  end

  def test_pinned_upload_overwrites_and_keeps_the_same_url
    first  = with_file("one") { |p| Uploader.upload(p, name: "awh-test.plist") }
    second = with_file("two") { |p| Uploader.upload(p, name: "awh-test.plist") }

    assert_equal first, second
    assert_equal "two", Net::HTTP.get(URI(second))
  end

  def test_a_missing_file_raises
    assert_raises(SystemExit) { Uploader.upload("/nope/missing.plist", name: "x.plist") }
  end

  def test_a_bad_token_raises
    original = ENV["AWH_UPLOAD_TOKEN"]
    ENV["AWH_UPLOAD_TOKEN"] = "wrong-token"

    assert_raises(SystemExit) do
      with_file("<plist/>") { |p| Uploader.upload(p, name: "awh-test.plist") }
    end
  ensure
    ENV["AWH_UPLOAD_TOKEN"] = original
  end
end
```

- [ ] **Step 2: Run the test to verify it fails**

```bash
cd /Users/grillermo/c/awh
ruby ios/uploader_test.rb
```

Expected: FAIL — `cannot load such file -- ./uploader`.

- [ ] **Step 3: Implement the uploader**

`ios/uploader.rb`:

```ruby
# frozen_string_literal: true

# uploader.rb
#
# Publishes release artifacts to the file_to_s3 service at files.chiq.me.
#
# Every upload here is *pinned* (`?name=`), meaning it lands at exactly that
# name and overwrites whatever was there. That is what gives the install page
# and the OTA manifest URLs that never move, so one Home Screen bookmark keeps
# installing the newest release forever.
#
# curl rather than an HTTP gem, so ./deploy needs no bundle.

require "cgi"
require "shellwords"
require "tempfile"

require_relative "ipa_builder"

module Uploader
  DEFAULT_BASE = "https://files.chiq.me"

  module_function

  def base_url
    ENV.fetch("AWH_UPLOAD_BASE", DEFAULT_BASE).sub(%r{/+\z}, "")
  end

  def token
    value = ENV["AWH_UPLOAD_TOKEN"]
    if value.nil? || value.strip.empty?
      IpaBuilder.die(
        "AWH_UPLOAD_TOKEN is not set. It must match file_to_s3's AUTH_TOKEN.\n" \
        "  export AWH_UPLOAD_TOKEN=..."
      )
    end
    value
  end

  # Uploads `path` under exactly `name`, returning its public URL.
  def upload(path, name:)
    IpaBuilder.die("file to upload not found: #{path}") unless File.file?(path)

    url = "#{base_url}/upload?name=#{CGI.escape(name)}"
    body, status = post(url, path)

    unless status == "200"
      IpaBuilder.die("upload of #{name} failed (HTTP #{status}): #{body}")
    end

    expected = "#{base_url}/files/#{name}"
    unless body == expected
      IpaBuilder.die("upload of #{name} returned #{body.inspect}, expected #{expected.inspect}. " \
                     "Does files.chiq.me have the ?name= pinning change deployed?")
    end

    body
  end

  # Returns [response_body, http_status]. The body goes to a temp file so the
  # status can be read from -w without the two getting interleaved.
  def post(url, path)
    Tempfile.create("awh-upload-response") do |out|
      status = IO.popen([
        "curl", "-sS",
        "-X", "POST", url,
        "-H", "Authorization: Bearer #{token}",
        "-F", "file=@#{path}",
        "-o", out.path,
        "-w", "%{http_code}"
      ], &:read)

      [File.read(out.path).strip, status.strip]
    end
  end
end
```

- [ ] **Step 4: Run the test against a local server**

In one terminal:

```bash
cd /Users/grillermo/c/file_to_s3
mkdir -p /tmp/awh-upload-test
FILES_DIR=/tmp/awh-upload-test AUTH_TOKEN=test-token bin/rackup -s webrick
```

In another:

```bash
cd /Users/grillermo/c/awh
AWH_UPLOAD_BASE=http://localhost:33333 AWH_UPLOAD_TOKEN=test-token ruby ios/uploader_test.rb
```

Expected: 4 runs, 0 failures, 0 errors. Then stop the server and re-run without the env vars — expected: 4 runs, 4 skips.

- [ ] **Step 5: Commit**

```bash
cd /Users/grillermo/c/awh
git add ios/uploader.rb ios/uploader_test.rb
git commit -m "Add the pinned uploader for release artifacts

Every release file is pinned so its URL never moves, and a mismatched
response URL is treated as a failure — that is the signal that files.chiq.me
has not got the ?name= change deployed."
```

---

## Task 9: `awh` — the deploy script

**Repo:** `/Users/grillermo/c/awh`

**Files:**
- Create: `deploy`
- Create: `ios/install.md`
- Create: `ios/manifest.plist` (written by the first run)

**Interfaces:**
- Consumes: `IpaBuilder.build(manifest:)`, `IpaBuilder.marketing_version`, `IpaBuilder::PROJECT_YML`, `Uploader.upload(path, name:)`, `Uploader.base_url`.
- Produces: `./deploy` publishes a release. Nothing later consumes it.

**Context:** The delivery chain is:

```
Home Screen bookmark
  -> https://files.chiq.me/files/awh-install.html      (pinned)
     -> itms-services://?action=download-manifest&url=
        https://files.chiq.me/files/awh-manifest.plist (pinned)
        -> https://files.chiq.me/files/AWH-1.2.3.ipa   (pinned, one per release)
           https://files.chiq.me/files/awh-icon.png    (pinned)
```

The `.ipa` URL must be known before the build, because `ipa_builder` bakes it into the manifest — which is why it is derived from the version rather than assigned at upload.

Order matters: upload the `.ipa` **before** the manifest, and the manifest before the install page. A manifest published ahead of its binary points at a 404.

- [ ] **Step 1: Write the deploy script**

`deploy`, at the repo root:

```ruby
#!/usr/bin/env ruby
# frozen_string_literal: true

# deploy
#
# One-shot release of the AWH iOS app.
#
#   1. bump MARKETING_VERSION (+ build number) in ios/AWH/project.yml
#   2. build an ad-hoc-signed .ipa + its OTA manifest   (ios/ipa_builder.rb)
#   3. upload the .ipa, icon, manifest and install page to files.chiq.me
#   4. commit the bump + manifest, tag, and push
#
# The install page URL never changes, so a Home Screen bookmark on the device
# always installs the newest release. It is printed at the end and documented
# in ios/install.md.
#
# Usage:
#   ./deploy                  # bump patch (1.0.0 -> 1.0.1)
#   ./deploy patch|minor|major
#   ./deploy 1.4.2            # set an explicit version
#   ./deploy --yes            # skip the confirmation prompt
#
# Release notes (shown on the install page):
#
#   ./deploy minor --summary "Faster reconnect" \
#            --note "Retry now ramps 2/5/10s" \
#            --note "Home URL is editable in Settings"
#
# --summary is a one-line headline, --note is repeatable and becomes a bullet.
# With neither, the notes come from commit subjects since the last release tag.
#
# Requires AWH_UPLOAD_TOKEN in the environment (file_to_s3's AUTH_TOKEN).

require_relative "ios/ipa_builder"
require_relative "ios/uploader"
require "digest"
require "fileutils"
require "shellwords"
require "time"

B = IpaBuilder

APP_NAME      = IpaBuilder::APP_NAME
PROJECT_YML   = IpaBuilder::PROJECT_YML
BUNDLE_ID     = "com.awh.app"
MANIFEST_REL  = "ios/manifest.plist"
ICON_REL      = "apple-touch-icon.png"

PINNED_MANIFEST = "awh-manifest.plist"
PINNED_INSTALL  = "awh-install.html"
PINNED_ICON     = "awh-icon.png"

MANIFEST_URL = "#{Uploader.base_url}/files/#{PINNED_MANIFEST}"
INSTALL_URL  = "#{Uploader.base_url}/files/#{PINNED_INSTALL}"
ICON_URL     = "#{Uploader.base_url}/files/#{PINNED_ICON}"
ITMS_URL     = "itms-services://?action=download-manifest&url=#{MANIFEST_URL}"

MIN_OS = File.read(PROJECT_YML)[/^\s*iOS:\s*"?([\d.]+)"?/, 1] || "18.0"

# --- flags -------------------------------------------------------------------

ASSUME_YES = !ARGV.delete("--yes").nil?

# Repeatable "--flag value" / "--flag=value" options, pulled out of ARGV so the
# version argument stays positional wherever it sits.
def take_option(argv, *names)
  values = []
  i = 0
  while i < argv.length
    arg = argv[i]
    if names.include?(arg)
      value = argv[i + 1]
      B.die("#{arg} needs a value") if value.nil? || value.start_with?("--")
      values << value
      argv.slice!(i, 2)
    elsif (eq = arg.match(/\A(--[a-z-]+)=(.*)\z/m)) && names.include?(eq[1])
      values << eq[2]
      argv.slice!(i, 1)
    else
      i += 1
    end
  end
  values
end

SUMMARY = take_option(ARGV, "--summary", "-s").last
NOTES   = take_option(ARGV, "--note", "--notes", "-n")
          .flat_map { |n| n.split("\n") }
          .map { |n| n.strip.sub(/\A[-*•]\s*/, "") }
          .reject(&:empty?)

# --- preflight ---------------------------------------------------------------

Uploader.token # dies early with instructions if unset

BRANCH = `git rev-parse --abbrev-ref HEAD`.strip
B.die("on branch '#{BRANCH}', not 'main'; releases are cut from main") unless BRANCH == "main"

# --- version bump ------------------------------------------------------------

def bump(current, arg)
  return arg if arg =~ /\A\d+\.\d+\.\d+\z/

  major, minor, patch = current.split(".").map(&:to_i)
  case arg
  when "major" then "#{major + 1}.0.0"
  when "minor" then "#{major}.#{minor + 1}.0"
  when "patch", nil then "#{major}.#{minor}.#{patch + 1}"
  else B.die("bad argument '#{arg}' (use patch|minor|major or an X.Y.Z version)")
  end
end

current = B.marketing_version
version = bump(current, ARGV[0])
tag     = "v#{version}"
asset   = "#{APP_NAME}-#{version}.ipa"

# The .ipa name carries the version, so this URL is both predictable before the
# build (ipa_builder needs it) and unique per release (pinning never clobbers a
# past one). This tag check is what guards against re-releasing a version.
download = "#{Uploader.base_url}/files/#{asset}"

B.die("tag #{tag} already exists — bump to a new version") if `git tag -l #{tag}`.strip == tag

# --- release notes -----------------------------------------------------------

NOTE_NOISE = /\A(Release iOS v|Merge |Commit pending changes|Bump |wip\b)/i

def notes_from_git
  last = `git describe --tags --abbrev=0 --match 'v*' 2>/dev/null`.strip
  range = last.empty? ? "HEAD -n 20" : "#{last}..HEAD"
  subjects = `git log --no-merges --pretty=%s #{range}`.lines.map(&:strip)
  subjects.reject { |s| s.empty? || s =~ NOTE_NOISE }.uniq.first(8)
end

def release_notes(version)
  bullets  = NOTES.empty? ? notes_from_git : NOTES
  headline = SUMMARY || (bullets.empty? ? "Release #{version}." : "What's new in #{version}:")
  lines = [headline]
  lines << "" unless bullets.empty?
  lines.concat(bullets.map { |b| "• #{b}" })
  lines.join("\n")
end

DESCRIPTION = release_notes(version)

yml   = File.read(PROJECT_YML)
build = (yml[/^\s*CURRENT_PROJECT_VERSION:\s*"?(\d+)"?/, 1] || "0").to_i + 1
yml   = yml.sub(/^(\s*MARKETING_VERSION:\s*)"?[\d.]+"?/, "\\1\"#{version}\"")
yml   = yml.sub(/^(\s*CURRENT_PROJECT_VERSION:\s*)"?\d+"?/, "\\1\"#{build}\"")
File.write(PROJECT_YML, yml)

puts B.bold("\n==> Releasing #{APP_NAME} #{current} -> #{version} (build #{build})")
puts B.bold("\n    What's New:")
puts DESCRIPTION.lines.map { |l| "      #{l}" }.join
puts "\n    Install page: #{INSTALL_URL}"

unless ASSUME_YES
  print "\n    Continue? [y/N] "
  B.die("aborted") unless $stdin.gets.to_s.strip.downcase.start_with?("y")
end

# --- build -------------------------------------------------------------------

ipa = B.build(manifest: { app_url: download, icon_url: ICON_URL })
built_manifest = File.join(File.dirname(ipa), "manifest.plist")

size   = File.size(ipa)
sha256 = Digest::SHA256.file(ipa).hexdigest
puts B.green("    #{asset} — #{(size / 1_048_576.0).round(1)} MB  sha256:#{sha256[0, 12]}…")

B.die("export produced no manifest.plist — cannot publish the OTA route") unless File.exist?(built_manifest)

# --- publish -----------------------------------------------------------------
#
# Order matters. The manifest points at the .ipa and the install page points at
# the manifest, so each must exist before the thing that references it — a
# manifest published ahead of its binary points at a 404.

B.step "Uploading the .ipa"
puts "    #{Uploader.upload(ipa, name: asset)}"

B.step "Uploading the icon"
puts "    #{Uploader.upload(File.join(__dir__, ICON_REL), name: PINNED_ICON)}"

B.step "Uploading the OTA manifest"
FileUtils.cp(built_manifest, File.join(__dir__, MANIFEST_REL))
puts "    #{Uploader.upload(built_manifest, name: PINNED_MANIFEST)}"

def h(str)
  str.to_s.gsub("&", "&amp;").gsub("<", "&lt;").gsub(">", "&gt;").gsub('"', "&quot;")
end

install_page = <<~HTML
  <!doctype html>
  <html lang="en">
  <head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">
  <title>Install AWH</title>
  <style>
    :root { color-scheme: dark; }
    body {
      margin: 0; padding: 2.5rem 1.25rem 4rem;
      background: #111; color: #f2f2f2;
      font: 16px/1.55 -apple-system, BlinkMacSystemFont, "Segoe UI", system-ui, sans-serif;
      display: flex; flex-direction: column; align-items: center;
    }
    main { width: 100%; max-width: 26rem; }
    img.icon { width: 88px; height: 88px; border-radius: 20px; display: block; margin: 0 auto 1.25rem; }
    h1 { font-size: 1.6rem; margin: 0 0 .25rem; text-align: center; }
    .version { text-align: center; color: #9a9a9a; margin: 0 0 2rem; font-size: .95rem; }
    a.install {
      display: block; background: #1E88E5; color: #fff; text-decoration: none;
      font-weight: 600; font-size: 1.05rem; text-align: center;
      padding: 1rem; border-radius: 14px; margin-bottom: 1.5rem;
    }
    a.install:active { background: #1667b0; }
    section { border-top: 1px solid #2a2a2a; padding-top: 1.25rem; margin-top: 1.25rem; }
    h2 { font-size: .8rem; text-transform: uppercase; letter-spacing: .08em; color: #8a8a8a; margin: 0 0 .6rem; }
    pre.notes { white-space: pre-wrap; margin: 0; font: inherit; color: #d8d8d8; }
    p { color: #b0b0b0; font-size: .9rem; }
    code { background: #1c1c1c; padding: .15em .4em; border-radius: 5px; font-size: .85em; word-break: break-all; }
  </style>
  </head>
  <body>
  <main>
    <img class="icon" src="#{h(ICON_URL)}" alt="">
    <h1>AWH</h1>
    <p class="version">Version #{h(version)} (build #{h(build)})</p>

    <a class="install" href="#{h(ITMS_URL)}">Install AWH</a>

    <p>Open this page in <strong>Safari</strong> — other browsers cannot start an
    install. Then confirm on the Home Screen prompt.</p>

    <section>
      <h2>What&rsquo;s new</h2>
      <pre class="notes">#{h(DESCRIPTION)}</pre>
    </section>

    <section>
      <h2>Updating later</h2>
      <p>Add this page to your Home Screen (Share &rarr; Add to Home Screen). The
      Install button always installs the newest release, so one tap updates the
      app in place.</p>
    </section>

    <section>
      <h2>Requirements</h2>
      <p>Your device must be registered on the developer account before a build
      is made. A device added afterwards needs a fresh <code>./deploy</code>
      before it can install. iOS #{h(MIN_OS)} or later.</p>
    </section>
  </main>
  </body>
  </html>
HTML

B.step "Uploading the install page"
page = File.join(File.dirname(ipa), PINNED_INSTALL)
File.write(page, install_page)
puts "    #{Uploader.upload(page, name: PINNED_INSTALL)}"

FileUtils.remove_entry(File.dirname(ipa))

# --- commit ------------------------------------------------------------------

B.step "Committing + pushing"
B.run("git add #{PROJECT_YML.shellescape} #{MANIFEST_REL.shellescape}", chdir: __dir__)
B.run("git commit -m 'Release iOS #{tag}'", chdir: __dir__)
B.run("git tag #{tag}", chdir: __dir__)

# git-sync is installed as a global post-commit hook and pushes to this remote
# the instant a commit lands, so by the time we push the ref may already be
# where we wanted it — which git reports as a failure. Treating that as fatal
# would abort *after* publishing, leaving an install page advertising a build
# whose commit was never pushed. So on failure, ask the remote what it holds.
def push_branch(branch, chdir:)
  cmd = "git push github #{branch} --tags"
  puts "    $ #{cmd}"
  return if system(cmd, chdir: chdir)

  local  = `git -C #{chdir.shellescape} rev-parse HEAD`.strip
  remote = `git -C #{chdir.shellescape} ls-remote github refs/heads/#{branch}`.split.first.to_s
  unless !local.empty? && local == remote
    B.die("command failed: #{cmd} (remote is at #{remote.empty? ? "nothing" : remote[0, 7]}, " \
          "we are at #{local[0, 7]})")
  end

  puts B.green("    remote already at #{local[0, 7]} — pushed by the git-sync hook; continuing")
end

push_branch(BRANCH, chdir: __dir__)

puts B.green("\n✓ Published #{APP_NAME} #{version}")
puts B.bold("\n    Install from Safari on the device:")
puts B.bold("    #{INSTALL_URL}")
puts "\n    Add it to the Home Screen once; it always installs the newest release."
```

- [ ] **Step 2: Make it executable and check it parses**

```bash
cd /Users/grillermo/c/awh
chmod +x deploy
ruby -c deploy
```

Expected: `Syntax OK`.

- [ ] **Step 3: Verify the preflight guards fire**

```bash
cd /Users/grillermo/c/awh
env -u AWH_UPLOAD_TOKEN ./deploy --yes
```

Expected: dies with the `AWH_UPLOAD_TOKEN is not set` message before building anything. Confirm `git diff ios/AWH/project.yml` is empty afterwards — the guard must run before the version bump.

- [ ] **Step 4: Write `ios/install.md`**

````markdown
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
````

- [ ] **Step 5: Cut the first release**

Requires `com.awh.app` registered on the portal and the device UDID registered.

```bash
cd /Users/grillermo/c/awh
export AWH_UPLOAD_TOKEN=<file_to_s3's AUTH_TOKEN>
./deploy 1.0.0 --summary "First release" --note "Kiosk view of awh.chiq.me/live"
```

Expected: a build, four uploads, a commit, a tag, a push, and the install URL printed.

- [ ] **Step 6: Verify the published chain**

```bash
curl -sS -o /dev/null -w "install page: %{http_code} %{content_type}\n" https://files.chiq.me/files/awh-install.html
curl -sS -o /dev/null -w "manifest:     %{http_code} %{content_type}\n" https://files.chiq.me/files/awh-manifest.plist
curl -sS -o /dev/null -w "ipa GET:      %{http_code}\n" https://files.chiq.me/files/AWH-1.0.0.ipa
curl -sS -o /dev/null -w "ipa HEAD:     %{http_code}\n" -I https://files.chiq.me/files/AWH-1.0.0.ipa
curl -sS https://files.chiq.me/files/awh-manifest.plist | grep -A1 software-package
```

Expected: the install page is `text/html`, everything is 200 including the `.ipa` HEAD, and the manifest's `software-package` URL is exactly the published `.ipa` URL.

- [ ] **Step 7: Install on the device and work the checklist**

Open the install page in Safari on the device, add it to the Home Screen, install, and work through the "Device checklist" in `ios/install.md`. Every box must pass before Task 10 — this is the gate that says the extraction actually worked.

- [ ] **Step 8: Commit**

Step 5 already committed the version bump and manifest. Commit the docs:

```bash
cd /Users/grillermo/c/awh
git add deploy ios/install.md
git commit -m "Add the OTA deploy pipeline

Builds, publishes to files.chiq.me under pinned names, and prints the
permanent install URL. No GitHub Releases and no Pages: the bookmark and the
manifest are overwritten in place, so one Home Screen tap always installs the
newest release."
```

---

## Task 10: `patatatube` — remove the web bridge

**Repo:** `/Users/grillermo/c/patatatube`

**Files:**
- Delete: `ios/PatataTube/Sources/WebBridgeView.swift`
- Delete: `ios/PatataTubeKit/Sources/PatataTubeKit/WebAddress.swift`
- Delete: `ios/PatataTubeKit/Sources/PatataTubeKit/WebHistoryStore.swift`
- Modify: `ios/PatataTube/Sources/VideoGridView.swift`
- Modify: `ios/PatataTube/Sources/AppModel.swift`
- Modify: `ios/PatataTube/Sources/QuickActions.swift`
- Modify: `ios/PatataTube/Sources/Info.plist`
- Modify: `ios/PatataTube/project.yml`
- Modify: `ios/README.md`

**Interfaces:**
- Consumes: nothing. Independent of Tasks 1–9, but must not land until Task 9's device checklist passes.
- Produces: nothing.

**Context:** AWH now owns this feature. PatataTube keeps its own `deploy`, `ipa_builder.rb`, `refresh-ipa.rb`, `apps.json`, and `manifest.plist` — it still ships as an app. Only the web bridge goes.

- [ ] **Step 1: Confirm the current test suite passes before touching anything**

```bash
cd /Users/grillermo/c/patatatube/ios/PatataTube
xcodegen generate
xcodebuild test -project PatataTube.xcodeproj -scheme PatataTube \
  -destination 'platform=iOS Simulator,name=iPhone 17 Pro' 2>&1 | tail -20
```

Expected: `TEST SUCCEEDED`. If it already fails, stop — that is a pre-existing problem and this task must not be blamed for it.

- [ ] **Step 2: Delete the three files**

```bash
cd /Users/grillermo/c/patatatube
git rm ios/PatataTube/Sources/WebBridgeView.swift \
       ios/PatataTubeKit/Sources/PatataTubeKit/WebAddress.swift \
       ios/PatataTubeKit/Sources/PatataTubeKit/WebHistoryStore.swift
```

- [ ] **Step 3: Remove the call sites in `VideoGridView.swift`**

Four edits:

- around `:177` — the toolbar button that opens the live page,
- `:180` — `@State private var showWebBridge = false`,
- `:364` — `.onChange(of: model.webBridgeRequests) { _, _ in showWebBridge = true }`,
- `:367-369` — the `.fullScreenCover(isPresented: $showWebBridge, …) { WebBridgeView() }`.

Line numbers are from the pre-edit file, so work bottom-up. Find each with:

```bash
cd /Users/grillermo/c/patatatube/ios
grep -n "showWebBridge\|WebBridgeView\|webBridgeRequests" PatataTube/Sources/VideoGridView.swift
```

- [ ] **Step 4: Remove the quick action**

In `PatataTube/Sources/AppModel.swift`, remove `webBridgeRequests` and the `.openWeb` branch — three sites, at roughly `:110`, `:120`, and `:234`. In `PatataTube/Sources/QuickActions.swift:7`, remove `case openWeb = "com.patatatube.openWeb"`.

```bash
cd /Users/grillermo/c/patatatube/ios
grep -n "openWeb\|webBridgeRequests" PatataTube/Sources/AppModel.swift PatataTube/Sources/QuickActions.swift
```

The compiler will flag any switch over `QuickAction` that is no longer exhaustive — fix those where they appear.

- [ ] **Step 5: Remove the plist entries**

In `PatataTube/project.yml`, delete the `com.patatatube.openWeb` entry from `UIApplicationShortcutItems` (around `:51`) and the `NSAppleMusicUsageDescription` line (`:79`). In `PatataTube/Sources/Info.plist`, delete the same shortcut item (around `:45`) and the `NSAppleMusicUsageDescription` key and its string (`:28`).

The usage string goes because the sound bridge was its only consumer — PatataTube touches `MPMusicPlayerController` nowhere else. Verify:

```bash
cd /Users/grillermo/c/patatatube/ios
grep -rn "MPMusicPlayer" PatataTube/Sources PatataTubeKit/Sources
```

Expected: no output. If anything remains, keep the usage string.

- [ ] **Step 6: Remove the README section**

In `ios/README.md`, delete the "### Web bridge address bar" section — the heading and every checklist item under it, up to the next `## Notes` heading.

- [ ] **Step 7: Rebuild and test**

```bash
cd /Users/grillermo/c/patatatube/ios/PatataTube
xcodegen generate
xcodebuild test -project PatataTube.xcodeproj -scheme PatataTube \
  -destination 'platform=iOS Simulator,name=iPhone 17 Pro' 2>&1 | tail -20
```

Expected: `TEST SUCCEEDED`, the same test count as Step 1. No test file references the web bridge, so the count should not change.

- [ ] **Step 8: Confirm nothing survived**

```bash
cd /Users/grillermo/c/patatatube
grep -rn "WebBridge\|WebHistory\|WebAddress\|soundBridge\|openWeb\|AppleMusic" ios/ --include=*.swift --include=*.yml --include=*.plist --include=*.md
```

Expected: no output.

`AppIconSmall.imageset` becomes unreferenced — `WebBridgeView` was its only consumer — but stays. It is a plausible thing to want back and costs 30 KB.

- [ ] **Step 9: Commit**

```bash
cd /Users/grillermo/c/patatatube
git add -A ios/
git commit -m "Remove the in-app web bridge

Extracted into its own app (grillermo/awh, ios/), which is where a kiosk
view of awh.chiq.me/live belongs. Takes WebAddress and WebHistoryStore with
it — the address bar was their only consumer — and the Apple Music usage
string, since the sound bridge was the only thing touching
MPMusicPlayerController."
```

---

## Verification summary

| Task | Gate |
|---|---|
| 1 | `rake test` green; `git status --porcelain files/` empty |
| 2 | `rake test` green; production `?name=` returns the pinned URL and `HEAD` is 200 |
| 3 | `xcodebuild test` green, 8 tests |
| 4 | `xcodebuild test` green, 16 tests |
| 5 | App runs in the Simulator; page loads, pull-to-refresh works, retry recovers |
| 6 | `Settings.bundle/Root.plist` present in the built `.app`; field visible in Settings.app |
| 7 | `IpaBuilder.build` produces an `.ipa` and a manifest whose URL matches what was passed |
| 8 | `uploader_test.rb` green against a local `file_to_s3`; skips cleanly without one |
| 9 | Every URL in the chain returns 200 (including `HEAD` on the `.ipa`); device checklist passes |
| 10 | PatataTube's suite green with the same test count; the grep finds nothing |

import Foundation
import Testing
@testable import AWH

struct AppQuickActionTests {
    private let deployedInfo: [String: Any] = [
        AppQuickAction.otaManifestURLInfoKey: "https://files.example.com/files/awh-manifest.plist"
    ]

    @Test
    func installLatestStartsTheOTAInstallFromThePinnedManifest() {
        // ./deploy builds in the manifest it republishes under the same name on
        // every release, so the link always installs the newest OTA build.
        #expect(
            AppQuickAction.installLatest.url(infoDictionary: deployedInfo)
                == URL(string: "itms-services://?action=download-manifest&url=https://files.example.com/files/awh-manifest.plist")
        )
    }

    @Test
    func deployedBuildOffersInstallLatest() {
        #expect(AppQuickAction.available(infoDictionary: deployedInfo) == [.installLatest])
    }

    @Test
    func buildWithoutAManifestOffersNoQuickActions() {
        let localBuild: [String: Any] = [AppQuickAction.otaManifestURLInfoKey: ""]
        // An unexpanded build setting must not count as a URL either.
        let unexpanded: [String: Any] = [AppQuickAction.otaManifestURLInfoKey: "$(AWH_OTA_MANIFEST_URL)"]

        #expect(AppQuickAction.available(infoDictionary: localBuild).isEmpty)
        #expect(AppQuickAction.available(infoDictionary: unexpanded).isEmpty)
        #expect(AppQuickAction.available(infoDictionary: nil).isEmpty)
        #expect(AppQuickAction.installLatest.url(infoDictionary: localBuild) == nil)
    }

    @Test
    func unknownShortcutTypeIsIgnored() {
        #expect(AppQuickAction(shortcutType: "com.example.unknown") == nil)
    }
}

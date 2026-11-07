import Foundation

/// Home Screen quick actions, registered at launch from `available(infoDictionary:)`.
/// Each raw value is the item's `UIApplicationShortcutItem.type`.
enum AppQuickAction: String, Equatable {
    case installLatest = "awh.install-latest"

    /// Info.plist key that ./deploy fills with its pinned OTA manifest URL.
    static let otaManifestURLInfoKey = "AWHOTAManifestURL"

    init?(shortcutType: String) {
        self.init(rawValue: shortcutType)
    }

    /// Builds made outside ./deploy carry no manifest URL, so they offer no
    /// Install Latest action: there is no release for them to install.
    static func available(infoDictionary: [String: Any]?) -> [AppQuickAction] {
        otaManifestURL(infoDictionary: infoDictionary) == nil ? [] : [.installLatest]
    }

    func url(infoDictionary: [String: Any]?) -> URL? {
        switch self {
        case .installLatest:
            // Same link as the install page's button: iOS asks to install straight
            // away. ./deploy republishes the manifest under this pinned name on
            // every release, so it always installs the newest build.
            Self.otaManifestURL(infoDictionary: infoDictionary).flatMap {
                URL(string: "itms-services://?action=download-manifest&url=\($0.absoluteString)")
            }
        }
    }

    private static func otaManifestURL(infoDictionary: [String: Any]?) -> URL? {
        guard let value = infoDictionary?[otaManifestURLInfoKey] as? String,
              !value.isEmpty,
              !value.hasPrefix("$(") else {
            return nil
        }
        return URL(string: value)
    }
}

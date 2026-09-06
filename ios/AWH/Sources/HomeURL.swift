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

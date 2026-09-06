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

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

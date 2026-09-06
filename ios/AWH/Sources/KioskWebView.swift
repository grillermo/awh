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

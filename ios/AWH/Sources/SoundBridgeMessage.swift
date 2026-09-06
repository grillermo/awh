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

import Foundation

/// The one message the page is allowed to send the app.
///
/// `templates/live.html` posts
/// `window.webkit.messageHandlers.soundBridge.postMessage("playSound")` when a
/// reset fires. Apart from `OcrSamplesMessage` below, every other name or body
/// is ignored rather than interpreted.
enum SoundBridgeMessage {
    static let handlerName = "soundBridge"

    static func isPlaySound(name: String, body: Any) -> Bool {
        name == handlerName && (body as? String) == "playSound"
    }
}

/// The live page's "OCR SAMPLES" link posts
/// `window.webkit.messageHandlers.ocrSamples.postMessage("open")` to open the
/// native sample picker. The page only shows the link when this handler exists.
enum OcrSamplesMessage {
    static let handlerName = "ocrSamples"

    static func isOpen(name: String, body: Any) -> Bool {
        name == handlerName && (body as? String) == "open"
    }
}

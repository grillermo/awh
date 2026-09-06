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

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

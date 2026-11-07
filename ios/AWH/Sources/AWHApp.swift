import SwiftUI
import UIKit

@main
struct AWHApp: App {
    @UIApplicationDelegateAdaptor(AppDelegate.self) private var appDelegate
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

/// Registers the Home Screen quick actions and routes them to
/// `AppQuickActionSceneDelegate`. "Install Latest" only appears in builds made
/// by ./deploy, which bake in the OTA manifest URL.
final class AppDelegate: NSObject, UIApplicationDelegate {
    func application(
        _ application: UIApplication,
        didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]? = nil
    ) -> Bool {
        application.shortcutItems = AppQuickAction.available(infoDictionary: Bundle.main.infoDictionary).map {
            UIApplicationShortcutItem(type: $0.rawValue, localizedTitle: "Install Latest", localizedSubtitle: nil,
                                      icon: UIApplicationShortcutIcon(systemImageName: "arrow.down.app"))
        }
        return true
    }

    func application(
        _ application: UIApplication,
        configurationForConnecting connectingSceneSession: UISceneSession,
        options: UIScene.ConnectionOptions
    ) -> UISceneConfiguration {
        let config = UISceneConfiguration(name: nil, sessionRole: connectingSceneSession.role)
        if connectingSceneSession.role == .windowApplication {
            config.delegateClass = AppQuickActionSceneDelegate.self
        }
        return config
    }
}

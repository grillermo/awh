import UIKit

/// Performs Home Screen quick actions. Install it from the app delegate's
/// `application(_:configurationForConnecting:options:)`, and register the items
/// in `application(_:didFinishLaunchingWithOptions:)`:
///
///     application.shortcutItems = AppQuickAction.available(infoDictionary: Bundle.main.infoDictionary).map {
///         UIApplicationShortcutItem(type: $0.rawValue, localizedTitle: "Install Latest", localizedSubtitle: nil,
///                                   icon: UIApplicationShortcutIcon(systemImageName: "arrow.down.app"))
///     }
final class AppQuickActionSceneDelegate: NSObject, UIWindowSceneDelegate {
    // A cold launch delivers the item before the scene is active, and opening a
    // URL then is unreliable, so it waits for the first activation.
    private var pendingAction: AppQuickAction?

    func scene(
        _ scene: UIScene,
        willConnectTo session: UISceneSession,
        options connectionOptions: UIScene.ConnectionOptions
    ) {
        pendingAction = connectionOptions.shortcutItem.flatMap { AppQuickAction(shortcutType: $0.type) }
    }

    func sceneDidBecomeActive(_ scene: UIScene) {
        guard let action = pendingAction else { return }
        pendingAction = nil
        perform(action)
    }

    func windowScene(
        _ windowScene: UIWindowScene,
        performActionFor shortcutItem: UIApplicationShortcutItem,
        completionHandler: @escaping (Bool) -> Void
    ) {
        guard let action = AppQuickAction(shortcutType: shortcutItem.type) else {
            completionHandler(false)
            return
        }
        perform(action)
        completionHandler(true)
    }

    private func perform(_ action: AppQuickAction) {
        guard let url = action.url(infoDictionary: Bundle.main.infoDictionary) else { return }
        UIApplication.shared.open(url) { opened in
            guard opened, action == .installLatest else { return }
            Self.exitForInstall()
        }
    }

    /// iOS replaces the app bundle under a running process, which crashes it and
    /// only starts the install once the user leaves the app. Exiting cleanly
    /// after the install prompt is up lets the install begin on its own.
    private static func exitForInstall() {
        DispatchQueue.main.asyncAfter(deadline: .now() + 1.5) {
            exit(0)
        }
    }
}

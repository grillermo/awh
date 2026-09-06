import SwiftUI

@main
struct AWHApp: App {
    var body: some Scene {
        WindowGroup {
            Text(HomeURL.resolve().absoluteString)
        }
    }
}

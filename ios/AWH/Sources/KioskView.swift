import SwiftUI

struct KioskView: View {
    var body: some View {
        KioskWebView(url: HomeURL.resolve())
            .ignoresSafeArea()
            .background(.black)
            .statusBarHidden(true)
    }
}

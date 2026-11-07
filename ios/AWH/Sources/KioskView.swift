import SwiftUI

struct KioskView: View {
    // `-openOcrSamples YES` as a launch argument opens the picker on launch (for screenshots).
    @State private var showingSamples = UserDefaults.standard.bool(forKey: "openOcrSamples")
    private let homeURL = HomeURL.resolve()

    var body: some View {
        KioskWebView(url: homeURL, onOpenOcrSamples: { showingSamples = true })
            .ignoresSafeArea()
            .background(.black)
            .statusBarHidden(true)
            .fullScreenCover(isPresented: $showingSamples) {
                OcrSamplesView(api: OcrSamplesAPI(homeURL: homeURL))
            }
    }
}

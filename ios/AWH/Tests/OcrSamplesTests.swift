import Foundation
import Testing
@testable import AWH

struct OcrSamplesTests {
    @Test func diffSendsOnlyWhatChanged() {
        let diff = SelectionDiff(from: ["a", "b"], to: ["b", "c", "d"])
        #expect(diff.select == ["c", "d"])
        #expect(diff.deselect == ["a"])
        #expect(SelectionDiff(from: ["a"], to: ["a"]).isEmpty)
    }

    @Test func apiLivesAtTheKioskPagesOrigin() {
        let api = OcrSamplesAPI(homeURL: URL(string: "http://192.168.1.1:3060/live?x=1")!)
        #expect(api.listURL.absoluteString == "http://192.168.1.1:3060/api/ocr-samples")
        #expect(api.selectionURL.absoluteString == "http://192.168.1.1:3060/api/ocr-samples/selection")
        #expect(api.imageURL("text/20261106-182945_4C_ocr.png").absoluteString
                == "http://192.168.1.1:3060/ocr-samples/text/20261106-182945_4C_ocr.png")
    }

    @Test func decodesTheServersList() throws {
        let json = """
        {"ok": true, "samples": [{"id": "text/20261106-182945_4C", "kind": "text", "ocr_text": "4C",
          "at": 1794011385.5, "ocr_image": "text/20261106-182945_4C_ocr.png",
          "raw_image": "text/20261106-182945_4C_raw.png", "selected": true}]}
        """
        let samples = try OcrSamplesAPI.decodeList(Data(json.utf8))
        #expect(samples.count == 1)
        #expect(samples[0].ocrText == "4C")
        #expect(samples[0].selected)
    }

    @Test func openMessageIsRecognisedAndNothingElse() {
        #expect(OcrSamplesMessage.isOpen(name: "ocrSamples", body: "open"))
        #expect(!OcrSamplesMessage.isOpen(name: "soundBridge", body: "open"))
        #expect(!OcrSamplesMessage.isOpen(name: "ocrSamples", body: "close"))
    }
}

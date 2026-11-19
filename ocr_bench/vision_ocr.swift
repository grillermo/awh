// Apple Vision text recognition (the engine behind Live Text) for one image.
//   swiftc -O vision_ocr.swift -o vision_ocr
//   ./vision_ocr accurate|fast <image>
// Prints the recognised strings, space-separated. Language correction is off:
// it "fixes" digit strings into words.
import AppKit
import Vision

let args = CommandLine.arguments
guard args.count == 3, let image = NSImage(contentsOfFile: args[2]),
      let cg = image.cgImage(forProposedRect: nil, context: nil, hints: nil) else {
    FileHandle.standardError.write("usage: vision_ocr accurate|fast <image>\n".data(using: .utf8)!)
    exit(64)
}

let request = VNRecognizeTextRequest()
request.recognitionLevel = args[1] == "fast" ? .fast : .accurate
request.usesLanguageCorrection = false
request.minimumTextHeight = 0.1

try VNImageRequestHandler(cgImage: cg).perform([request])
let texts = (request.results ?? []).compactMap { $0.topCandidates(1).first?.string }
print(texts.joined(separator: " "))

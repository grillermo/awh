import Foundation

/// One saved OCR miss, as listed by the server's `GET /api/ocr-samples`.
struct OcrSample: Decodable, Identifiable, Hashable, Sendable {
    let id: String
    let kind: String
    let ocrText: String
    let at: Double
    let ocrImage: String
    let rawImage: String
    let selected: Bool

    var capturedAt: Date { Date(timeIntervalSince1970: at) }
}

/// What changed between the selection the server has and the one on screen.
struct SelectionDiff: Equatable, Sendable {
    let select: [String]
    let deselect: [String]

    init(from saved: Set<String>, to current: Set<String>) {
        select = current.subtracting(saved).sorted()
        deselect = saved.subtracting(current).sorted()
    }

    var isEmpty: Bool { select.isEmpty && deselect.isEmpty }
}

/// The three endpoints the samples screen talks to, all on the kiosk's server.
struct OcrSamplesAPI: Sendable {
    let origin: URL

    /// The server is whatever host the kiosk page is on.
    init(homeURL: URL) {
        origin = HomeURL.origin(of: homeURL)
    }

    var listURL: URL { origin.appending(path: "api/ocr-samples") }
    var selectionURL: URL { origin.appending(path: "api/ocr-samples/selection") }

    func imageURL(_ relativePath: String) -> URL {
        origin.appending(path: "ocr-samples").appending(path: relativePath)
    }

    static func decodeList(_ data: Data) throws -> [OcrSample] {
        struct Response: Decodable { let samples: [OcrSample] }
        let decoder = JSONDecoder()
        decoder.keyDecodingStrategy = .convertFromSnakeCase
        return try decoder.decode(Response.self, from: data).samples
    }

    func fetch() async throws -> [OcrSample] {
        let (data, response) = try await URLSession.shared.data(from: listURL)
        try Self.check(response)
        return try Self.decodeList(data)
    }

    func save(_ diff: SelectionDiff) async throws {
        var request = URLRequest(url: selectionURL)
        request.httpMethod = "POST"
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        request.httpBody = try JSONSerialization.data(
            withJSONObject: ["select": diff.select, "deselect": diff.deselect]
        )
        let (_, response) = try await URLSession.shared.data(for: request)
        try Self.check(response)
    }

    private static func check(_ response: URLResponse) throws {
        guard let http = response as? HTTPURLResponse, (200..<300).contains(http.statusCode) else {
            throw URLError(.badServerResponse)
        }
    }
}

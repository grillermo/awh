import Foundation
import Observation

/// Samples on screen plus the checked ones, kept in step with the server.
///
/// A finger dragged down the checkboxes changes the selection once per row,
/// so saves are debounced and sent as one diff against what the server last
/// confirmed. A failed save is retried; nothing is lost while offline.
@MainActor
@Observable
final class OcrSamplesStore {
    enum LoadState: Equatable { case loading, loaded, failed(String) }

    private(set) var samples: [OcrSample] = []
    private(set) var loadState: LoadState = .loading
    private(set) var saveError: String?

    var selection: Set<String> = [] {
        didSet { scheduleSave() }
    }

    let api: OcrSamplesAPI
    private var saved: Set<String> = []
    private var saveTask: Task<Void, Never>?

    init(api: OcrSamplesAPI) {
        self.api = api
    }

    func load() async {
        loadState = .loading
        do {
            let fetched = try await api.fetch()
            samples = fetched
            saved = Set(fetched.filter(\.selected).map(\.id))
            selection = saved
            loadState = .loaded
        } catch {
            loadState = .failed(error.localizedDescription)
        }
    }

    var allSelected: Bool { !samples.isEmpty && selection.count == samples.count }

    func toggleSelectAll() {
        selection = allSelected ? [] : Set(samples.map(\.id))
    }

    /// Sends whatever is pending right away; used when the screen closes.
    func flush() async {
        saveTask?.cancel()
        await save()
    }

    private func scheduleSave() {
        saveTask?.cancel()
        saveTask = Task { [weak self] in
            try? await Task.sleep(for: .milliseconds(400))
            guard !Task.isCancelled else { return }
            await self?.save()
        }
    }

    private func save() async {
        let target = selection
        let diff = SelectionDiff(from: saved, to: target)
        guard !diff.isEmpty else { return }
        do {
            try await api.save(diff)
            saved = target
            saveError = nil
        } catch {
            saveError = "Not saved yet — retrying"
            try? await Task.sleep(for: .seconds(2))
            if !Task.isCancelled { scheduleSave() }
        }
    }
}

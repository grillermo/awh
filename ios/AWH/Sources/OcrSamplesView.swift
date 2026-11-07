import SwiftUI

/// Pick OCR samples by hand.
///
/// A `List` with a selection set, held in edit mode, is the same control the
/// Files app uses: tap a circle to check one row, or put a finger on the
/// circles and slide up or down to check every row it passes (the list scrolls
/// when the finger reaches an edge). Every change is saved to the server.
struct OcrSamplesView: View {
    @State private var store: OcrSamplesStore
    @Environment(\.dismiss) private var dismiss

    init(api: OcrSamplesAPI) {
        _store = State(initialValue: OcrSamplesStore(api: api))
    }

    var body: some View {
        NavigationStack {
            content
                .navigationTitle(title)
                .navigationBarTitleDisplayMode(.inline)
                .toolbar {
                    ToolbarItem(placement: .topBarLeading) {
                        Button(store.allSelected ? "Deselect All" : "Select All") {
                            store.toggleSelectAll()
                        }
                        .disabled(store.samples.isEmpty)
                    }
                    ToolbarItem(placement: .topBarTrailing) {
                        Button("Done") {
                            Task {
                                await store.flush()
                                dismiss()
                            }
                        }
                        .fontWeight(.semibold)
                    }
                }
                .safeAreaInset(edge: .bottom) {
                    if let error = store.saveError {
                        Text(error)
                            .font(.footnote.weight(.medium))
                            .padding(.horizontal, 14).padding(.vertical, 8)
                            .background(.red.opacity(0.85), in: Capsule())
                            .foregroundStyle(.white)
                            .padding(.bottom, 8)
                    }
                }
        }
        .preferredColorScheme(.dark)
        .task { await store.load() }
    }

    private var title: String {
        store.selection.isEmpty ? "OCR Samples" : "\(store.selection.count) Selected"
    }

    @ViewBuilder private var content: some View {
        switch store.loadState {
        case .loading where store.samples.isEmpty:
            ProgressView()
        case .failed(let message):
            ContentUnavailableView {
                Label("Couldn't load samples", systemImage: "wifi.exclamationmark")
            } description: {
                Text(message)
            } actions: {
                Button("Try Again") { Task { await store.load() } }
            }
        default:
            if store.samples.isEmpty {
                ContentUnavailableView("No samples yet", systemImage: "photo.on.rectangle",
                                       description: Text("They are saved while the live page is open."))
            } else {
                list
            }
        }
    }

    private var list: some View {
        List(store.samples, selection: $store.selection) { sample in
            OcrSampleRow(sample: sample, imageURL: store.api.imageURL(sample.ocrImage))
                .listRowInsets(EdgeInsets(top: 8, leading: 8, bottom: 8, trailing: 16))
        }
        .listStyle(.plain)
        .environment(\.editMode, .constant(.active))
        .refreshable { await store.load() }
    }
}

private struct OcrSampleRow: View {
    let sample: OcrSample
    let imageURL: URL

    var body: some View {
        HStack(spacing: 16) {
            AsyncImage(url: imageURL) { phase in
                switch phase {
                case .success(let image):
                    image.resizable().interpolation(.none).scaledToFit()
                case .failure:
                    Image(systemName: "photo.badge.exclamationmark").foregroundStyle(.secondary)
                default:
                    ProgressView()
                }
            }
            .frame(width: 150, height: 150)
            .background(.black)
            .clipShape(RoundedRectangle(cornerRadius: 10))

            VStack(alignment: .leading, spacing: 6) {
                Text(sample.ocrText.isEmpty ? "∅" : sample.ocrText)
                    .font(.system(size: 34, weight: .bold, design: .monospaced))
                    .foregroundStyle(sample.ocrText.isEmpty ? Color.secondary : Color.orange)
                    .lineLimit(2)
                    .minimumScaleFactor(0.5)
                Text(sample.kind.uppercased())
                    .font(.caption2.weight(.semibold))
                    .padding(.horizontal, 6).padding(.vertical, 2)
                    .background(.white.opacity(0.12), in: Capsule())
                Text(sample.capturedAt, format: .dateTime.day().month(.abbreviated).hour().minute().second())
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            Spacer(minLength: 0)
        }
        .contentShape(Rectangle())
    }
}

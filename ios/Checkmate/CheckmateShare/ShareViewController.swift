import CheckmateCore
import SwiftUI
import UIKit
import UniformTypeIdentifiers

final class ShareViewController: UIViewController {
  override func viewDidLoad() {
    super.viewDidLoad()
    loadSharedContent { [weak self] title, url in
      guard let self else { return }
      let root = ShareCaptureView(initialTitle: title, referenceURL: url) { success in
        if success {
          self.extensionContext?.completeRequest(returningItems: nil)
        } else {
          self.extensionContext?.cancelRequest(withError: CancellationError())
        }
      }
      let host = UIHostingController(rootView: root)
      addChild(host)
      host.view.translatesAutoresizingMaskIntoConstraints = false
      view.addSubview(host.view)
      NSLayoutConstraint.activate([
        host.view.leadingAnchor.constraint(equalTo: view.leadingAnchor),
        host.view.trailingAnchor.constraint(equalTo: view.trailingAnchor),
        host.view.topAnchor.constraint(equalTo: view.topAnchor),
        host.view.bottomAnchor.constraint(equalTo: view.bottomAnchor),
      ])
      host.didMove(toParent: self)
    }
  }

  private func loadSharedContent(completion: @escaping (String, URL?) -> Void) {
    let items = extensionContext?.inputItems as? [NSExtensionItem]
    let suggestedTitle = items?.compactMap {
      $0.attributedTitle?.string ?? $0.attributedContentText?.string
    }.first
    guard
      let providers = items?
        .compactMap(\.attachments)
        .flatMap({ $0 })
    else {
      completion("", nil)
      return
    }
    if let provider = providers.first(where: {
      $0.hasItemConformingToTypeIdentifier(UTType.url.identifier)
    }) {
      provider.loadItem(forTypeIdentifier: UTType.url.identifier) { item, _ in
        let url = item as? URL
        DispatchQueue.main.async { completion(suggestedTitle ?? url?.absoluteString ?? "", url) }
      }
    } else if let provider = providers.first(where: {
      $0.hasItemConformingToTypeIdentifier(UTType.plainText.identifier)
    }) {
      provider.loadItem(forTypeIdentifier: UTType.plainText.identifier) { item, _ in
        DispatchQueue.main.async { completion((item as? String) ?? suggestedTitle ?? "", nil) }
      }
    } else {
      completion("", nil)
    }
  }
}

private struct ShareCaptureView: View {
  let referenceURL: URL?
  let completion: (Bool) -> Void
  @State private var title: String
  @State private var contexts: [CheckmateContext] = []
  @State private var contextId: String?
  @State private var dueOn = ""
  @State private var saving = false
  @State private var errorMessage: String?

  init(initialTitle: String, referenceURL: URL?, completion: @escaping (Bool) -> Void) {
    self.referenceURL = referenceURL
    self.completion = completion
    _title = State(initialValue: initialTitle)
  }

  var body: some View {
    NavigationStack {
      Form {
        Section("Task") {
          TextField("What needs doing?", text: $title, axis: .vertical).lineLimit(2...5)
          if let referenceURL {
            Text(referenceURL.absoluteString).font(.caption).foregroundStyle(.secondary).lineLimit(
              2)
          }
        }
        Section("Organize") {
          Picker("Context", selection: $contextId) {
            Text("Inbox").tag(String?.none)
            ForEach(contexts) { Text($0.name).tag(Optional($0.id)) }
          }
          TextField("Due · YYYY-MM-DD", text: $dueOn)
        }
        if let errorMessage {
          Section {
            Label(errorMessage, systemImage: "exclamationmark.triangle").foregroundStyle(.red)
          }
        }
      }
      .navigationTitle("Add to Checkmate")
      .navigationBarTitleDisplayMode(.inline)
      .toolbar {
        ToolbarItem(placement: .cancellationAction) { Button("Cancel") { completion(false) } }
        ToolbarItem(placement: .confirmationAction) {
          Button(saving ? "Saving…" : "Save") { save() }
            .disabled(title.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || saving)
        }
      }
      .task { await loadContexts() }
    }
  }

  private func loadContexts() async {
    do {
      let store = try LocalStore.shared()
      contexts = try await store.snapshot().contexts.filter {
        $0.archivedAt == nil && $0.deletedAt == nil
      }
    } catch { errorMessage = "Open Checkmate once before using the share sheet." }
  }

  private func save() {
    saving = true
    errorMessage = nil
    _Concurrency.Task {
      do {
        var body: [String: JSONValue] = [
          "title": .string(title.trimmingCharacters(in: .whitespacesAndNewlines)),
          // The API contract assigns `chrome_ext` to every share extension client.
          "capture_method": .string("chrome_ext"),
        ]
        if let contextId { body["context_id"] = .string(contextId) }
        if !dueOn.isEmpty { body["due_on"] = .string(dueOn) }
        if let referenceURL {
          body["reference_url"] = .string(referenceURL.absoluteString)
          body["reference_label"] = .string(title)
        }
        _ = try await SharedConfiguration.client().createTask(body)
        completion(true)
      } catch {
        errorMessage =
          "\(error.localizedDescription) The share extension cannot queue changes offline."
        saving = false
      }
    }
  }
}

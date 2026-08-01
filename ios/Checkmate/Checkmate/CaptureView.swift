import CheckmateCore
import SwiftUI

struct CaptureView: View {
  @Environment(\.dismiss) private var dismiss
  @Environment(AppModel.self) private var model
  @State private var text = ""
  @State private var isSaving = false
  @State private var errorMessage: String?
  @FocusState private var focused: Bool

  private var parse: CaptureParse {
    CaptureParser.parse(text, contexts: model.contexts, people: model.people)
  }

  var body: some View {
    NavigationStack {
      WarmPage {
        VStack(alignment: .leading, spacing: 14) {
          TextField("What needs doing?", text: $text, axis: .vertical)
            .font(.title3)
            .lineLimit(2...5)
            .focused($focused)
            .submitLabel(.done)
            .onSubmit { save() }

          preview
          if let errorMessage {
            Label(errorMessage, systemImage: "exclamationmark.triangle")
              .font(.caption)
              .foregroundStyle(CheckmateTheme.overdue)
          }
          if model.isOffline {
            Text(
              "Capture is disabled while the server is unreachable. Your draft stays in this sheet."
            )
            .font(.caption)
            .foregroundStyle(CheckmateTheme.overdue)
          }
          Spacer()
          tokenBar
        }
        .padding(20)
      }
      .navigationTitle("Add a task")
      .navigationBarTitleDisplayMode(.inline)
      .toolbar {
        ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
        ToolbarItem(placement: .confirmationAction) {
          Button(isSaving ? "Adding…" : "Add") { save() }
            .disabled(parse.title.isEmpty || isSaving || model.isOffline)
        }
      }
      .task { focused = true }
    }
    .presentationDetents([.medium, .large])
    .interactiveDismissDisabled(!text.isEmpty && errorMessage != nil)
  }

  private var preview: some View {
    ScrollView(.horizontal, showsIndicators: false) {
      HStack(spacing: 8) {
        if let context = parse.context {
          chip(context.name, color: Color(hex: context.color) ?? CheckmateTheme.accent)
        }
        if let due = parse.dueOn { chip("Due \(due)", color: CheckmateTheme.ochre) }
        if let planned = parse.plannedOn { chip("Plan \(planned)", color: CheckmateTheme.olive) }
        if let estimate = parse.estimateMinutes {
          chip("\(estimate)m", color: CheckmateTheme.secondary)
        }
        if let person = parse.person { chip("→ \(person.name)", color: CheckmateTheme.dusk) }
        if let source = parse.source { chip(source, color: CheckmateTheme.tertiary) }
        ForEach(parse.unresolved, id: \.self) { chip($0, color: CheckmateTheme.overdue) }
      }
    }
    .frame(minHeight: 30)
  }

  private var tokenBar: some View {
    ScrollView(.horizontal, showsIndicators: false) {
      HStack(spacing: 8) {
        ForEach(suggestions, id: \.self) { token in
          Button(token) {
            text += (text.isEmpty || text.hasSuffix(" ") ? "" : " ") + token
            focused = true
          }
          .buttonStyle(.bordered)
          .font(.caption.monospaced())
        }
      }
    }
  }

  private var suggestions: [String] {
    model.contexts.prefix(4).map { "#\($0.slug)" } + ["today", ">tomorrow", "30m"]
      + model.people.prefix(1).map { "@\($0.name)" }
  }

  private func chip(_ label: String, color: Color) -> some View {
    Text(label)
      .font(.caption.weight(.medium))
      .padding(.horizontal, 9).padding(.vertical, 5)
      .foregroundStyle(color)
      .background(color.opacity(0.12), in: Capsule())
  }

  private func save() {
    guard !parse.title.isEmpty else { return }
    isSaving = true
    errorMessage = nil
    _Concurrency.Task {
      do {
        try await model.createTask(from: parse)
        UINotificationFeedbackGenerator().notificationOccurred(.success)
        dismiss()
      } catch {
        errorMessage = error.localizedDescription
        UINotificationFeedbackGenerator().notificationOccurred(.error)
      }
      isSaving = false
    }
  }
}

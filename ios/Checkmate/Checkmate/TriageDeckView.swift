import CheckmateCore
import SwiftUI

struct TriageDeckView: View {
  @Environment(\.dismiss) private var dismiss
  @Environment(AppModel.self) private var model
  let tasks: [CheckmateTask]
  @State private var index = 0
  @State private var offset: CGSize = .zero
  @State private var contextPicker = false

  private var current: CheckmateTask? { index < tasks.count ? tasks[index] : nil }

  var body: some View {
    NavigationStack {
      WarmPage {
        VStack(spacing: 20) {
          ProgressView(value: Double(index), total: Double(max(tasks.count, 1)))
            .tint(CheckmateTheme.accent)
          if let task = current {
            VStack(alignment: .leading, spacing: 16) {
              Text(task.title).font(.checkmateTitle(.title))
              if let details = task.details {
                Text(details).foregroundStyle(CheckmateTheme.secondary)
              }
              Text("Swipe right for To do · up for context · down to skip")
                .font(.caption).foregroundStyle(CheckmateTheme.tertiary)
            }
            .padding(24)
            .frame(maxWidth: .infinity, minHeight: 300, alignment: .topLeading)
            .background(CheckmateTheme.card, in: RoundedRectangle(cornerRadius: 22))
            .shadow(color: .black.opacity(0.08), radius: 18, y: 8)
            .offset(offset)
            .rotationEffect(.degrees(Double(offset.width / 30)))
            .gesture(DragGesture().onChanged { offset = $0.translation }.onEnded(handleDrag))

            HStack {
              Button("Skip", systemImage: "arrow.down") { advance() }.buttonStyle(.bordered)
              Button("Context", systemImage: "arrow.up") { contextPicker = true }.buttonStyle(
                .bordered)
              Button("To do", systemImage: "arrow.right") { update(["status": .string("todo")]) }
                .buttonStyle(.borderedProminent)
            }
          } else {
            ContentUnavailableView(
              "Inbox triaged", systemImage: "checkmark.circle",
              description: Text("Every captured task has been reviewed."))
            Button("Done") { dismiss() }.buttonStyle(.borderedProminent)
          }
          Spacer()
        }
        .padding(20)
      }
      .navigationTitle("Triage")
      .navigationBarTitleDisplayMode(.inline)
      .toolbar { ToolbarItem(placement: .cancellationAction) { Button("Close") { dismiss() } } }
      .confirmationDialog("Choose a context", isPresented: $contextPicker) {
        ForEach(model.contexts.filter { $0.archivedAt == nil }) { context in
          Button(context.name) {
            update(["context_id": .string(context.id), "status": .string("todo")])
          }
        }
      }
    }
  }

  private func handleDrag(_ value: DragGesture.Value) {
    if value.translation.width > 100 {
      update(["status": .string("todo")])
    } else if value.translation.height < -100 {
      contextPicker = true
      offset = .zero
    } else if value.translation.height > 100 {
      advance()
    } else {
      withAnimation(.spring) { offset = .zero }
    }
  }

  private func update(_ body: [String: JSONValue]) {
    guard let current else { return }
    _Concurrency.Task {
      do {
        try await model.updateTask(current, body: body)
        UIImpactFeedbackGenerator(style: .medium).impactOccurred()
        advance()
      } catch {
        model.alertMessage = error.localizedDescription
        offset = .zero
      }
    }
  }

  private func advance() {
    withAnimation(.snappy) {
      index += 1
      offset = .zero
    }
  }
}

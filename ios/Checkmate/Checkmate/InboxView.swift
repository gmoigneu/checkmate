import CheckmateCore
import SwiftUI

struct InboxView: View {
  @Environment(AppModel.self) private var model
  @State private var triagePresented = false

  private var inbox: [CheckmateTask] {
    model.tasks.filter { $0.status == .inbox }.sorted { $0.createdAt < $1.createdAt }
  }

  var body: some View {
    NavigationStack {
      WarmPage {
        ScrollView {
          VStack(spacing: 16) {
            if model.isOffline { OfflineBanner(lastSyncAt: model.lastSyncAt) }
            if inbox.isEmpty {
              ContentUnavailableView(
                "Inbox zero", systemImage: "tray",
                description: Text("Everything has been given a place.")
              )
              .padding(.top, 80)
            } else {
              Button("Triage \(inbox.count) tasks") { triagePresented = true }
                .buttonStyle(.borderedProminent)
              TaskCardSection(title: "Oldest first", tasks: inbox)
            }
          }
          .padding(12)
        }
        .refreshable { await model.refresh() }
      }
      .navigationTitle("Inbox")
      .navigationDestination(for: CheckmateTask.self) { TaskDetailView(task: $0) }
      .sheet(isPresented: $triagePresented) { TriageDeckView(tasks: inbox) }
    }
  }
}

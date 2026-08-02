import CheckmateCore
import SwiftUI

struct WaitingView: View {
  @Environment(AppModel.self) private var model

  private var groups: [WaitingGroup] {
    model.brief?.waitingOn ?? []
  }

  var body: some View {
    WarmPage {
      ScrollView {
        LazyVStack(spacing: 18) {
          if model.isOffline { OfflineBanner(lastSyncAt: model.lastSyncAt) }
          if groups.isEmpty {
            ContentUnavailableView(
              "Nothing waiting", systemImage: "hourglass",
              description: Text("Delegated work will be grouped here by person.")
            )
            .padding(.top, 80)
          }
          ForEach(groups) { group in
            TaskCardSection(title: group.personName, tasks: group.tasks)
          }
        }
        .padding(12)
      }
      .refreshable { await model.refresh() }
    }
    .navigationTitle("Waiting")
    .navigationDestination(for: CheckmateTask.self) { TaskDetailView(task: $0) }
  }
}

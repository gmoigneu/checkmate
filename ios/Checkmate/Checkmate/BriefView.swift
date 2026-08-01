import CheckmateCore
import SwiftUI

struct BriefView: View {
  @Environment(AppModel.self) private var model
  @State private var selectedContextId: String?
  @State private var selectedDate = Date.now
  @State private var isLoadingDate = false

  private var dateString: String { CalendarDate.string(selectedDate) }

  var body: some View {
    NavigationStack {
      WarmPage {
        ScrollView {
          LazyVStack(spacing: 20) {
            if model.isOffline { OfflineBanner(lastSyncAt: model.lastSyncAt) }
            if let brief = model.brief {
              BriefSummaryView(totals: brief.totals)
              sections(brief)
                .padding(.horizontal, 12)
            } else {
              ContentUnavailableView(
                "No brief yet", systemImage: "sun.max",
                description: Text("Pull to refresh after connecting to your server.")
              )
              .padding(.top, 80)
            }
          }
          .padding(.vertical, 12)
        }
        .refreshable { await loadBrief() }
      }
      .navigationTitle(
        Calendar.current.isDateInToday(selectedDate)
          ? "Today" : selectedDate.formatted(date: .complete, time: .omitted)
      )
      .navigationBarTitleDisplayMode(.large)
      .toolbar {
        ToolbarItem(placement: .topBarLeading) {
          HStack(spacing: 4) {
            Button {
              stepDate(-1)
            } label: {
              Image(systemName: "chevron.left")
            }
            Button("Today") {
              selectedDate = .now
              _Concurrency.Task { await loadBrief() }
            }
            .font(.caption)
            Button {
              stepDate(1)
            } label: {
              Image(systemName: "chevron.right")
            }
          }
          .disabled(isLoadingDate)
        }
        ToolbarItem(placement: .topBarTrailing) {
          Menu {
            Button("All contexts") { selectContext(nil) }
            ForEach(model.contexts.filter { $0.archivedAt == nil }) { context in
              Button(context.name) { selectContext(context.id) }
            }
          } label: {
            Image(
              systemName: selectedContextId == nil
                ? "slider.horizontal.3" : "line.3.horizontal.decrease.circle.fill")
          }
        }
      }
      .overlay { if isLoadingDate { ProgressView().controlSize(.large) } }
      .navigationDestination(for: CheckmateCore.Task.self) { TaskDetailView(task: $0) }
    }
  }

  @ViewBuilder
  private func sections(_ brief: Brief) -> some View {
    let overdueIds = Set(brief.overdue.map(\.id))
    let due = brief.dueToday.filter { !overdueIds.contains($0.id) }
    let seen = overdueIds.union(due.map(\.id))
    let planned = brief.planned.filter { !seen.contains($0.id) }

    if !brief.routine.isEmpty {
      TaskCardSection(
        title: "Routine · \(brief.totals.routineDone)/\(brief.totals.routine)", tasks: brief.routine
      )
    }
    if dateString <= CalendarDate.string(.now), !brief.inProgress.isEmpty {
      TaskCardSection(title: "In progress · \(brief.totals.inProgress)", tasks: brief.inProgress)
    }
    if !brief.overdue.isEmpty {
      TaskCardSection(title: "Overdue · \(brief.totals.overdue)", tasks: brief.overdue)
    }
    if !due.isEmpty {
      TaskCardSection(
        title: "Due today · \(brief.totals.dueToday)", tasks: due,
        footer: "Unestimated tasks come first — this is the order from your server.")
    }
    if !planned.isEmpty {
      TaskCardSection(
        title: "Planned today · \(brief.totals.planned) · \(brief.totals.plannedMinutes)m",
        tasks: planned)
    }
    if !brief.blocked.isEmpty {
      TaskCardSection(title: "Blocked · \(brief.totals.blocked)", tasks: brief.blocked)
    }
    ForEach(brief.waitingOn) { group in
      TaskCardSection(title: "Waiting on \(group.personName)", tasks: group.tasks)
    }
    if !brief.inbox.isEmpty {
      TaskCardSection(
        title: "Inbox · \(brief.totals.inbox) · all contexts", tasks: Array(brief.inbox.prefix(3)))
    }
    if dateString <= CalendarDate.string(.now), !brief.completedToday.isEmpty {
      TaskCardSection(
        title: "Completed · \(brief.totals.completedToday)", tasks: brief.completedToday)
    }
    if dateString <= CalendarDate.string(.now), !brief.cancelledToday.isEmpty {
      TaskCardSection(
        title: "Cancelled · \(brief.totals.cancelledToday)", tasks: brief.cancelledToday)
    }
  }

  private func selectContext(_ id: String?) {
    selectedContextId = id
    _Concurrency.Task { await loadBrief() }
  }

  private func stepDate(_ value: Int) {
    selectedDate =
      Calendar.current.date(byAdding: .day, value: value, to: selectedDate) ?? selectedDate
    _Concurrency.Task { await loadBrief() }
  }

  private func loadBrief() async {
    guard let client = model.client else { return }
    isLoadingDate = true
    defer { isLoadingDate = false }
    do {
      model.brief = try await client.brief(date: dateString, contextId: selectedContextId)
      model.isOffline = false
    } catch {
      model.isOffline = true
      model.alertMessage = error.localizedDescription
    }
  }
}

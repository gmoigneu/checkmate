import CheckmateCore
import SwiftUI

struct TaskRowView: View {
  @Environment(AppModel.self) private var model
  let task: CheckmateCore.Task
  var showContext = true

  private var context: CheckmateContext? {
    model.contexts.first { $0.id == task.contextId }
  }

  var body: some View {
    HStack(alignment: .top, spacing: 12) {
      Button {
        _Concurrency.Task { await model.setStatus(task, to: task.status == .done ? .todo : .done) }
      } label: {
        Image(systemName: task.status == .done ? "checkmark.circle.fill" : "circle")
          .font(.title3)
          .foregroundStyle(task.status == .done ? CheckmateTheme.olive : CheckmateTheme.tertiary)
      }
      .buttonStyle(.plain)
      .disabled(task.status == .expired || model.isOffline)
      .accessibilityLabel(task.status == .done ? "Reopen \(task.title)" : "Complete \(task.title)")

      VStack(alignment: .leading, spacing: 6) {
        Text(task.title)
          .font(.body)
          .foregroundStyle(task.status.isOpen ? CheckmateTheme.primary : CheckmateTheme.tertiary)
          .strikethrough(task.status == .done || task.status == .cancelled)
          .multilineTextAlignment(.leading)

        HStack(spacing: 8) {
          if showContext, let context {
            Label {
              Text(context.name)
            } icon: {
              ContextDot(context: context, size: 7)
            }
          }
          if let due = task.dueOn {
            Label(due, systemImage: "flag")
              .foregroundStyle(
                due < CalendarDate.string(.now) && task.status.isOpen
                  ? CheckmateTheme.overdue : CheckmateTheme.tertiary)
          }
          if let planned = task.plannedOn {
            Label(planned, systemImage: "calendar")
              .foregroundStyle(CheckmateTheme.olive)
          }
          if let estimate = task.estimateMinutes {
            Label(format(minutes: estimate), systemImage: "clock")
          }
        }
        .font(.caption.monospacedDigit())
        .foregroundStyle(CheckmateTheme.tertiary)
        .lineLimit(1)
      }
      Spacer(minLength: 0)
      if task.status == .blocked {
        Image(systemName: "pause.circle").foregroundStyle(CheckmateTheme.dusk)
      }
      if task.status == .delegated {
        Image(systemName: "person.crop.circle.badge.clock").foregroundStyle(
          CheckmateTheme.secondary)
      }
    }
    .padding(.horizontal, 12)
    .padding(.vertical, 11)
    .contentShape(Rectangle())
    .accessibilityElement(children: .combine)
  }

  private func format(minutes: Int64) -> String {
    minutes >= 60
      ? "\(minutes / 60)h\(minutes % 60 == 0 ? "" : " \(minutes % 60)m")" : "\(minutes)m"
  }
}

struct TaskCardSection: View {
  let title: String
  let tasks: [CheckmateCore.Task]
  var footer: String?

  var body: some View {
    InsetCard(title: title, footer: footer) {
      ForEach(Array(tasks.enumerated()), id: \.element.id) { index, task in
        NavigationLink(value: task) { TaskRowView(task: task) }
          .buttonStyle(.plain)
        if index < tasks.count - 1 { Divider().padding(.leading, 46) }
      }
    }
  }
}

struct BriefSummaryView: View {
  let totals: BriefTotals

  var body: some View {
    ScrollView(.horizontal, showsIndicators: false) {
      HStack(spacing: 10) {
        metric(totals.overdue, "overdue", CheckmateTheme.overdue)
        metric(totals.dueToday, "due today", CheckmateTheme.ochre)
        metric(totals.planned, "planned", CheckmateTheme.olive)
        metric(totals.completedToday, "done", CheckmateTheme.tertiary)
        VStack(alignment: .leading, spacing: 2) {
          Text(format(minutes: totals.plannedMinutes)).font(.headline.monospacedDigit())
          Text(
            totals.plannedWithoutEstimate == 0
              ? "estimated" : "+ \(totals.plannedWithoutEstimate) unestimated"
          )
          .font(.caption2)
        }
        .padding(.horizontal, 12).padding(.vertical, 10)
        .background(CheckmateTheme.card, in: RoundedRectangle(cornerRadius: 10))
      }
      .padding(.horizontal, 16)
    }
  }

  private func metric(_ value: Int, _ label: String, _ color: Color) -> some View {
    VStack(alignment: .leading, spacing: 2) {
      Text(value, format: .number).font(.headline.monospacedDigit()).foregroundStyle(color)
      Text(label).font(.caption2).foregroundStyle(CheckmateTheme.tertiary)
    }
    .padding(.horizontal, 12).padding(.vertical, 10)
    .background(CheckmateTheme.card, in: RoundedRectangle(cornerRadius: 10))
  }

  private func format(minutes: Int) -> String {
    minutes >= 60 ? "\(minutes / 60)h\(minutes % 60 == 0 ? "" : "\(minutes % 60)")" : "\(minutes)m"
  }
}

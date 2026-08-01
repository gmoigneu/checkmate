import AppIntents
import CheckmateCore
import SwiftUI
import WidgetKit

private struct BriefEntry: TimelineEntry {
  let date: Date
  let overdue: Int
  let dueToday: Int
  let planned: Int
  let plannedMinutes: Int64
  let waiting: Int
  let tasks: [CheckmateCore.Task]
  let lastSyncAt: Date?
}

private struct BriefProvider: TimelineProvider {
  func placeholder(in context: Context) -> BriefEntry { sample }

  func getSnapshot(in context: Context, completion: @escaping (BriefEntry) -> Void) {
    load(completion: completion)
  }

  func getTimeline(in context: Context, completion: @escaping (Timeline<BriefEntry>) -> Void) {
    load { entry in
      let refresh =
        Calendar.current.date(byAdding: .minute, value: 30, to: .now)
        ?? .now.addingTimeInterval(1_800)
      completion(Timeline(entries: [entry], policy: .after(refresh)))
    }
  }

  private func load(completion: @escaping (BriefEntry) -> Void) {
    _Concurrency.Task {
      do {
        let store = try LocalStore(storeURL: LocalStore.sharedStoreURL())
        let snapshot = try await store.snapshot()
        let today = CalendarDate.string(.now)
        let open = snapshot.tasks.filter {
          $0.deletedAt == nil && $0.status.isOpen && $0.kind != .routine
        }
        let overdueTasks = open.filter { ($0.dueOn ?? today) < today }.sorted {
          ($0.dueOn ?? "") < ($1.dueOn ?? "")
        }
        let dueTasks = open.filter { $0.dueOn == today }.sorted {
          ($0.estimateMinutes ?? 0) < ($1.estimateMinutes ?? 0)
        }
        let plannedTasks = open.filter { $0.plannedOn == today }
        let unique = Dictionary(grouping: overdueTasks + dueTasks + plannedTasks, by: \.id)
          .compactMap(\.value.first)
        completion(
          BriefEntry(
            date: .now,
            overdue: overdueTasks.count,
            dueToday: dueTasks.count,
            planned: plannedTasks.count,
            plannedMinutes: plannedTasks.compactMap(\.estimateMinutes).reduce(0, +),
            waiting: open.filter { $0.status == .delegated }.count,
            tasks: Array(unique.prefix(8)),
            lastSyncAt: snapshot.lastSyncAt
          ))
      } catch {
        completion(sample)
      }
    }
  }

  private var sample: BriefEntry {
    BriefEntry(
      date: .now, overdue: 0, dueToday: 0, planned: 0, plannedMinutes: 0, waiting: 0, tasks: [],
      lastSyncAt: nil)
  }
}

private struct BriefWidgetView: View {
  @Environment(\.widgetFamily) private var family
  let entry: BriefEntry

  var body: some View {
    switch family {
    case .systemSmall: small
    case .systemMedium: taskList(limit: 4)
    case .systemLarge: taskList(limit: 8)
    case .accessoryCircular: circular
    case .accessoryRectangular: rectangular
    case .accessoryInline: inline
    default: small
    }
  }

  private var small: some View {
    Link(destination: URL(string: "io.nls.checkmate://brief")!) {
      VStack(alignment: .leading, spacing: 10) {
        Label("Today", systemImage: "sun.max.fill").font(.headline)
        if entry.overdue + entry.dueToday + entry.planned == 0 {
          Spacer()
          Label("Clear day", systemImage: "checkmark.seal.fill").font(.title3)
          Text("Nothing needs your attention.").font(.caption).foregroundStyle(.secondary)
        } else {
          HStack(spacing: 12) {
            metric(entry.overdue, "late", .red)
            metric(entry.dueToday, "due", .orange)
            metric(entry.planned, "plan", .green)
          }
          Spacer()
          Text("\(format(entry.plannedMinutes)) planned").font(.caption).foregroundStyle(.secondary)
        }
        freshness
      }
    }
  }

  private func taskList(limit: Int) -> some View {
    VStack(alignment: .leading, spacing: 8) {
      Link(destination: URL(string: "io.nls.checkmate://brief")!) {
        HStack {
          Label("Checkmate", systemImage: "sun.max.fill").font(.headline)
          Spacer()
          Text("\(entry.overdue) late · \(entry.dueToday) due").font(.caption).foregroundStyle(
            .secondary)
        }
      }
      if entry.tasks.isEmpty {
        Spacer()
        Label("You're clear for today", systemImage: "checkmark.seal.fill")
          .font(.title3)
        Spacer()
      } else {
        ForEach(entry.tasks.prefix(limit)) { task in
          Link(destination: URL(string: "io.nls.checkmate://task/\(task.id)")!) {
            HStack(spacing: 7) {
              Circle().stroke(.secondary, lineWidth: 1.2).frame(width: 13, height: 13)
              Text(task.title).lineLimit(1).font(.caption)
              Spacer()
              if let due = task.dueOn {
                Text(due).font(.caption2.monospacedDigit()).foregroundStyle(.secondary)
              }
            }
          }
        }
        Spacer(minLength: 0)
      }
      HStack {
        freshness
        Spacer()
        if entry.waiting > 0 { Text("\(entry.waiting) waiting").font(.caption2) }
      }
    }
  }

  private var circular: some View {
    Gauge(value: Double(min(entry.overdue, 10)), in: 0...10) {
      Image(systemName: "exclamationmark")
    } currentValueLabel: {
      Text(entry.overdue, format: .number)
    }
    .gaugeStyle(.accessoryCircularCapacity)
  }

  private var rectangular: some View {
    VStack(alignment: .leading) {
      Text(entry.tasks.first?.title ?? "Clear day").lineLimit(1)
      Text("\(entry.overdue) overdue · \(entry.dueToday) due").font(.caption)
    }
  }

  private var inline: some View {
    Text("\(entry.overdue) overdue · \(entry.dueToday) due · \(entry.planned) planned")
  }

  private var freshness: some View {
    Text(entry.lastSyncAt.map { "Updated \($0, style: .relative)" } ?? "Open app to sync")
      .font(.caption2).foregroundStyle(.secondary).lineLimit(1)
  }

  private func metric(_ value: Int, _ label: String, _ color: Color) -> some View {
    VStack(alignment: .leading, spacing: 1) {
      Text(value, format: .number).font(.title2.bold()).foregroundStyle(color)
      Text(label).font(.caption2).foregroundStyle(.secondary)
    }
  }

  private func format(_ minutes: Int64) -> String {
    minutes >= 60 ? "\(minutes / 60)h \(minutes % 60)m" : "\(minutes)m"
  }
}

struct CheckmateBriefWidget: Widget {
  let kind = "CheckmateBrief"

  var body: some WidgetConfiguration {
    StaticConfiguration(kind: kind, provider: BriefProvider()) { entry in
      BriefWidgetView(entry: entry)
        .containerBackground(for: .widget) { Color(red: 0.98, green: 0.96, blue: 0.93) }
    }
    .configurationDisplayName("Today's Brief")
    .description("Overdue, due, planned, and the next tasks from your last sync.")
    .supportedFamilies([
      .systemSmall, .systemMedium, .systemLarge, .accessoryCircular, .accessoryRectangular,
      .accessoryInline,
    ])
  }
}

@available(iOSApplicationExtension 18.0, *)
struct CheckmateCaptureControl: ControlWidget {
  static let kind = "io.nls.Checkmate.capture"

  var body: some ControlWidgetConfiguration {
    StaticControlConfiguration(kind: Self.kind) {
      ControlWidgetButton(action: OpenURLIntent(URL(string: "io.nls.checkmate://capture")!)) {
        Label("Capture", systemImage: "plus.circle.fill")
      }
    }
    .displayName("Capture in Checkmate")
    .description("Open the Checkmate capture sheet.")
  }
}

@main
struct CheckmateWidgetsBundle: WidgetBundle {
  var body: some Widget {
    CheckmateBriefWidget()
    CheckmateCaptureControl()
  }
}

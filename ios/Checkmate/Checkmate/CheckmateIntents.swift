import AppIntents
import CheckmateCore
import Foundation

struct CheckmateContextEntity: AppEntity, Hashable {
  static let typeDisplayRepresentation = TypeDisplayRepresentation(name: "Checkmate context")
  static let defaultQuery = CheckmateContextQuery()

  let id: String
  let name: String

  var displayRepresentation: DisplayRepresentation { DisplayRepresentation(title: "\(name)") }
}

struct CheckmateContextQuery: EntityQuery {
  func entities(for identifiers: [String]) async throws -> [CheckmateContextEntity] {
    try await all().filter { identifiers.contains($0.id) }
  }

  func suggestedEntities() async throws -> [CheckmateContextEntity] { try await all() }

  private func all() async throws -> [CheckmateContextEntity] {
    let store = try LocalStore(storeURL: LocalStore.sharedStoreURL())
    return try await store.snapshot().contexts
      .filter { $0.deletedAt == nil && $0.archivedAt == nil }
      .map { CheckmateContextEntity(id: $0.id, name: $0.name) }
  }
}

struct CheckmateTaskEntity: AppEntity, Hashable {
  static let typeDisplayRepresentation = TypeDisplayRepresentation(name: "Checkmate task")
  static let defaultQuery = CheckmateTaskQuery()

  let id: String
  let title: String

  var displayRepresentation: DisplayRepresentation { DisplayRepresentation(title: "\(title)") }
}

struct CheckmateTaskQuery: EntityStringQuery {
  func entities(for identifiers: [String]) async throws -> [CheckmateTaskEntity] {
    try await all().filter { identifiers.contains($0.id) }
  }

  func entities(matching string: String) async throws -> [CheckmateTaskEntity] {
    try await all().filter { $0.title.localizedCaseInsensitiveContains(string) }
  }

  func suggestedEntities() async throws -> [CheckmateTaskEntity] {
    try await Array(all().prefix(20))
  }

  private func all() async throws -> [CheckmateTaskEntity] {
    let store = try LocalStore(storeURL: LocalStore.sharedStoreURL())
    return try await store.snapshot().tasks
      .filter { $0.deletedAt == nil && $0.status.isOpen }
      .map { CheckmateTaskEntity(id: $0.id, title: $0.title) }
  }
}

struct AddCheckmateTaskIntent: AppIntent {
  static let title: LocalizedStringResource = "Add a task"
  static let description = IntentDescription(
    "Add a task to Checkmate with an optional context and due date.")
  static let openAppWhenRun = false

  @Parameter(title: "Task") var taskTitle: String
  @Parameter(title: "Context") var context: CheckmateContextEntity?
  @Parameter(title: "Due") var due: Date?

  static var parameterSummary: some ParameterSummary {
    Summary("Add \(\.$taskTitle) to Checkmate") {
      \.$context
      \.$due
    }
  }

  func perform() async throws -> some IntentResult & ProvidesDialog {
    let client = try SharedConfiguration.client()
    var body: [String: JSONValue] = [
      "title": .string(taskTitle), "capture_method": .string("voice"),
    ]
    if let context { body["context_id"] = .string(context.id) }
    if let due { body["due_on"] = .string(CalendarDate.string(due)) }
    let created = try await client.createTask(body)
    return .result(dialog: IntentDialog("Added “\(created.title)” to Checkmate."))
  }
}

struct CheckmateTodayIntent: AppIntent {
  static let title: LocalizedStringResource = "What's on today"
  static let description = IntentDescription("Read today's Checkmate headline counts.")

  func perform() async throws -> some IntentResult & ProvidesDialog {
    do {
      let brief = try await SharedConfiguration.client().brief()
      let totals = brief.totals
      let sentence =
        "You have \(totals.overdue) overdue, \(totals.dueToday) due today, and \(totals.planned) planned."
      return .result(dialog: IntentDialog(stringLiteral: sentence))
    } catch {
      let store = try LocalStore(storeURL: LocalStore.sharedStoreURL())
      let snapshot = try await store.snapshot()
      let today = CalendarDate.string(.now)
      let open = snapshot.tasks.filter(\.status.isOpen)
      let overdue = open.filter { ($0.dueOn ?? today) < today }.count
      let due = open.filter { $0.dueOn == today }.count
      let planned = open.filter { $0.plannedOn == today }.count
      return .result(
        dialog: IntentDialog(
          "From your last sync: \(overdue) overdue, \(due) due today, and \(planned) planned."))
    }
  }
}

struct CompleteCheckmateTaskIntent: AppIntent {
  static let title: LocalizedStringResource = "Complete a task"
  static let description = IntentDescription("Mark an open Checkmate task as done.")

  @Parameter(title: "Task") var task: CheckmateTaskEntity

  static var parameterSummary: some ParameterSummary { Summary("Complete \(\.$task)") }

  func perform() async throws -> some IntentResult & ProvidesDialog {
    let updated = try await SharedConfiguration.client().updateTask(
      id: task.id, body: ["status": .string("done")])
    return .result(dialog: IntentDialog("Completed “\(updated.title)”."))
  }
}

struct CheckmateShortcuts: AppShortcutsProvider {
  static var appShortcuts: [AppShortcut] {
    AppShortcut(
      intent: AddCheckmateTaskIntent(),
      phrases: ["Add a task to \(.applicationName)", "Remind me in \(.applicationName)"],
      shortTitle: "Add task",
      systemImageName: "plus.circle.fill"
    )
    AppShortcut(
      intent: CheckmateTodayIntent(),
      phrases: ["What's on today in \(.applicationName)"],
      shortTitle: "What's on today",
      systemImageName: "sun.max"
    )
    AppShortcut(
      intent: CompleteCheckmateTaskIntent(),
      phrases: ["Complete a task in \(.applicationName)"],
      shortTitle: "Complete task",
      systemImageName: "checkmark.circle"
    )
  }
}

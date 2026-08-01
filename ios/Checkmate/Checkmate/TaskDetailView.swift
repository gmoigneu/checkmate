import CheckmateCore
import SwiftUI

struct TaskDetailView: View {
  @Environment(AppModel.self) private var model
  @Environment(\.dismiss) private var dismiss
  let task: CheckmateTask

  @State private var title: String
  @State private var details: String
  @State private var status: TaskStatus
  @State private var priority: TaskPriority?
  @State private var contextId: String?
  @State private var projectId: String?
  @State private var delegatedToId: String?
  @State private var blockedById: String?
  @State private var source: String?
  @State private var dueOn: String
  @State private var plannedOn: String
  @State private var estimate: String
  @State private var referenceURL: String
  @State private var referenceLabel: String
  @State private var saving = false
  @State private var confirmDelete = false

  init(task: CheckmateTask) {
    self.task = task
    _title = State(initialValue: task.title)
    _details = State(initialValue: task.details ?? "")
    _status = State(initialValue: task.status)
    _priority = State(initialValue: task.priority)
    _contextId = State(initialValue: task.contextId)
    _projectId = State(initialValue: task.projectId)
    _delegatedToId = State(initialValue: task.delegatedToId)
    _blockedById = State(initialValue: task.blockedById)
    _source = State(initialValue: task.source)
    _dueOn = State(initialValue: task.dueOn ?? "")
    _plannedOn = State(initialValue: task.plannedOn ?? "")
    _estimate = State(initialValue: task.estimateMinutes.map(String.init) ?? "")
    _referenceURL = State(initialValue: task.referenceUrl ?? "")
    _referenceLabel = State(initialValue: task.referenceLabel ?? "")
  }

  private var availableProjects: [Project] {
    model.projects.filter { $0.contextId == contextId && $0.status != .archived }
  }

  private var children: [CheckmateTask] {
    model.tasks.filter { $0.parentId == task.id }
  }

  private var blocking: [CheckmateTask] {
    model.tasks.filter { $0.blockedById == task.id }
  }

  var body: some View {
    Form {
      Section {
        TextField("Title", text: $title, axis: .vertical)
          .font(.checkmateTitle(.title2))
          .accessibilityIdentifier("task-title")
        TextField("Notes", text: $details, axis: .vertical)
          .lineLimit(3...12)
      }

      Section("State") {
        Picker("Status", selection: $status) {
          ForEach(TaskStatus.allCases.filter { $0 != .expired }, id: \.self) {
            Text(label($0)).tag($0)
          }
        }
        Picker("Priority", selection: $priority) {
          Text("No priority").tag(TaskPriority?.none)
          ForEach(TaskPriority.allCases, id: \.self) {
            Text($0.rawValue.capitalized).tag(Optional($0))
          }
        }
        Picker("Waiting on", selection: $delegatedToId) {
          Text("Nobody").tag(String?.none)
          ForEach(model.people) { Text($0.name).tag(Optional($0.id)) }
        }
        if status == .delegated && delegatedToId == nil {
          Label(
            "Choose who you are waiting on before saving.", systemImage: "exclamationmark.triangle"
          )
          .font(.caption)
          .foregroundStyle(CheckmateTheme.overdue)
        }
        Picker("Blocked by", selection: $blockedById) {
          Text("Nothing").tag(String?.none)
          ForEach(model.tasks.filter { $0.id != task.id && $0.status.isOpen }) {
            Text($0.title).tag(Optional($0.id))
          }
        }
      }

      Section("Organize") {
        Picker("Context", selection: $contextId) {
          Text("Inbox").tag(String?.none)
          ForEach(model.contexts.filter { $0.archivedAt == nil }) {
            Text($0.name).tag(Optional($0.id))
          }
        }
        .onChange(of: contextId) { _, newValue in
          if let projectId,
            !model.projects.contains(where: { $0.id == projectId && $0.contextId == newValue })
          {
            self.projectId = nil
          }
        }
        Picker("Project", selection: $projectId) {
          Text("No project").tag(String?.none)
          ForEach(availableProjects) { Text($0.name).tag(Optional($0.id)) }
        }
        .disabled(contextId == nil)
        Picker("Source", selection: $source) {
          Text("None").tag(String?.none)
          ForEach(model.sources) { Text($0.label).tag(Optional($0.key)) }
        }
      }

      Section {
        TextField("Due · YYYY-MM-DD", text: $dueOn).keyboardType(.numbersAndPunctuation)
        TextField("Planned · YYYY-MM-DD", text: $plannedOn).keyboardType(.numbersAndPunctuation)
        TextField("Estimate in minutes", text: $estimate).keyboardType(.numberPad)
      } header: {
        Text("Schedule")
      } footer: {
        Text("Due is the outside deadline; planned is when you intend to work.")
      }

      Section("Reference") {
        TextField("https://…", text: $referenceURL)
          .keyboardType(.URL)
          .textInputAutocapitalization(.never)
          .autocorrectionDisabled()
        TextField("Link label", text: $referenceLabel)
        if let url = URL(string: referenceURL), !referenceURL.isEmpty {
          Link("Open reference", destination: url)
        }
      }

      if !children.isEmpty {
        Section("Subtasks · \(children.filter { $0.status == .done }.count) of \(children.count)") {
          ForEach(children) { child in
            NavigationLink(value: child) { TaskRowView(task: child, showContext: false) }
          }
        }
      }
      if !blocking.isEmpty {
        Section("Blocking") {
          ForEach(blocking) { blocked in
            NavigationLink(value: blocked) { TaskRowView(task: blocked, showContext: false) }
          }
        }
      }

      if let recurrence = model.recurrences.first(where: { $0.id == task.recurrenceId }) {
        Section("Recurring") {
          LabeledContent("Rule", value: recurrence.rrule)
          if let next = recurrence.nextOccurrenceOn { LabeledContent("Next", value: next) }
        }
      }

      Section("Metadata") {
        LabeledContent("Captured via", value: task.captureMethod)
        LabeledContent("Created", value: task.createdAt)
        LabeledContent("Updated", value: task.updatedAt)
        LabeledContent("Kind", value: task.kind.rawValue)
      }

      Section {
        Button("Delete task", role: .destructive) { confirmDelete = true }
      }
    }
    .scrollContentBackground(.hidden)
    .background(CheckmateTheme.page)
    .navigationTitle("Task")
    .navigationBarTitleDisplayMode(.inline)
    .disabled(
      task.status == .expired || saving || model.isOffline || model.profile?.canWrite == false
    )
    .toolbar {
      ToolbarItem(placement: .confirmationAction) {
        Button(saving ? "Saving…" : "Save") { save() }
          .disabled(!canSave)
          .accessibilityIdentifier("save-task")
      }
    }
    .confirmationDialog("Delete this task?", isPresented: $confirmDelete, titleVisibility: .visible)
    {
      Button("Delete", role: .destructive) { delete() }
    } message: {
      Text(
        "Its subtasks are deleted with it. Checkmate can restore the deleted batch from activity history."
      )
    }
  }

  private var canSave: Bool {
    !title.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
      && (status != .delegated || delegatedToId != nil)
  }

  private func save() {
    saving = true
    var body: [String: JSONValue] = [:]
    if title != task.title { body["title"] = .string(title) }
    if details != (task.details ?? "") { body.set("details", details.isEmpty ? nil : details) }
    if status != task.status { body["status"] = .string(status.rawValue) }
    if priority != task.priority {
      body["priority"] = priority.map { .string($0.rawValue) } ?? .null
    }
    if contextId != task.contextId { body["context_id"] = contextId.map(JSONValue.string) ?? .null }
    if projectId != task.projectId { body["project_id"] = projectId.map(JSONValue.string) ?? .null }
    if delegatedToId != task.delegatedToId {
      body["delegated_to_id"] = delegatedToId.map(JSONValue.string) ?? .null
    }
    if blockedById != task.blockedById {
      body["blocked_by_id"] = blockedById.map(JSONValue.string) ?? .null
    }
    if source != task.source { body["source"] = source.map(JSONValue.string) ?? .null }
    if dueOn != (task.dueOn ?? "") { body.set("due_on", dueOn.isEmpty ? nil : dueOn) }
    if plannedOn != (task.plannedOn ?? "") {
      body.set("planned_on", plannedOn.isEmpty ? nil : plannedOn)
      if plannedOn.isEmpty { body["day_slot"] = .null }
    }
    let minutes = Int64(estimate)
    if minutes != task.estimateMinutes {
      body["estimate_minutes"] = minutes.map(JSONValue.integer) ?? .null
    }
    if referenceURL != (task.referenceUrl ?? "") {
      body.set("reference_url", referenceURL.isEmpty ? nil : referenceURL)
    }
    if referenceLabel != (task.referenceLabel ?? "") {
      body.set("reference_label", referenceLabel.isEmpty ? nil : referenceLabel)
    }

    _Concurrency.Task {
      do {
        try await model.updateTask(task, body: body)
        dismiss()
      } catch { model.alertMessage = error.localizedDescription }
      saving = false
    }
  }

  private func delete() {
    _Concurrency.Task {
      do {
        try await model.deleteTask(task)
        dismiss()
      } catch { model.alertMessage = error.localizedDescription }
    }
  }

  private func label(_ status: TaskStatus) -> String {
    switch status {
    case .inProgress: "In progress"
    case .todo: "To do"
    case .delegated: "Waiting on"
    default: status.rawValue.capitalized
    }
  }
}

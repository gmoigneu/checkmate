import CheckmateCore
import SwiftUI

func isUpcomingTask(status: TaskStatus, deletedAt: String?) -> Bool {
  guard deletedAt == nil else { return false }
  return switch status {
  case .todo, .inProgress, .blocked, .delegated: true
  case .inbox, .done, .cancelled, .expired: false
  }
}

func isUpcomingTask(_ task: CheckmateTask) -> Bool {
  isUpcomingTask(status: task.status, deletedAt: task.deletedAt)
}

func upcomingPriorityRank(_ priority: TaskPriority?) -> Int {
  switch priority {
  case .urgent: 0
  case .high: 1
  case .medium: 2
  case .low: 3
  case nil: 4
  }
}

func upcomingTaskComesBefore(
  priority: TaskPriority?, id: String, than otherPriority: TaskPriority?, otherID: String
) -> Bool {
  let rank = upcomingPriorityRank(priority)
  let otherRank = upcomingPriorityRank(otherPriority)
  return rank == otherRank ? id > otherID : rank < otherRank
}

struct UpcomingView: View {
  @Environment(AppModel.self) private var model
  @State private var selectedContextId: String?
  var embedded = false

  private var tasks: [CheckmateTask] {
    Array(
      model.tasks
        .filter {
          isUpcomingTask($0)
            && (selectedContextId == nil || $0.contextId == selectedContextId)
        }
        .sorted {
          upcomingTaskComesBefore(
            priority: $0.priority, id: $0.id, than: $1.priority, otherID: $1.id)
        }
        .prefix(200)
    )
  }

  @ViewBuilder
  var body: some View {
    if embedded {
      content
    } else {
      NavigationStack { content }
    }
  }

  private var content: some View {
    WarmPage {
      ScrollView {
        LazyVStack(spacing: 18) {
          if model.isOffline { OfflineBanner(lastSyncAt: model.lastSyncAt) }
          if tasks.isEmpty {
            ContentUnavailableView(
              "Nothing upcoming", systemImage: "calendar",
              description: Text("Open work will appear here.")
            )
            .padding(.top, 80)
          } else {
            TaskCardSection(title: "Open work · \(tasks.count)", tasks: tasks)
          }
        }
        .padding(12)
      }
      .refreshable { await model.refresh() }
    }
    .navigationTitle("Upcoming")
    .toolbar {
      ToolbarItem(placement: .topBarTrailing) {
        Menu {
          Button("All contexts") { selectedContextId = nil }
          ForEach(model.contexts.filter { $0.archivedAt == nil }) { context in
            Button(context.name) { selectedContextId = context.id }
          }
        } label: {
          Image(
            systemName: selectedContextId == nil
              ? "slider.horizontal.3" : "line.3.horizontal.decrease.circle.fill")
        }
      }
    }
    .navigationDestination(for: CheckmateTask.self) { TaskDetailView(task: $0) }
  }
}

struct ContextsView: View {
  @Environment(AppModel.self) private var model
  @State private var creating = false

  var body: some View {
    List {
      ForEach(model.contexts) { context in
        NavigationLink {
          ContextDetailView(context: context)
        } label: {
          HStack(spacing: 12) {
            ContextDot(context: context, size: 12)
            VStack(alignment: .leading, spacing: 3) {
              Text(context.name)
              let count = model.tasks.filter { $0.contextId == context.id && $0.status.isOpen }
                .count
              Text(
                "\(count) open · \(model.projects.filter { $0.contextId == context.id }.count) projects"
              )
              .font(.caption).foregroundStyle(.secondary)
            }
          }
        }
      }
    }
    .navigationTitle("Contexts")
    .toolbar { Button("New", systemImage: "plus") { creating = true } }
    .sheet(isPresented: $creating) { CreateContextView() }
    .overlay {
      if model.contexts.isEmpty {
        ContentUnavailableView(
          "No contexts", systemImage: "square.grid.2x2",
          description: Text("Create a context for each part of your life."))
      }
    }
  }
}

private struct CreateContextView: View {
  @Environment(AppModel.self) private var model
  @Environment(\.dismiss) private var dismiss
  @State private var name = ""
  @State private var color = "#C05E3C"
  @State private var saving = false

  var body: some View {
    NavigationStack {
      Form {
        TextField("Name", text: $name)
        TextField("Colour · #RRGGBB", text: $color)
          .textInputAutocapitalization(.characters)
          .autocorrectionDisabled()
      }
      .navigationTitle("New context")
      .toolbar {
        ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
        ToolbarItem(placement: .confirmationAction) {
          Button(saving ? "Creating…" : "Create") { create() }
            .disabled(name.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || saving)
        }
      }
    }
  }

  private func create() {
    saving = true
    _Concurrency.Task {
      do {
        try await model.mutate { client in
          _ = try await client.createContext(["name": .string(name), "color": .string(color)])
        }
        dismiss()
      } catch { model.alertMessage = error.localizedDescription }
      saving = false
    }
  }
}

private struct ContextDetailView: View {
  @Environment(AppModel.self) private var model
  @Environment(\.dismiss) private var dismiss
  let context: CheckmateContext
  @State private var name: String
  @State private var color: String
  @State private var creatingProject = false
  @State private var confirmDelete = false

  init(context: CheckmateContext) {
    self.context = context
    _name = State(initialValue: context.name)
    _color = State(initialValue: context.color ?? "#C05E3C")
  }

  var body: some View {
    List {
      Section("Context") {
        TextField("Name", text: $name)
        TextField("Colour · #RRGGBB", text: $color)
        Button("Save changes") { save() }
        Button(context.archivedAt == nil ? "Archive context" : "Unarchive context") { archive() }
      }
      Section("Projects") {
        ForEach(model.projects.filter { $0.contextId == context.id }) { project in
          NavigationLink {
            ProjectDetailView(project: project)
          } label: {
            VStack(alignment: .leading, spacing: 3) {
              Text(project.name)
              Text(project.status.rawValue.capitalized).font(.caption).foregroundStyle(.secondary)
            }
          }
        }
        Button("New project", systemImage: "plus") { creatingProject = true }
      }
      Section("Tasks") {
        ForEach(
          model.tasks.filter {
            $0.contextId == context.id && $0.projectId == nil && $0.status.isOpen
          }
        ) { task in
          NavigationLink(value: task) { TaskRowView(task: task, showContext: false) }
        }
      }
      Section { Button("Delete context", role: .destructive) { confirmDelete = true } }
    }
    .navigationTitle(context.name)
    .sheet(isPresented: $creatingProject) { CreateProjectView(context: context) }
    .confirmationDialog(
      "Delete \(context.name)?", isPresented: $confirmDelete, titleVisibility: .visible
    ) {
      Button("Delete context", role: .destructive) { delete() }
    } message: {
      Text(
        "Projects and recurrences in this context will be deleted. Its tasks move back to Inbox, and associated people become global."
      )
    }
  }

  private func save() {
    _Concurrency.Task {
      do {
        try await model.mutate { client in
          _ = try await client.updateContext(
            id: context.id, body: ["name": .string(name), "color": .string(color)])
        }
      } catch { model.alertMessage = error.localizedDescription }
    }
  }

  private func archive() {
    _Concurrency.Task {
      do {
        try await model.mutate { client in
          _ = try await client.updateContext(
            id: context.id, body: ["archived": .bool(context.archivedAt == nil)])
        }
        dismiss()
      } catch { model.alertMessage = error.localizedDescription }
    }
  }

  private func delete() {
    _Concurrency.Task {
      do {
        try await model.mutate { try await $0.deleteContext(id: context.id) }
        dismiss()
      } catch { model.alertMessage = error.localizedDescription }
    }
  }
}

private struct CreateProjectView: View {
  @Environment(AppModel.self) private var model
  @Environment(\.dismiss) private var dismiss
  let context: CheckmateContext
  @State private var name = ""
  @State private var details = ""

  var body: some View {
    NavigationStack {
      Form {
        TextField("Name", text: $name)
        TextField("Description", text: $details, axis: .vertical)
      }
      .navigationTitle("New project")
      .toolbar {
        ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
        ToolbarItem(placement: .confirmationAction) {
          Button("Create") { create() }.disabled(
            name.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
        }
      }
    }
  }

  private func create() {
    _Concurrency.Task {
      do {
        try await model.mutate { client in
          _ = try await client.createProject([
            "context_id": .string(context.id), "name": .string(name),
            "description": details.isEmpty ? .null : .string(details),
          ])
        }
        dismiss()
      } catch { model.alertMessage = error.localizedDescription }
    }
  }
}

private struct ProjectDetailView: View {
  @Environment(AppModel.self) private var model
  @Environment(\.dismiss) private var dismiss
  let project: Project
  @State private var name: String
  @State private var details: String
  @State private var status: ProjectStatus
  @State private var confirmDelete = false

  init(project: Project) {
    self.project = project
    _name = State(initialValue: project.name)
    _details = State(initialValue: project.description ?? "")
    _status = State(initialValue: project.status)
  }

  var body: some View {
    List {
      Section("Project") {
        TextField("Name", text: $name)
        TextField("Description", text: $details, axis: .vertical)
        Picker("Status", selection: $status) {
          ForEach(ProjectStatus.allCases, id: \.self) { Text($0.rawValue.capitalized).tag($0) }
        }
        Button("Save changes") { save() }
      }
      Section("Tasks") {
        ForEach(model.tasks.filter { $0.projectId == project.id && $0.status.isOpen }) { task in
          NavigationLink(value: task) { TaskRowView(task: task, showContext: false) }
        }
      }
      Section { Button("Delete project", role: .destructive) { confirmDelete = true } }
    }
    .navigationTitle(project.name)
    .confirmationDialog(
      "Delete \(project.name)?", isPresented: $confirmDelete, titleVisibility: .visible
    ) {
      Button("Delete project", role: .destructive) { delete() }
    } message: {
      Text(
        "Tasks stay in their context and lose this project grouping. Recurrences also lose the association."
      )
    }
  }

  private func save() {
    _Concurrency.Task {
      do {
        try await model.mutate { client in
          _ = try await client.updateProject(
            id: project.id,
            body: [
              "name": .string(name), "description": details.isEmpty ? .null : .string(details),
              "status": .string(status.rawValue),
            ])
        }
      } catch { model.alertMessage = error.localizedDescription }
    }
  }

  private func delete() {
    _Concurrency.Task {
      do {
        try await model.mutate { try await $0.deleteProject(id: project.id) }
        dismiss()
      } catch { model.alertMessage = error.localizedDescription }
    }
  }
}

struct RoutinesView: View {
  @Environment(AppModel.self) private var model
  @State private var creating = false

  var body: some View {
    List {
      ForEach(DaySlot.allCases, id: \.self) { slot in
        let rows = model.recurrences.filter { $0.kind == .routine && $0.daySlot == slot }
        if !rows.isEmpty {
          Section(slot.rawValue.capitalized) {
            ForEach(rows) { item in RecurrenceRow(item: item) }
          }
        }
      }
      Section("Repeating") {
        ForEach(model.recurrences.filter { $0.kind == .classic }) { item in
          RecurrenceRow(item: item)
        }
      }
    }
    .navigationTitle("Routines")
    .toolbar { Button("New", systemImage: "plus") { creating = true } }
    .sheet(isPresented: $creating) { CreateRecurrenceView() }
  }
}

struct PeopleView: View {
  @Environment(AppModel.self) private var model
  @State private var name = ""

  var body: some View {
    List {
      Section("Add someone") {
        HStack {
          TextField("Name", text: $name)
          Button("Add") { create() }.disabled(
            name.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
        }
      }
      Section("Waiting on") {
        ForEach(model.people) { person in
          VStack(alignment: .leading, spacing: 3) {
            Text(person.name)
            let count = model.tasks.filter {
              $0.delegatedToId == person.id && $0.status == .delegated
            }.count
            Text("\(count) delegated task\(count == 1 ? "" : "s")")
              .font(.caption).foregroundStyle(.secondary)
          }
          .swipeActions {
            Button("Delete", role: .destructive) { delete(person) }
          }
        }
      }
    }
    .navigationTitle("People")
  }

  private func create() {
    let value = name.trimmingCharacters(in: .whitespacesAndNewlines)
    _Concurrency.Task {
      do {
        try await model.mutate { client in
          _ = try await client.createPerson(["name": .string(value)])
        }
        name = ""
      } catch { model.alertMessage = error.localizedDescription }
    }
  }

  private func delete(_ person: Person) {
    _Concurrency.Task {
      do { try await model.mutate { try await $0.deletePerson(id: person.id) } } catch {
        model.alertMessage = error.localizedDescription
      }
    }
  }
}

private struct RecurrenceRow: View {
  @Environment(AppModel.self) private var model
  let item: Recurrence

  var body: some View {
    VStack(alignment: .leading, spacing: 5) {
      HStack {
        Text(item.title)
        Spacer()
        Text(item.state.rawValue.capitalized).font(.caption).foregroundStyle(.secondary)
      }
      Text(item.rrule).font(.caption.monospaced()).foregroundStyle(.secondary)
      if item.state != .finished {
        Button(item.active ? "Pause" : "Resume") {
          _Concurrency.Task {
            do {
              try await model.mutate { client in
                _ = try await client.updateRecurrence(
                  id: item.id, body: ["active": .bool(!item.active)])
              }
            } catch { model.alertMessage = error.localizedDescription }
          }
        }
        .font(.caption)
      }
    }
  }
}

private struct CreateRecurrenceView: View {
  @Environment(AppModel.self) private var model
  @Environment(\.dismiss) private var dismiss
  @State private var title = ""
  @State private var contextId: String?
  @State private var rrule = "FREQ=WEEKLY"
  @State private var startsOn = CalendarDate.string(.now)
  @State private var isRoutine = false
  @State private var daySlot = DaySlot.morning

  var body: some View {
    NavigationStack {
      Form {
        TextField("Title", text: $title)
        Picker("Context", selection: $contextId) {
          Text("Choose…").tag(String?.none)
          ForEach(model.contexts) { Text($0.name).tag(Optional($0.id)) }
        }
        Toggle("Daily Routine item", isOn: $isRoutine)
        if isRoutine {
          Picker("Day slot", selection: $daySlot) {
            ForEach(DaySlot.allCases, id: \.self) { Text($0.rawValue.capitalized).tag($0) }
          }
        }
        TextField("RRULE", text: $rrule).textInputAutocapitalization(.characters)
          .autocorrectionDisabled()
        TextField("Starts · YYYY-MM-DD", text: $startsOn)
      }
      .navigationTitle("New recurrence")
      .toolbar {
        ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
        ToolbarItem(placement: .confirmationAction) {
          Button("Create") { create() }.disabled(title.isEmpty || contextId == nil || rrule.isEmpty)
        }
      }
    }
  }

  private func create() {
    guard let contextId else { return }
    _Concurrency.Task {
      do {
        try await model.mutate { client in
          var body: [String: JSONValue] = [
            "context_id": .string(contextId), "title": .string(title),
            "rrule": .string(rrule), "starts_on": .string(startsOn),
            "timezone": .string(model.profile?.timezone ?? "UTC"),
            "kind": .string(isRoutine ? "routine" : "classic"),
          ]
          if isRoutine { body["day_slot"] = .string(daySlot.rawValue) }
          _ = try await client.createRecurrence(body)
        }
        dismiss()
      } catch { model.alertMessage = error.localizedDescription }
    }
  }
}

struct ActivityView: View {
  @Environment(AppModel.self) private var model
  @State private var activity: [TaskActivity] = []
  @State private var loading = true

  var body: some View {
    List(activity) { item in
      VStack(alignment: .leading, spacing: 4) {
        Text(item.taskTitle)
        Text("\(item.action.capitalized) · \(item.occurredAt)").font(.caption).foregroundStyle(
          .secondary)
        if !item.changedFields.isEmpty {
          Text(item.changedFields.joined(separator: ", ")).font(.caption2).foregroundStyle(
            .tertiary)
        }
      }
    }
    .navigationTitle("Activity")
    .overlay {
      if loading {
        ProgressView()
      } else if activity.isEmpty {
        ContentUnavailableView("No activity", systemImage: "clock")
      }
    }
    .task {
      do { activity = try await model.client?.activity().data ?? [] } catch {
        model.alertMessage = error.localizedDescription
      }
      loading = false
    }
  }
}

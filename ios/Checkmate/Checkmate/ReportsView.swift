import CheckmateCore
import SwiftUI

struct ReportsView: View {
  @Environment(AppModel.self) private var model
  @State private var generatorPresented = false

  var body: some View {
    List {
      if model.reportConfiguration?.configured == false {
        Section {
          Label(
            "Report generation is not configured on this server.",
            systemImage: "exclamationmark.triangle"
          )
          .foregroundStyle(CheckmateTheme.overdue)
        }
      }
      Section("Saved reports") {
        ForEach(model.reports) { report in
          NavigationLink {
            ReportDetailView(reportId: report.id)
          } label: {
            VStack(alignment: .leading, spacing: 4) {
              Text(report.title).font(.headline)
              Text("\(report.startOn) – \(report.endOn)").font(.caption.monospacedDigit())
                .foregroundStyle(.secondary)
              Text("Updated \(report.updatedAt)").font(.caption2).foregroundStyle(.tertiary)
            }
          }
        }
      }
    }
    .navigationTitle("Reports")
    .overlay {
      if model.reports.isEmpty {
        ContentUnavailableView(
          "No saved reports", systemImage: "doc.text",
          description: Text("Generate a meeting-ready update from your task activity."))
      }
    }
    .toolbar { Button("Generate", systemImage: "sparkles") { generatorPresented = true } }
    .sheet(isPresented: $generatorPresented) { ReportGeneratorView() }
  }
}

struct ReportGeneratorView: View {
  @Environment(\.dismiss) private var dismiss
  @Environment(AppModel.self) private var model
  @State private var start = ReportDatePreset.sevenDays.range().lowerBound
  @State private var end = Date.now
  @State private var selected = Set<String>()
  @State private var includeInbox = false
  @State private var focus = ""
  @State private var preview: ReportPreview?
  @State private var loadingPreview = false
  @State private var generating = false
  @State private var errorMessage: String?

  var body: some View {
    NavigationStack {
      Form {
        Section("Date range") {
          DatePicker("Start", selection: $start, in: ...end, displayedComponents: .date)
          DatePicker("End", selection: $end, in: start...Date.now, displayedComponents: .date)
          ScrollView(.horizontal, showsIndicators: false) {
            HStack {
              ForEach(ReportDatePreset.allCases) { preset in
                Button(preset.rawValue) {
                  let range = preset.range()
                  start = range.lowerBound
                  end = range.upperBound
                }
                .buttonStyle(.bordered)
              }
            }
          }
        }
        Section("Contexts") {
          ForEach(model.contexts) { context in
            Toggle(isOn: selectionBinding(for: context.id)) {
              HStack {
                ContextDot(context: context)
                Text(context.name)
              }
            }
          }
          Toggle("Include Inbox", isOn: $includeInbox)
        }
        Section("Focus · optional") {
          TextField("Emphasize launch risks and decisions needed.", text: $focus, axis: .vertical)
            .lineLimit(2...5)
        }
        Section("Preview") {
          if loadingPreview {
            ProgressView("Checking activity…")
          } else if let preview {
            ReportMetricsView(metrics: preview.metrics)
            DisclosureGroup("\(preview.tasks.count) source tasks") {
              ForEach(preview.tasks) { item in
                VStack(alignment: .leading) {
                  Text(item.title)
                  Text("\(item.category) · \(item.contextName)").font(.caption).foregroundStyle(
                    .secondary)
                }
              }
            }
          } else {
            Text("Select at least one context or Inbox.").foregroundStyle(.secondary)
          }
        }
        if let errorMessage {
          Section { Text(errorMessage).foregroundStyle(CheckmateTheme.overdue) }
        }
      }
      .navigationTitle("Generate report")
      .toolbar {
        ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
        ToolbarItem(placement: .confirmationAction) {
          Button(generating ? "Generating…" : "Generate") { generate() }
            .disabled(
              preview?.tasks.isEmpty != false || generating
                || model.reportConfiguration?.configured == false)
        }
      }
      .task {
        selected = Set(model.contexts.filter { $0.archivedAt == nil }.map(\.id))
        await loadPreview()
      }
      .onChange(of: start) { _Concurrency.Task { await loadPreview() } }
      .onChange(of: end) { _Concurrency.Task { await loadPreview() } }
      .onChange(of: selected) { _Concurrency.Task { await loadPreview() } }
      .onChange(of: includeInbox) { _Concurrency.Task { await loadPreview() } }
    }
  }

  private var request: ReportRequest {
    ReportRequest(
      startOn: CalendarDate.string(start), endOn: CalendarDate.string(end),
      contextIds: Array(selected), includeInbox: includeInbox, focus: focus)
  }

  private func selectionBinding(for id: String) -> Binding<Bool> {
    Binding(
      get: { selected.contains(id) },
      set: { enabled in
        if enabled { selected.insert(id) } else { selected.remove(id) }
      }
    )
  }

  private func loadPreview() async {
    guard !selected.isEmpty || includeInbox, let client = model.client else {
      preview = nil
      return
    }
    loadingPreview = true
    defer { loadingPreview = false }
    do {
      preview = try await client.previewReport(request)
      errorMessage = nil
    } catch {
      preview = nil
      errorMessage = error.localizedDescription
    }
  }

  private func generate() {
    guard let client = model.client else { return }
    generating = true
    _Concurrency.Task {
      do {
        let report = try await client.generateReport(request)
        model.reports.insert(report, at: 0)
        dismiss()
      } catch { errorMessage = error.localizedDescription }
      generating = false
    }
  }
}

private struct ReportMetricsView: View {
  let metrics: ReportMetrics
  var body: some View {
    HStack {
      metric(metrics.completed, "completed")
      metric(metrics.open, "open")
      metric(metrics.blocked, "blocked")
      metric(metrics.delegated, "delegated")
      metric(metrics.dropped, "dropped")
    }
    .font(.caption)
  }
  private func metric(_ value: Int, _ label: String) -> some View {
    VStack {
      Text(value, format: .number).font(.headline.monospacedDigit())
      Text(label).foregroundStyle(.secondary)
    }
    .frame(maxWidth: .infinity)
  }
}

struct ReportDetailView: View {
  @Environment(AppModel.self) private var model
  @Environment(\.dismiss) private var dismiss
  let reportId: String
  @State private var report: Report?
  @State private var title = ""
  @State private var content = ""
  @State private var editing = false
  @State private var busy = false
  @State private var confirmDelete = false

  var body: some View {
    Group {
      if let report {
        ScrollView {
          VStack(alignment: .leading, spacing: 18) {
            TextField("Title", text: $title).font(.checkmateTitle(.title))
            Text("\(report.startOn) – \(report.endOn)").font(.caption.monospacedDigit())
              .foregroundStyle(.secondary)
            if editing {
              TextEditor(text: $content).frame(minHeight: 420).font(.body.monospaced())
            } else {
              Text(markdown(content)).frame(maxWidth: .infinity, alignment: .leading).textSelection(
                .enabled)
            }
          }.padding()
        }
        .toolbar {
          Button(editing ? "Preview" : "Edit") { editing.toggle() }
          Button("Save") { save() }.disabled(busy)
          Menu {
            Button("Regenerate", systemImage: "arrow.clockwise") { regenerate() }
            Button("Delete", systemImage: "trash", role: .destructive) { confirmDelete = true }
          } label: {
            Image(systemName: "ellipsis.circle")
          }
        }
      } else {
        ProgressView()
      }
    }
    .navigationTitle("Report")
    .task { await load() }
    .confirmationDialog("Delete this report?", isPresented: $confirmDelete) {
      Button("Delete", role: .destructive) { delete() }
    }
  }

  private func load() async {
    guard let client = model.client else { return }
    do {
      let value = try await client.report(id: reportId)
      report = value
      title = value.title
      content =
        value.versions?.first(where: { $0.versionNumber == value.latestVersion })?.contentMarkdown
        ?? ""
    } catch { model.alertMessage = error.localizedDescription }
  }

  private func save() {
    guard let client = model.client, let report else { return }
    busy = true
    _Concurrency.Task {
      do {
        let updated = try await client.updateReport(
          id: reportId,
          body: [
            "title": .string(title), "content_markdown": .string(content),
            "version_number": .integer(report.latestVersion),
          ])
        self.report = updated
        model.toastMessage = "Report saved"
      } catch { model.alertMessage = error.localizedDescription }
      busy = false
    }
  }

  private func regenerate() {
    guard let client = model.client else { return }
    busy = true
    _Concurrency.Task {
      do {
        report = try await client.regenerateReport(id: reportId)
        await load()
      } catch { model.alertMessage = error.localizedDescription }
      busy = false
    }
  }

  private func delete() {
    guard let client = model.client else { return }
    _Concurrency.Task {
      do {
        try await client.deleteReport(id: reportId)
        model.reports.removeAll { $0.id == reportId }
        dismiss()
      } catch { model.alertMessage = error.localizedDescription }
    }
  }

  private func markdown(_ value: String) -> AttributedString {
    (try? AttributedString(markdown: value, options: .init(interpretedSyntax: .full)))
      ?? AttributedString(value)
  }
}

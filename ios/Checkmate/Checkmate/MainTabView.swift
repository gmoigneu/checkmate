import CheckmateCore
import SwiftUI

struct MainTabView: View {
  @Environment(AppModel.self) private var model
  private enum Tab: Hashable { case brief, inbox, capture, waiting, settings }

  @State private var selected: Tab = .brief
  @State private var previous: Tab = .brief
  @State private var capturePresented = false

  var body: some View {
    TabView(selection: $selected) {
      BriefView()
        .tabItem { Label("Brief", systemImage: "sun.max") }
        .tag(Tab.brief)
      InboxView()
        .tabItem { Label("Inbox", systemImage: "tray") }
        .badge(model.brief?.totals.inbox ?? model.tasks.filter { $0.status == .inbox }.count)
        .tag(Tab.inbox)
      Color.clear
        .tabItem { Label("Capture", systemImage: "plus.circle.fill") }
        .tag(Tab.capture)
      WaitingView()
        .tabItem { Label("Waiting", systemImage: "hourglass") }
        .badge(
          model.brief?.totals.waitingOn ?? model.tasks.filter { $0.status == .delegated }.count
        )
        .tag(Tab.waiting)
      SettingsView()
        .tabItem { Label("Settings", systemImage: "gearshape") }
        .tag(Tab.settings)
    }
    .onChange(of: selected) { old, new in
      if new == .capture {
        previous = old == .capture ? .brief : old
        selected = previous
        model.requestedCaptureMethod = .form
        capturePresented = true
      } else {
        previous = new
      }
    }
    .onChange(of: model.captureRequested) { _, requested in
      guard requested else { return }
      capturePresented = true
      model.captureRequested = false
    }
    .onChange(of: model.briefRequested) { _, requested in
      guard requested else { return }
      selected = .brief
      model.briefRequested = false
    }
    .sheet(isPresented: $capturePresented) {
      CaptureView(captureMethod: model.requestedCaptureMethod)
    }
    .sheet(
      item: Binding(
        get: { model.deepLinkedTask },
        set: { model.deepLinkedTask = $0 }
      )
    ) { task in
      NavigationStack { TaskDetailView(task: task) }
    }
  }
}

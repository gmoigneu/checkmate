import CheckmateCore
import SwiftUI

struct MoreView: View {
  @Environment(AppModel.self) private var model

  var body: some View {
    NavigationStack {
      WarmPage {
        ScrollView {
          VStack(spacing: 20) {
            InsetCard(title: "Tasks") {
              link("Upcoming", icon: "calendar", destination: UpcomingView(embedded: true))
              divider
              link(
                "Waiting",
                value: model.brief?.totals.waitingOn
                  ?? model.tasks.filter { $0.status == .delegated }.count,
                icon: "hourglass", destination: WaitingView())
              divider
              link("Search", icon: "magnifyingglass", destination: SearchTasksView())
            }
            InsetCard(title: "Organize") {
              link(
                "Contexts & projects", icon: "square.grid.2x2", destination: ContextsView())
              divider
              link("People", icon: "person.2", destination: PeopleView())
              divider
              link("Daily routine", icon: "repeat", destination: RoutinesView())
            }
            InsetCard(title: "Review") {
              link("Reports", icon: "doc.text", destination: ReportsView())
              divider
              link("Activity", icon: "clock.arrow.circlepath", destination: ActivityView())
            }
            InsetCard(title: "Checkmate") {
              link("Settings", icon: "gearshape", destination: SettingsView(embedded: true))
            }
          }
          .padding(12)
        }
      }
      .navigationTitle("More")
      .navigationDestination(for: CheckmateTask.self) { TaskDetailView(task: $0) }
    }
  }

  private var divider: some View { Divider().padding(.leading, 44) }

  private func link<Destination: View>(
    _ title: String, value: Int? = nil, icon: String, destination: Destination
  ) -> some View {
    NavigationLink(destination: destination) {
      HStack(spacing: 12) {
        Image(systemName: icon).frame(width: 22).foregroundStyle(CheckmateTheme.accent)
        Text(title).foregroundStyle(CheckmateTheme.primary)
        Spacer()
        if let value, value > 0 {
          Text("\(value)").font(.subheadline.monospacedDigit()).foregroundStyle(
            CheckmateTheme.tertiary)
        }
        Image(systemName: "chevron.right").font(.caption).foregroundStyle(
          CheckmateTheme.tertiary)
      }
      .padding(.horizontal, 12).frame(minHeight: 48)
      .contentShape(Rectangle())
    }
    .buttonStyle(.plain)
  }
}

struct SettingsView: View {
  @Environment(AppModel.self) private var model
  @State private var confirmSignOut = false
  @AppStorage("appearance", store: SharedConfiguration.defaults) private var appearance =
    AppearanceOption.system.rawValue
  var embedded = false

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
        VStack(spacing: 20) {
          InsetCard(
            title: "Server",
            footer: "Signing out clears the token from Keychain and deletes the local cache."
          ) {
            row("Address", value: model.serverURL?.host() ?? "—", icon: "server.rack")
            divider
            row("Signed in as", value: model.profile?.email ?? "—", icon: "person.crop.circle")
            divider
            Button(role: .destructive) {
              confirmSignOut = true
            } label: {
              row("Sign out", icon: "rectangle.portrait.and.arrow.right")
            }
          }
          InsetCard(title: "Work") {
            link("Reports", icon: "doc.text", destination: ReportsView())
            divider
            link("Search", icon: "magnifyingglass", destination: SearchTasksView())
            divider
            link("Contexts & projects", icon: "square.grid.2x2", destination: ContextsView())
            divider
            link("People", icon: "person.2", destination: PeopleView())
            divider
            link("Daily routine", icon: "repeat", destination: RoutinesView())
            divider
            link("Activity", icon: "clock.arrow.circlepath", destination: ActivityView())
          }
          InsetCard(
            title: "Sync",
            footer:
              "Badges are computed on this device from synced data; the server does not push."
          ) {
            row(
              "Last sync",
              value: model.lastSyncAt?.formatted(.relative(presentation: .named)) ?? "Never",
              icon: "arrow.triangle.2.circlepath")
            divider
            Button {
              _Concurrency.Task { await model.refresh(fullSync: true) }
            } label: {
              row("Force full resync", icon: "arrow.clockwise")
            }
          }
          InsetCard(title: "Appearance") {
            Picker("Appearance", selection: $appearance) {
              ForEach(AppearanceOption.allCases) { option in
                Label(option.label, systemImage: option.icon).tag(option.rawValue)
              }
            }
            .pickerStyle(.segmented)
            .padding(12)
          }
          InsetCard(
            title: "Devices & tokens",
            footer:
              "New device tokens require a browser session, so they are created in the web app rather than from an existing device credential."
          ) {
            if let url = model.serverURL?.appending(path: "settings/devices") {
              Link(destination: url) {
                HStack {
                  row("Manage in web app", icon: "key.horizontal")
                  Image(systemName: "arrow.up.right")
                    .font(.caption)
                    .foregroundStyle(CheckmateTheme.tertiary)
                    .padding(.trailing, 12)
                }
              }
              .buttonStyle(.plain)
            } else {
              row("Manage in web app", value: "Sign in first", icon: "key.horizontal")
            }
          }
          InsetCard(title: "Shortcuts & widgets") {
            row("Set up Siri phrases", icon: "waveform")
            divider
            row("Add the Home Screen widget", icon: "widget.small")
            divider
            row("Action Button → capture", icon: "button.programmable")
          }
          InsetCard(title: "About") {
            row(
              "Version",
              value: Bundle.main.infoDictionary?["CFBundleShortVersionString"] as? String
                ?? "1.0", icon: "info.circle")
          }
        }
        .padding(12)
      }
    }
    .navigationTitle("Settings")
    .confirmationDialog(
      "Sign out of Checkmate?", isPresented: $confirmSignOut, titleVisibility: .visible
    ) {
      Button("Sign out", role: .destructive) { _Concurrency.Task { await model.signOut() } }
    } message: {
      Text("The server is unchanged. This device’s credential and local cache will be removed.")
    }
    .navigationDestination(for: CheckmateTask.self) { TaskDetailView(task: $0) }
  }

  private var divider: some View { Divider().padding(.leading, 44) }

  private func row(_ title: String, value: String? = nil, icon: String) -> some View {
    HStack(spacing: 12) {
      Image(systemName: icon).frame(width: 22).foregroundStyle(CheckmateTheme.accent)
      Text(title).foregroundStyle(CheckmateTheme.primary)
      Spacer()
      if let value {
        Text(value).font(.subheadline).foregroundStyle(CheckmateTheme.tertiary).lineLimit(1)
      }
    }
    .padding(.horizontal, 12).frame(minHeight: 48)
    .contentShape(Rectangle())
  }

  private func link<Destination: View>(_ title: String, icon: String, destination: Destination)
    -> some View
  {
    NavigationLink(destination: destination) {
      HStack {
        row(title, icon: icon)
        Image(systemName: "chevron.right").font(.caption).foregroundStyle(CheckmateTheme.tertiary)
          .padding(.trailing, 12)
      }
    }
    .buttonStyle(.plain)
  }
}

enum AppearanceOption: String, CaseIterable, Identifiable {
  case system, light, dark

  var id: String { rawValue }
  var label: String { rawValue.capitalized }
  var icon: String {
    switch self {
    case .system: "circle.lefthalf.filled"
    case .light: "sun.max"
    case .dark: "moon"
    }
  }
  var colorScheme: ColorScheme? {
    switch self {
    case .system: nil
    case .light: .light
    case .dark: .dark
    }
  }
}

struct SearchTasksView: View {
  @Environment(AppModel.self) private var model
  @State private var query = ""

  private var results: [CheckmateTask] {
    guard !query.isEmpty else { return [] }
    return model.tasks.filter {
      $0.title.localizedCaseInsensitiveContains(query)
        || ($0.details?.localizedCaseInsensitiveContains(query) ?? false)
    }
  }

  var body: some View {
    List(results) { task in NavigationLink(value: task) { TaskRowView(task: task) } }
      .searchable(text: $query, prompt: "Titles and notes")
      .navigationTitle("Search")
      .overlay {
        if query.isEmpty {
          ContentUnavailableView(
            "Search Checkmate", systemImage: "magnifyingglass",
            description: Text("Search works from the complete local cache."))
        }
      }
  }
}

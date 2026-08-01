import CheckmateCore
import SwiftUI

@main
struct CheckmateApp: App {
  @State private var model = AppModel()
  @Environment(\.scenePhase) private var scenePhase
  @AppStorage("appearance", store: SharedConfiguration.defaults) private var appearance =
    AppearanceOption.system.rawValue

  var body: some Scene {
    WindowGroup {
      RootView()
        .environment(model)
        .tint(CheckmateTheme.accent)
        .preferredColorScheme(AppearanceOption(rawValue: appearance)?.colorScheme)
        .onOpenURL { url in _Concurrency.Task { await model.open(url) } }
        .onChange(of: scenePhase) { _, newValue in
          guard newValue == .active, model.phase == .ready else { return }
          _Concurrency.Task { await model.refresh() }
        }
    }
  }
}

import SwiftUI

struct RootView: View {
  @Environment(AppModel.self) private var model

  var body: some View {
    ZStack(alignment: .bottom) {
      switch model.phase {
      case .launching:
        LoadingState(title: "Opening Checkmate…")
      case .onboarding, .reauthentication:
        OnboardingView()
      case .syncing(let progress):
        LoadingState(title: progress)
      case .ready:
        MainTabView()
      }

      if let toast = model.toastMessage {
        Text(toast)
          .font(.subheadline.weight(.medium))
          .padding(.horizontal, 16)
          .padding(.vertical, 11)
          .background(.regularMaterial, in: Capsule())
          .shadow(radius: 8, y: 4)
          .padding(.bottom, 80)
          .transition(.move(edge: .bottom).combined(with: .opacity))
          .task {
            try? await Task.sleep(for: .seconds(3))
            withAnimation { model.toastMessage = nil }
          }
      }
    }
    .task { await model.restoreSession() }
    .alert(
      "Checkmate",
      isPresented: Binding(
        get: { model.alertMessage != nil },
        set: { if !$0 { model.alertMessage = nil } }
      )
    ) {
      Button("OK") { model.alertMessage = nil }
    } message: {
      Text(model.alertMessage ?? "")
    }
  }
}

import CheckmateCore
import SwiftUI

struct OnboardingView: View {
  private enum Step { case welcome, server, signIn, token }

  @Environment(AppModel.self) private var model
  @State private var step: Step = .welcome
  @State private var serverAddress = "http://localhost:8080"
  @State private var token = ""
  @State private var isWorking = false
  @State private var errorMessage: String?

  var body: some View {
    WarmPage {
      VStack(alignment: .leading, spacing: 22) {
        switch step {
        case .welcome: welcome
        case .server: server
        case .signIn: signIn
        case .token: tokenEntry
        }
      }
      .padding(.horizontal, 24)
      .padding(.vertical, 32)
      .frame(maxWidth: 560)
    }
    .animation(.snappy, value: step)
  }

  private var welcome: some View {
    Group {
      Spacer()
      CheckmateMark()
      Text("Checkmate")
        .font(.system(size: 46, weight: .semibold, design: .serif))
      Text("Four lives, one calm list. Capture in three seconds; decide later.")
        .font(.title3)
        .foregroundStyle(CheckmateTheme.secondary)
      Spacer()
      primaryButton("Get started") { step = .server }
        .accessibilityIdentifier("get-started")
    }
  }

  private var server: some View {
    Group {
      Text("Where does your Checkmate live?").font(.checkmateTitle(.title))
      Text(
        "Checkmate is self-hosted, so this app cannot guess the address. Paste the URL you use in the browser."
      )
      .foregroundStyle(CheckmateTheme.secondary)
      TextField("https://tasks.example.com", text: $serverAddress)
        .textContentType(.URL)
        .keyboardType(.URL)
        .textInputAutocapitalization(.never)
        .autocorrectionDisabled()
        .padding(14)
        .background(CheckmateTheme.card, in: RoundedRectangle(cornerRadius: 10))
        .overlay(RoundedRectangle(cornerRadius: 10).stroke(CheckmateTheme.border))
        .accessibilityLabel("Server address")
        .accessibilityIdentifier("server-address")
      if let errorMessage { error(errorMessage) }
      Spacer()
      primaryButton(isWorking ? "Checking…" : "Continue") {
        await validateServer()
      }
      .disabled(isWorking)
    }
  }

  private var signIn: some View {
    Group {
      Text("Sign in").font(.checkmateTitle(.title))
      Text(
        "A secure browser sheet opens on your server’s own consent screen. Checkmate asks to read, write, and stay connected."
      )
      .foregroundStyle(CheckmateTheme.secondary)
      if let errorMessage { error(errorMessage) }
      Button {
        _Concurrency.Task { await oauthSignIn() }
      } label: {
        HStack {
          if isWorking { ProgressView().tint(.white) }
          Text(isWorking ? "Signing in…" : "Continue with Google")
            .frame(maxWidth: .infinity)
        }
      }
      .buttonStyle(CheckmatePrimaryButtonStyle())
      .disabled(isWorking)
      Button("Paste a device token instead") { step = .token }
        .buttonStyle(.borderless)
        .frame(maxWidth: .infinity)
      Text(
        "Tokens are made in the web app under Settings → Devices, because a token cannot mint another token."
      )
      .font(.caption)
      .foregroundStyle(CheckmateTheme.tertiary)
      Spacer()
    }
  }

  private var tokenEntry: some View {
    Group {
      Text("Paste a device token").font(.checkmateTitle(.title))
      Text(
        "Use the token shown once by the web app or the Checkmate CLI. It will be stored securely in this device’s Keychain."
      )
      .foregroundStyle(CheckmateTheme.secondary)
      SecureField("cm_…", text: $token)
        .textContentType(.password)
        .textInputAutocapitalization(.never)
        .autocorrectionDisabled()
        .padding(14)
        .background(CheckmateTheme.card, in: RoundedRectangle(cornerRadius: 10))
        .overlay(RoundedRectangle(cornerRadius: 10).stroke(CheckmateTheme.border))
      if let errorMessage { error(errorMessage) }
      Spacer()
      primaryButton(isWorking ? "Connecting…" : "Connect") { await deviceTokenSignIn() }
        .disabled(token.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || isWorking)
      Button("Back") { step = .signIn }
        .frame(maxWidth: .infinity)
    }
  }

  @ViewBuilder
  private func primaryButton(_ title: String, action: @escaping () async -> Void) -> some View {
    Button(title) { _Concurrency.Task { await action() } }
      .buttonStyle(CheckmatePrimaryButtonStyle())
      .frame(maxWidth: .infinity)
  }

  private func validateServer() async {
    isWorking = true
    defer { isWorking = false }
    do {
      let server = try await model.validateServer(serverAddress)
      errorMessage = nil
      step = server.requiresDeviceToken ? .token : .signIn
    } catch {
      errorMessage = error.localizedDescription
    }
  }

  private func oauthSignIn() async {
    isWorking = true
    defer { isWorking = false }
    do {
      try await model.signInWithOAuth()
      errorMessage = nil
    } catch {
      errorMessage = error.localizedDescription
    }
  }

  private func deviceTokenSignIn() async {
    isWorking = true
    defer { isWorking = false }
    do {
      try await model.signInWithDeviceToken(token)
      errorMessage = nil
    } catch {
      errorMessage = error.localizedDescription
    }
  }

  private func error(_ message: String) -> some View {
    Label(message, systemImage: "exclamationmark.triangle.fill")
      .font(.caption)
      .foregroundStyle(CheckmateTheme.overdue)
      .padding(12)
      .frame(maxWidth: .infinity, alignment: .leading)
      .background(CheckmateTheme.overdue.opacity(0.1), in: RoundedRectangle(cornerRadius: 10))
  }
}

private struct CheckmateMark: View {
  var body: some View {
    ZStack {
      RoundedRectangle(cornerRadius: 28).fill(CheckmateTheme.accent)
      HStack(spacing: 4) {
        ForEach(0..<5) { index in
          RoundedRectangle(cornerRadius: 3)
            .fill(.white.opacity(index.isMultiple(of: 2) ? 1 : 0.55))
            .frame(width: 18, height: 18)
            .offset(y: CGFloat(2 - index) * 12)
        }
      }
    }
    .frame(width: 132, height: 92)
    .accessibilityHidden(true)
  }
}

private struct CheckmatePrimaryButtonStyle: ButtonStyle {
  func makeBody(configuration: Configuration) -> some View {
    configuration.label
      .font(.headline)
      .foregroundStyle(.white)
      .frame(maxWidth: .infinity)
      .padding(.vertical, 14)
      .background(
        CheckmateTheme.accent.opacity(configuration.isPressed ? 0.8 : 1),
        in: RoundedRectangle(cornerRadius: 12)
      )
      .scaleEffect(configuration.isPressed ? 0.98 : 1)
  }
}

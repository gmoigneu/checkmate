import CheckmateCore
import SwiftUI

enum CheckmateTheme {
  static let accent = Color(light: 0xC05E3C, dark: 0xE08A62)
  static let page = Color(light: 0xFAF6F0, dark: 0x1A1714)
  static let card = Color(light: 0xFFFDFA, dark: 0x221E1A)
  static let sunken = Color(light: 0xF4EEE5, dark: 0x151210)
  static let primary = Color(light: 0x241F1A, dark: 0xF5EFE6)
  static let secondary = Color(light: 0x6B6055, dark: 0xB4A897)
  static let tertiary = Color(light: 0x8B7F71, dark: 0x94897A)
  static let border = Color(light: 0x241F1A, dark: 0xF5EFE6).opacity(0.12)
  static let overdue = Color(light: 0xA8452F, dark: 0xEC9C89)
  static let olive = Color(light: 0x6E7A4F, dark: 0xA9B87E)
  static let ochre = Color(light: 0xC39A3A, dark: 0xDFBB63)
  static let dusk = Color(light: 0x6A6B7C, dark: 0xA3A4B6)
}

extension Color {
  init(light: UInt, dark: UInt) {
    self.init(
      uiColor: UIColor { traits in
        UIColor(rgb: traits.userInterfaceStyle == .dark ? dark : light)
      })
  }

  init?(hex: String?) {
    guard let hex else { return nil }
    let cleaned = hex.trimmingCharacters(in: CharacterSet(charactersIn: "#"))
    guard cleaned.count == 6, let value = UInt(cleaned, radix: 16) else { return nil }
    self.init(uiColor: UIColor(rgb: value))
  }
}

extension UIColor {
  fileprivate convenience init(rgb: UInt) {
    self.init(
      red: CGFloat((rgb >> 16) & 0xff) / 255,
      green: CGFloat((rgb >> 8) & 0xff) / 255,
      blue: CGFloat(rgb & 0xff) / 255,
      alpha: 1
    )
  }
}

extension Font {
  static func checkmateTitle(_ style: TextStyle = .largeTitle) -> Font {
    .system(style, design: .serif, weight: .semibold)
  }
}

struct WarmPage<Content: View>: View {
  @ViewBuilder let content: Content

  var body: some View {
    ZStack {
      CheckmateTheme.page.ignoresSafeArea()
      content
    }
    .foregroundStyle(CheckmateTheme.primary)
  }
}

struct InsetCard<Content: View>: View {
  let title: String?
  let footer: String?
  @ViewBuilder let content: Content

  init(title: String? = nil, footer: String? = nil, @ViewBuilder content: () -> Content) {
    self.title = title
    self.footer = footer
    self.content = content()
  }

  var body: some View {
    VStack(alignment: .leading, spacing: 6) {
      if let title {
        Text(title.uppercased())
          .font(.caption2.monospaced())
          .tracking(1.1)
          .foregroundStyle(CheckmateTheme.tertiary)
          .padding(.horizontal, 10)
      }
      VStack(spacing: 0) { content }
        .background(CheckmateTheme.card, in: RoundedRectangle(cornerRadius: 12))
        .overlay(RoundedRectangle(cornerRadius: 12).stroke(CheckmateTheme.border))
        .clipShape(RoundedRectangle(cornerRadius: 12))
      if let footer {
        Text(footer)
          .font(.caption)
          .foregroundStyle(CheckmateTheme.tertiary)
          .padding(.horizontal, 10)
      }
    }
  }
}

struct ContextDot: View {
  let context: CheckmateContext?
  var size: CGFloat = 8

  var body: some View {
    Circle()
      .fill(Color(hex: context?.color) ?? CheckmateTheme.tertiary)
      .frame(width: size, height: size)
      .accessibilityHidden(true)
  }
}

struct OfflineBanner: View {
  let lastSyncAt: Date?

  var body: some View {
    Label(
      lastSyncAt.map { "Last updated \($0.formatted(.relative(presentation: .named)))" }
        ?? "No cached data yet", systemImage: "icloud.slash"
    )
    .font(.caption)
    .foregroundStyle(CheckmateTheme.overdue)
    .frame(maxWidth: .infinity)
    .padding(.vertical, 5)
    .background(CheckmateTheme.overdue.opacity(0.12))
  }
}

struct LoadingState: View {
  let title: String

  var body: some View {
    WarmPage {
      VStack(spacing: 16) {
        ProgressView().tint(CheckmateTheme.accent)
        Text(title).foregroundStyle(CheckmateTheme.secondary)
      }
    }
  }
}

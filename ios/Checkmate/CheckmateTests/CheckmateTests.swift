import SwiftUI
import Testing

@testable import Checkmate

@Suite("App presentation")
struct CheckmateTests {
  @Test func appearanceMapsToNativeSchemes() {
    #expect(AppearanceOption.system.colorScheme == nil)
    #expect(AppearanceOption.light.colorScheme == .light)
    #expect(AppearanceOption.dark.colorScheme == .dark)
  }

  @Test func everyAppearanceHasAnIcon() {
    #expect(AppearanceOption.allCases.allSatisfy { !$0.icon.isEmpty })
  }
}

import CheckmateCore
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

  @Test func upcomingRequiresOpenFutureDatedWork() {
    let today = "2026-08-01"

    #expect(
      isUpcomingTask(
        status: .todo, kind: .short, dueOn: "2026-08-02", deletedAt: nil, after: today))
    #expect(
      !isUpcomingTask(
        status: .todo, kind: .short, dueOn: today, deletedAt: nil, after: today))
    #expect(
      !isUpcomingTask(
        status: .done, kind: .short, dueOn: "2026-08-02", deletedAt: nil, after: today))
    #expect(
      !isUpcomingTask(
        status: .todo, kind: .routine, dueOn: "2026-08-02", deletedAt: nil, after: today))
  }
}

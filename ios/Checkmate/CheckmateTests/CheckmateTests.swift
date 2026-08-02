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

  @Test func upcomingMatchesWebOpenWork() {
    #expect(isUpcomingTask(status: .todo, deletedAt: nil))
    #expect(isUpcomingTask(status: .inProgress, deletedAt: nil))
    #expect(isUpcomingTask(status: .blocked, deletedAt: nil))
    #expect(isUpcomingTask(status: .delegated, deletedAt: nil))
    #expect(!isUpcomingTask(status: .inbox, deletedAt: nil))
    #expect(!isUpcomingTask(status: .done, deletedAt: nil))
    #expect(!isUpcomingTask(status: .cancelled, deletedAt: nil))
    #expect(!isUpcomingTask(status: .expired, deletedAt: nil))
    #expect(
      !isUpcomingTask(status: .todo, deletedAt: "2026-08-01T12:00:00Z"))
  }

  @Test func upcomingUsesTheWebDefaultOrder() {
    #expect(
      upcomingTaskComesBefore(
        priority: .urgent, id: "1", than: .high, otherID: "9"))
    #expect(
      upcomingTaskComesBefore(
        priority: .low, id: "1", than: nil, otherID: "9"))
    #expect(
      upcomingTaskComesBefore(
        priority: .medium, id: "9", than: .medium, otherID: "1"))
    #expect(
      !upcomingTaskComesBefore(
        priority: nil, id: "9", than: .low, otherID: "1"))
  }
}

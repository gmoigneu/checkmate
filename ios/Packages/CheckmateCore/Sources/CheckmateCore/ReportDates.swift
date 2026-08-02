import Foundation

public enum ReportDatePreset: String, CaseIterable, Sendable, Identifiable {
  case thisWeek = "This week"
  case lastWeek = "Last week"
  case sevenDays = "Last 7 days"
  case thirtyDays = "Last 30 days"

  public var id: String { rawValue }

  public func range(endingAt today: Date = .now, calendar input: Calendar = .current)
    -> ClosedRange<Date>
  {
    var calendar = input
    calendar.firstWeekday = 2
    let day = calendar.startOfDay(for: today)
    let weekday = calendar.component(.weekday, from: day)
    let daysSinceMonday = (weekday + 5) % 7
    let monday = calendar.date(byAdding: .day, value: -daysSinceMonday, to: day) ?? day
    switch self {
    case .thisWeek:
      return monday...day
    case .lastWeek:
      let start = calendar.date(byAdding: .day, value: -7, to: monday) ?? monday
      let end = calendar.date(byAdding: .day, value: -1, to: monday) ?? monday
      return start...end
    case .sevenDays:
      return (calendar.date(byAdding: .day, value: -6, to: day) ?? day)...day
    case .thirtyDays:
      return (calendar.date(byAdding: .day, value: -29, to: day) ?? day)...day
    }
  }
}

public enum CalendarDate {
  public static func string(_ date: Date, calendar: Calendar = .current) -> String {
    let values = calendar.dateComponents([.year, .month, .day], from: date)
    return String(format: "%04d-%02d-%02d", values.year ?? 0, values.month ?? 0, values.day ?? 0)
  }
}

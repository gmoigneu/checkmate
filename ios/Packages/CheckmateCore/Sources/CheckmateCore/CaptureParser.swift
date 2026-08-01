import Foundation

public struct CaptureParse: Sendable, Equatable {
  public var title: String
  public var context: CheckmateContext?
  public var person: Person?
  public var source: String?
  public var estimateMinutes: Int64?
  public var dueOn: String?
  public var plannedOn: String?
  public var unresolved: [String]

  public init(title: String, unresolved: [String] = []) {
    self.title = title
    self.unresolved = unresolved
  }

  public var createBody: [String: JSONValue] {
    var body: [String: JSONValue] = [
      "title": .string(title),
      "capture_method": .string("form"),
    ]
    if let context { body["context_id"] = .string(context.id) }
    if let person {
      body["delegated_to_id"] = .string(person.id)
      body["status"] = .string(TaskStatus.delegated.rawValue)
    }
    if let source { body["source"] = .string(source) }
    if let estimateMinutes { body["estimate_minutes"] = .integer(estimateMinutes) }
    if let dueOn { body["due_on"] = .string(dueOn) }
    if let plannedOn { body["planned_on"] = .string(plannedOn) }
    return body
  }
}

public enum CaptureParser {
  public static func parse(
    _ value: String,
    contexts: [CheckmateContext],
    people: [Person],
    now: Date = .now,
    calendar: Calendar = .current
  ) -> CaptureParse {
    var working = value.trimmingCharacters(in: .whitespacesAndNewlines)
    var result = CaptureParse(title: working)

    if let match = match(#"(?:^|\s)#([\w-]+)"#, in: working), let raw = match.groups.first ?? nil {
      let needle = raw.lowercased()
      let candidates = contexts.filter {
        $0.name.lowercased().hasPrefix(needle) || $0.slug.lowercased().hasPrefix(needle)
      }
      if candidates.count == 1 {
        result.context = candidates[0]
        working = remove(match.range, from: working)
      } else {
        result.unresolved.append("#\(raw)")
      }
    }
    if let match = match(#"(?:^|\s)@([\w .'-]+)"#, in: working), let raw = match.groups.first ?? nil
    {
      let needle = raw.trimmingCharacters(in: .whitespaces).lowercased()
      if let person = people.first(where: { $0.name.lowercased() == needle }) {
        result.person = person
        working = remove(match.range, from: working)
      } else {
        result.unresolved.append("@\(raw.trimmingCharacters(in: .whitespaces))")
      }
    }
    if let match = match(#"(?:^|\s)!(self|email|slack|google_chat|meeting|phone)\b"#, in: working),
      let raw = match.groups.first ?? nil
    {
      result.source = raw.lowercased()
      working = remove(match.range, from: working)
    }
    if let match = match(#"(?:^|\s)(\d+h(?:\d+m)?|\d+m)\b"#, in: working),
      let raw = match.groups.first ?? nil
    {
      let hours = capture(#"(\d+)h"#, in: raw).flatMap(Int64.init) ?? 0
      let minutes = capture(#"(\d+)m"#, in: raw).flatMap(Int64.init) ?? 0
      result.estimateMinutes = hours * 60 + minutes
      working = remove(match.range, from: working)
    }

    if let planned = match(#"(?:^|\s)>(today|tomorrow|in\s+\d+\s+days?)\b"#, in: working),
      let token = planned.groups.first ?? nil,
      let date = relativeDate(token, now: now, calendar: calendar)
    {
      result.plannedOn = dateString(date, calendar: calendar)
      working = remove(planned.range, from: working)
    } else if let due = match(#"(?:^|\s)(today|tomorrow|in\s+\d+\s+days?)\b"#, in: working),
      let token = due.groups.first ?? nil,
      let date = relativeDate(token, now: now, calendar: calendar)
    {
      result.dueOn = dateString(date, calendar: calendar)
      working = remove(due.range, from: working)
    }

    result.title =
      working
      .split(whereSeparator: { $0.isWhitespace })
      .joined(separator: " ")
    return result
  }

  private struct Match {
    let range: Range<String.Index>
    let groups: [String?]
  }

  private static func match(_ pattern: String, in value: String) -> Match? {
    guard let regex = try? NSRegularExpression(pattern: pattern, options: [.caseInsensitive]),
      let found = regex.firstMatch(in: value, range: NSRange(value.startIndex..., in: value)),
      let range = Range(found.range, in: value)
    else { return nil }
    let groups = (1..<found.numberOfRanges).map { index -> String? in
      guard let range = Range(found.range(at: index), in: value) else { return nil }
      return String(value[range])
    }
    return Match(range: range, groups: groups)
  }

  private static func capture(_ pattern: String, in value: String) -> String? {
    match(pattern, in: value)?.groups.first ?? nil
  }

  private static func remove(_ range: Range<String.Index>, from value: String) -> String {
    var copy = value
    copy.replaceSubrange(range, with: " ")
    return copy
  }

  private static func relativeDate(_ token: String, now: Date, calendar: Calendar) -> Date? {
    let lower = token.lowercased()
    if lower == "today" { return now }
    if lower == "tomorrow" { return calendar.date(byAdding: .day, value: 1, to: now) }
    guard let days = capture(#"in\s+(\d+)\s+days?"#, in: lower).flatMap(Int.init) else {
      return nil
    }
    return calendar.date(byAdding: .day, value: days, to: now)
  }

  private static func dateString(_ date: Date, calendar: Calendar) -> String {
    let components = calendar.dateComponents([.year, .month, .day], from: date)
    return String(
      format: "%04d-%02d-%02d", components.year ?? 0, components.month ?? 0, components.day ?? 0)
  }
}

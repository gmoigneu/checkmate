import Foundation
import Testing

@testable import CheckmateCore

@Suite("Server discovery")
struct ServerDiscoveryTests {
  @Test func addsHTTPSForHostedServers() throws {
    #expect(
      try ServerDiscovery.normalizedURL(from: "tasks.example.com").absoluteString
        == "https://tasks.example.com")
  }

  @Test func keepsHTTPForLocalDevelopment() throws {
    #expect(
      try ServerDiscovery.normalizedURL(from: "localhost:8080/").absoluteString
        == "http://localhost:8080")
  }

  @Test func rejectsUnsupportedSchemes() {
    #expect(throws: ServerValidationError.invalidAddress) {
      try ServerDiscovery.normalizedURL(from: "file:///tmp/checkmate")
    }
  }

  @Test func rejectsInsecureRemoteServers() {
    #expect(throws: ServerValidationError.insecureTransport) {
      try ServerDiscovery.normalizedURL(from: "http://tasks.example.com")
    }
  }

  @Test func rejectsHostnamesDisguisedAsLoopbackAddresses() {
    for address in [
      "http://127.evil.example",
      "http://127.0.0.1.evil.example",
      "http://127.300.0.1",
    ] {
      #expect(throws: ServerValidationError.insecureTransport) {
        try ServerDiscovery.normalizedURL(from: address)
      }
    }
  }

  @Test func acceptsTheIPv4LoopbackRange() throws {
    #expect(
      try ServerDiscovery.normalizedURL(from: "http://127.12.34.56:8080").absoluteString
        == "http://127.12.34.56:8080")
  }
}

@Suite("Authentication")
struct AuthenticationTests {
  @Test func generatesOAuthSafePKCE() throws {
    let pair = try PKCEPair.generate()
    #expect((43...128).contains(pair.verifier.count))
    #expect(!pair.verifier.contains("="))
    #expect(!pair.challenge.contains("/"))
    #expect(pair.challenge.count == 43)
  }

  @Test func parsesOAuthCallbackParameters() throws {
    let url = URL(string: "io.nls.checkmate:/oauth/callback?code=abc&state=expected")!
    #expect(
      try OAuthService.callbackParameters(from: url)
        == ["code": "abc", "state": "expected"])
  }

  @Test func rejectsDuplicateOAuthCallbackParameters() {
    let url = URL(string: "io.nls.checkmate:/oauth/callback?code=abc&code=def")!
    #expect(throws: OAuthCallbackError.duplicateParameter("code")) {
      try OAuthService.callbackParameters(from: url)
    }
  }
}

@Suite("Duration text")
struct DurationTextTests {
  @Test func formatsMinutesAndHours() {
    #expect(DurationText.minutes(45) == "45m")
    #expect(DurationText.minutes(60) == "1h")
    #expect(DurationText.minutes(90) == "1h 30m")
  }
}

@Suite("Capture methods")
struct CaptureMethodTests {
  @Test func shareExtensionUsesTheServerContractValue() {
    #expect(CaptureMethod.shareExtension == .chromeExtension)
    #expect(CaptureMethod.shareExtension.rawValue == "chrome_ext")
  }
}

@Suite("Capture parser")
struct CaptureParserTests {
  @Test func extractsKnownTokensAndPreservesTheTitle() {
    var calendar = Calendar(identifier: .gregorian)
    calendar.timeZone = TimeZone(secondsFromGMT: 0)!
    let now = Date(timeIntervalSince1970: 1_788_134_400)  // 2026-08-31 UTC
    let parsed = CaptureParser.parse(
      "Send invoice #upsun @Marc Dupuis !email 1h30m >tomorrow",
      contexts: [context], people: [person], now: now, calendar: calendar
    )
    #expect(parsed.title == "Send invoice")
    #expect(parsed.context?.id == "c1")
    #expect(parsed.person?.id == "p1")
    #expect(parsed.source == "email")
    #expect(parsed.estimateMinutes == 90)
    #expect(parsed.plannedOn == "2026-09-01")
    #expect(parsed.unresolved.isEmpty)
    #expect(parsed.createBody["status"] == .string("delegated"))
  }

  @Test func leavesAmbiguousContextInTheTitle() {
    let second = CheckmateContext(
      id: "c2", name: "Upsun Labs", slug: "upsun-labs", color: nil, sortOrder: 1,
      archivedAt: nil, createdAt: "", updatedAt: "", deletedAt: nil, rev: 2
    )
    let parsed = CaptureParser.parse("Review #up", contexts: [context, second], people: [])
    #expect(parsed.title == "Review #up")
    #expect(parsed.unresolved == ["#up"])
  }

  private var context: CheckmateContext {
    CheckmateContext(
      id: "c1", name: "Upsun", slug: "upsun", color: "#C05E3C", sortOrder: 0,
      archivedAt: nil, createdAt: "", updatedAt: "", deletedAt: nil, rev: 1
    )
  }

  private var person: Person {
    Person(
      id: "p1", name: "Marc Dupuis", email: nil, contextId: nil, notes: nil,
      createdAt: "", updatedAt: "", deletedAt: nil, rev: 1
    )
  }
}

@Suite("Report dates")
struct ReportDateTests {
  @Test func lastWeekUsesMondayThroughSunday() {
    var calendar = Calendar(identifier: .gregorian)
    calendar.timeZone = TimeZone(secondsFromGMT: 0)!
    let today = calendar.date(from: DateComponents(year: 2026, month: 8, day: 1))!
    let range = ReportDatePreset.lastWeek.range(endingAt: today, calendar: calendar)
    #expect(CalendarDate.string(range.lowerBound, calendar: calendar) == "2026-07-20")
    #expect(CalendarDate.string(range.upperBound, calendar: calendar) == "2026-07-26")
  }
}

@Suite("API client", .serialized)
struct APIClientTests {
  @Test func sendsBearerTokenAndDecodesSnakeCase() async throws {
    let session = mockSession { request in
      #expect(request.value(forHTTPHeaderField: "Authorization") == "Bearer cm_test")
      let body =
        #"{"user_id":"u1","email":"hello@example.com","name":"Hello","timezone":"UTC","auth_via":"device_token","scopes":["read","write"]}"#
        .data(using: .utf8)!
      return (
        HTTPURLResponse(url: request.url!, statusCode: 200, httpVersion: nil, headerFields: nil)!,
        body
      )
    }
    let client = APIClient(baseURL: URL(string: "https://checkmate.example")!, session: session) {
      "cm_test"
    }
    let profile = try await client.me()
    #expect(profile.userId == "u1")
    #expect(profile.canWrite)
  }

  @Test func mapsFieldValidationErrors() async throws {
    let session = mockSession { request in
      let body = #"{"error":"validation failed","fields":{"due_on":"must be a date"}}"#.data(
        using: .utf8)!
      return (
        HTTPURLResponse(url: request.url!, statusCode: 422, httpVersion: nil, headerFields: nil)!,
        body
      )
    }
    let client = APIClient(baseURL: URL(string: "https://checkmate.example")!, session: session)
    do {
      _ = try await client.createTask(["title": .string("Test")])
      Issue.record("Expected a validation error")
    } catch let error as APIError {
      #expect(error.status == 422)
      #expect(error.fields["due_on"] == "must be a date")
    }
  }

  @Test func stopsWhenSyncCursorDoesNotAdvance() async throws {
    let session = mockSession { request in
      let body =
        #"{"cursor":0,"has_more":true,"changes":{"contexts":[],"projects":[],"people":[],"recurrences":[],"tasks":[]},"server_time":"2026-08-01T00:00:00Z"}"#
        .data(
          using: .utf8)!
      return (
        HTTPURLResponse(
          url: request.url!, statusCode: 200, httpVersion: nil, headerFields: nil)!, body
      )
    }
    let client = APIClient(baseURL: URL(string: "https://checkmate.example")!, session: session)
    let store = try LocalStore(inMemory: true)

    await #expect(throws: SyncError.stalledCursor(0)) {
      try await SyncEngine(client: client, store: store).run(full: true)
    }
  }

  private func mockSession(
    _ handler: @escaping @Sendable (URLRequest) throws -> (HTTPURLResponse, Data)
  ) -> URLSession {
    MockURLProtocol.handler = handler
    let configuration = URLSessionConfiguration.ephemeral
    configuration.protocolClasses = [MockURLProtocol.self]
    return URLSession(configuration: configuration)
  }
}

private final class MockURLProtocol: URLProtocol, @unchecked Sendable {
  nonisolated(unsafe) static var handler:
    (@Sendable (URLRequest) throws -> (HTTPURLResponse, Data))?

  override class func canInit(with request: URLRequest) -> Bool { true }
  override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

  override func startLoading() {
    do {
      guard let handler = Self.handler else { throw URLError(.badServerResponse) }
      let (response, data) = try handler(request)
      client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
      client?.urlProtocol(self, didLoad: data)
      client?.urlProtocolDidFinishLoading(self)
    } catch {
      client?.urlProtocol(self, didFailWithError: error)
    }
  }

  override func stopLoading() {}
}

@Suite("Local sync cache")
struct LocalStoreTests {
  @Test func appliesUpdatesAndTombstonesInRevisionOrder() async throws {
    let store = try LocalStore(inMemory: true)
    let live = context(rev: 2, deletedAt: nil)
    try await store.apply(sync(cursor: 2, contexts: [live]))
    #expect(try await store.snapshot().contexts.map(\.id) == ["c1"])

    try await store.apply(sync(cursor: 1, contexts: [context(rev: 1, deletedAt: nil)]))
    #expect(try await store.snapshot().contexts.first?.rev == 2)

    try await store.apply(
      sync(cursor: 3, contexts: [context(rev: 3, deletedAt: "2026-08-01T00:00:00Z")]))
    #expect(try await store.snapshot().contexts.isEmpty)
    #expect(try await store.cursor() == 3)
  }

  private func sync(cursor: Int64, contexts: [CheckmateContext]) -> SyncResult {
    SyncResult(
      cursor: cursor, hasMore: false,
      changes: SyncChanges(
        contexts: contexts, projects: [], people: [], recurrences: [], tasks: []),
      sources: [], serverTime: "2026-08-01T00:00:00Z"
    )
  }

  private func context(rev: Int64, deletedAt: String?) -> CheckmateContext {
    CheckmateContext(
      id: "c1", name: "Personal", slug: "personal", color: "#C05E3C", sortOrder: 0,
      archivedAt: nil, createdAt: "", updatedAt: "", deletedAt: deletedAt, rev: rev
    )
  }
}

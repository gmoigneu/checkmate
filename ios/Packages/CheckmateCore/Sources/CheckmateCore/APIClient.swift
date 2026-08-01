import Foundation

#if canImport(FoundationNetworking)
  import FoundationNetworking
#endif

public enum HTTPMethod: String, Sendable {
  case get = "GET"
  case post = "POST"
  case patch = "PATCH"
  case delete = "DELETE"
}

public struct APIError: Error, LocalizedError, Sendable, Equatable {
  public let status: Int
  public let message: String
  public let fields: [String: String]

  public init(status: Int, message: String, fields: [String: String] = [:]) {
    self.status = status
    self.message = message
    self.fields = fields
  }

  public var errorDescription: String? { message }
  public var isUnauthorized: Bool { status == 401 }
  public var isReadOnly: Bool { status == 403 }
}

public struct EmptyResponse: Decodable, Sendable {
  public init() {}
}

public struct APIClient: Sendable {
  public typealias TokenProvider = @Sendable () async throws -> String?

  public let baseURL: URL
  private let session: URLSession
  private let tokenProvider: TokenProvider

  public init(
    baseURL: URL,
    session: URLSession = .shared,
    tokenProvider: @escaping TokenProvider = { nil }
  ) {
    self.baseURL = baseURL
    self.session = session
    self.tokenProvider = tokenProvider
  }

  public func health() async throws -> Health {
    try await request("healthz", authorized: false)
  }

  public func authConfiguration() async throws -> AuthConfiguration {
    try await request("auth/config", authorized: false)
  }

  public func me() async throws -> UserProfile { try await request("v1/me") }

  public func brief(date: String? = nil, contextId: String? = nil) async throws -> Brief {
    var query: [URLQueryItem] = []
    if let date { query.append(.init(name: "date", value: date)) }
    if let contextId { query.append(.init(name: "context_id", value: contextId)) }
    return try await request("v1/brief", query: query)
  }

  public func sync(since: Int64, limit: Int = 200) async throws -> SyncResult {
    try await request(
      "v1/sync",
      query: [
        .init(name: "since", value: String(since)),
        .init(name: "limit", value: String(limit)),
      ])
  }

  public func contexts(includeArchived: Bool = false) async throws -> CollectionResponse<
    CheckmateContext
  > {
    try await request(
      "v1/contexts",
      query: [
        .init(name: "limit", value: "200"),
        .init(name: "include_archived", value: includeArchived ? "true" : "false"),
      ])
  }

  public func createContext(_ body: [String: JSONValue]) async throws -> CheckmateContext {
    try await request("v1/contexts", method: .post, body: body)
  }

  public func updateContext(id: String, body: [String: JSONValue]) async throws -> CheckmateContext
  {
    try await request("v1/contexts/\(id)", method: .patch, body: body)
  }

  public func deleteContext(id: String) async throws {
    let _: EmptyResponse = try await request("v1/contexts/\(id)", method: .delete)
  }

  public func projects(contextId: String? = nil) async throws -> CollectionResponse<Project> {
    var query = [URLQueryItem(name: "limit", value: "200")]
    if let contextId { query.append(.init(name: "context_id", value: contextId)) }
    return try await request("v1/projects", query: query)
  }

  public func createProject(_ body: [String: JSONValue]) async throws -> Project {
    try await request("v1/projects", method: .post, body: body)
  }

  public func updateProject(id: String, body: [String: JSONValue]) async throws -> Project {
    try await request("v1/projects/\(id)", method: .patch, body: body)
  }

  public func deleteProject(id: String) async throws {
    let _: EmptyResponse = try await request("v1/projects/\(id)", method: .delete)
  }

  public func people() async throws -> CollectionResponse<Person> {
    try await request("v1/people", query: [.init(name: "limit", value: "200")])
  }

  public func createPerson(_ body: [String: JSONValue]) async throws -> Person {
    try await request("v1/people", method: .post, body: body)
  }

  public func updatePerson(id: String, body: [String: JSONValue]) async throws -> Person {
    try await request("v1/people/\(id)", method: .patch, body: body)
  }

  public func deletePerson(id: String) async throws {
    let _: EmptyResponse = try await request("v1/people/\(id)", method: .delete)
  }

  public func recurrences(kind: RecurrenceKind? = nil) async throws -> CollectionResponse<
    Recurrence
  > {
    var query = [URLQueryItem(name: "limit", value: "200")]
    if let kind { query.append(.init(name: "kind", value: kind.rawValue)) }
    return try await request("v1/recurrences", query: query)
  }

  public func createRecurrence(_ body: [String: JSONValue]) async throws -> Recurrence {
    try await request("v1/recurrences", method: .post, body: body)
  }

  public func updateRecurrence(id: String, body: [String: JSONValue]) async throws -> Recurrence {
    try await request("v1/recurrences/\(id)", method: .patch, body: body)
  }

  public func deleteRecurrence(id: String) async throws {
    let _: EmptyResponse = try await request("v1/recurrences/\(id)", method: .delete)
  }

  public func tasks(query: [URLQueryItem] = []) async throws -> CollectionResponse<Task> {
    var items = query
    if !items.contains(where: { $0.name == "limit" }) {
      items.append(.init(name: "limit", value: "200"))
    }
    return try await request("v1/tasks", query: items)
  }

  public func task(id: String) async throws -> Task { try await request("v1/tasks/\(id)") }

  public func createTask(_ body: [String: JSONValue]) async throws -> Task {
    try await request("v1/tasks", method: .post, body: body)
  }

  public func updateTask(id: String, body: [String: JSONValue]) async throws -> Task {
    try await request("v1/tasks/\(id)", method: .patch, body: body)
  }

  public func deleteTask(id: String) async throws {
    let _: EmptyResponse = try await request("v1/tasks/\(id)", method: .delete)
  }

  public func restoreTask(id: String) async throws -> Task {
    try await request("v1/tasks/\(id)/restore", method: .post, body: Optional<String>.none)
  }

  public func activity(cursor: String? = nil) async throws -> CollectionResponse<TaskActivity> {
    var query = [URLQueryItem(name: "limit", value: "100")]
    if let cursor { query.append(.init(name: "cursor", value: cursor)) }
    return try await request("v1/activity", query: query)
  }

  public func reportConfiguration() async throws -> ReportConfiguration {
    try await request("v1/reports/config")
  }

  public func reports() async throws -> CollectionResponse<Report> {
    try await request("v1/reports")
  }

  public func report(id: String) async throws -> Report {
    try await request("v1/reports/\(id)")
  }

  public func previewReport(_ body: ReportRequest) async throws -> ReportPreview {
    try await request("v1/reports/preview", method: .post, body: body)
  }

  public func generateReport(_ body: ReportRequest) async throws -> Report {
    try await request("v1/reports/generate", method: .post, body: body)
  }

  public func updateReport(id: String, body: [String: JSONValue]) async throws -> Report {
    try await request("v1/reports/\(id)", method: .patch, body: body)
  }

  public func regenerateReport(id: String) async throws -> Report {
    try await request("v1/reports/\(id)/regenerate", method: .post, body: Optional<String>.none)
  }

  public func deleteReport(id: String) async throws {
    let _: EmptyResponse = try await request("v1/reports/\(id)", method: .delete)
  }

  public func request<Response: Decodable & Sendable, Body: Encodable & Sendable>(
    _ path: String,
    method: HTTPMethod = .get,
    query: [URLQueryItem] = [],
    body: Body? = Optional<String>.none,
    authorized: Bool = true,
    contentType: String = "application/json"
  ) async throws -> Response {
    var components = URLComponents(
      url: baseURL.appending(path: path), resolvingAgainstBaseURL: false)
    components?.queryItems = query.isEmpty ? nil : query
    guard let url = components?.url else {
      throw APIError(status: 0, message: "The server URL is invalid.")
    }

    var request = URLRequest(url: url)
    request.httpMethod = method.rawValue
    request.timeoutInterval = 30
    request.setValue("application/json", forHTTPHeaderField: "Accept")
    if authorized, let token = try await tokenProvider(), !token.isEmpty {
      request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
    }
    if let body {
      let encoder = JSONEncoder()
      encoder.keyEncodingStrategy = .convertToSnakeCase
      request.httpBody = try encoder.encode(body)
      request.setValue(contentType, forHTTPHeaderField: "Content-Type")
    }

    let (data, response) = try await session.data(for: request)
    guard let http = response as? HTTPURLResponse else {
      throw APIError(status: 0, message: "The server returned an unreadable response.")
    }
    guard (200..<300).contains(http.statusCode) else {
      let decoder = JSONDecoder()
      decoder.keyDecodingStrategy = .convertFromSnakeCase
      let payload = try? decoder.decode(APIErrorPayload.self, from: data)
      throw APIError(
        status: http.statusCode,
        message: payload?.error ?? HTTPURLResponse.localizedString(forStatusCode: http.statusCode),
        fields: payload?.fields ?? [:]
      )
    }
    if http.statusCode == 204 || data.isEmpty {
      guard let empty = EmptyResponse() as? Response else {
        throw APIError(status: http.statusCode, message: "The server returned no data.")
      }
      return empty
    }
    let decoder = JSONDecoder()
    decoder.keyDecodingStrategy = .convertFromSnakeCase
    do {
      return try decoder.decode(Response.self, from: data)
    } catch {
      throw APIError(
        status: http.statusCode,
        message: "Checkmate returned data this app could not read: \(error.localizedDescription)")
    }
  }
}

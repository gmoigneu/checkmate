import CheckmateCore
import Foundation
import Observation

@MainActor
@Observable
final class AppModel {
  enum Phase: Equatable {
    case launching
    case onboarding
    case syncing(progress: String)
    case ready
    case reauthentication
  }

  var phase: Phase = .launching
  var profile: UserProfile?
  var brief: Brief?
  var contexts: [CheckmateContext] = []
  var projects: [Project] = []
  var people: [Person] = []
  var recurrences: [Recurrence] = []
  var tasks: [CheckmateCore.Task] = []
  var sources: [CheckmateCore.Source] = []
  var reports: [Report] = []
  var reportConfiguration: ReportConfiguration?
  var lastSyncAt: Date?
  var isRefreshing = false
  var isOffline = false
  var alertMessage: String?
  var toastMessage: String?
  var captureRequested = false
  var briefRequested = false
  var deepLinkedTask: CheckmateCore.Task?

  private(set) var serverURL: URL?
  private(set) var validatedServer: ValidatedServer?
  private(set) var client: APIClient?
  private let vault: CredentialVault
  private let store: LocalStore
  private let defaults: UserDefaults
  private let oauthCoordinator = OAuthCoordinator()

  init(defaults: UserDefaults = SharedConfiguration.defaults) {
    let isUITesting = ProcessInfo.processInfo.arguments.contains("-ui-testing")
    self.defaults =
      isUITesting ? (UserDefaults(suiteName: "io.nls.Checkmate.ui-testing") ?? defaults) : defaults
    vault = SharedConfiguration.credentialVault()
    do {
      store = try isUITesting ? LocalStore(inMemory: true) : LocalStore.shared()
    } catch {
      store = try! LocalStore(inMemory: true)
      alertMessage =
        "The local cache could not be opened. Checkmate will use a temporary cache for this launch."
    }
  }

  func restoreSession() async {
    guard phase == .launching else { return }
    if ProcessInfo.processInfo.arguments.contains("-ui-testing") {
      defaults.removeObject(forKey: SharedConfiguration.serverURLKey)
    }
    do {
      let snapshot = try await store.snapshot()
      apply(snapshot)
      guard let raw = defaults.string(forKey: SharedConfiguration.serverURLKey),
        let url = URL(string: raw),
        try await vault.credential() != nil
      else {
        phase = .onboarding
        return
      }
      configureClient(baseURL: url)
      try await loadAuthenticatedSession(firstSync: snapshot.cursor == 0)
    } catch {
      phase = .onboarding
      alertMessage = error.localizedDescription
    }
  }

  func validateServer(_ address: String) async throws -> ValidatedServer {
    let server = try await ServerDiscovery.validate(address)
    validatedServer = server
    serverURL = server.url
    return server
  }

  func signInWithDeviceToken(_ token: String) async throws {
    guard let url = serverURL ?? validatedServer?.url else {
      throw ServerValidationError.invalidAddress
    }
    let value = token.trimmingCharacters(in: .whitespacesAndNewlines)
    guard !value.isEmpty else { throw APIError(status: 0, message: "Paste a device token first.") }
    try await vault.store(.device(token: value))
    configureClient(baseURL: url)
    do {
      _ = try await client?.me()
      persistServer(url)
      try await loadAuthenticatedSession(firstSync: true)
    } catch {
      try? await vault.clear()
      throw error
    }
  }

  func signInWithOAuth() async throws {
    guard let url = serverURL ?? validatedServer?.url else {
      throw ServerValidationError.invalidAddress
    }
    let oauth = OAuthService(baseURL: url)
    let clientKey = "oauthClientId:\(url.absoluteString)"
    let clientId: String
    if let saved = defaults.string(forKey: clientKey) {
      clientId = saved
    } else {
      let registered = try await oauth.registerClient()
      clientId = registered.clientId
      defaults.set(clientId, forKey: clientKey)
    }
    let request = try oauth.authorizationRequest(clientId: clientId)
    let callback = try await oauthCoordinator.authenticate(url: request.url)
    let components = URLComponents(url: callback, resolvingAgainstBaseURL: false)
    let values = Dictionary(
      uniqueKeysWithValues: (components?.queryItems ?? []).compactMap { item in
        item.value.map { (item.name, $0) }
      })
    if let error = values["error"] {
      throw APIError(status: 0, message: values["error_description"] ?? error)
    }
    guard values["state"] == request.state else {
      throw APIError(status: 0, message: "The sign-in response could not be verified.")
    }
    guard let code = values["code"] else {
      throw APIError(status: 0, message: "The server did not return an authorization code.")
    }
    let tokens = try await oauth.exchange(
      code: code, verifier: request.verifier, clientId: clientId)
    try await vault.store(.oauth(tokens))
    configureClient(baseURL: url)
    persistServer(url)
    try await loadAuthenticatedSession(firstSync: true)
  }

  func refresh(fullSync: Bool = false) async {
    guard !isRefreshing, let client else { return }
    isRefreshing = true
    defer { isRefreshing = false }
    do {
      let snapshot = try await SyncEngine(client: client, store: store).run(full: fullSync)
      apply(snapshot)
      async let nextBrief = client.brief()
      async let nextReports = client.reports()
      async let nextConfig = client.reportConfiguration()
      brief = try await nextBrief
      reports = try await nextReports.data
      reportConfiguration = try await nextConfig
      isOffline = false
    } catch let error as APIError where error.isUnauthorized {
      phase = .reauthentication
      alertMessage =
        "Your Checkmate credential was revoked or expired. Sign in again; your local data is still here."
    } catch {
      isOffline = true
      alertMessage = error.localizedDescription
    }
  }

  func setStatus(_ task: CheckmateCore.Task, to status: TaskStatus) async {
    guard let client else { return }
    guard profile?.canWrite != false else {
      toastMessage = "This credential is read-only."
      return
    }
    do {
      _ = try await client.updateTask(id: task.id, body: ["status": .string(status.rawValue)])
      toastMessage = status == .done ? "“\(task.title)” completed" : "Task updated"
      await refresh()
    } catch {
      alertMessage = error.localizedDescription
    }
  }

  func updateTask(_ task: CheckmateCore.Task, body: [String: JSONValue]) async throws {
    guard let client else { return }
    _ = try await client.updateTask(id: task.id, body: body)
    await refresh()
  }

  func deleteTask(_ task: CheckmateCore.Task) async throws {
    guard let client else { return }
    try await client.deleteTask(id: task.id)
    await refresh()
  }

  func mutate(_ operation: (APIClient) async throws -> Void) async throws {
    guard let client else { throw APIError(status: 0, message: "Connect to your server first.") }
    guard !isOffline else {
      throw APIError(status: 0, message: "Changes are disabled while offline.")
    }
    guard profile?.canWrite != false else {
      throw APIError(status: 403, message: "This credential is read-only.")
    }
    try await operation(client)
    await refresh()
  }

  func createTask(from parse: CaptureParse, captureMethod: String = "form") async throws {
    guard let client else { throw APIError(status: 0, message: "Connect to your server first.") }
    guard !isOffline else {
      throw APIError(status: 0, message: "Capture is unavailable offline. Your text is still here.")
    }
    var body = parse.createBody
    body["capture_method"] = .string(captureMethod)
    let created = try await client.createTask(body)
    toastMessage =
      created.contextId.flatMap { id in contexts.first(where: { $0.id == id })?.name }.map {
        "Added to \($0)"
      } ?? "Added to Inbox"
    await refresh()
  }

  func signOut() async {
    try? await vault.clear()
    try? await store.clear()
    defaults.removeObject(forKey: SharedConfiguration.serverURLKey)
    profile = nil
    brief = nil
    contexts = []
    projects = []
    people = []
    recurrences = []
    tasks = []
    reports = []
    client = nil
    phase = .onboarding
  }

  func startReauthentication() {
    phase = .onboarding
  }

  func open(_ url: URL) async {
    guard url.scheme?.lowercased() == "io.nls.checkmate" else { return }
    let host = url.host?.lowercased()
    if host == "capture" {
      captureRequested = true
      return
    }
    if host == "brief" {
      briefRequested = true
      return
    }
    if host == "task", let id = url.pathComponents.dropFirst().first {
      do {
        if let cached = tasks.first(where: { $0.id == id }) {
          deepLinkedTask = cached
        } else if let client {
          deepLinkedTask = try await client.task(id: id)
        }
      } catch let error as APIError where error.status == 404 {
        toastMessage = "That task no longer exists."
      } catch {
        alertMessage = error.localizedDescription
      }
    }
  }

  private func configureClient(baseURL: URL) {
    serverURL = baseURL
    let oauth = OAuthService(baseURL: baseURL)
    let vault = vault
    client = APIClient(baseURL: baseURL) {
      try await vault.accessToken(oauth: oauth)
    }
  }

  private func loadAuthenticatedSession(firstSync: Bool) async throws {
    guard let client else { return }
    phase = .syncing(progress: firstSync ? "Preparing your first sync…" : "Refreshing your brief…")
    profile = try await client.me()
    let snapshot = try await SyncEngine(client: client, store: store).run(full: firstSync)
    apply(snapshot)
    brief = try await client.brief()
    reports = (try? await client.reports().data) ?? []
    reportConfiguration = try? await client.reportConfiguration()
    isOffline = false
    phase = .ready
  }

  private func apply(_ snapshot: CacheSnapshot) {
    contexts = snapshot.contexts.filter { $0.deletedAt == nil }.sorted {
      $0.sortOrder < $1.sortOrder
    }
    projects = snapshot.projects.filter { $0.deletedAt == nil }
    people = snapshot.people.filter { $0.deletedAt == nil }.sorted {
      $0.name.localizedCaseInsensitiveCompare($1.name) == .orderedAscending
    }
    recurrences = snapshot.recurrences.filter { $0.deletedAt == nil }
    tasks = snapshot.tasks.filter { $0.deletedAt == nil }
    sources = snapshot.sources.sorted { $0.sortOrder < $1.sortOrder }
    lastSyncAt = snapshot.lastSyncAt
  }

  private func persistServer(_ url: URL) {
    defaults.set(url.absoluteString, forKey: SharedConfiguration.serverURLKey)
  }
}

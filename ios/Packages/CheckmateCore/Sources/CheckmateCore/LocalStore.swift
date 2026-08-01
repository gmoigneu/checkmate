import Foundation
import SwiftData

@Model
public final class CachedRecord {
  @Attribute(.unique) public var key: String
  public var entityType: String
  public var recordId: String
  public var rev: Int64
  public var payload: Data

  public init(key: String, entity: String, recordId: String, rev: Int64, payload: Data) {
    self.key = key
    self.entityType = entity
    self.recordId = recordId
    self.rev = rev
    self.payload = payload
  }
}

@Model
public final class CacheMetadata {
  @Attribute(.unique) public var key: String
  public var cursor: Int64
  public var serverTime: String?
  public var lastSyncAt: Date?

  public init(
    key: String = "sync", cursor: Int64 = 0, serverTime: String? = nil, lastSyncAt: Date? = nil
  ) {
    self.key = key
    self.cursor = cursor
    self.serverTime = serverTime
    self.lastSyncAt = lastSyncAt
  }
}

public struct CacheSnapshot: Sendable {
  public let contexts: [CheckmateContext]
  public let projects: [Project]
  public let people: [Person]
  public let recurrences: [Recurrence]
  public let tasks: [Task]
  public let sources: [Source]
  public let cursor: Int64
  public let lastSyncAt: Date?

  public init(
    contexts: [CheckmateContext] = [], projects: [Project] = [], people: [Person] = [],
    recurrences: [Recurrence] = [], tasks: [Task] = [], sources: [Source] = [],
    cursor: Int64 = 0, lastSyncAt: Date? = nil
  ) {
    self.contexts = contexts
    self.projects = projects
    self.people = people
    self.recurrences = recurrences
    self.tasks = tasks
    self.sources = sources
    self.cursor = cursor
    self.lastSyncAt = lastSyncAt
  }
}

public actor LocalStore {
  public static let appGroupIdentifier = SharedConfiguration.appGroupIdentifier

  private let container: ModelContainer
  private let encoder: JSONEncoder
  private let decoder: JSONDecoder

  public init(storeURL: URL? = nil, inMemory: Bool = false) throws {
    let schema = Schema([CachedRecord.self, CacheMetadata.self])
    let configuration: ModelConfiguration
    if inMemory {
      configuration = ModelConfiguration(schema: schema, isStoredInMemoryOnly: true)
    } else if let storeURL {
      configuration = ModelConfiguration("Checkmate", schema: schema, url: storeURL)
    } else {
      configuration = ModelConfiguration("Checkmate", schema: schema)
    }
    container = try ModelContainer(for: schema, configurations: configuration)
    encoder = JSONEncoder()
    encoder.keyEncodingStrategy = .convertToSnakeCase
    decoder = JSONDecoder()
    decoder.keyDecodingStrategy = .convertFromSnakeCase
  }

  public static func sharedStoreURL() -> URL? {
    FileManager.default
      .containerURL(forSecurityApplicationGroupIdentifier: appGroupIdentifier)?
      .appending(path: "Checkmate.store")
  }

  public func cursor() throws -> Int64 {
    let context = ModelContext(container)
    return try metadata(in: context)?.cursor ?? 0
  }

  public func apply(_ result: SyncResult) throws {
    let context = ModelContext(container)
    let existing = try context.fetch(FetchDescriptor<CachedRecord>())
    var byKey = Dictionary(uniqueKeysWithValues: existing.map { ($0.key, $0) })

    try apply(result.changes.contexts, entity: "contexts", in: context, existing: &byKey)
    try apply(result.changes.projects, entity: "projects", in: context, existing: &byKey)
    try apply(result.changes.people, entity: "people", in: context, existing: &byKey)
    try apply(result.changes.recurrences, entity: "recurrences", in: context, existing: &byKey)
    try apply(result.changes.tasks, entity: "tasks", in: context, existing: &byKey)
    if let sources = result.sources {
      for record in existing where record.entityType == "sources" { context.delete(record) }
      for source in sources {
        let record = CachedRecord(
          key: "sources:\(source.key)", entity: "sources", recordId: source.key,
          rev: 0, payload: try encoder.encode(source)
        )
        context.insert(record)
      }
    }

    let meta = try metadata(in: context) ?? CacheMetadata()
    if meta.modelContext == nil { context.insert(meta) }
    meta.cursor = max(meta.cursor, result.cursor)
    meta.serverTime = result.serverTime
    meta.lastSyncAt = .now
    try context.save()
  }

  public func snapshot() throws -> CacheSnapshot {
    let context = ModelContext(container)
    let records = try context.fetch(FetchDescriptor<CachedRecord>())
    let meta = try metadata(in: context)
    return CacheSnapshot(
      contexts: try decode(records, entity: "contexts"),
      projects: try decode(records, entity: "projects"),
      people: try decode(records, entity: "people"),
      recurrences: try decode(records, entity: "recurrences"),
      tasks: try decode(records, entity: "tasks"),
      sources: try decode(records, entity: "sources"),
      cursor: meta?.cursor ?? 0,
      lastSyncAt: meta?.lastSyncAt
    )
  }

  public func clear() throws {
    let context = ModelContext(container)
    try context.delete(model: CachedRecord.self)
    try context.delete(model: CacheMetadata.self)
    try context.save()
  }

  private func metadata(in context: ModelContext) throws -> CacheMetadata? {
    var descriptor = FetchDescriptor<CacheMetadata>(predicate: #Predicate { $0.key == "sync" })
    descriptor.fetchLimit = 1
    return try context.fetch(descriptor).first
  }

  private func apply<Value: CacheableRecord>(
    _ values: [Value], entity: String, in context: ModelContext,
    existing: inout [String: CachedRecord]
  ) throws {
    for value in values {
      let key = "\(entity):\(value.id)"
      if value.deletedAt != nil {
        if let record = existing.removeValue(forKey: key) { context.delete(record) }
        continue
      }
      let payload = try encoder.encode(value)
      if let record = existing[key] {
        guard value.rev >= record.rev else { continue }
        record.rev = value.rev
        record.payload = payload
      } else {
        let record = CachedRecord(
          key: key, entity: entity, recordId: value.id, rev: value.rev, payload: payload)
        context.insert(record)
        existing[key] = record
      }
    }
  }

  private func decode<Value: Decodable>(_ records: [CachedRecord], entity: String) throws -> [Value]
  {
    try records.filter { $0.entityType == entity }.map {
      try decoder.decode(Value.self, from: $0.payload)
    }
  }
}

public struct SyncEngine: Sendable {
  public let client: APIClient
  public let store: LocalStore

  public init(client: APIClient, store: LocalStore) {
    self.client = client
    self.store = store
  }

  @discardableResult
  public func run(full: Bool = false) async throws -> CacheSnapshot {
    var cursor = full ? 0 : try await store.cursor()
    repeat {
      let page = try await client.sync(since: cursor)
      try await store.apply(page)
      cursor = page.cursor
      if !page.hasMore { break }
    } while true
    return try await store.snapshot()
  }
}

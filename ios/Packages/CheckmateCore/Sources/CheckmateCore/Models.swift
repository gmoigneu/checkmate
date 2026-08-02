import Foundation

public enum TaskStatus: String, Codable, CaseIterable, Sendable {
  case inbox, todo
  case inProgress = "in_progress"
  case blocked, delegated, done, cancelled, expired

  public var isOpen: Bool {
    switch self {
    case .inbox, .todo, .inProgress, .blocked, .delegated: true
    case .done, .cancelled, .expired: false
    }
  }
}

public enum TaskKind: String, Codable, CaseIterable, Sendable {
  case short, long, blocked, delegated, recurring, routine
}

public enum CaptureMethod: String, Codable, Sendable {
  case form
  case api
  case hermes
  case chromeExtension = "chrome_ext"
  case iosWidget = "ios_widget"
  case voice
  case recurrence

  /// The server contract uses the browser/share bucket for share extensions.
  public static let shareExtension = Self.chromeExtension
}

public enum TaskPriority: String, Codable, CaseIterable, Sendable {
  case urgent, high, medium, low
}

public enum DaySlot: String, Codable, CaseIterable, Sendable {
  case morning, midday, afternoon, evening, night
}

public enum ProjectStatus: String, Codable, CaseIterable, Sendable {
  case active, paused, done, archived
}

public enum RecurrenceKind: String, Codable, CaseIterable, Sendable {
  case classic, routine
}

public enum RecurrenceState: String, Codable, CaseIterable, Sendable {
  case active, paused, finished
}

public struct Health: Codable, Hashable, Sendable {
  public let status: String
  public let version: String
  public let database: String

  public init(status: String, version: String, database: String) {
    self.status = status
    self.version = version
    self.database = database
  }
}

public struct AuthConfiguration: Codable, Hashable, Sendable {
  public let providers: [String]
}

public struct UserProfile: Codable, Hashable, Sendable {
  public let userId: String
  public let email: String
  public let name: String
  public let timezone: String
  public let authVia: String
  public let scopes: [String]

  public var canWrite: Bool { scopes.contains("write") }
}

public struct Source: Codable, Identifiable, Hashable, Sendable {
  public var id: String { key }
  public let key: String
  public let label: String
  public let sortOrder: Int64
}

public struct CheckmateContext: Codable, Identifiable, Hashable, Sendable {
  public let id: String
  public let name: String
  public let slug: String
  public let color: String?
  public let sortOrder: Int64
  public let archivedAt: String?
  public let createdAt: String
  public let updatedAt: String
  public let deletedAt: String?
  public let rev: Int64
}

public struct Project: Codable, Identifiable, Hashable, Sendable {
  public let id: String
  public let contextId: String
  public let name: String
  public let description: String?
  public let status: ProjectStatus
  public let createdAt: String
  public let updatedAt: String
  public let deletedAt: String?
  public let rev: Int64
}

public struct Person: Codable, Identifiable, Hashable, Sendable {
  public let id: String
  public let name: String
  public let email: String?
  public let contextId: String?
  public let notes: String?
  public let createdAt: String
  public let updatedAt: String
  public let deletedAt: String?
  public let rev: Int64
}

public struct CheckmateTask: Codable, Identifiable, Hashable, Sendable {
  public let id: String
  public let contextId: String?
  public let projectId: String?
  public let parentId: String?
  public let recurrenceId: String?
  public let occurrenceOn: String?
  public let source: String?
  public let captureMethod: String
  public let title: String
  public let details: String?
  public let status: TaskStatus
  public let priority: TaskPriority?
  public let dueOn: String?
  public let plannedOn: String?
  public let daySlot: DaySlot?
  public let slotOrder: Int64
  public let estimateMinutes: Int64?
  public let delegatedToId: String?
  public let blockedById: String?
  public let referenceUrl: String?
  public let referenceLabel: String?
  public let kind: TaskKind
  public let completedAt: String?
  public let cancelledAt: String?
  public let expiredAt: String?
  public let createdAt: String
  public let updatedAt: String
  public let deletedAt: String?
  public let rev: Int64
}

public struct Recurrence: Codable, Identifiable, Hashable, Sendable {
  public let id: String
  public let kind: RecurrenceKind
  public let contextId: String
  public let projectId: String?
  public let source: String?
  public let title: String
  public let details: String?
  public let daySlot: DaySlot?
  public let slotOrder: Int64
  public let rrule: String
  public let timezone: String
  public let estimateMinutes: Int64?
  public let delegatedToId: String?
  public let leadDays: Int64
  public let startsOn: String
  public let endsOn: String?
  public let nextOccurrenceOn: String?
  public let lastSpawnedOn: String?
  public let active: Bool
  public let state: RecurrenceState
  public let completedAt: String?
  public let createdAt: String
  public let updatedAt: String
  public let deletedAt: String?
  public let rev: Int64
}

public struct WaitingGroup: Codable, Identifiable, Hashable, Sendable {
  public var id: String { personId }
  public let personId: String
  public let personName: String
  public let tasks: [CheckmateTask]
}

public struct BriefTotals: Codable, Hashable, Sendable {
  public let overdue: Int
  public let dueToday: Int
  public let planned: Int
  public let inbox: Int
  public let blocked: Int
  public let waitingOn: Int
  public let inProgress: Int
  public let completedToday: Int
  public let cancelledToday: Int
  public let routine: Int
  public let routineOpen: Int
  public let routineDone: Int
  public let routineExpired: Int
  public let plannedMinutes: Int
  public let plannedWithoutEstimate: Int
}

public struct Brief: Codable, Hashable, Sendable {
  public let date: String
  public let timezone: String
  public let overdue: [CheckmateTask]
  public let dueToday: [CheckmateTask]
  public let planned: [CheckmateTask]
  public let inProgress: [CheckmateTask]
  public let inbox: [CheckmateTask]
  public let blocked: [CheckmateTask]
  public let waitingOn: [WaitingGroup]
  public let routine: [CheckmateTask]
  public let completedToday: [CheckmateTask]
  public let cancelledToday: [CheckmateTask]
  public let totals: BriefTotals
}

public struct CollectionResponse<Value: Codable & Sendable>: Codable, Sendable {
  public let data: [Value]
  public let nextCursor: String?
}

public struct TaskActivity: Codable, Identifiable, Hashable, Sendable {
  public let id: Int64
  public let taskId: String
  public let taskTitle: String
  public let action: String
  public let changedFields: [String]
  public let statusBefore: TaskStatus?
  public let statusAfter: TaskStatus?
  public let occurredAt: String
}

public struct SyncChanges: Codable, Sendable {
  public let contexts: [CheckmateContext]
  public let projects: [Project]
  public let people: [Person]
  public let recurrences: [Recurrence]
  public let tasks: [CheckmateTask]
}

public struct SyncResult: Codable, Sendable {
  public let cursor: Int64
  public let hasMore: Bool
  public let changes: SyncChanges
  public let sources: [Source]?
  public let serverTime: String
}

public struct ReportContext: Codable, Identifiable, Hashable, Sendable {
  public let id: String
  public let name: String
}

public struct ReportVersion: Codable, Identifiable, Hashable, Sendable {
  public let id: String
  public let versionNumber: Int64
  public let contentMarkdown: String
  public let model: String
  public let inputTokens: Int64?
  public let outputTokens: Int64?
  public let createdAt: String
  public let updatedAt: String
}

public struct Report: Codable, Identifiable, Hashable, Sendable {
  public let id: String
  public let title: String
  public let startOn: String
  public let endOn: String
  public let focus: String?
  public let contexts: [ReportContext]
  public let includeInbox: Bool
  public let latestVersion: Int64
  public let createdAt: String
  public let updatedAt: String
  public let versions: [ReportVersion]?
}

public struct ReportMetrics: Codable, Hashable, Sendable {
  public let completed: Int
  public let open: Int
  public let blocked: Int
  public let delegated: Int
  public let dropped: Int
  public let inbox: Int
  public let completedEstimateMinutes: Int64
  public let openEstimateMinutes: Int64
  public let completedWithoutEstimate: Int
  public let openWithoutEstimate: Int
}

public struct ReportPreviewTask: Codable, Identifiable, Hashable, Sendable {
  public var id: String { taskId }
  public let taskId: String
  public let title: String
  public let category: String
  public let contextName: String
}

public struct ReportPreview: Codable, Hashable, Sendable {
  public let metrics: ReportMetrics
  public let tasks: [ReportPreviewTask]
  public let legacyHistory: Bool
}

public struct ReportRequest: Codable, Hashable, Sendable {
  public var startOn: String
  public var endOn: String
  public var contextIds: [String]
  public var includeInbox: Bool
  public var focus: String

  public init(
    startOn: String, endOn: String, contextIds: [String], includeInbox: Bool, focus: String = ""
  ) {
    self.startOn = startOn
    self.endOn = endOn
    self.contextIds = contextIds
    self.includeInbox = includeInbox
    self.focus = focus
  }
}

public struct ReportConfiguration: Codable, Hashable, Sendable {
  public let configured: Bool
  public let model: String
}

public struct APIErrorPayload: Codable, Sendable {
  public let error: String
  public let fields: [String: String]?
}

public protocol CacheableRecord: Codable, Identifiable, Sendable where ID == String {
  var rev: Int64 { get }
  var deletedAt: String? { get }
}

extension CheckmateContext: CacheableRecord {}
extension Project: CacheableRecord {}
extension Person: CacheableRecord {}
extension CheckmateTask: CacheableRecord {}
extension Recurrence: CacheableRecord {}

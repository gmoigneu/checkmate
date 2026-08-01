import Foundation
import Security

public struct KeychainStore: Sendable {
  public let service: String
  public let accessGroup: String?

  public init(service: String = "io.nls.Checkmate.credentials", accessGroup: String? = nil) {
    self.service = service
    self.accessGroup = accessGroup
  }

  public func data(for account: String) throws -> Data? {
    var query = baseQuery(account: account)
    query[kSecReturnData as String] = true
    query[kSecMatchLimit as String] = kSecMatchLimitOne
    var item: CFTypeRef?
    let status = SecItemCopyMatching(query as CFDictionary, &item)
    if status == errSecItemNotFound { return nil }
    guard status == errSecSuccess, let data = item as? Data else {
      throw KeychainError(status: status)
    }
    return data
  }

  public func set(_ data: Data, for account: String) throws {
    let query = baseQuery(account: account)
    let attributes: [String: Any] = [
      kSecValueData as String: data,
      kSecAttrAccessible as String: kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly,
    ]
    let status = SecItemUpdate(query as CFDictionary, attributes as CFDictionary)
    if status == errSecItemNotFound {
      var add = query
      for (key, value) in attributes {
        add[key] = value
      }
      let addStatus = SecItemAdd(add as CFDictionary, nil)
      guard addStatus == errSecSuccess else { throw KeychainError(status: addStatus) }
    } else if status != errSecSuccess {
      throw KeychainError(status: status)
    }
  }

  public func delete(_ account: String) throws {
    let status = SecItemDelete(baseQuery(account: account) as CFDictionary)
    guard status == errSecSuccess || status == errSecItemNotFound else {
      throw KeychainError(status: status)
    }
  }

  private func baseQuery(account: String) -> [String: Any] {
    var query: [String: Any] = [
      kSecClass as String: kSecClassGenericPassword,
      kSecAttrService as String: service,
      kSecAttrAccount as String: account,
    ]
    if let accessGroup { query[kSecAttrAccessGroup as String] = accessGroup }
    return query
  }
}

public struct KeychainError: Error, LocalizedError, Sendable {
  public let status: OSStatus
  public var errorDescription: String? {
    SecCopyErrorMessageString(status, nil) as String? ?? "Keychain error \(status)"
  }
}

public enum StoredCredential: Codable, Sendable, Equatable {
  case device(token: String)
  case oauth(OAuthTokens)

  private enum CodingKeys: String, CodingKey { case kind, token, oauth }
  private enum Kind: String, Codable { case device, oauth }

  public init(from decoder: Decoder) throws {
    let container = try decoder.container(keyedBy: CodingKeys.self)
    switch try container.decode(Kind.self, forKey: .kind) {
    case .device: self = .device(token: try container.decode(String.self, forKey: .token))
    case .oauth: self = .oauth(try container.decode(OAuthTokens.self, forKey: .oauth))
    }
  }

  public func encode(to encoder: Encoder) throws {
    var container = encoder.container(keyedBy: CodingKeys.self)
    switch self {
    case .device(let token):
      try container.encode(Kind.device, forKey: .kind)
      try container.encode(token, forKey: .token)
    case .oauth(let tokens):
      try container.encode(Kind.oauth, forKey: .kind)
      try container.encode(tokens, forKey: .oauth)
    }
  }
}

public actor CredentialVault {
  private let keychain: KeychainStore
  private let account: String
  private var cached: StoredCredential?

  public init(keychain: KeychainStore = KeychainStore(), account: String = "primary") {
    self.keychain = keychain
    self.account = account
  }

  public func credential() throws -> StoredCredential? {
    if let cached { return cached }
    guard let data = try keychain.data(for: account) else { return nil }
    let value = try JSONDecoder().decode(StoredCredential.self, from: data)
    cached = value
    return value
  }

  public func store(_ credential: StoredCredential) throws {
    let data = try JSONEncoder().encode(credential)
    try keychain.set(data, for: account)
    cached = credential
  }

  public func clear() throws {
    try keychain.delete(account)
    cached = nil
  }

  public func accessToken(oauth: OAuthService? = nil) async throws -> String? {
    guard let credential = try credential() else { return nil }
    switch credential {
    case .device(let token):
      return token
    case .oauth(let tokens):
      if tokens.expiresAt.timeIntervalSinceNow > 60 { return tokens.accessToken }
      guard let oauth, let refreshToken = tokens.refreshToken else { return tokens.accessToken }
      let refreshed = try await oauth.refresh(refreshToken: refreshToken, clientId: tokens.clientId)
      try store(.oauth(refreshed))
      return refreshed.accessToken
    }
  }
}

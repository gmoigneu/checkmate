import Foundation

public enum SharedConfiguration {
  public static let appGroupIdentifier = "group.io.nls.checkmate"
  public static let serverURLKey = "serverURL"

  public static var defaults: UserDefaults {
    UserDefaults(suiteName: appGroupIdentifier) ?? .standard
  }

  public static var serverURL: URL? {
    get {
      defaults.string(forKey: serverURLKey).flatMap(URL.init(string:))
    }
    set {
      if let newValue {
        defaults.set(newValue.absoluteString, forKey: serverURLKey)
      } else {
        defaults.removeObject(forKey: serverURLKey)
      }
    }
  }

  public static var keychainAccessGroup: String? {
    Bundle.main.object(forInfoDictionaryKey: "CheckmateKeychainAccessGroup") as? String
  }

  public static func credentialVault() -> CredentialVault {
    CredentialVault(keychain: KeychainStore(accessGroup: keychainAccessGroup))
  }

  public static func client() throws -> APIClient {
    guard let serverURL else {
      throw APIError(status: 0, message: "Open Checkmate and connect it to your server first.")
    }
    let vault = credentialVault()
    let oauth = OAuthService(baseURL: serverURL)
    return APIClient(baseURL: serverURL) {
      try await vault.accessToken(oauth: oauth)
    }
  }
}

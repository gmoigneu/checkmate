import Foundation

public enum ServerValidationError: Error, LocalizedError, Sendable, Equatable {
  case invalidAddress
  case unreachable
  case untrustedCertificate
  case wrongService
  case degraded

  public var errorDescription: String? {
    switch self {
    case .invalidAddress: "Enter a complete Checkmate server address."
    case .unreachable: "Cannot reach that address. Check the hostname or your network."
    case .untrustedCertificate: "That certificate is not trusted, so no credentials were sent."
    case .wrongService: "Something answered, but it is not a Checkmate server."
    case .degraded: "The server is running but its database is unreachable. Try again shortly."
    }
  }
}

public struct ValidatedServer: Sendable, Equatable {
  public let url: URL
  public let health: Health
  public let auth: AuthConfiguration

  public var requiresDeviceToken: Bool { auth.providers.isEmpty }
}

public enum ServerDiscovery {
  public static func normalizedURL(from input: String) throws -> URL {
    var value = input.trimmingCharacters(in: .whitespacesAndNewlines)
    guard !value.isEmpty else { throw ServerValidationError.invalidAddress }
    if !value.contains("://") {
      let lower = value.lowercased()
      value =
        (lower.hasPrefix("localhost") || lower.hasPrefix("127.0.0.1") ? "http://" : "https://")
        + value
    }
    guard var components = URLComponents(string: value),
      let scheme = components.scheme?.lowercased(),
      ["http", "https"].contains(scheme),
      components.host != nil
    else {
      throw ServerValidationError.invalidAddress
    }
    components.query = nil
    components.fragment = nil
    if components.path == "/" { components.path = "" }
    guard let url = components.url else { throw ServerValidationError.invalidAddress }
    return url
  }

  public static func validate(_ input: String, session: URLSession = .shared) async throws
    -> ValidatedServer
  {
    let url = try normalizedURL(from: input)
    let client = APIClient(baseURL: url, session: session)
    do {
      let health = try await client.health()
      guard health.status == "ok", health.database == "ok" else {
        throw ServerValidationError.degraded
      }
      let auth = try await client.authConfiguration()
      return ValidatedServer(url: url, health: health, auth: auth)
    } catch let error as ServerValidationError {
      throw error
    } catch let error as APIError {
      if error.status == 503 { throw ServerValidationError.degraded }
      throw ServerValidationError.wrongService
    } catch let error as URLError {
      switch error.code {
      case .serverCertificateUntrusted, .serverCertificateHasBadDate,
        .serverCertificateHasUnknownRoot, .serverCertificateNotYetValid,
        .secureConnectionFailed:
        throw ServerValidationError.untrustedCertificate
      default:
        throw ServerValidationError.unreachable
      }
    } catch {
      throw ServerValidationError.unreachable
    }
  }
}

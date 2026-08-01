import Foundation

public enum ServerValidationError: Error, LocalizedError, Sendable, Equatable {
  case invalidAddress
  case unreachable
  case untrustedCertificate
  case insecureTransport
  case wrongService
  case degraded

  public var errorDescription: String? {
    switch self {
    case .invalidAddress: "Enter a complete Checkmate server address."
    case .unreachable: "Cannot reach that address. Check the hostname or your network."
    case .untrustedCertificate: "That certificate is not trusted, so no credentials were sent."
    case .insecureTransport:
      "Use HTTPS for remote servers. HTTP is only allowed for localhost development."
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
      let host = components.host
    else {
      throw ServerValidationError.invalidAddress
    }
    if scheme == "http", !isLoopback(host) {
      throw ServerValidationError.insecureTransport
    }
    components.query = nil
    components.fragment = nil
    if components.path == "/" { components.path = "" }
    guard let url = components.url else { throw ServerValidationError.invalidAddress }
    return url
  }

  private static func isLoopback(_ host: String) -> Bool {
    let value = host.lowercased()
    return value == "localhost" || value == "::1" || value.hasPrefix("127.")
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

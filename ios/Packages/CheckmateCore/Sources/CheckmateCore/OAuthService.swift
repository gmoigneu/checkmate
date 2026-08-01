import Foundation

#if canImport(FoundationNetworking)
  import FoundationNetworking
#endif

public struct OAuthClientRegistration: Codable, Sendable, Equatable {
  public let clientId: String
  public let clientName: String
  public let redirectUris: [String]
  public let clientIdIssuedAt: Int64
}

public struct OAuthTokenResponse: Codable, Sendable, Equatable {
  public let accessToken: String
  public let tokenType: String
  public let expiresIn: Int
  public let refreshToken: String?
  public let scope: String
}

public struct OAuthTokens: Codable, Sendable, Equatable {
  public let accessToken: String
  public let refreshToken: String?
  public let expiresAt: Date
  public let scope: String
  public let clientId: String

  public init(response: OAuthTokenResponse, clientId: String, now: Date = .now) {
    accessToken = response.accessToken
    refreshToken = response.refreshToken
    expiresAt = now.addingTimeInterval(TimeInterval(response.expiresIn))
    scope = response.scope
    self.clientId = clientId
  }
}

public struct OAuthAuthorizationRequest: Sendable, Equatable {
  public let url: URL
  public let state: String
  public let verifier: String
}

public struct OAuthService: Sendable {
  public let baseURL: URL
  public let redirectURI: String
  public let resource: String
  private let session: URLSession

  public init(
    baseURL: URL,
    redirectURI: String = "io.nls.checkmate:/oauth/callback",
    session: URLSession = .shared
  ) {
    self.baseURL = baseURL
    self.redirectURI = redirectURI
    self.resource = baseURL.absoluteString.trimmingCharacters(in: CharacterSet(charactersIn: "/"))
    self.session = session
  }

  public func registerClient(name: String = "Checkmate for iPhone") async throws
    -> OAuthClientRegistration
  {
    struct Body: Encodable, Sendable {
      let clientName: String
      let redirectUris: [String]
      let applicationType = "native"
      let grantTypes = ["authorization_code", "refresh_token"]
      let responseTypes = ["code"]
      let tokenEndpointAuthMethod = "none"
      let scope = "read write offline_access"
    }
    let client = APIClient(baseURL: baseURL, session: session)
    return try await client.request(
      "oauth/register",
      method: .post,
      body: Body(clientName: name, redirectUris: [redirectURI]),
      authorized: false
    )
  }

  public func authorizationRequest(clientId: String) throws -> OAuthAuthorizationRequest {
    let pair = try PKCEPair.generate()
    let state = try SecureRandom.state()
    var components = URLComponents(
      url: baseURL.appending(path: "oauth/authorize"), resolvingAgainstBaseURL: false)
    components?.queryItems = [
      .init(name: "client_id", value: clientId),
      .init(name: "redirect_uri", value: redirectURI),
      .init(name: "response_type", value: "code"),
      .init(name: "scope", value: "read write offline_access"),
      .init(name: "state", value: state),
      .init(name: "code_challenge", value: pair.challenge),
      .init(name: "code_challenge_method", value: "S256"),
      .init(name: "resource", value: resource),
    ]
    guard let url = components?.url else {
      throw APIError(status: 0, message: "Could not create the sign-in URL.")
    }
    return .init(url: url, state: state, verifier: pair.verifier)
  }

  public func exchange(code: String, verifier: String, clientId: String) async throws -> OAuthTokens
  {
    let response: OAuthTokenResponse = try await formRequest(
      "oauth/token",
      fields: [
        "grant_type": "authorization_code",
        "code": code,
        "redirect_uri": redirectURI,
        "code_verifier": verifier,
        "client_id": clientId,
        "resource": resource,
      ])
    return OAuthTokens(response: response, clientId: clientId)
  }

  public func refresh(refreshToken: String, clientId: String) async throws -> OAuthTokens {
    let response: OAuthTokenResponse = try await formRequest(
      "oauth/token",
      fields: [
        "grant_type": "refresh_token",
        "refresh_token": refreshToken,
        "client_id": clientId,
        "resource": resource,
      ])
    return OAuthTokens(response: response, clientId: clientId)
  }

  public func revoke(token: String, clientId: String) async throws {
    let _: EmptyResponse = try await formRequest(
      "oauth/revoke",
      fields: [
        "token": token,
        "client_id": clientId,
      ])
  }

  private func formRequest<Response: Decodable & Sendable>(_ path: String, fields: [String: String])
    async throws -> Response
  {
    var request = URLRequest(url: baseURL.appending(path: path))
    request.httpMethod = "POST"
    request.setValue("application/x-www-form-urlencoded", forHTTPHeaderField: "Content-Type")
    request.setValue("application/json", forHTTPHeaderField: "Accept")
    request.httpBody =
      fields
      .sorted { $0.key < $1.key }
      .map { "\($0.key.formEncoded)=\($0.value.formEncoded)" }
      .joined(separator: "&")
      .data(using: .utf8)
    let (data, response) = try await session.data(for: request)
    guard let http = response as? HTTPURLResponse else {
      throw APIError(
        status: 0, message: "The authorization server returned an unreadable response.")
    }
    guard (200..<300).contains(http.statusCode) else {
      let decoder = JSONDecoder()
      decoder.keyDecodingStrategy = .convertFromSnakeCase
      let payload = try? decoder.decode(APIErrorPayload.self, from: data)
      throw APIError(status: http.statusCode, message: payload?.error ?? "Authorization failed.")
    }
    if data.isEmpty, let empty = EmptyResponse() as? Response { return empty }
    let decoder = JSONDecoder()
    decoder.keyDecodingStrategy = .convertFromSnakeCase
    return try decoder.decode(Response.self, from: data)
  }
}

extension String {
  fileprivate var formEncoded: String {
    addingPercentEncoding(
      withAllowedCharacters: CharacterSet.alphanumerics.union(CharacterSet(charactersIn: "-._~")))
      ?? self
  }
}

import CryptoKit
import Foundation
import Security

public struct PKCEPair: Sendable, Equatable {
  public let verifier: String
  public let challenge: String

  public init(verifier: String, challenge: String) {
    self.verifier = verifier
    self.challenge = challenge
  }

  public static func generate() throws -> PKCEPair {
    let verifier = try randomURLSafeString(byteCount: 64)
    let digest = SHA256.hash(data: Data(verifier.utf8))
    return PKCEPair(verifier: verifier, challenge: Data(digest).base64URLEncodedString())
  }
}

public enum SecureRandom {
  public static func state() throws -> String { try randomURLSafeString(byteCount: 32) }
}

private func randomURLSafeString(byteCount: Int) throws -> String {
  var bytes = [UInt8](repeating: 0, count: byteCount)
  let status = SecRandomCopyBytes(kSecRandomDefault, bytes.count, &bytes)
  guard status == errSecSuccess else {
    throw APIError(status: 0, message: "The device could not create secure random data.")
  }
  return Data(bytes).base64URLEncodedString()
}

extension Data {
  fileprivate func base64URLEncodedString() -> String {
    base64EncodedString()
      .replacingOccurrences(of: "+", with: "-")
      .replacingOccurrences(of: "/", with: "_")
      .replacingOccurrences(of: "=", with: "")
  }
}

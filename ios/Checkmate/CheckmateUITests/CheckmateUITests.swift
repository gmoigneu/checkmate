import XCTest

final class CheckmateUITests: XCTestCase {
  override func setUpWithError() throws {
    continueAfterFailure = false
  }

  @MainActor
  func testOnboardingAdvancesToServerEntry() throws {
    let app = XCUIApplication()
    app.launchArguments = ["-ui-testing"]
    app.launch()

    let getStarted = app.buttons["get-started"]
    XCTAssertTrue(getStarted.waitForExistence(timeout: 5))
    getStarted.tap()

    let address = app.textFields["server-address"]
    XCTAssertTrue(address.waitForExistence(timeout: 2))
    XCTAssertEqual(address.value as? String, "http://localhost:8080")
    XCTAssertTrue(app.staticTexts["Where does your Checkmate live?"].exists)
  }

  @MainActor
  func testDiscoversLocalCheckmateServerWhenAvailable() async throws {
    guard let url = URL(string: "http://localhost:8080/healthz"),
      let (_, response) = try? await URLSession.shared.data(from: url),
      (response as? HTTPURLResponse)?.statusCode == 200
    else {
      throw XCTSkip("The optional local Checkmate server is not running.")
    }

    let app = XCUIApplication()
    app.launchArguments = ["-ui-testing"]
    app.launch()
    XCTAssertTrue(app.buttons["get-started"].waitForExistence(timeout: 5))
    app.buttons["get-started"].tap()
    XCTAssertTrue(app.textFields["server-address"].waitForExistence(timeout: 2))
    app.buttons["Continue"].tap()

    XCTAssertTrue(app.staticTexts["Sign in"].waitForExistence(timeout: 15))
    XCTAssertTrue(app.buttons["Continue with Google"].exists)
  }

  @MainActor
  func testLaunchPerformance() throws {
    measure(metrics: [XCTApplicationLaunchMetric()]) {
      let app = XCUIApplication()
      app.launchArguments = ["-ui-testing"]
      app.launch()
    }
  }
}

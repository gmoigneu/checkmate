# Checkmate for iPhone — implementation plan

The native client follows `specs/checkmate-design.html` for visual direction and
`specs/design-spec.md` / `specs/openapi.yaml` for behavior. The server remains the
source of truth; the local SwiftData store is a read cache and never accepts
offline writes.

## Product structure

- **Main tabs:** Brief, Inbox, Capture, Upcoming, and More. Upcoming keeps
  future-due work one tap away; More is the stable directory for secondary
  destinations.
- **Secondary navigation:** Waiting, search, contexts, projects, people,
  routines, reports, activity, and Settings are all reachable from More.
- **Reports:** generation and saved reports live under More. This keeps the
  high-frequency task navigation unchanged while making the web feature
  available natively.
- **Capture:** a centered tab opens a sheet instead of becoming a navigation
  destination. Draft text survives request failures.

## Modules

- `CheckmateCore/API`: Codable API models, typed endpoints, errors, bearer auth,
  token refresh, OAuth discovery, and server validation.
- `CheckmateCore/Auth`: Keychain credential storage and PKCE helpers.
- `CheckmateCore/Persistence`: SwiftData record cache and sync cursor.
- `CheckmateCore/Capture`: native implementation of the web quick-capture grammar.
- `Checkmate/App`: session lifecycle and dependency construction.
- `Checkmate/DesignSystem`: warm-paper colors, serif headings, inset groups, task
  rows, chips, completion controls, and global states.
- `Checkmate/Features`: onboarding, brief, inbox/triage, waiting, search, contexts,
  task detail/editing, reports, capture, and settings.

## Delivery order

1. Server validation, device-token sign-in, OAuth 2.1 + PKCE, first sync.
2. Shared cache and live Brief/Inbox/Waiting task flows.
3. Capture, task detail/editing, contexts/projects, search, routines, and activity.
4. Report preview/generation/editor/history.
5. Widget/Control, App Intents/Siri, and share-extension targets using an App Group.
6. Accessibility, Dynamic Type, dark mode, reduced motion, unit/UI tests, and
   simulator verification.

## Quality gates

- Unit tests for URL normalization, server validation, API errors, PKCE, capture
  parsing, sync replay, and report date presets.
- Mock-transport tests for every mutation and authentication refresh behavior.
- UI smoke tests for onboarding, tab navigation, capture draft preservation,
  offline/401 states, and task completion.
- `xcodebuild build`, package tests, app tests, and simulator launch must pass
  before the PR is opened.

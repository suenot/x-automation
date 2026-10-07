# Changelog

All notable changes to this project are documented here.

## [Unreleased]

## [0.1.3] - 2026-10-08

### Fixed

- Scope text, media, readiness, errors and the sole Post click to the visible innermost composer dialog, excluding the background timeline composer.
- Dismiss trailing hashtag/mention completion before leaving the text editor; avoid the empty popover backdrop created by Tab. Require the exposed Post button to pass trial mouse checks before the journal boundary, then click it once; avoid Enter because the live X editor dispatched two create requests from one key activation.
- Preserve private submission phases, API paths without query strings, and browser action diagnostics for uncertain results.
- Bound guest media readiness even when the browser's play promise never settles.
- Verify X's separate signed-out article layout using the exact permalink, first author link, caption block and playable video.
- Compare permalink and author handles without case sensitivity while retaining the exact post ID.
- Enter captions with native ASCII key events and Unicode insertion, and read DraftJS blocks as exact lines, preventing duplicated text and false mismatches on blank lines.

## [0.1.2] - 2026-10-07

### Fixed

- Report `FIREFOX_PROFILE_BUSY` when a cookie snapshot reaches its deadline because SQLite is locked, with the correct quit-and-retry instruction.

### Changed

- Verify a newly authorized Firefox session in persistent Camoufox and confirm its exact X identity and public account setting. Publication still requires the intended account to be confirmed.

## [0.1.1] - 2026-10-07

### Fixed

- Reject a completed request ID when its local video file has different bytes at the same path, without reopening Camoufox or clicking Post.
- Confirm and guest-check video-only posts when X omits the empty post-text element.

## [0.1.0] - 2026-10-07

### Added

- Primary Python `x-publish` CLI with persistent Camoufox account binding, Firefox session import, full metadata schema validation, public video preparation and one-click publication.
- Private SQLite journal, process locks, immutable normalized caption/media fingerprint, crash recovery, cached outcomes, diagnostics, status, manual reconciliation and cookie-free guest playback verification.
- Pinned Python/browser dependencies, automated contract checks and documented macOS/Linux setup.

### Changed

- The project's primary purpose and interface are the reusable Camoufox publisher.
- Historical Go analytics implementation and tests are isolated under `analytics/`; the root compatibility facade preserves existing imports and exported types/methods.

### Known limitations

- Live X publication and guest verification await a verified brand session. Audited Firefox profiles contain no X session, and the desired 16-character cross-platform nickname exceeds X's 15-character handle limit.
- Live composer/response selectors, Linux runtime and default browser cache selection remain unverified.

# Changelog

All notable changes to this project are documented here.

## [Unreleased]

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

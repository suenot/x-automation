# Changelog

All notable changes to this project are documented here.

## [Unreleased]

### Fixed

- Type the caption at a pace a hand produces. `press_sequentially(delay=5)` left the same 5 ms between every keystroke, and the Unicode runs went in through `execCommand("insertText")`, which puts text in with no keystroke at all. The composer text is still compared with the request before the upload, so input that does not land fails there.

### Added

- `human` module for input that leaves the trace a person leaves: an eased approach to a control, a pause before pressing, typing with a varying interval, insertion for what a keyboard cannot produce, and a real clipboard paste. The paste is the default for text a person would not retype from memory (long, multi-line or outside ASCII), because nobody composes a caption in an upload form; short single-line ASCII is typed. `allow_clipboard=False` turns the paste off. The clipboard is global, so it is re-read immediately before the paste key and the paste is abandoned if it is no longer ours. Kept in step with the copies in the other publishers, which are standalone repositories sharing no package. The measurements behind it are in `docs-site/@docs/guides/what-a-page-sees-when-we-type.md`.
- Test that fails the build if any module in the package dispatches a browser event or puts text in without keystrokes through `execCommand`, `fill`, `insert_text` or `press_sequentially`.

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

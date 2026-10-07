# x-automation

Reusable X video publisher through persistent Camoufox, with the same account, metadata and request journal contract as the other `*-automation` projects. The primary interface is the Python 3.12 CLI **`x-publish`**. Publication uses browser controls and observes the response to that one Post click to identify its concrete URL.

The historical Go analytics package remains compatible at `github.com/suenot/x-automation`. Its implementation and original tests now live in [`analytics/`](analytics/README.md); the root package exports a small compatibility facade. Existing Go imports and exported types/methods continue to work. `Config.CamoufoxURL` is still an unimplemented analytics fallback; it does not configure the publisher. Analytics never writes the publication journal.

## Install on macOS or Linux

```sh
uv sync --locked --python 3.12
uv run python -m camoufox fetch
uv run x-publish --version
```

Install `ffmpeg` so `ffprobe` can inspect the uploaded snapshot. Dependencies are pinned in `pyproject.toml` and `uv.lock`: Camoufox 0.5.7, Playwright 1.60.0, and metadata-automation 0.2.0 at `994ecce4c904c9c1a7f5de9086ef2a9785c25efa`. The paired browser is official 156.0.1-beta.34. Every launch checks the release's `version.json`. `CAMOUFOX_EXECUTABLE_PATH` can select an existing paired installation without changing the global browser cache. On macOS that installation needs `application.ini` beside the executable, linked from `../Resources/application.ini`.

Default mode is visible Camoufox. `--headless` uses headless Camoufox on macOS and `headless="virtual"` on Linux. Primary interactive login requires a visible window. Firefox is only an authentication source; all account, composition, Post and verification operations use Camoufox. [Camoufox persistent contexts and headless mode](https://camoufox.com/python/usage/) describe supported launch options.

## Bind an account

X usernames contain at most 15 ASCII letters, digits or underscores. A local alias can be longer: `caribbeanawesome` is a valid alias, but its 16 characters cannot be an X username. Supply the actual X handle explicitly; the CLI never truncates, guesses or renames it. See [X username rules](https://help.x.com/en/managing-your-account/x-username-rules).

```sh
uv run x-publish login --account brand --handle ACTUAL_X_HANDLE
uv run x-publish accounts list
```

To reuse an already authorized Firefox session, quit Firefox if its cookie DB is locked, then import into an unused Camoufox profile:

```sh
uv run x-publish login --account brand --handle ACTUAL_X_HANDLE \
  --import-firefox '/absolute/path/to/Firefox/Profiles/default-release' --headless
```

The importer takes a consistent read-only SQLite backup in memory, selects live unpartitioned `x.com` cookies, preserves cookie security and SameSite scope, and never prints cookie values. Unknown schemas and container/partition scopes are rejected or skipped. Failed imports clear imported cookies and delete that new profile. The authenticated navigation Profile link and account switcher must agree with `--handle` before the alias is bound; its display name and canonical URL are saved. An alias cannot be rebound to another handle, and a second alias cannot bind the same handle. Renew an existing profile with visible login rather than importing over it.

`--state-dir PATH` (or `X_PUBLISH_STATE_DIR`) selects state storage; default is `.local/` relative to the caller's directory. Always reuse the same state directory and account alias for the same publisher identity.

## Prepare and publish

`--metadata-file` validates the complete pinned metadata-automation schema, supports v1 and v2, and uses **`platforms.x.text` verbatim**. Hashtags are already included; their metadata array is never appended again. `--caption-file` is the alternative UTF-8 source. Supply only one source. Omitting both creates a video post without text.

Only public video posts are supported. The account's "Protect your posts" setting must be observed unchecked before preparation and immediately before Post. The CLI stops on account mismatch, unavailable settings, existing draft media, changed controls, processing errors, different composer text, or disabled Post. Clear a prepared draft manually before a subsequent request if X restored its attachment. [X public/protected posts](https://help.x.com/en/safety-and-security/public-and-protected-posts) explain account visibility. The local snapshot must be a nonempty H.264 video, at most 512 MiB and 140 seconds; X's composer performs its own final compatibility checks. [X video limits](https://help.x.com/en/using-x/x-videos) document the ordinary account limits.

```sh
uv run x-publish accept-ui --account brand \
  --video /absolute/path/video.mp4 --metadata-file /absolute/path/metadata.json \
  --visibility public --headless

uv run x-publish publish --account brand --request-id campaign-x-001 \
  --video /absolute/path/video.mp4 --metadata-file /absolute/path/metadata.json \
  --visibility public --headless --timeout-seconds 1800

uv run x-publish status --account brand --request-id campaign-x-001
uv run x-publish verify-public --account brand --request-id campaign-x-001 --headless
```

`accept-ui` exercises the actual account, visibility, media and exact-text checks, saves a private editor screenshot, and returns `submitted: false`. It creates no journal request and never presses Post. `publish` performs those same checks, commits `submitting` synchronously, and clicks Post once. It observes the response caused by that click, requiring the expected author, exact text and one video before constructing a canonical status URL. It then verifies the exact post's author, caption and video in Camoufox. A changed response schema fails closed as `uncertain`.

`verify-public` uses a disposable cookie-free Camoufox profile, checks the saved exact post URL/author/text, starts its video, and requires playable media. It changes no publication status and returns `public: true` and `playable: true` only after those guest checks pass. It does not guess a latest post from the profile.

The caption is included in normalized arguments before any cached return. The snapshot SHA-256 and all options form the durable request fingerprint. A different caption or video bytes at the same path conflict with an old ID, including a completed request. Cached published/uncertain results hash the local video without staging it or reopening the browser. Use `status` when an original text or video source is no longer available.

## Journal, recovery and caller contract

Every command outputs one JSON object on one stdout line; progress goes to stderr. Publication/status include `platform: "x"`, `account`, `request_id`, `status`, `post_url`, and `error`. `--help` and `--version` also return JSON. Default timeout is 1800 seconds.

| Exit | Meaning |
| --- | --- |
| 0 | Confirmed publication, cached publication, successful login/list, or existing status record. |
| 2 | Invalid arguments/media/text, unknown account, expired login, account mismatch, private account or request conflict. Post was not clicked. |
| 3 | Profile busy, unavailable browser, changed UI or other failure before submission. Post was not clicked. |
| 4 | Submission is uncertain; Post is never automatically clicked again. |

Private `.local/` contains the SQLite journal, per-account profile/fingerprint/lock, request directories and screenshots. Directories are mode 0700 and files 0600; all are ignored by Git. Per-attempt media copies are hashed/probed/uploaded and removed afterward. The alias's nonblocking process lock covers login, publication, acceptance and reconciliation. SQLite uses `synchronous=FULL`, immutable request fingerprints and guarded state transitions. A process crash after `submitting` becomes `uncertain`.

After manually comparing the exact uncertain post's video to the request, reconciliation can bind its explicit URL and verify author/text/video without upload or Post:

```sh
uv run x-publish reconcile --account brand --request-id campaign-x-001 \
  --post-url https://x.com/ACTUAL_X_HANDLE/status/POST_ID \
  --confirm-request-video --headless
```

A URL already bound to a request cannot be replaced. Reconciliation does not prove byte identity; `--confirm-request-video` records the operator's manual comparison. Keep the journal when renewing sessions. Never use another request ID to retry an uncertain submission until the old one has been inspected.

## Verification and current release scope

```sh
uv run pytest -q
go test ./...
```

The 0.1.1 unit checks cover JSON/exit codes, full metadata validation, exact caption and video-byte changes, private snapshots, protected account/identity errors before Post, fingerprint conflicts, process locking, monotonic journal transitions, crash recovery, one Post, response author/media validation and Firefox scope filtering. The Go suite verifies the original analytics behavior and stable root import. An opt-in controlled Camoufox test (`XPUB_TEST_CAMOUFOX=1 uv run pytest -q tests/test_camoufox.py`, with the paired executable selected) intercepts every browser request and verifies real controls, zero-click acceptance, one causal Post response, cached outcomes, captionless video, cookie-free guest playback, protected-account rejection and existing-draft media rejection. It passed on macOS; it uses dummy locally served posts and never contacts X.

The October 7, 2026 macOS headless Camoufox smoke launched paired 156.0.1-beta.34 successfully with an isolated profile after running outside the restricted sandbox. An initial sandbox launch aborted in macOS application registration before any page or submission. The audited Firefox profiles contain **no X or Twitter session cookies**, and the intended cross-platform `caribbeanawesome` nickname exceeds X's limit. No X account has been bound and no Post has been attempted. Real X composer selectors, CreateTweet response shape, actual publication and guest visibility still require a verified brand login and the first authorized live run. Linux and default browser cache selection were not tested in this release.

## License

MIT

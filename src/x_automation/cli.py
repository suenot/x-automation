import argparse
import asyncio
from contextlib import redirect_stdout
import json
import os
from pathlib import Path
import sys

from .errors import PublishError
from .service import accept_ui, login, publish, reconcile, verify_public_post
from .state import State, account_name, request_id, result


class VersionRequested(Exception):
    pass


class HelpRequested(Exception):
    def __init__(self, text):
        self.text = text


class Parser(argparse.ArgumentParser):
    def error(self, message):
        raise PublishError("INVALID_ARGUMENTS", message)

    def print_help(self, file=None):
        raise HelpRequested(self.format_help())


def timeout_seconds(value):
    try:
        number = int(value)
        if number < 1:
            raise ValueError()
        return number
    except ValueError as exc:
        raise argparse.ArgumentTypeError("timeout must be a positive number of seconds") from exc


def parser():
    root = Parser(prog="x-publish", description="Publish one local video through persistent Camoufox.")
    root.add_argument("--version", action="store_true")
    commands = root.add_subparsers(dest="command", required=True)
    common = Parser(add_help=False)
    common.add_argument("--state-dir", type=Path, default=Path(os.environ.get("X_PUBLISH_STATE_DIR", Path(".local"))))
    common.add_argument("--account", required=True)
    runtime = Parser(add_help=False)
    runtime.add_argument("--timeout-seconds", type=timeout_seconds, default=1800)
    runtime.add_argument("--headless", action="store_true")
    log = commands.add_parser("login", parents=[common])
    log.add_argument("--timeout-seconds", type=timeout_seconds, default=1800)
    log.add_argument("--handle", help="Expected X username; required when importing Firefox.")
    log.add_argument("--import-firefox", type=Path)
    log.add_argument("--headless", action="store_true")
    pub = commands.add_parser("publish", parents=[common, runtime])
    pub.add_argument("--request-id", required=True)
    pub.add_argument("--video", required=True)
    pub.add_argument("--visibility", required=True, choices=["public"])
    captions = pub.add_mutually_exclusive_group()
    captions.add_argument("--caption-file")
    captions.add_argument("--metadata-file")
    accept = commands.add_parser("accept-ui", parents=[common])
    accept.add_argument("--timeout-seconds", type=timeout_seconds, default=1800)
    accept.add_argument("--headless", action="store_true")
    accept.add_argument("--video", required=True)
    accept.add_argument("--visibility", required=True, choices=["public"])
    acceptance_caption = accept.add_mutually_exclusive_group()
    acceptance_caption.add_argument("--caption-file")
    acceptance_caption.add_argument("--metadata-file")
    status = commands.add_parser("status", parents=[common])
    status.add_argument("--request-id", required=True)
    rec = commands.add_parser("reconcile", parents=[common, runtime])
    rec.add_argument("--request-id", required=True)
    rec.add_argument("--post-url")
    rec.add_argument("--confirm-request-video", action="store_true")
    guest = commands.add_parser("verify-public", parents=[common, runtime])
    guest.add_argument("--request-id", required=True)
    accounts = commands.add_parser("accounts")
    actions = accounts.add_subparsers(dest="action", required=True)
    listing = actions.add_parser("list")
    listing.add_argument("--state-dir", type=Path, default=common.get_default("state_dir"))
    return root


def failure(args, error, raw):
    payload = {"platform": "x", "status": "failed", "error": error.json()}
    command = args.command if args else (raw[0] if raw else None)
    if command in {"publish", "status", "reconcile", "verify-public"}:
        payload.update({"account": getattr(args, "account", None), "request_id": getattr(args, "request_id", None), "post_url": None})
    elif command in {"login", "accept-ui"}:
        payload["account"] = getattr(args, "account", None)
    return payload


def main(argv=None):
    raw = list(sys.argv[1:] if argv is None else argv)
    args, state = None, None
    try:
        # Includes library diagnostics. Only the final JSON line writes to stdout.
        with redirect_stdout(sys.stderr):
            if raw == ["--version"]:
                raise VersionRequested()
            args = parser().parse_args(raw)
            if hasattr(args, "account"):
                account_name(args.account)
            if hasattr(args, "request_id"):
                request_id(args.request_id)
            old_umask = os.umask(0o077)
            try:
                state = State(args.state_dir.expanduser().absolute())
                if args.command == "accounts":
                    payload, code = {"platform": "x", "status": "ok", "accounts": state.accounts(), "error": None}, 0
                elif args.command == "status":
                    row = state.get(args.account, args.request_id)
                    if row is None:
                        raise PublishError("REQUEST_NOT_FOUND", "No journal entry for this account and request ID.")
                    # Read-only status reports a crashed boundary as uncertain without writing.
                    if row["status"] == "submitting":
                        row["status"] = "uncertain"
                        row["error"] = json.dumps({"code": "INTERRUPTED_SUBMISSION", "message": "Submission was interrupted; inspect this post manually."})
                    payload, code = result(row), 0
                else:
                    handler = {"login": login, "publish": publish, "reconcile": reconcile, "accept-ui": accept_ui, "verify-public": verify_public_post}[args.command]
                    payload, code = asyncio.run(handler(state, args))
            finally:
                os.umask(old_umask)
    except VersionRequested:
        from importlib.metadata import version
        payload, code = {"platform": "x", "status": "ok", "version": version("x-automation"), "error": None}, 0
    except HelpRequested as exc:
        payload, code = {"platform": "x", "status": "ok", "help": exc.text, "error": None}, 0
    except PublishError as exc:
        payload, code = failure(args, exc, raw), exc.exit_code
    except KeyboardInterrupt:
        error = PublishError("INTERRUPTED", "Command interrupted; read status before trying to publish again.", 3)
        payload, code = failure(args, error, raw), 3
        if args and state and args.command == "publish":
            try:
                row = state.get(args.account, args.request_id)
                if row and row["status"] in {"submitting", "uncertain"}:
                    payload, code = result(state.recover(row)), 4
            except Exception:
                payload, code = failure(args, PublishError("INTERRUPTED_UNCERTAIN", "Interrupted with unavailable journal; inspect this request manually.", 4), raw), 4
                payload["status"] = "uncertain"
    except Exception:
        payload, code = failure(args, PublishError("TECHNICAL_FAILURE", "Local state or browser operation failed.", 3), raw), 3
    print(json.dumps(payload, ensure_ascii=False, separators=(",", ":")))
    return code


if __name__ == "__main__":
    raise SystemExit(main())

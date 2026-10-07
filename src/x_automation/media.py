import hashlib
import json
import os
from pathlib import Path
import shutil
import stat
import subprocess

from .errors import PublishError


def read_caption(caption_file=None, metadata_file=None) -> str:
    try:
        if caption_file:
            with Path(caption_file).open(encoding="utf-8", newline="") as stream:
                return stream.read()
        if metadata_file:
            from metadata_automation import validate
            envelope = json.loads(Path(metadata_file).read_text(encoding="utf-8"))
            validate(envelope)
            return envelope["platforms"]["x"]["text"]
        return ""
    except (OSError, UnicodeError, ValueError, KeyError, TypeError) as exc:
        raise PublishError("INVALID_CAPTION", f"Caption or metadata file is invalid ({type(exc).__name__}).") from exc


def copy_file(source: str, destination: Path) -> str:
    path = Path(source)
    try:
        with os.fdopen(os.open(path, os.O_RDONLY | os.O_NONBLOCK), "rb") as incoming:
            if not stat.S_ISREG(os.fstat(incoming.fileno()).st_mode):
                raise PublishError("INVALID_MEDIA", "Media must be a readable regular local file.")
            with destination.open("wb") as outgoing:
                destination.chmod(0o600)
                shutil.copyfileobj(incoming, outgoing)
        with destination.open("rb") as stream:
            return hashlib.file_digest(stream, "sha256").hexdigest()
    except OSError as exc:
        raise PublishError("INVALID_MEDIA", "Media must be a readable regular local file.") from exc


def hash_file(source: str) -> str:
    """Compare a completed request with the current local file without staging it again."""
    try:
        with os.fdopen(os.open(source, os.O_RDONLY | os.O_NONBLOCK), "rb") as stream:
            if not stat.S_ISREG(os.fstat(stream.fileno()).st_mode):
                raise PublishError("INVALID_MEDIA", "Media must be a readable regular local file.")
            return hashlib.file_digest(stream, "sha256").hexdigest()
    except OSError as exc:
        raise PublishError("INVALID_MEDIA", "Media must be a readable regular local file.") from exc


def probe_video(path: Path) -> None:
    # Validate actual media; X's current per-account limits belong to Studio.
    if not 0 < path.stat().st_size <= 512 * 1024 * 1024:
        raise PublishError("INVALID_MEDIA", "Video must be nonempty and at most 512 MiB.")
    try:
        process = subprocess.run(["ffprobe", "-v", "error", "-show_format", "-show_streams", "-of", "json", str(path)],
                                 capture_output=True, timeout=30, check=True)
        data = json.loads(process.stdout)
        video = next(s for s in data["streams"] if s["codec_type"] == "video")
        duration = float(data["format"]["duration"])
        if not 0 < duration <= 140 or video.get("codec_name") != "h264" or min(video["width"], video["height"]) <= 0:
            raise PublishError("INVALID_MEDIA", "Video must have a recognized codec, duration and dimensions.")
    except FileNotFoundError as exc:
        raise PublishError("FFPROBE_MISSING", "Install ffmpeg (ffprobe) before publishing.", 3) from exc
    except (subprocess.SubprocessError, ValueError, KeyError, StopIteration, TypeError) as exc:
        raise PublishError("INVALID_MEDIA", "ffprobe could not validate this video.") from exc

"""X UI adapter. Publish is never retried after the journal boundary."""
import asyncio
from contextlib import asynccontextmanager
import json
import os
from pathlib import Path
import re
import sys
from urllib.parse import urlparse
from playwright.async_api import expect

from .errors import PublishError

POST_RE = re.compile(r"^https://x\.com/([A-Za-z0-9_]+)/status/(\d+)$")

def verify_browser_build():
    from camoufox.browser_pin import load_pin
    from camoufox.multiversion import get_active_path

    pin = load_pin()
    executable = os.environ.get("CAMOUFOX_EXECUTABLE_PATH")
    if executable:
        metadata = next((p / "version.json" for p in Path(executable).resolve().parents if (p / "version.json").is_file()), None)
    else:
        active = get_active_path()
        metadata = active / "version.json" if active else None
    if not metadata or not metadata.is_file():
        raise PublishError("BROWSER_MISSING", "Install the paired Camoufox browser with python -m camoufox fetch.", 3)
    actual = json.loads(metadata.read_text(encoding="utf-8"))
    if not pin or (actual.get("version"), actual.get("build")) != (pin.version, pin.build):
        raise PublishError("BROWSER_VERSION_MISMATCH", "Browser build differs from the pinned Camoufox package's paired release.", 3)


@asynccontextmanager
async def browser_session(profile: Path, headless=False):
    from camoufox.fingerprints import generate_fingerprint
    from camoufox.async_api import AsyncCamoufox

    verify_browser_build()
    fingerprint_path = profile.parent / "fingerprint.json"
    if fingerprint_path.exists():
        fingerprint = json.loads(fingerprint_path.read_text(encoding="utf-8"))
    else:
        fingerprint = generate_fingerprint(os="macos" if sys.platform == "darwin" else "linux")
        with fingerprint_path.open("w", encoding="utf-8") as stream:
            fingerprint_path.chmod(0o600)
            json.dump(fingerprint, stream)
    mode = "virtual" if headless and sys.platform.startswith("linux") else headless
    # Third-party initialization/progress must not pollute machine-readable stdout.
    try:
        async with AsyncCamoufox(persistent_context=True, user_data_dir=str(profile), fingerprint=fingerprint,
                                headless=mode, humanize=1.0, locale="en-US", block_images=False,
                                main_world_eval=True,
                                i_know_what_im_doing=True) as context:
            yield context
    finally:
        for parent, dirs, files in os.walk(profile):
            Path(parent).chmod(0o700)
            for name in files:
                path = Path(parent) / name
                if not path.is_symlink():
                    path.chmod(0o600)


async def one(locator, description):
    count = await locator.count()
    if count != 1:
        raise PublishError("UI_CHANGED", f"Cannot uniquely locate {description}; found {count} controls.", 3)
    return locator


async def composer_text(editor):
    """Read DraftJS lines without Firefox's extra newline for empty blocks."""
    return await editor.evaluate(r"""element => {
        const blocks = [...element.querySelectorAll(':scope > [data-contents] > [data-block]')];
        if (blocks.length) return blocks.map(block => block.textContent).join('\n');
        const children = [...element.childNodes];
        if (children.length && children.every(node => node.nodeType === 1 && node.tagName === 'DIV'))
            return children.map(block => block.textContent).join('\n');
        return element.innerText;
    }""")


async def current_identity(context):
    page = await context.new_page()
    try:
        await page.goto("https://x.com/home", wait_until="domcontentloaded")
        profile = page.locator('header a[data-testid="AppTabBar_Profile_Link"]')
        await profile.wait_for(state="visible", timeout=30000)
        href = await (await one(profile, "authenticated Profile navigation")).get_attribute("href")
        match = re.fullmatch(r"/([A-Za-z0-9_]{1,15})", href or "")
        if not match:
            raise PublishError("LOGIN_REQUIRED", "Authenticated Profile navigation does not identify an X account.")
        handle = match.group(1).lower()
        switcher = page.locator('[data-testid="SideNav_AccountSwitcher_Button"]')
        text = await (await one(switcher, "authenticated account switcher")).inner_text()
        if re.findall(r"@([A-Za-z0-9_]{1,15})\b", text) != [match.group(1)]:
            raise PublishError("ACCOUNT_MISMATCH", "Profile navigation and account switcher identify different X accounts.")
        display_name = text.split("\n@")[0].strip()
        if not display_name:
            raise PublishError("LOGIN_REQUIRED", "Cannot verify X display name.")
        return handle, display_name
    except PublishError:
        raise
    except Exception:
        raise PublishError("LOGIN_REQUIRED", "Cannot verify authenticated X account; renew the session.") from None
    finally:
        await page.close()


async def current_handle(context):
    return (await current_identity(context))[0]


async def verify_account(context, expected):
    if await current_handle(context) != expected:
        raise PublishError("ACCOUNT_MISMATCH", "Authenticated X username differs from the bound account.")


async def verify_public(context):
    page = await context.new_page()
    try:
        await page.goto("https://x.com/settings/audience_and_tagging", wait_until="domcontentloaded")
        protected = page.get_by_role("checkbox", name="Protect your posts", exact=True)
        await protected.wait_for(state="visible", timeout=30000)
        if await (await one(protected, "Protect your posts setting")).is_checked():
            raise PublishError("PRIVATE_ACCOUNT", "X account has protected posts; public publication cannot proceed.")
    except PublishError:
        raise
    except Exception:
        raise PublishError("PUBLIC_VISIBILITY_UNCONFIRMED", "Cannot verify the account's Protect your posts setting.", 3) from None
    finally:
        await page.close()


def created_post(payload, expected_handle, expected_text):
    """Accept only the response to this composer action, with exact author/media/text."""
    try:
        tweet = payload["data"]["create_tweet"]["tweet_results"]["result"]
        if tweet.get("__typename") == "TweetWithVisibilityResults":
            tweet = tweet["tweet"]
        user = tweet["core"]["user_results"]["result"]
        handle = user.get("legacy", {}).get("screen_name") or user.get("core", {}).get("screen_name")
        media = tweet["legacy"]["extended_entities"]["media"]
        identifier = tweet["rest_id"]
        actual_text = tweet["legacy"]["full_text"]
        if len(media) == 1 and media[0].get("url"):
            suffix = media[0]["url"]
            media_only = {suffix, " " + suffix, "\n" + suffix} if not expected_text else set()
            if actual_text in {expected_text + " " + suffix, expected_text + "\n" + suffix} | media_only:
                actual_text = expected_text
        if (not re.fullmatch(r"\d+", identifier) or not isinstance(handle, str)
                or handle.lower() != expected_handle or actual_text != expected_text
                or len(media) != 1 or media[0]["type"] != "video"):
            raise ValueError()
        return f"https://x.com/{expected_handle}/status/{identifier}"
    except (KeyError, TypeError, ValueError, AttributeError):
        raise PublishError("SUBMISSION_UNCONFIRMED", "X's composer response did not confirm this exact account, video and text.", 4) from None


class XComposer:
    def __init__(self, context, diagnostics):
        self.context, self.diagnostics = context, diagnostics
        self.page = None
        self.editor = None
        self.post_url = None
        self.response_payload = None
        self.submission_evidence = {"phase": "not_started", "requests": []}

    def save_submission_evidence(self):
        self.diagnostics.mkdir(parents=True, exist_ok=True, mode=0o700)
        path = self.diagnostics / "submission.json"
        with path.open("w", encoding="utf-8") as stream:
            path.chmod(0o600)
            json.dump(self.submission_evidence, stream)

    async def open_editor(self, handle):
        await verify_account(self.context, handle)
        await verify_public(self.context)
        self.page = await self.context.new_page()
        await self.page.goto("https://x.com/compose/post", wait_until="domcontentloaded")
        dialogs = self.page.locator('[role="dialog"]:has([data-testid="tweetTextarea_0"]):not(:has([role="dialog"]))').filter(visible=True)
        await dialogs.first.wait_for(state="visible", timeout=30000)
        self.editor = await one(dialogs, "active composer dialog")
        await self.editor.locator('[data-testid="tweetTextarea_0"]').wait_for(state="visible", timeout=30000)

    async def prepare(self, video, caption, visibility):
        if visibility != "public":
            raise PublishError("INVALID_VISIBILITY", "Only public X posts are supported.")
        if await self.editor.locator('video,[data-testid="attachments"] img').count():
            raise PublishError("EXISTING_MEDIA", "Composer already contains media; clear that draft before preparing this request.", 3)
        editor = await one(self.editor.locator('[data-testid="tweetTextarea_0"]'), "post text")
        # Firefox DOM fill duplicates text in the live rich editor. Native key
        # events update its saved state once, including explicit blank lines.
        await editor.click()
        await editor.press("ControlOrMeta+A")
        await editor.press("Backspace")
        for index, line in enumerate(caption.split("\n")):
            if index:
                await editor.press("Enter")
            for segment in re.findall(r"[\x00-\x7f]+|[^\x00-\x7f]+", line):
                if segment.isascii():
                    await editor.press_sequentially(segment, delay=5)
                elif not await editor.evaluate('(element, text) => document.execCommand("insertText", false, text)', segment):
                    raise PublishError("CAPTION_INPUT_FAILED", "The composer rejected Unicode caption input.", 3)
        # Tab accepts/opens X's trailing hashtag suggestion popover, whose empty
        # backdrop can cover Post. Cancel completion and blur without Tab.
        if re.search(r"(?:^|\s)[#@][\w]+$", caption):
            await editor.press("Escape")
        await editor.evaluate('element => element.blur()')
        if await composer_text(editor) != caption:
            raise PublishError("CAPTION_MISMATCH", "Composer text differs from the validated caption before upload.", 3)
        upload = await one(self.editor.locator('input[data-testid="fileInput"]'), "video upload")
        await upload.set_input_files(str(video))
        await self.editor.locator('video').wait_for(state="visible", timeout=180000)
        button = await one(self.editor.locator('[data-testid="tweetButton"]'), "Post button")
        await expect(button).to_be_enabled(timeout=180000)

    async def verify(self, handle, caption, visibility):
        await verify_account(self.context, handle)
        await verify_public(self.context)
        editor = await one(self.editor.locator('[data-testid="tweetTextarea_0"]'), "post text")
        if await composer_text(editor) != caption:
            raise PublishError("CAPTION_MISMATCH", "Composer text differs from the validated caption.", 3)
        await one(self.editor.locator('video'), "attached video")
        button = await one(self.editor.locator('[data-testid="tweetButton"]'), "Post button")
        if not await button.is_enabled():
            raise PublishError("UPLOAD_UNREADY", "X has not enabled Post after video processing.", 3)
        await self.page.bring_to_front()
        # Keep the scrollable video dialog's visible sticky footer position.
        await button.evaluate('element => element.focus({preventScroll: true})')
        control = await button.evaluate("""element => {
            const box = element.getBoundingClientRect();
            const hit = document.elementFromPoint(box.x + box.width / 2, box.y + box.height / 2);
            return {focused: document.activeElement === element, width: box.width, height: box.height,
                x: box.x, y: box.y, exposed: hit === element || element.contains(hit),
                hitRole: hit?.getAttribute('role'), hitTestId: hit?.getAttribute('data-testid')};
        }""")
        self.submission_evidence["post_control"] = control
        self.save_submission_evidence()
        if not (control["focused"] and control["width"] > 0 and control["height"] > 0 and control["exposed"]):
            raise PublishError("POST_OBSTRUCTED", "The active composer Post button is not exposed and focused.", 3)
        await button.click(trial=True, timeout=30000)
        if await self.editor.get_by_text(re.compile(r"(failed to upload|could not be processed|unsupported|Something went wrong)", re.I)).count():
            raise PublishError("UPLOAD_FAILED", "X reports an upload or processing error.", 3)

    async def submit(self):
        # UI click only. The response is observed to causally bind the concrete URL.
        def observe_request(request):
            parsed = urlparse(request.url)
            if request.method == "POST" and parsed.hostname == "x.com" and parsed.path.startswith("/i/api/"):
                self.submission_evidence["requests"] = (self.submission_evidence["requests"] + [{"path": parsed.path}])[-50:]
                self.save_submission_evidence()
        self.page.on("request", observe_request)
        self.submission_evidence["phase"] = "click_started"
        self.save_submission_evidence()
        try:
            async with self.page.expect_response(lambda r: "/CreateTweet" in r.url and r.request.method == "POST", timeout=60000) as pending:
                # One native mouse click. X can dispatch two CreateTweet requests
                # for Enter activation, so keyboard submission is not used.
                button = await one(self.editor.locator('[data-testid="tweetButton"]'), "Post button")
                await button.click(no_wait_after=True, timeout=30000)
                self.submission_evidence["phase"] = "click_returned"
                self.save_submission_evidence()
            response = await pending.value
            self.submission_evidence.update(phase="response_received", status=response.status)
            self.save_submission_evidence()
        except Exception as exc:
            phase = self.submission_evidence["phase"]
            raise PublishError("SUBMISSION_UNCONFIRMED", f"X submission stopped during {phase}; inspect private submission.json and reconcile without retrying Post.", 4) from exc
        finally:
            self.page.remove_listener("request", observe_request)
        if response.status != 200:
            raise PublishError("SUBMISSION_UNCONFIRMED", "X did not acknowledge the composer submission.", 4)
        self.response_payload = await response.json()
        if isinstance(self.response_payload, dict):
            errors = self.response_payload.get("errors", [])
            self.submission_evidence["error_codes"] = [item.get("code") for item in errors if isinstance(item, dict) and isinstance(item.get("code"), (int, str))] if isinstance(errors, list) else []
            self.save_submission_evidence()

    async def confirm(self, handle, caption):
        self.post_url = created_post(self.response_payload, handle, caption)
        self.submission_evidence["post_url"] = self.post_url
        self.save_submission_evidence()
        await self.verify_post(self.post_url, handle, caption)
        return self.post_url

    async def verify_post(self, url, handle, caption, require_playback=False):
        match = POST_RE.fullmatch(url)
        if not match or match.group(1).lower() != handle:
            raise PublishError("INVALID_POST_URL", "Post URL does not identify this account.")
        page = await self.context.new_page()
        try:
            await page.goto(url, wait_until="domcontentloaded")
            article = page.locator('article').filter(has=page.locator(f'a[href="/{handle}/status/{match.group(2)}" i]'))
            await article.wait_for(state="visible", timeout=60000)
            await one(article, "the exact published post")
            standard_layout = await article.get_attribute("data-testid") == "tweet"
            # X's public, signed-out page has semantic article/author/permalink
            # elements but none of the authenticated app's data-testid fields.
            post_text = article.locator('[data-testid="tweetText"]' if standard_layout else 'div[dir="auto"].whitespace-pre-wrap')
            text_count = await post_text.count()
            if text_count > 1 or (caption and text_count != 1):
                raise PublishError("POST_MISMATCH", "Published post text differs from this request.", 4)
            actual_text = await post_text.inner_text() if text_count else ""
            if actual_text != caption:
                raise PublishError("POST_MISMATCH", "Published post text differs from this request.", 4)
            if standard_layout:
                author_matches = bool(await article.locator(f'[data-testid="User-Name"] a[href="/{handle}" i]').count())
            else:
                author_href = await article.locator('a[href]').first.get_attribute("href")
                author_matches = (author_href or "").lower() == f"/{handle}"
            if not author_matches:
                raise PublishError("POST_MISMATCH", "Published post author differs from this request.", 4)
            video = article.locator('video')
            await video.wait_for(state="visible", timeout=30000)
            if require_playback:
                await video.scroll_into_view_if_needed()
                # Do not await the play() promise: a stalled media load can keep
                # it pending forever. The bounded readiness loop owns the wait.
                await video.evaluate("v => { v.play().catch(() => {}); }")
                for _ in range(30):
                    if await video.evaluate("v => v.readyState >= 2 && v.duration > 0 && (!v.paused || v.currentTime > 0)"):
                        break
                    await asyncio.sleep(1)
                else:
                    raise PublishError("PUBLIC_PLAYBACK_UNCONFIRMED", "Guest Camoufox could not play this post's video.", 3)
        finally:
            await page.close()

    async def screenshot(self, filename="failure.png"):
        if not self.page:
            return None
        try:
            self.diagnostics.mkdir(parents=True, exist_ok=True, mode=0o700)
            self.diagnostics.chmod(0o700)
            path = self.diagnostics / filename
            await self.page.screenshot(path=str(path))
            path.chmod(0o600)
            return str(path)
        except Exception:
            return None

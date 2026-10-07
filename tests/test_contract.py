from contextlib import asynccontextmanager
from pathlib import Path
from types import SimpleNamespace
import json
import pytest

from x_automation.cli import main
from x_automation.errors import PublishError
from x_automation.service import publish
from x_automation.state import State, canonical


@pytest.fixture
def state(tmp_path):
    result = State(tmp_path / 'state')
    result.bind('brand', 'brand', 'Brand')
    return result


@pytest.fixture
def args(tmp_path):
    video = tmp_path / 'source.mp4'; video.write_bytes(b'video bytes')
    caption = tmp_path / 'caption.txt'; caption.write_text('Exact caption\n\n#word')
    return SimpleNamespace(account='brand', request_id='once', video=str(video),
                           caption_file=str(caption), metadata_file=None, visibility='public',
                           headless=True, timeout_seconds=10)


@asynccontextmanager
async def session(*_):
    yield object()


class Adapter:
    clicks = 0
    fail = None
    state = None
    post_url = None
    def __init__(self, *_): pass
    async def open_editor(self, *_):
        if self.fail == 'account': raise PublishError('ACCOUNT_MISMATCH', 'Account mismatch.')
    async def prepare(self, video, *_):
        assert Path(video).read_bytes() == b'video bytes'
    async def verify(self, *_): pass
    async def submit(self):
        assert self.state.get('brand', 'once')['status'] == 'submitting'
        type(self).clicks += 1
        if self.fail == 'submit': raise RuntimeError('connection closed')
    async def confirm(self, *_): return 'https://x.com/brand/status/123'
    async def screenshot(self): return None


@pytest.fixture(autouse=True)
def reset(monkeypatch):
    Adapter.clicks = 0; Adapter.fail = None
    monkeypatch.setattr('x_automation.service.probe_video', lambda *_: None)


@pytest.mark.asyncio
async def test_publish_cached_without_second_click(state, args):
    Adapter.state = state
    first, code = await publish(state, args, session, Adapter)
    assert code == 0 and first['status'] == 'published'
    second, code = await publish(state, args, session, Adapter)
    assert code == 0 and second == first and Adapter.clicks == 1
    Path(args.caption_file).write_text('changed at same path')
    with pytest.raises(PublishError, match='different arguments'):
        await publish(state, args, session, Adapter)
    assert Adapter.clicks == 1


@pytest.mark.asyncio
async def test_uncertain_is_not_retried(state, args):
    Adapter.state = state; Adapter.fail = 'submit'
    first, code = await publish(state, args, session, Adapter)
    assert code == 4 and first['status'] == 'uncertain'
    second, code = await publish(state, args, session, Adapter)
    assert code == 4 and second == first and Adapter.clicks == 1


@pytest.mark.asyncio
async def test_account_mismatch_before_click_and_changed_bytes_conflict(state, args):
    Adapter.state = state; Adapter.fail = 'account'
    first, code = await publish(state, args, session, Adapter)
    assert first['status'] == 'failed' and code == 2 and Adapter.clicks == 0
    previous = state.get('brand', 'once')
    Path(args.video).write_bytes(b'changed bytes')
    with pytest.raises(PublishError): await publish(state, args, session, Adapter)
    assert state.get('brand', 'once')['fingerprint'] == previous['fingerprint']
    assert Adapter.clicks == 0


def test_crash_recovery_and_monotonic_states(state):
    state.start('brand', 'once', {}, 'hash', {})
    state.finish('brand', 'once', 'submitting')
    assert state.recover(state.get('brand', 'once'))['status'] == 'uncertain'
    with pytest.raises(PublishError): state.start('brand', 'once', {}, 'hash', {})
    with pytest.raises(PublishError): state.finish('brand', 'once', 'failed')


def test_lock_and_identity_guards(state):
    with state.lock('brand'):
        with pytest.raises(PublishError):
            with state.lock('brand'): pass
    with pytest.raises(PublishError): state.bind('other', 'brand', 'Brand')
    with pytest.raises(PublishError): state.bind('brand', 'other', 'Other')
    assert state.accounts()[0]['display_name'] == 'Brand'


@pytest.mark.parametrize('argv', [[], ['publish'], ['login','--account','brand','--handle','caribbeanawesome','--headless','--state-dir','unused']])
def test_cli_one_json_error(argv, capsys, tmp_path, monkeypatch):
    monkeypatch.chdir(tmp_path)
    code = main(argv)
    lines = capsys.readouterr().out.splitlines()
    assert code == 2 and len(lines) == 1 and json.loads(lines[0])['status'] == 'failed'


def test_version_json(capsys):
    assert main(['--version']) == 0
    assert json.loads(capsys.readouterr().out)['version'] == '0.1.0'

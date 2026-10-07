"""Optional controlled browser smoke; all requests are fulfilled locally, no X login."""
import html
import json
import os
from contextlib import asynccontextmanager
from pathlib import Path
import subprocess
from types import SimpleNamespace
import pytest

from x_automation.browser import browser_session, composer_text
from x_automation.service import accept_ui, publish, verify_public_post
from x_automation.state import State


@pytest.mark.skipif(os.environ.get('XPUB_TEST_CAMOUFOX') != '1', reason='opt-in paired Camoufox smoke')
@pytest.mark.asyncio
async def test_draftjs_caption_lines_preserve_exact_text(tmp_path):
    profile = tmp_path / 'profile'; profile.mkdir()
    async with browser_session(profile, True) as context:
        page = await context.new_page()
        for lines in [['The “key”', '', 'Taíno #word'], ['', 'leading', ''], ['', ''], [''], ['twice', 'twice']]:
            blocks = ''.join('<div data-block="true"><span>' + html.escape(line) + '</span>' + ('<br data-text="true">' if not line else '') + '</div>' for line in lines)
            await page.set_content('<div contenteditable="true"><div data-contents="true">' + blocks + '</div></div>')
            assert await composer_text(page.locator('[contenteditable]')) == '\n'.join(lines)


@pytest.mark.skipif(os.environ.get('XPUB_TEST_CAMOUFOX') != '1', reason='opt-in paired Camoufox smoke')
@pytest.mark.asyncio
async def test_real_controls_and_single_causal_submission(tmp_path):
    video = tmp_path/'video.mp4'
    subprocess.run(['ffmpeg','-v','error','-f','lavfi','-i','color=c=blue:s=32x32:d=1',
                    '-an','-c:v','libx264','-pix_fmt','yuv420p','-y',str(video)],check=True)
    caption = 'The “key” traces to Taíno\n\n#word'; text = tmp_path/'caption.txt'; text.write_text(caption)
    state = State(tmp_path/'state'); state.bind('brand','brand','Brand')
    args = SimpleNamespace(account='brand', request_id='controlled-once', video=str(video),
                           caption_file=str(text), metadata_file=None, visibility='public',
                           headless=True, timeout_seconds=90)
    clicks = []
    protected = False
    existing_media = False
    obstructed = False
    navigation = '<header><a data-testid="AppTabBar_Profile_Link" href="/brand">Profile</a><div data-testid="SideNav_AccountSwitcher_Button">Brand<br>@brand</div></header>'
    response = {'data': {'create_tweet': {'tweet_results': {'result': {
        'rest_id': '123', 'core': {'user_results': {'result': {'legacy': {'screen_name': 'brand'}}}},
        'legacy': {'full_text': caption+' https://t.co/abc', 'extended_entities': {'media': [{'type': 'video', 'url': 'https://t.co/abc'}]}}
    }}}}}
    composer = navigation + '''<div data-testid="tweetTextarea_0" contenteditable="true">Background composer</div>
      <input data-testid="fileInput" type="file"><button data-testid="tweetButton" disabled>Background Post</button><video controls></video>
      <div role="dialog"><div role="dialog" id="composer"><div data-testid="tweetTextarea_0" contenteditable="true" style="white-space:pre-wrap"></div>
      <input data-testid="fileInput" type="file"><button data-testid="tweetButton" disabled>Post</button>
      </div></div><script>const editor=document.querySelector('#composer');editor.querySelector('input').onchange=()=>{const v=document.createElement('video');v.controls=true;v.style='width:64px;height:64px';v.src=URL.createObjectURL(editor.querySelector('input').files[0]);editor.append(v);editor.querySelector('button').disabled=false};editor.querySelector('button').onclick=()=>fetch('/i/api/graphql/control/CreateTweet',{method:'POST'});</script>'''
    post = navigation + '<article data-testid="tweet"><div data-testid="User-Name"><a href="/Brand">Brand</a></div><a href="/Brand/status/123"><time>now</time></a><div style="white-space:pre-wrap" data-testid="tweetText">'+html.escape(caption)+'</div><video src="/fixture.mp4" controls style="width:64px;height:64px"></video></article>'

    async def route(request):
        path = request.request.url.split('x.com')[-1]
        if path == '/home': body = navigation
        elif path == '/settings/audience_and_tagging': body = navigation+f'<label><input type="checkbox" {"checked" if protected else ""}>Protect your posts</label>'
        elif path == '/compose/post':
            body = composer.replace('<div role="dialog" id="composer">', '<div role="dialog" id="composer">' + ('<video controls></video>' if existing_media else ''))
            if obstructed:
                body = body.replace('editor.querySelector(\'button\').disabled=false', 'editor.querySelector(\'button\').disabled=false;const shield=document.createElement("div");shield.style="position:fixed;inset:0;z-index:9999;background:transparent";document.body.append(shield)')
        elif path in {'/brand/status/123', '/brand/status/124'}:
            body = post
            if '/guest-' in str(active_profile):
                body = body.replace('article data-testid="tweet"', 'article').replace('data-testid="User-Name"', '').replace('data-testid="tweetText"', 'dir="auto" class="whitespace-pre-wrap"').replace('<time>now</time>', 'now')
        elif path == '/fixture.mp4':
            await request.fulfill(body=video.read_bytes(),content_type='video/mp4'); return
        elif path == '/i/api/graphql/control/CreateTweet':
            clicks.append('post'); await request.fulfill(body=json.dumps(response),content_type='application/json'); return
        else:
            await request.fulfill(status=404,body='controlled fixture'); return
        await request.fulfill(body=body,content_type='text/html; charset=utf-8')

    active_profile = None

    @asynccontextmanager
    async def controlled(profile, headless):
        nonlocal active_profile
        active_profile = profile
        async with browser_session(profile, headless) as context:
            await context.route('**/*', route)
            yield context

    acceptance, code = await accept_ui(state,args,controlled)
    assert code == 0 and acceptance['submitted'] is False and clicks == []
    result, code = await publish(state,args,controlled)
    assert code == 0 and result['post_url'] == 'https://x.com/brand/status/123'
    again, code = await publish(state,args,controlled)
    assert again == result and clicks == ['post']
    guest, code = await verify_public_post(state,args,controlled)
    assert code == 0 and guest['public'] and guest['playable'] and clicks == ['post']
    protected = True; args.request_id = 'protected-request'
    failure, code = await publish(state,args,controlled)
    assert code == 2 and failure['error']['code'] == 'PRIVATE_ACCOUNT' and clicks == ['post']
    protected = False; obstructed = True; args.request_id = 'obstructed-post'
    failure, code = await publish(state,args,controlled)
    assert code == 3 and failure['error']['code'] == 'POST_OBSTRUCTED' and clicks == ['post']
    obstructed = False; existing_media = True; args.request_id = 'existing-draft'
    failure, code = await publish(state,args,controlled)
    assert code == 3 and failure['error']['code'] == 'EXISTING_MEDIA' and clicks == ['post']

    existing_media = False; args.request_id = 'video-only'; args.caption_file = None
    response['data']['create_tweet']['tweet_results']['result']['rest_id'] = '124'
    response['data']['create_tweet']['tweet_results']['result']['legacy']['full_text'] = 'https://t.co/abc'
    post = navigation + '<article data-testid="tweet"><div data-testid="User-Name"><a href="/Brand">Brand</a></div><a href="/Brand/status/124"><time>now</time></a><video src="/fixture.mp4" controls style="width:64px;height:64px"></video></article>'
    video_only, code = await publish(state,args,controlled)
    assert code == 0 and video_only['post_url'] == 'https://x.com/brand/status/124' and clicks == ['post', 'post']
    public_video_only, code = await verify_public_post(state,args,controlled)
    assert code == 0 and public_video_only['public'] and public_video_only['playable']

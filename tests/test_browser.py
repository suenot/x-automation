import copy
import pytest
from x_automation.browser import created_post
from x_automation.errors import PublishError


def payload(text='hello'):
    return {'data': {'create_tweet': {'tweet_results': {'result': {
        'rest_id': '123', 'core': {'user_results': {'result': {'legacy': {'screen_name': 'brand'}}}},
        'legacy': {'full_text': text, 'extended_entities': {'media': [{'type': 'video', 'url': 'https://t.co/abc'}]}}
    }}}}}


def test_response_causally_identifies_exact_post():
    assert created_post(payload('hello https://t.co/abc'), 'brand', 'hello') == 'https://x.com/brand/status/123'
    assert created_post(payload(), 'brand', 'hello') == 'https://x.com/brand/status/123'
    assert created_post(payload('https://t.co/abc'), 'brand', '') == 'https://x.com/brand/status/123'


@pytest.mark.parametrize('mutation', ['author', 'text', 'media', 'id', 'empty'])
def test_response_fails_closed(mutation):
    data = payload(); tweet = data['data']['create_tweet']['tweet_results']['result']
    if mutation == 'author': tweet['core']['user_results']['result']['legacy']['screen_name'] = 'other'
    if mutation == 'text': tweet['legacy']['full_text'] = 'different'
    if mutation == 'media': tweet['legacy']['extended_entities']['media'][0]['type'] = 'photo'
    if mutation == 'id': tweet['rest_id'] = 'not-a-post'
    if mutation == 'empty': data = {}
    with pytest.raises(PublishError) as failure: created_post(data, 'brand', 'hello')
    assert failure.value.exit_code == 4

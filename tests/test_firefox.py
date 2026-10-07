import sqlite3
import time
from pathlib import Path
import pytest
from x_automation.firefox_cookies import firefox_cookies
from x_automation.errors import PublishError


def test_import_snapshot_filters_scope_expiry_and_partitions(tmp_path):
    db = sqlite3.connect(tmp_path/'cookies.sqlite')
    db.execute('PRAGMA user_version=17')
    db.execute('CREATE TABLE moz_cookies (name,value,host,path,expiry,isSecure,isHttpOnly,sameSite,originAttributes)')
    rows = [
        ('auth_token','dummy','.x.com','/',int((time.time()+600)*1000),1,1,2,''),
        ('expired','dummy','.x.com','/',1,1,1,2,''),
        ('container','dummy','.x.com','/',int((time.time()+600)*1000),1,1,2,'^userContextId=1'),
        ('unrelated','dummy','.example.com','/',int((time.time()+600)*1000),1,1,2,''),
    ]
    db.executemany('INSERT INTO moz_cookies VALUES(?,?,?,?,?,?,?,?,?)', rows); db.commit(); db.close()
    cookies = firefox_cookies(tmp_path)
    assert [c['name'] for c in cookies] == ['auth_token']
    assert cookies[0]['httpOnly'] and cookies[0]['sameSite'] == 'Strict'
    assert cookies[0]['expires'] < time.time()+601


def test_missing_session_does_not_request_or_publish(tmp_path):
    with pytest.raises(PublishError) as failure: firefox_cookies(tmp_path)
    assert failure.value.code == 'FIREFOX_PROFILE_UNREADABLE'

#!/usr/bin/env python3
"""Encrypted recovery acceptance against a real disposable Go daemon.

Only fictional content. Delayed/lost responses originate at the actual daemon;
no fake API replies. Storage faults test honest failure, not a fake server.
"""
import argparse
import json
import os
from pathlib import Path
import re
import secrets
import subprocess
import tempfile
import time
import urllib.request


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', type=Path, required=True)
    parser.add_argument('--chromium', default='/usr/bin/chromium')
    parser.add_argument('--report', type=Path, required=True)
    args = parser.parse_args()
    args.report.parent.mkdir(parents=True, exist_ok=True)
    from playwright.sync_api import sync_playwright, expect
    checks, errors = [], []
    env = {k: v for k, v in os.environ.items() if not k.startswith('FOLIO_')}
    with tempfile.TemporaryDirectory(prefix='folio-recovery-') as tmp:
        root = Path(tmp); data = root / 'data'
        subprocess.run([str(args.binary.resolve()), 'init', '--data', str(data)],
                       env=env, capture_output=True, check=True, timeout=20)
        owner = (data / 'token').read_text().strip()
        draft, read, proposal = [secrets.token_hex(32) for _ in range(3)]
        env |= {'FOLIO_DRAFT_TOKEN': draft, 'FOLIO_READ_TOKEN': read, 'FOLIO_PROPOSAL_TOKEN': proposal}
        with (root / 'daemon.log').open('wb') as log:
            proc = subprocess.Popen([str(args.binary.resolve()), 'serve', '--data', str(data),
                                     '--addr', '127.0.0.1:0', '--pause-schedules'], env=env, stdout=log, stderr=log)
        try:
            for _ in range(200):
                assert proc.poll() is None
                match = re.search(r'http://127\.0\.0\.1:\d+', (root / 'daemon.log').read_text())
                if match: base = match.group(); break
                time.sleep(.025)
            else: raise AssertionError('daemon did not start')

            def call(operation, payload=None):
                request = urllib.request.Request(base + '/api/op/' + operation,
                    data=json.dumps(payload or {}).encode(),
                    headers={'Content-Type': 'application/json', 'Authorization': 'Bearer ' + owner})
                with urllib.request.urlopen(request, timeout=10) as response:
                    result = json.load(response); assert result['ok']; return result['data']

            def create(name):
                return call('posts.create', {'title': 'Fictional ' + name, 'slug': name,
                    'markdown': 'Server baseline ' + name, 'idempotency_key': name})['post']

            def done(name):
                checks.append(name); print('PASS ' + name, flush=True)

            with sync_playwright() as pw:
                browser = pw.chromium.launch(executable_path=args.chromium, headless=True)

                def page_in(context):
                    page = context.new_page(); page.on('pageerror', lambda e: errors.append(str(e)))
                    page.on('dialog', lambda d: d.accept()); return page

                def login(page, token=owner, path='/studio/new'):
                    page.goto(base + path); expect(page.locator('#login-form')).to_be_visible()
                    page.evaluate("window.FolioI18n.setLocale('en')")
                    page.locator('#access-token').fill(token)
                    page.locator('#login-form button[type="submit"]').click()
                    if path == '/studio/new': expect(page.locator('#post-markdown')).to_be_visible()
                    elif path == '/studio': expect(page.locator('.post-table')).to_be_visible()
                    else: expect(page.locator('#post-markdown')).to_be_visible()

                def ready(page):
                    expect(page.locator('#local-status')).to_contain_text(re.compile('Ready|已准备|Local copies off|本机副本已关闭|Local copy ready|本机副本已就绪'))
                    expect(page.locator('#local-clear')).to_be_enabled()

                def copied(page):
                    expect(page.locator('#local-status')).to_contain_text(re.compile('Local copy ready|本机副本已就绪'))

                def review(page, title=None):
                    expect(page.locator('#local-review')).to_be_enabled(); page.locator('#local-review').click()
                    item = page.locator('[data-local-copy]')
                    if title: item = item.filter(has_text=title)
                    item.first.click(); expect(page.locator('#local-restore')).to_be_visible()

                def clear(page):
                    page.locator('#local-clear').click(); expect(page.locator('#local-status')).to_contain_text(re.compile('off|关闭'))
                    page.locator('#local-mode').select_option('session'); ready(page)

                def assert_private(post_id):
                    record = call('posts.get', {'id': post_id}); assert record['live'] is None; return record

                def storage_plaintext(page):
                    return page.evaluate("JSON.stringify([Object.entries(sessionStorage).filter(([k])=>k.startsWith('folio.recovery.')),Object.entries(localStorage).filter(([k])=>k.startsWith('folio.recovery.'))])")

                ctx = browser.new_context(viewport={'width': 1280, 'height': 900}); page = page_in(ctx)
                login(page); ready(page)
                before = call('posts.list')
                page.locator('#post-title').fill('Fictional refresh title')
                page.locator('#post-slug').fill('custom-refresh-slug')
                text = '# Local only / 仅本机\n\n<script>window.badRecovery=true</script>\nUnfinished words.'
                page.locator('#post-markdown').fill(text); page.locator('#post-excerpt').fill('Local deck')
                page.locator('#post-category').fill('Local category'); page.locator('#post-tags').fill('alpha, 测试')
                page.locator('#post-featured').check(); copied(page)
                assert call('posts.list') == before
                for secret in [owner, 'Fictional refresh title', 'custom-refresh-slug', text, 'Local deck']:
                    assert secret not in storage_plaintext(page)
                page.reload(); expect(page.locator('#post-title')).to_have_value(''); review(page)
                expect(page.locator('.local-comparison')).to_contain_text('<script>window.badRecovery=true</script>')
                page.locator('[data-action="close-modal"]').last.click(); expect(page.locator('#post-title')).to_have_value('')
                assert call('posts.list') == before
                review(page); page.locator('#local-restore').click()
                expect(page.locator('#post-title')).to_have_value('Fictional refresh title')
                expect(page.locator('#post-slug')).to_have_value('custom-refresh-slug')
                expect(page.locator('#post-markdown')).to_have_value(text)
                expect(page.locator('#post-excerpt')).to_have_value('Local deck')
                expect(page.locator('#post-category')).to_have_value('Local category')
                expect(page.locator('#post-tags')).to_have_value('alpha, 测试'); expect(page.locator('#post-featured')).to_be_checked()
                assert not page.evaluate('!!window.badRecovery'); assert call('posts.list') == before
                with page.expect_response(lambda r: r.url.endswith('/api/op/posts.create')) as response:
                    page.locator('[data-action="save"]').click()
                saved = response.value.json()['data']['post']; assert_private(saved['id']); assert saved['markdown'] == text
                done('new draft refresh: all fields, escaped review, cancel, explicit restore and private Save')

                clear(page); baseline = call('posts.get', {'id': saved['id']})
                page.locator('#post-markdown').fill('Existing private recovered text'); copied(page); page.reload()
                expect(page.locator('#post-markdown')).to_have_value(text); review(page); page.locator('#local-restore').click()
                expect(page.locator('#post-markdown')).to_have_value('Existing private recovered text')
                assert call('posts.get', {'id': saved['id']}) == baseline
                done('existing article refresh leaves revision and live snapshot unchanged until Save')

                clear(page); page.locator('#local-mode').select_option('persistent'); copied(page)
                page.locator('#post-markdown').fill('Persistent closed-tab words'); copied(page); page.close()
                page = page_in(ctx); login(page, path='/studio/posts/' + saved['id']); ready(page); review(page)
                page.locator('#local-restore').click(); expect(page.locator('#post-markdown')).to_have_value('Persistent closed-tab words')
                done('explicit seven-day opt-in restores after closing and fresh same-token authentication')

                page.locator('[data-action="logout"]').first.click(); expect(page.locator('#login-form')).to_be_visible()
                assert 'Persistent closed-tab words' not in page.locator('body').inner_text()
                assert 'Persistent closed-tab words' not in storage_plaintext(page)
                for token in [read, proposal]:
                    login(page, token, '/studio/posts/' + saved['id']); assert page.locator('#local-status').count() == 0
                    assert 'Persistent closed-tab words' not in page.locator('body').inner_text()
                    page.locator('[data-action="logout"]').first.click()
                login(page, draft, '/studio/posts/' + saved['id']); ready(page); expect(page.locator('#local-review')).to_be_disabled()
                page.locator('[data-action="logout"]').first.click()
                login(page, path='/studio/posts/' + saved['id']); ready(page); review(page)
                page.locator('[data-action="close-modal"]').last.click()
                done('Lock/login and reader, proposal, draft-token isolation never expose owner recovery content')

                clear(page); page.locator('#post-markdown').fill('Tab-only lock words'); copied(page)
                page.locator('.studio-nav a[href="/studio"]').click(); expect(page.locator('.post-table')).to_be_visible()
                page.locator('[data-action="logout"]').first.click()
                login(page, path='/studio/posts/' + saved['id']); ready(page); expect(page.locator('#local-review')).to_be_disabled()
                done('Lock from the article list clears previous editor session copies')

                clear(page); page.locator('#post-markdown').fill('Offline editing words'); copied(page)
                ctx.set_offline(True); page.locator('#post-markdown').fill('Offline newest words'); copied(page)
                page.locator('[data-action="save"]').click(); expect(page.locator('#save-state')).to_contain_text('unconfirmed')
                copied(page); ctx.set_offline(False); page.reload(); review(page); page.locator('#local-restore').click()
                expect(page.locator('#post-markdown')).to_have_value('Offline newest words')
                assert call('posts.get', {'id': saved['id']}) == baseline
                with page.expect_response(lambda r: r.url.endswith('/api/op/posts.update')):
                    page.locator('[data-action="save"]').click()
                assert assert_private(saved['id'])['post']['markdown'] == 'Offline newest words'
                done('already-authenticated offline editing retains recovery without an acknowledged server Save')

                clear(page); page.locator('#post-markdown').fill('Before quota failure'); copied(page)
                page.evaluate("()=>{window.originalStorageSet=Storage.prototype.setItem; Storage.prototype.setItem=function(k,v){if(k.startsWith('folio.recovery.v1.'))throw new DOMException('full','QuotaExceededError');return window.originalStorageSet.call(this,k,v);};}")
                page.locator('#post-markdown').fill('After quota failure'); expect(page.locator('#local-error')).to_contain_text('full')
                assert 'Local copy ready' not in page.locator('#local-status').inner_text()
                with page.expect_response(lambda r: r.url.endswith('/api/op/posts.update')):
                    page.locator('[data-action="save"]').click()
                page.evaluate('()=>{Storage.prototype.setItem=window.originalStorageSet;}')
                assert assert_private(saved['id'])['post']['markdown'] == 'After quota failure'
                done('quota failure is readable and never blocks explicit server Save')
                ctx.close()

                # Separate persistent slots + explicit conflict review + CAS after review.
                post = create('parallel-copies'); ctx = browser.new_context(); a = page_in(ctx); b = page_in(ctx)
                login(a, path='/studio/posts/' + post['id']); ready(a)
                a.locator('#local-mode').select_option('persistent'); a.locator('#post-markdown').fill('First independent copy'); copied(a)
                login(b, path='/studio/posts/' + post['id']); ready(b)
                b.locator('#post-markdown').fill('Second independent copy'); copied(b)
                a.reload(); review(a, 'Fictional parallel-copies'); assert a.locator('#local-ack').count() == 0
                a.locator('[data-action="close-modal"]').last.click(); a.locator('#local-review').click()
                assert a.locator('[data-local-copy]').count() == 2
                assert a.locator('.local-copy-list').inner_text().count('Fictional parallel-copies') == 2
                a.locator('[data-local-copy]').last.click(); a.locator('#local-restore').click()
                expect(a.locator('#post-markdown')).to_have_value('First independent copy')
                with b.expect_response(lambda r: r.url.endswith('/api/op/posts.update')):
                    b.locator('[data-action="save"]').click()
                a.reload(); review(a); expect(a.locator('#local-ack')).to_be_visible()
                a.locator('#local-restore').click(); expect(a.locator('#local-review-error')).to_contain_text('acknowledge')
                a.locator('#local-ack').check(); a.locator('#local-restore').click()
                current = call('posts.get', {'id': post['id']})['post']
                call('posts.update', {**{k: current[k] for k in ['title','slug','markdown','excerpt','tags','category','cover','featured']},
                    'id': post['id'], 'expected_revision': current['revision'], 'markdown': 'Server change after review', 'idempotency_key': 'after-review'})
                with a.expect_response(lambda r: r.url.endswith('/api/op/posts.update')) as response:
                    a.locator('[data-action="save"]').click()
                assert response.value.status == 409
                expect(a.locator('#post-markdown')).to_have_value('First independent copy')
                assert assert_private(post['id'])['post']['markdown'] == 'Server change after review'
                done('parallel tab copies coexist; changed base requires acknowledgement; later CAS conflict preserves local text')
                b.locator('#post-markdown').fill('Typing before cross-tab clear'); copied(b)
                a.locator('#local-clear').click(); expect(b.locator('#local-mode')).to_have_value('off')
                b.locator('#post-markdown').fill('Typing after cross-tab clear'); b.wait_for_timeout(400)
                assert b.evaluate("Object.keys(localStorage).filter(k=>k.startsWith('folio.recovery.v1.')).length") == 0
                assert b.evaluate("Object.keys(sessionStorage).filter(k=>k.startsWith('folio.recovery.v1.')).length") == 0
                done('cross-tab clear invalidates pending copies and stops subsequent automatic writes')
                ctx.close()

                # A held real recovery read cannot outlive Clear, Lock or navigation.
                for destination in ['clear', 'lock', 'other']:
                    source, other = create('late-recovery-' + destination), create('late-other-' + destination)
                    ctx = browser.new_context(); page = page_in(ctx)
                    login(page, path='/studio/posts/' + source['id']); ready(page)
                    page.locator('#post-markdown').fill('Late local words ' + destination); copied(page); page.reload(); ready(page)
                    page.locator('#local-review').click(); held = []
                    def hold_read(route):
                        held.append((route, route.fetch()))
                    page.route('**/api/op/posts.get', hold_read)
                    page.locator('[data-local-copy]').first.click()
                    for _ in range(100):
                        if held: break
                        page.wait_for_timeout(10)
                    assert held, 'real recovery read was not held'
                    page.locator('[data-action="close-modal"]').last.click()
                    if destination == 'clear': page.locator('#local-clear').click()
                    elif destination == 'lock':
                        page.locator('[data-action="logout"]').first.click(); expect(page.locator('#login-form')).to_be_visible()
                    else:
                        page.route('**/api/op/posts.get', lambda r: r.continue_())
                        page.locator('.studio-nav a[href="/studio"]').click(); expect(page.locator('.post-table')).to_be_visible()
                        page.locator('a[href="/studio/posts/' + other['id'] + '"]').first.click()
                        expect(page.locator('#post-markdown')).to_have_value(other['markdown'])
                    route, response = held.pop(); route.fulfill(response=response); page.wait_for_timeout(200)
                    assert page.locator('#local-restore').count() == 0
                    assert assert_private(source['id'])['post'] == source
                    if destination == 'other': expect(page.locator('#post-markdown')).to_have_value(other['markdown'])
                    ctx.close()
                done('delayed actual recovery reads are invalidated by Clear, Lock and another article')

                # The daemon commits a create, but the actual response is deliberately lost.
                ctx = browser.new_context(); page = page_in(ctx); login(page); ready(page)
                page.locator('#post-title').fill('Fictional lost response'); page.locator('#post-slug').fill('lost-recovery-create')
                page.locator('#post-markdown').fill('Original committed text'); copied(page); requests = []
                def lose(route):
                    requests.append(json.loads(route.request.post_data)); result = route.fetch(); assert result.status == 200
                    route.abort('failed')
                page.route('**/api/op/posts.create', lose); page.locator('[data-action="save"]').click()
                expect(page.locator('#save-state')).to_contain_text('unconfirmed')
                page.locator('#post-markdown').fill('Newer text after unconfirmed create'); copied(page)
                committed = [p for p in call('posts.list')['posts'] if p['slug'] == 'lost-recovery-create']; assert len(committed) == 1
                page.unroute('**/api/op/posts.create'); page.reload(); review(page); page.locator('#local-restore').click()
                with page.expect_response(lambda r: r.url.endswith('/api/op/posts.create')) as response:
                    page.locator('[data-action="save"]').click()
                assert response.value.request.post_data_json == requests[0]
                expect(page.locator('#post-markdown')).to_have_value('Newer text after unconfirmed create')
                expect(page.locator('#save-state')).to_contain_text('Unsaved')
                with page.expect_response(lambda r: r.url.endswith('/api/op/posts.update')):
                    page.locator('[data-action="save"]').click()
                actual = [p for p in call('posts.list')['posts'] if p['slug'] == 'lost-recovery-create']; assert len(actual) == 1
                assert actual[0]['revision'] == 2; assert actual[0]['markdown'] == 'Newer text after unconfirmed create'; assert_private(actual[0]['id'])
                done('refresh after lost committed create reuses the exact retry identity, then saves newer text without duplication')

                # Original pending-create copy remains separate after successful recovery.
                current = actual[0]
                call('posts.update', {**{k: current[k] for k in ['title','slug','markdown','excerpt','tags','category','cover','featured']},
                    'id': current['id'], 'expected_revision': current['revision'], 'markdown': 'Server newer than receipt', 'idempotency_key': 'newer-than-receipt'})
                page.locator('.studio-nav a[href="/studio"]').click(); expect(page.locator('.post-table')).to_be_visible()
                page.locator('a[href="/studio/new"]').first.click(); ready(page); review(page)
                page.locator('#local-restore').click()
                with page.expect_response(lambda r: r.url.endswith('/api/op/posts.get')):
                    page.locator('[data-action="save"]').click()
                expect(page.locator('#save-state')).to_contain_text('Unsaved')
                expect(page.locator('#post-markdown')).to_have_value('Newer text after unconfirmed create')
                assert assert_private(current['id'])['post']['markdown'] == 'Server newer than receipt'
                with page.expect_response(lambda r: r.url.endswith('/api/op/posts.update')) as response:
                    page.locator('[data-action="save"]').click()
                assert response.value.request.post_data_json['expected_revision'] == 3
                assert assert_private(current['id'])['post']['revision'] == 4
                done('historical retry receipt refreshes the server head and never labels a divergent restored editor as saved')

                page.locator('.studio-nav a[href="/studio"]').click(); expect(page.locator('.post-table')).to_be_visible()
                page.locator('a[href="/studio/new"]').first.click(); ready(page)
                page.locator('#local-clear').click(); page.locator('#local-mode').select_option('session'); ready(page)
                page.locator('#post-title').fill('Fictional receipt check failure'); page.locator('#post-slug').fill('receipt-check-failure')
                page.locator('#post-markdown').fill('Original receipt-check text'); copied(page)
                page.route('**/api/op/posts.create', lose); page.locator('[data-action="save"]').click()
                expect(page.locator('#save-state')).to_contain_text('unconfirmed')
                page.locator('#post-markdown').fill('Newer receipt-check text'); copied(page)
                page.unroute('**/api/op/posts.create'); page.reload(); review(page); page.locator('#local-restore').click()
                def lose_check(route):
                    result = route.fetch(); assert result.status == 200; route.abort('failed')
                page.route('**/api/op/posts.get', lose_check); page.locator('[data-action="save"]').click()
                expect(page.locator('#save-state')).to_contain_text('Save acknowledged')
                copied(page); assert '/studio/posts/' in page.url
                page.unroute('**/api/op/posts.get'); page.reload(); review(page); page.locator('#local-restore').click()
                expect(page.locator('#post-markdown')).to_have_value('Newer receipt-check text')
                with page.expect_response(lambda r: r.url.endswith('/api/op/posts.update')):
                    page.locator('[data-action="save"]').click()
                recovered = [p for p in call('posts.list')['posts'] if p['slug'] == 'receipt-check-failure']
                assert len(recovered) == 1 and recovered[0]['revision'] == 2 and recovered[0]['markdown'] == 'Newer receipt-check text'
                assert_private(recovered[0]['id'])
                done('failed latest-head check retains the acknowledged ID and dirty text across another refresh without duplicate create')

                clear(page); page.locator('#post-markdown').fill('Mobile recovery text'); copied(page); page.reload()
                for width in [320, 390]:
                    page.set_viewport_size({'width': width, 'height': 844})
                    for locale in ['en', 'zh-CN']:
                        page.evaluate('(locale)=>window.FolioI18n.setLocale(locale)', locale)
                        review(page); assert page.evaluate('document.documentElement.scrollWidth<=innerWidth')
                        assert page.locator('#modal-title').bounding_box()['y'] >= 0
                        assert page.evaluate("document.querySelector('.modal').scrollTop") == 0
                        expect(page.locator('.local-comparison')).to_contain_text('Mobile recovery text')
                        page.screenshot(path=str(args.report.with_name('local-' + str(width) + '-' + locale + '.png')))
                        page.locator('[data-action="close-modal"]').last.click()
                done('English/Chinese recovery review remains usable without horizontal overflow at 320 and 390 pixels')
                ctx.close()

                ctx = browser.new_context(); ctx.add_init_script("Object.defineProperty(window,'sessionStorage',{get(){throw new DOMException('blocked','SecurityError');}}); Object.defineProperty(window,'localStorage',{get(){throw new DOMException('blocked','SecurityError');}});")
                page = page_in(ctx); login(page); expect(page.locator('#local-error')).to_contain_text('unavailable')
                page.locator('#post-title').fill('Fictional unavailable storage'); page.locator('#post-slug').fill('unavailable-storage')
                page.locator('#post-markdown').fill('Server save remains possible')
                with page.expect_response(lambda r: r.url.endswith('/api/op/posts.create')) as response:
                    page.locator('[data-action="save"]').click()
                assert_private(response.value.json()['data']['post']['id'])
                done('blocked browser storage offers honest memory-only login and working private server Save')
                ctx.close(); browser.close()
                assert not errors, 'browser errors: ' + repr(errors)
        finally:
            proc.terminate()
            try: proc.wait(timeout=10)
            except subprocess.TimeoutExpired: proc.kill(); proc.wait(timeout=5)
            args.report.parent.mkdir(parents=True, exist_ok=True)
            args.report.write_text(json.dumps({'checks': checks, 'browser_errors': errors, 'count': len(checks)}, indent=2)+'\n')


if __name__ == '__main__':
    main()

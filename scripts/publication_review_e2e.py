#!/usr/bin/env python3
"""Real delayed review responses cannot bind publication to a different editor."""
import argparse
import json
import os
from pathlib import Path
import re
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
    from playwright.sync_api import sync_playwright, expect
    results, errors = [], []
    env = {k: v for k, v in os.environ.items() if not k.startswith('FOLIO_')}
    binary = str(args.binary.resolve())
    with tempfile.TemporaryDirectory(prefix='folio-review-') as tmp:
        root = Path(tmp); data = root / 'data'
        subprocess.run([binary, 'init', '--data', str(data)], env=env,
                       capture_output=True, check=True, timeout=20)
        owner = (data / 'token').read_text().strip()
        with (root / 'daemon.log').open('wb') as log:
            proc = subprocess.Popen([binary, 'serve', '--data', str(data), '--addr', '127.0.0.1:0', '--pause-schedules'],
                                    env=env, stdout=log, stderr=log)
        try:
            for _ in range(200):
                assert proc.poll() is None, 'daemon exited during startup'
                match = re.search(r'http://127\.0\.0\.1:\d+', (root / 'daemon.log').read_text())
                if match: base = match.group(); break
                time.sleep(.025)
            else: raise AssertionError('daemon did not start')

            def call(operation, payload=None):
                request = urllib.request.Request(base + '/api/op/' + operation,
                    data=json.dumps(payload or {}, separators=(',', ':')).encode(),
                    headers={'Content-Type': 'application/json', 'Authorization': 'Bearer ' + owner})
                with urllib.request.urlopen(request, timeout=10) as response:
                    result = json.load(response); assert result['ok']
                    return result['data']

            def create(name):
                return call('posts.create', {'title': 'Fictional ' + name, 'slug': name,
                    'markdown': 'Private original ' + name, 'idempotency_key': name})['post']

            with sync_playwright() as pw:
                browser = pw.chromium.launch(executable_path=args.chromium, headless=True)
                for operation in ['posts.preview', 'posts.get']:
                    for destination in ['new', 'existing', 'lock', 'back-return']:
                        name = operation.replace('.', '-') + '-' + destination
                        a, b = create(name + '-a'), create(name + '-b')
                        before_a = call('posts.get', {'id': a['id']})
                        before_b = call('posts.get', {'id': b['id']})
                        context = browser.new_context(viewport={'width': 1280, 'height': 900})
                        page = context.new_page(); page.on('pageerror', lambda error: errors.append(str(error)))
                        page.on('dialog', lambda dialog: dialog.accept())
                        page.goto(base + '/studio'); expect(page.locator('#login-form')).to_be_visible()
                        page.locator('#access-token').fill(owner)
                        page.locator('#login-form button[type="submit"]').click()
                        expect(page.locator('.post-table')).to_be_visible()
                        page.locator('a[href="/studio/posts/' + a['id'] + '"]').first.click()
                        expect(page.locator('#post-markdown')).to_have_value(a['markdown'])
                        held = []
                        reads = []
                        page.on('request', lambda request: reads.append(request.url.rsplit('/', 1)[-1])
                                if request.url.startswith(base + '/api/op/') else None)
                        pattern = '**/api/op/' + operation

                        def defer_a(route):
                            if json.loads(route.request.post_data)['id'] == a['id'] and not held:
                                # Obtain the actual daemon response, then delay its delivery.
                                held.append((route, route.fetch()))
                            else: route.continue_()

                        page.route(pattern, defer_a)
                        page.locator('[data-action="publish"]').click()
                        for _ in range(100):
                            if held: break
                            page.wait_for_timeout(10)
                        assert len(held) == 1, 'review response was not deferred'
                        if destination == 'lock':
                            page.locator('[data-action="logout"]').first.click()
                            expect(page.locator('#login-form')).to_be_visible()
                        elif destination == 'back-return':
                            page.evaluate('history.back()')
                            expect(page.locator('.post-table')).to_be_visible()
                            page.locator('a[href="/studio/posts/' + a['id'] + '"]').first.click()
                            expect(page.locator('#post-markdown')).to_have_value(a['markdown'])
                        else:
                            page.locator('.studio-nav a[href="/studio"]').click()
                            expect(page.locator('.post-table')).to_be_visible()
                            target = '/studio/new' if destination == 'new' else '/studio/posts/' + b['id']
                            page.locator('a[href="' + target + '"]').first.click()
                            expect(page.locator('#post-markdown')).to_be_visible()
                            page.locator('#post-title').fill('New private target ' + name)
                            page.locator('#post-slug').fill(name + '-target-edited')
                            page.locator('#post-markdown').fill('Target-only unsaved words ' + name)
                        expected_url = page.url
                        route, response = held.pop()
                        with page.expect_response(lambda r: r.url.endswith('/api/op/' + operation)):
                            route.fulfill(response=response)
                        page.unroute(pattern)
                        page.wait_for_timeout(500)
                        stale_dialog = page.locator('#confirm-publish').count() > 0
                        # Complete the unsafe legacy flow to establish actual persisted
                        # corruption, rather than treating a late dialog alone as proof.
                        if stale_dialog:
                            with page.expect_response(lambda r: r.url.endswith('/api/op/posts.publish')):
                                page.locator('#confirm-publish').click()
                            page.wait_for_timeout(150)
                        saved = None; write_id = None; write_operation = None
                        if destination in ['new', 'existing']:
                            with page.expect_response(lambda r: r.url.endswith(('/api/op/posts.create', '/api/op/posts.update'))) as item:
                                page.locator('[data-action="save"]').click()
                            request = item.value.request
                            write_operation = request.url.rsplit('/', 1)[-1]
                            write_id = json.loads(request.post_data).get('id')
                            result = item.value.json(); assert result['ok'], 'target draft save failed'
                            saved = result['data']['post']
                        after_a = call('posts.get', {'id': a['id']})
                        after_b = call('posts.get', {'id': b['id']})
                        expected_id = b['id'] if destination == 'existing' else None
                        extra_preview = operation == 'posts.get' and 'posts.preview' in reads
                        passed = not stale_dialog and not extra_preview and after_a == before_a
                        if destination in ['new', 'existing']:
                            saved_record = call('posts.get', {'id': saved['id']})
                            passed = passed and write_id == expected_id and saved['id'] != a['id']
                            passed = passed and saved['markdown'] == 'Target-only unsaved words ' + name
                            passed = passed and saved_record['post'] == saved and saved_record['live'] is None
                            passed = passed and page.url == (base + '/studio/posts/' + saved['id'] if destination == 'new' else expected_url)
                            if destination == 'new': passed = passed and after_b == before_b
                            else: passed = passed and after_b['post']['id'] == b['id'] and after_b['post']['revision'] == 2
                        else:
                            passed = passed and after_b == before_b and page.url == expected_url
                            if destination == 'lock': passed = passed and page.locator('#login-form').count() == 1
                            else: passed = passed and page.locator('#post-markdown').input_value() == a['markdown']
                        result = {'window': operation, 'destination': destination, 'passed': passed,
                                  'stale_dialog': stale_dialog, 'source_id': a['id'], 'other_id': b['id'],
                                  'obsolete_preview_requested': extra_preview,
                                  'save_operation': write_operation, 'save_request_id': write_id,
                                  'saved_id': saved['id'] if saved else None,
                                  'source_markdown_after': after_a['post']['markdown'],
                                  'source_unchanged': after_a == before_a}
                        results.append(result); print(json.dumps(result), flush=True)
                        context.close()
                # A current unsaved editor still supports Publish's first-save
                # promotion to a persistent ID and the exact reviewed revision.
                context = browser.new_context()
                page = context.new_page(); page.on('pageerror', lambda error: errors.append(str(error)))
                page.goto(base + '/studio'); expect(page.locator('#login-form')).to_be_visible()
                page.locator('#access-token').fill(owner)
                page.locator('#login-form button[type="submit"]').click()
                expect(page.locator('.post-table')).to_be_visible()
                page.locator('a[href="/studio/new"]').click()
                expect(page.locator('#post-markdown')).to_be_visible()
                page.locator('#post-title').fill('Current unsaved review / 当前新稿')
                page.locator('#post-slug').fill('current-unsaved-review')
                page.locator('#post-markdown').fill('Only this reviewed new story becomes public.')
                page.locator('[data-action="publish"]').click()
                expect(page.locator('#confirm-publish')).to_be_visible()
                with page.expect_response(lambda r: r.url.endswith('/api/op/posts.publish')) as item:
                    page.locator('#confirm-publish').click()
                published = item.value.json()['data']['post']
                expect(page.locator('#post-markdown')).to_have_value(published['markdown'])
                record = call('posts.get', {'id': published['id']})
                passed = published['revision'] == 1 and record['live']['id'] == published['id']
                passed = passed and page.url == base + '/studio/posts/' + published['id']
                passed = passed and sum(p['slug'] == 'current-unsaved-review' for p in call('posts.list')['posts']) == 1
                result = {'window': 'first save before review', 'destination': 'same editor', 'passed': passed,
                          'save_operation': 'posts.create', 'saved_id': published['id']}
                results.append(result); print(json.dumps(result), flush=True)
                context.close()
                browser.close()
        finally:
            proc.terminate(); proc.wait(timeout=10)
        assert owner not in (root / 'daemon.log').read_text(), 'daemon logged a fixture credential'
    summary = {'cases': results, 'passed': sum(r['passed'] for r in results),
               'total': len(results), 'javascript_errors': errors, 'public_deployment': False}
    args.report.parent.mkdir(parents=True, exist_ok=True)
    args.report.write_text(json.dumps(summary, ensure_ascii=False, indent=2) + '\n')
    assert summary['passed'] == summary['total'] and not errors, 'delayed review violated editor/publication identity'


if __name__ == '__main__': main()

#!/usr/bin/env python3
"""Chromium acceptance against a real disposable daemon; only fictional content.

Requires Python Playwright and a local Chromium executable. No mock API replies,
external service, persistent user data, or public deployment is used.
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
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    from playwright.sync_api import sync_playwright, expect
    args.output.mkdir(parents=True, exist_ok=True)
    checks, errors = [], []
    binary = str(args.binary.resolve())
    env = {k: v for k, v in os.environ.items() if not k.startswith('FOLIO_')}
    with tempfile.TemporaryDirectory(prefix='folio-browser-') as tmp:
        root = Path(tmp); data = root / 'data'
        subprocess.run([binary, 'init', '--data', str(data), '--demo'],
                       env=env, capture_output=True, check=True, timeout=20)
        owner = (data / 'token').read_text().strip()
        proposal, draft, read = [secrets.token_hex(32) for _ in range(3)]
        env |= {'FOLIO_PROPOSAL_TOKEN': proposal, 'FOLIO_DRAFT_TOKEN': draft, 'FOLIO_READ_TOKEN': read}
        with (root / 'daemon.log').open('wb') as log:
            proc = subprocess.Popen([binary, 'serve', '--data', str(data), '--addr', '127.0.0.1:0', '--pause-schedules'],
                                    env=env, stdout=log, stderr=log)
        try:
            for _ in range(200):
                assert proc.poll() is None, 'daemon exited during startup'
                match = re.search(r'http://127\.0\.0\.1:\d+', (root / 'daemon.log').read_text())
                if match:
                    base = match.group(); break
                time.sleep(.025)
            else:
                raise AssertionError('daemon did not start')

            def call(operation, payload=None, token=owner):
                request = urllib.request.Request(base + '/api/op/' + operation,
                    data=json.dumps(payload or {}, separators=(',', ':')).encode(),
                    headers={'Content-Type': 'application/json', 'Authorization': 'Bearer ' + token})
                with urllib.request.urlopen(request, timeout=10) as response:
                    return json.load(response)['data']

            def public():
                with urllib.request.urlopen(base + '/api/public/site', timeout=10) as response:
                    return json.load(response)

            def completed(name):
                checks.append(name); print('PASS ' + name, flush=True)

            with sync_playwright() as pw:
                browser = pw.chromium.launch(executable_path=args.chromium, headless=True)
                context = browser.new_context(viewport={'width': 1280, 'height': 900}, timezone_id='Asia/Shanghai', accept_downloads=True)
                page = context.new_page()
                page.on('pageerror', lambda error: errors.append(str(error)))
                dialogs = []
                page.on('dialog', lambda dialog: (dialogs.append(dialog.type), dialog.accept()))

                def loaded(url, selector):
                    page.goto(base + url); expect(page.locator(selector)).to_be_visible()

                def no_overflow():
                    overflow = page.evaluate('document.documentElement.scrollWidth > innerWidth + 1')
                    if overflow:
                        page.screenshot(path=str(args.output / 'overflow.png'), full_page=True, animations='disabled')
                        offenders = page.evaluate('''() => [...document.querySelectorAll('body *')].map(el => ({tag:el.tagName,id:el.id,classes:el.className,right:el.getBoundingClientRect().right,width:el.getBoundingClientRect().width})).filter(el => el.right > innerWidth+1 && el.width > 0).slice(0,20)''')
                        raise AssertionError('page-level horizontal overflow at ' + str(page.viewport_size['width']) + ' on ' + page.url.split(base)[-1] + ': ' + json.dumps(offenders))

                def save(operation='posts.update'):
                    with page.expect_response(lambda r: r.url.endswith('/api/op/' + operation)) as response:
                        page.locator('[data-action="save"]').click()
                    result = response.value.json()
                    assert result['ok'], 'draft save rejected'
                    return result['data']['post']

                def download(selector, name):
                    original_url = page.url
                    with page.expect_download() as item:
                        page.locator(selector).click()
                    path = root / name; item.value.save_as(path)
                    assert page.url == original_url, 'download navigated away from the workspace'
                    return path

                loaded('/', '.site-header')
                assert page.locator('html').get_attribute('lang') == 'zh-CN'
                page.screenshot(path=str(args.output / 'home-desktop.png'), full_page=True, animations='disabled')
                page.locator('[data-locale-toggle]').click()
                assert page.locator('html').get_attribute('lang') == 'en'
                loaded('/studio', '#login-form')
                page.locator('#access-token').fill(owner)
                page.locator('#login-form button[type="submit"]').click()
                expect(page.locator('.post-table')).to_be_visible()
                page.locator('a[href="/studio/new"]').click()
                expect(page.locator('#post-markdown')).to_be_visible()
                page.locator('#post-title').fill('Cloud acceptance / 云端虚构验收')
                page.locator('#post-slug').fill('cloud-acceptance')
                original = 'A fictional acceptance story.\n\n中文段落，保留原文。\n\n' + ('Long editing line.\n' * 200)
                page.locator('#post-markdown').fill(original)
                post = save('posts.create'); post_id = post['id']
                assert post['revision'] == 1 and not call('posts.get', {'id': post_id})['live']
                assert all(p['id'] != post_id for p in public()['posts'])
                completed('bilingual reader/login and real private first draft with revision history')

                submitted = original + 'Submitted save.\n'
                newest = submitted + 'Newer typing while the response is pending.\n'
                page.locator('#post-markdown').fill(submitted)
                held = []
                page.route('**/api/op/posts.update', lambda route: held.append(route))
                page.locator('[data-action="save"]').click()
                page.wait_for_timeout(150)
                assert len(held) == 1, 'save was not held in flight'
                page.locator('#post-markdown').fill(newest)
                page.locator('#post-markdown').evaluate('(el) => {el.focus();el.setSelectionRange(12,18);el.scrollTop=250;}')
                selected = page.locator('#post-markdown').evaluate('(el) => [el.selectionStart,el.selectionEnd,el.scrollTop]')
                editor_url = page.url
                page.locator('[data-action="logout"]').first.click()
                assert page.evaluate('Boolean(sessionStorage.getItem("folio.token"))'), 'locking during save erased the active session'
                page.evaluate('history.back()'); page.wait_for_timeout(150)
                assert page.url == editor_url, 'Back during save left the editor URL'
                assert not dialogs, 'in-flight save offered to discard editor state'
                page.locator('[data-locale-toggle]').click()
                assert page.locator('#post-markdown').input_value() == newest, 'pending editor text changed'
                assert page.locator('#post-markdown').evaluate('(el) => [el.selectionStart,el.selectionEnd,el.scrollTop]') == selected
                with page.expect_response(lambda r: r.url.endswith('/api/op/posts.update')):
                    held.pop().continue_()
                page.unroute('**/api/op/posts.update')
                expect(page.locator('#save-state')).to_have_text('有未保存的修改')
                assert page.locator('#post-markdown').input_value() == newest
                post = save()
                assert post['revision'] == 3 and post['markdown'] == newest
                assert len(call('posts.get', {'id': post_id})['revisions']) == 3
                completed('real deferred save: Lock, Back, locale, caret and newer typing preserved; one subsequent revision')

                page.locator('[data-action="publish"]').click()
                expect(page.locator('#confirm-publish')).to_be_visible()
                publishing = []
                page.route('**/api/op/posts.publish', lambda route: publishing.append(route))
                page.locator('#confirm-publish').click();page.wait_for_timeout(150)
                assert len(publishing) == 1
                lost_route = publishing.pop()
                first_publish_body = lost_route.request.post_data
                committed_response = lost_route.fetch()
                assert committed_response.ok, 'real publication did not commit before simulated response loss'
                lost_route.abort()
                expect(page.locator('#confirm-publish')).to_be_enabled()
                page.locator('#confirm-publish').click();page.wait_for_timeout(150)
                assert len(publishing) == 1 and publishing[0].request.post_data == first_publish_body, 'publish retry changed its exact payload/key'
                page.locator('#modal-root [data-action="close-modal"]').first.click()
                while_publishing = newest + 'New typing after closing the pending publish dialog.'
                page.locator('#post-markdown').fill(while_publishing)
                with page.expect_response(lambda r: r.url.endswith('/api/op/posts.publish')):
                    publishing.pop().continue_()
                page.unroute('**/api/op/posts.publish')
                page.wait_for_timeout(200)
                assert page.locator('#post-markdown').input_value() == while_publishing, 'publication completion discarded newer typing'
                expect(page.locator('#save-state')).to_have_text('有未保存的修改')
                assert call('posts.get', {'id': post_id})['live']['markdown'] == newest
                assert sum(e['operation'] == 'posts.publish' and e['target'] == post_id for e in call('audit.list')['events']) == 1
                completed('lost publication response retries the exact key once; closing its dialog preserves newer typing and the reviewed public snapshot')
                page.locator('#post-markdown').fill('Later private draft, never leak this text.')
                post = save()
                live = next(p for p in public()['posts'] if p['id'] == post_id)
                assert live['markdown'] == newest and post['revision'] == 4
                completed('explicit browser publication followed by private draft/live isolation')

                page.locator('[data-action="schedule"]').click()
                expect(page.locator('#schedule-approved')).to_be_visible()
                page.locator('#schedule-approved').check()
                with page.expect_response(lambda r: r.url.endswith('/api/op/schedules.create')) as response:
                    page.locator('#schedule-form button[type="submit"]').click()
                schedule = response.value.json()['data']['schedule']
                assert schedule['post_revision'] == 4 and schedule['publish_at'].endswith('Z')
                loaded('/studio/posts/' + post_id, '#post-markdown')
                page.locator('#post-markdown').fill('Newer private revision after scheduling.')
                post = save()
                assert post['revision'] == 5
                assert call('schedules.get', {'id': schedule['id']})['schedule']['snapshot']['markdown'] == 'Later private draft, never leak this text.'
                completed('browser schedule confirmation pins revision/time despite later edits')

                candidate = {k: post[k] for k in ['title','slug','markdown','excerpt','tags','category','cover','featured']}
                candidate['markdown'] = '<script>window.reviewExecuted=true</script>\n\nA proposal-only agent suggestion / 私有提案。'
                proposal_result = call('proposals.create', {'post_id': post_id, 'base_revision': 5,
                    'candidate': candidate, 'idempotency_key': 'browser-proposal'}, proposal)['proposal']
                page.set_viewport_size({'width': 390, 'height': 844})
                loaded('/studio/proposals/' + proposal_result['id'], '[data-proposal-decision="approve"]')
                assert not page.evaluate('Boolean(window.reviewExecuted)'), 'proposal text executed'
                no_overflow()
                page.screenshot(path=str(args.output / 'proposal-mobile.png'), full_page=True, animations='disabled')
                page.locator('[data-proposal-decision="approve"]').click()
                no_overflow()
                page.locator('#workflow-confirm').click()
                expect(page.locator('[data-proposal-decision="approve"]')).to_have_count(0)
                reviewed = call('posts.get', {'id': post_id})
                assert reviewed['post']['revision'] == 6 and reviewed['live']['revision'] == 3
                assert reviewed['post']['markdown'] == candidate['markdown']
                completed('390px owner review approves proposal-only agent text into a private draft; untrusted diff remains inert')

                loaded('/studio/posts/' + post_id, '#post-markdown')
                markdown_file = download('[data-action="export-markdown"]', 'draft.md')
                assert markdown_file.read_text() == candidate['markdown']
                no_overflow(); page.screenshot(path=str(args.output / 'editor-mobile.png'), full_page=True, animations='disabled')
                loaded('/studio/tools', '[data-action="export"]')
                backup_file = download('[data-action="export"]', 'backup.json')
                downloaded_backup = json.loads(backup_file.read_text())
                expected_backup = call('backup.export')
                assert downloaded_backup['state'] == expected_backup['state']
                assert downloaded_backup['sha256'] == expected_backup['sha256']
                loaded('/studio/migration', '#migration-files')
                source = b'---\ntitle: Fictional browser import\nslug: fictional-browser-import\ndraft: false\n---\n\nImported words remain private.\n'
                page.locator('#migration-files').set_input_files({'name': 'fictional.md', 'mimeType': 'text/markdown', 'buffer': source})
                page.locator('[data-action="migration-plan"]').click()
                expect(page.locator('[data-action="migration-download-plan"]')).to_be_visible()
                frozen_file = download('[data-action="migration-download-plan"]', 'plan.json')
                frozen = json.loads(frozen_file.read_text())
                assert frozen['plan']['entries'][0]['document']['slug'] == 'fictional-browser-import'
                page.locator('[data-action="migration-apply"]').click();no_overflow()
                page.locator('#workflow-confirm').click()
                expect(page.locator('[data-action="migration-download-report"]')).to_be_visible()
                report_file = download('[data-action="migration-download-report"]', 'report.json')
                assert json.loads(report_file.read_text())['complete']
                bundle_file = download('[data-action="migration-export"]', 'bundle.json')
                assert json.loads(bundle_file.read_text())['files']
                imported = next(p for p in call('posts.list')['posts'] if p['slug'] == 'fictional-browser-import')
                assert imported['status'] == 'draft' and not call('posts.get', {'id': imported['id']})['live']
                completed('all five browser-native downloads have actual matching files; reviewed Markdown import stays private')

                for width in [390, 320]:
                    page.set_viewport_size({'width': width, 'height': 844})
                    for url, selector in [('/', '.site-header'), ('/posts/cloud-acceptance', '.article-shell'),
                                          ('/studio/posts/' + post_id, '#post-markdown'),
                                          ('/studio/schedules', '.schedule-list'), ('/studio/migration', '.migration-export')]:
                        loaded(url, selector); no_overflow()
                assert not errors, 'browser JavaScript error detected'
                completed('Chinese public/article/editor/schedules/migration surfaces at 390px and 320px without page overflow or JavaScript errors')
                context.close();browser.close()
        finally:
            proc.terminate();proc.wait(timeout=10)
        log_text = (root / 'daemon.log').read_text()
        assert all(t not in log_text for t in [owner, proposal, draft, read]), 'daemon logged a fixture credential'
    summary = {'checks': checks, 'passed': len(checks), 'javascript_errors': errors,
               'fixture': 'disposable fictional demo and acceptance content', 'public_deployment': False}
    (args.output / 'browser-results.json').write_text(json.dumps(summary, ensure_ascii=False, indent=2) + '\n')
    print(json.dumps(summary, ensure_ascii=False, indent=2))


if __name__ == '__main__':
    main()

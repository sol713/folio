#!/usr/bin/env python3
"""Saved-story search against a real daemon, CLI, MCP and Chromium; disposable data."""
import argparse
import hashlib
import json
from pathlib import Path
import secrets
import tempfile
import urllib.error
import urllib.request
from e2e import Harness, MCP, require

FIELDS = ['title', 'slug', 'markdown', 'excerpt', 'tags', 'category', 'cover', 'featured']


def review_regressions(args, h, server, call, create, result, done, kind='all'):
    """Real source-coordinate, navigation-owner and Unicode query boundary probes."""
    from playwright.sync_api import sync_playwright, expect
    observations = result.setdefault('review_cases', [])
    failures = []
    base = server['url']
    with sync_playwright() as pw:
        browser = pw.chromium.launch(executable_path=args.chromium, headless=True)

        def page():
            ctx = browser.new_context()
            p = ctx.new_page()
            errors, writes = [], []
            p.on('pageerror', lambda e: errors.append(str(e)))
            p.on('request', lambda req: writes.append(req.url.rsplit('/', 1)[-1]) if req.url.rsplit('/', 1)[-1] in ['posts.create', 'posts.update', 'posts.restore', 'posts.publish', 'schedules.create'] else None)
            p.goto(base + '/studio')
            expect(p.locator('#login-form')).to_be_visible()
            p.evaluate("window.FolioI18n.setLocale('en')")
            p.locator('#access-token').fill(server['token'])
            p.locator('#login-form button[type="submit"]').click()
            expect(p.locator('.post-table')).to_be_visible()
            return ctx, p, errors, writes

        def settle(p):
            p.evaluate('()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r)))')

        def snapshot(p):
            return p.locator('#post-markdown').evaluate('(e)=>({value:e.value,start:e.selectionStart,end:e.selectionEnd})')

        def record(case, passed, **data):
            observations.append({'case':case, 'passed':passed, **data})
            if not passed:
                failures.append(case)

        def normalized(text):
            return text.replace('\r\n', '\n').replace('\r', '\n')

        def utf16(text):
            return len(text.encode('utf-16-le')) // 2

        if kind in ['all', 'newlines']:
            samples = [('crlf-api', 'first\r\n雨声', '雨声', 'markdown', False),
                       ('repeated-api', 'a\r\n声声', '声', 'markdown', False),
                       ('leading-lf-api', '\n雨声', '雨声', 'markdown', False),
                       ('cr-api', 'first\r雨声', '雨声', 'markdown', False),
                       ('emoji-crlf-cli', '🐾\r\n雨声🐾', '雨声', 'markdown', True),
                       ('emoji-lf-cli', '🐾\n雨声🐾', '雨声', 'markdown', True),
                       ('mixed-api', '\n🐾\r\nfirst\r声声\r\n雨声🐾', '雨声', 'markdown', False),
                       ('mixed-crlf-cli', 'first\r\n🐾\r\n雨声🐾', '雨声', 'markdown', True),
                       ('excerpt-leading-lf-api', '\n雨声', '雨声', 'excerpt', False),
                       ('title-crlf-api', 'first\r\n雨声', '雨声', 'title', False)]
            for name, source, query, field, cli in samples:
                payload = {'title':'Fictional '+name, 'slug':'review-'+name, 'markdown':'Fictional unrelated body', field:source}
                post = (h.call(server, 'posts.create', payload)['data'] if cli else call('posts.create', payload))['post']
                require(post[field] == source, 'saved source changed before probe '+name)
                before = call('backup.export')['state']
                r = call('posts.search', {'query':query, 'limit':50})
                hit = next(p for p in r['results'] if p['id'] == post['id'])
                match = next(m for m in hit['matches'] if m['field'] == field)
                raw_start = source.index(query)
                raw_end = raw_start + len(query)
                require((match['start_rune'], match['end_rune'], match['text']) == (raw_start,raw_end,query), 'raw API range drift '+name)
                expected = [utf16(normalized(source[:raw_start])), utf16(normalized(source[:raw_end]))]
                ctx,p,errors,writes = page()
                p.locator('#studio-search').fill(query)
                link = p.locator('[data-story-hit="'+post['id']+'"] .story-match').filter(has=p.locator('.story-match-label', has_text={'title':'Title','excerpt':'Summary','markdown':'Markdown'}[field])).first
                expect(link).to_be_visible()
                link.click()
                expect(p.locator('#post-markdown')).to_be_visible()
                settle(p)
                actual = p.locator('#post-'+field).evaluate('(e)=>({value:e.value,start:e.selectionStart,end:e.selectionEnd,selected:e.value.slice(e.selectionStart,e.selectionEnd)})')
                notice = p.locator('#story-search-location').text_content() if p.locator('#story-search-location').count() else ''
                unchanged = call('backup.export')['state'] == before
                saved = p.locator('#save-state').text_content() == 'All changes saved'
                passed = actual['value']==normalized(source) and [actual['start'],actual['end']]==expected and actual['selected']==normalized(query) and notice.startswith('Located ') and match['line']==normalized(source[:raw_start]).count('\n')+1 and saved and unchanged and not errors and not writes
                record(name,passed,field=field,via='CLI' if cli else 'API',raw_range=[raw_start,raw_end],expected_utf16_range=expected,actual=actual,notice=notice,source_line=match['line'],saved=saved,persisted_state_unchanged=unchanged,browser_errors=errors,writes=writes)
                ctx.close()
            article = create('review-same-revision-buffer', markdown='雨声 Fictional initial source')
            for dirty in [False,True]:
                before=call('backup.export')['state']
                ctx,p,errors,writes=page()
                p.locator('#studio-search').fill('雨声')
                link=p.locator('[data-story-hit="'+article['id']+'"] .story-match')
                expect(link).to_be_visible()
                text='雨声 Fictional subsequent buffer'
                p.evaluate('''({text,dirty})=>{window.reviewInputObserved=false;const observer=new MutationObserver(()=>{const el=document.querySelector('#post-markdown');if(!el)return;observer.disconnect();window.reviewInputObserved=true;el.value=text;el.setSelectionRange(3,11);if(dirty)el.dispatchEvent(new InputEvent('input',{bubbles:true}));});observer.observe(document.querySelector('#app'),{childList:true,subtree:true});}''', {'text':text,'dirty':dirty})
                link.click();expect(p.locator('#post-markdown')).to_be_visible();settle(p)
                require(p.evaluate('window.reviewInputObserved'),'native DOM/input probe did not run')
                actual=snapshot(p)
                notice=p.locator('#story-search-location').text_content() if p.locator('#story-search-location').count() else ''
                passed=actual=={'value':text,'start':3,'end':11} and not notice.startswith('Located ') and p.locator('#save-state').text_content()==('Unsaved changes' if dirty else 'All changes saved') and call('backup.export')['state']==before and not errors and not writes
                record('same-revision-'+('dirty-input' if dirty else 'whole-DOM-mismatch'),passed,actual=actual,notice=notice,browser_errors=errors,writes=writes,persisted_state_unchanged=call('backup.export')['state']==before)
                ctx.close()
            if not failures:
                done('ten raw newline/Unicode fixtures plus two same-revision text/input guards: exact first-match UTF16 range, leading LF, CR/CRLF, emoji and title/summary; bytes/buffer/caret preserved')

        if kind in ['all', 'back']:
            article = create('review-back', title='Fictional previously open A', markdown='Fictional BACK_MATCH source')
            for destination in ['list', 'new-dirty', 'reopen-same']:
                before = call('backup.export')['state']
                ctx,p,errors,writes = page()
                p.locator('a[href="/studio/posts/'+article['id']+'"]').first.click()
                expect(p.locator('#post-markdown')).to_have_value(article['markdown'])
                p.locator('.studio-nav a[href="/studio"]').evaluate('(e)=>e.click()')
                expect(p.locator('#studio-search')).to_be_visible()
                p.locator('#studio-search').fill('BACK_MATCH')
                expect(p.locator('[data-story-hit="'+article['id']+'"] .story-match')).to_be_visible()
                held = []
                captured = [False]
                def hold_editor(route):
                    if captured[0]:
                        route.continue_()
                    else:
                        captured[0] = True
                        held.append((route,route.fetch()))
                p.route('**/api/op/posts.get',hold_editor)
                p.locator('[data-story-hit="'+article['id']+'"] .story-match').click()
                for _ in range(400):
                    if held:
                        break
                    p.wait_for_timeout(20)
                require(len(held)==1 and held[0][1].status==200,'actual successful editor response was not held')
                p.evaluate('history.back()')
                expect(p.locator('#studio-search')).to_be_visible()
                expect(p.locator('[data-story-hit="'+article['id']+'"] .story-match')).to_be_visible()
                settle(p)
                errors_before_release = list(errors)
                if destination=='new-dirty':
                    p.locator('a[href="/studio/new"]').first.click()
                    expect(p.locator('#post-markdown')).to_be_visible()
                    p.locator('#post-markdown').fill('Fictional subsequent unsaved buffer')
                    p.locator('#post-markdown').evaluate('(e)=>e.setSelectionRange(3,11)')
                elif destination=='reopen-same':
                    p.locator('#studio-search-clear').click()
                    p.locator('a[href="/studio/posts/'+article['id']+'"]').first.click()
                    expect(p.locator('#post-markdown')).to_have_value(article['markdown'])
                    p.locator('#post-markdown').evaluate('(e)=>e.setSelectionRange(2,5)')
                route_before = p.url
                snapshot_before = snapshot(p) if p.locator('#post-markdown').count() else p.locator('#post-rows').inner_html()
                with p.expect_response(lambda r:r.url.endswith('/api/op/posts.get')) as event:
                    route,response=held.pop();route.fulfill(response=response)
                event.value.body();settle(p)
                p.unroute('**/api/op/posts.get')
                snapshot_after = snapshot(p) if p.locator('#post-markdown').count() else p.locator('#post-rows').inner_html()
                unchanged = call('backup.export')['state']==before
                passed=p.url==route_before and snapshot_after==snapshot_before and not errors and not writes and unchanged and not p.locator('#story-search-location').count()
                if destination=='list':
                    passed=passed and p.url==base+'/studio' and not p.locator('#post-markdown').count()
                if destination=='new-dirty':
                    passed=passed and p.locator('#save-state').text_content()=='Unsaved changes'
                record('held-editor-back-'+destination,passed,route_unchanged=p.url==route_before,buffer_or_rows_unchanged=snapshot_after==snapshot_before,browser_errors_before_release=errors_before_release,browser_errors=errors,writes=writes,persisted_state_unchanged=unchanged)
                ctx.close()
            if not failures:
                done('three held real posts.get after previously open A/search/Back: list, dirty new editor and same-article reopen retain route/content/caret; zero pageerrors or writes')

        if kind in ['all', 'whitespace']:
            spaces=[9,10,11,12,13,32,0x85,0xa0,0x1680,*range(0x2000,0x200b),0x2028,0x2029,0x202f,0x205f,0x3000]
            for code in spaces+[0x200b,0xfeff]:
                char=chr(code);raw=char+'雨声'+char
                wanted='雨声' if code in spaces else raw
                article=create('review-space-'+format(code,'x'),markdown='Fictional '+wanted+' source')
                r=call('posts.search',{'query':raw,'limit':50})
                require(r['query']==wanted,'Go Unicode whitespace contract drift')
                before=call('backup.export')['state']
                ctx,p,errors,writes=page()
                with p.expect_response(lambda r:r.url.endswith('/api/op/posts.search')) as event:
                    p.locator('#studio-search').fill(raw)
                response=event.value.json();settle(p)
                hit=p.locator('[data-story-hit="'+article['id']+'"]')
                marks=hit.locator('mark').all_text_contents() if hit.count() else []
                passed=response['ok'] and response['data']['query']==wanted and wanted in marks and not p.locator('#story-search-retry').count() and not errors and not writes and call('backup.export')['state']==before
                record('query-U+'+format(code,'04X'),passed,canonical_query=wanted,browser_response_query=response.get('data',{}).get('query'),marked_text=marks,browser_errors=errors,writes=writes)
                ctx.close()
            if not failures:
                done('all 25 Go Unicode White_Space boundaries agree with Studio; U+200B/U+FEFF remain literal; response identity checks and state isolation retained')
        browser.close()
    require(not failures,'review regressions failed: '+', '.join(failures))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', type=Path, required=True)
    parser.add_argument('--chromium')
    parser.add_argument('--report', type=Path, required=True)
    parser.add_argument('--baseline', action='store_true', help='Run the same body-search product assertion on the prior build')
    parser.add_argument('--review-case', choices=['newlines','back','whitespace'], help='Run one independently reproducible review probe against an exact prior or candidate build')
    args = parser.parse_args()
    checks, cases, errors = [], [], []
    result = {'binary_sha256': hashlib.sha256(args.binary.read_bytes()).hexdigest(), 'baseline': args.baseline,
              'checks': checks, 'browser_cases': cases, 'browser_errors': errors}

    def done(name):
        checks.append(name)
        print('PASS ' + name, flush=True)

    def report():
        args.report.parent.mkdir(parents=True, exist_ok=True)
        args.report.write_text(json.dumps(result, ensure_ascii=False, indent=2) + '\n')

    try:
        with tempfile.TemporaryDirectory(prefix='folio-story-search-') as tmp:
            h = Harness(args.binary.resolve(), Path(tmp))
            proposal = secrets.token_hex(32)
            h.base_env['FOLIO_PROPOSAL_TOKEN'] = proposal
            h.secrets.append(proposal)
            try:
                server = h.start('instance')
                base = server['url']

                def http(op, payload=None, token=None, status=200, locale='en', origin=None):
                    headers = {'Content-Type': 'application/json', 'Authorization': 'Bearer ' + (server['token'] if token is None else token), 'Accept-Language': locale}
                    if origin:
                        headers['Origin'] = origin
                    req = urllib.request.Request(base + '/api/op/' + op, data=json.dumps(payload or {}).encode(), headers=headers)
                    try:
                        response = urllib.request.urlopen(req, timeout=15)
                    except urllib.error.HTTPError as e:
                        response = e
                    with response:
                        require(response.status == status, op + ' HTTP ' + str(response.status))
                        body = response.read()
                        h.transcripts.append(body)
                        return json.loads(body)

                def call(op, payload=None):
                    return http(op, payload)['data']

                def create(slug, **fields):
                    return call('posts.create', {'title': 'Fictional ' + slug, 'slug': slug, 'markdown': 'Fictional ordinary source', **fields})['post']

                def update(p, **fields):
                    return call('posts.update', {k: p[k] for k in FIELDS} | fields | {'id': p['id'], 'expected_revision': p['revision']})['post']

                source = 'İİ 🐾\n' + 'Fictional plain source line\n' * 220 + '中文雨声 PRIVATE_BODY_MATCH <img src=x onerror=window.searchXSS=1>\n' + 'Fictional trailing line\n' * 30
                body = create('body-match', title='Fictional source only', markdown=source, tags=['legacy-topic'])
                summary = create('summary-match', excerpt='只有摘要有雨声，仅摘要 PRIVATE_SUMMARY_MATCH')
                title = create('title-match', title='雨声 Fictional title')
                title = call('posts.publish', {'id': title['id'], 'expected_revision': 1, 'confirm': True})['post']
                live = create('isolation', markdown='PUBLIC_ORIGINAL_SEARCH', excerpt='Fictional public summary')
                call('posts.publish', {'id': live['id'], 'expected_revision': 1, 'confirm': True})
                changed = update(live, markdown='PRIVATE_NEW_SEARCH 暗词', excerpt='PRIVATE_NEW_SUMMARY')
                require([p['id'] for p in call('posts.list', {'query': 'PRIVATE_BODY_MATCH'})['posts']] == [body['id']], 'body fixture is not saved/queryable through the existing API')
                result['body_fixture_saved_and_queryable'] = True
                if args.review_case:
                    require(args.chromium, '--review-case requires Chromium')
                    review_regressions(args,h,server,call,create,result,done,args.review_case)
                    h.check_no_secrets()
                    return
                if not args.baseline:
                    before = call('backup.export')['state']
                    query = {'query': '雨声', 'limit': 50}
                    envelope = http('posts.search', query)
                    r = envelope['data']
                    require([p['id'] for p in r['results']] == [title['id'], summary['id'], body['id']], 'field-priority ranking')
                    hit = r['results'][2]
                    m = hit['matches'][0]
                    start = source.index('雨声')
                    require(m['field'] == 'markdown' and m['line'] == 222 and m['start_rune'] == start and m['end_rune'] == start + 2 and m['text'] == '雨声', 'original CJK/Unicode position')
                    require(m['truncated_before'] and m['truncated_after'] and len(m['snippet']) <= 228 and len(json.dumps(r).encode()) < 16000, 'bounded plain snippets')
                    require(not any(k in hit for k in ['markdown', 'html', 'revisions', 'live']) and r['total'] == 3 and not r['truncated'], 'full body disclosure')
                    require(h.cli(['--lang', 'en', 'call', 'posts.search', '--json', json.dumps(query)], server) == envelope, 'CLI parity')
                    mcp = MCP(h, server, locale='en')
                    tools = mcp.request('tools/list', {})['tools']
                    tool = next(t for t in tools if t['name'] == 'posts_search')
                    require(len(tools) == 37 and tool['annotations']['readOnlyHint'] and tool['inputSchema']['additionalProperties'] is False and tool['inputSchema']['required'] == ['query'], '37-operation discovery/schema')
                    require(mcp.call('posts.search', query) == envelope, 'MCP parity')
                    mcp.close()
                    limited = call('posts.search', {'query': '雨声', 'limit': 1})
                    require(limited['total'] == 3 and limited['truncated'] and limited['results'][0]['id'] == title['id'], 'top limit/truncation')
                    require(call('posts.search', {'query': 'İİ'})['results'][0]['matches'][0]['text'] == 'İİ', 'case mapping source')
                    require(call('posts.search', {'query': 'PRIVATE_NEW_SEARCH', 'status': 'changed'})['results'][0]['revision'] == 2, 'current revision')
                    require(call('posts.search', {'query': 'PUBLIC_ORIGINAL_SEARCH'})['total'] == 0, 'live/history entered current search')
                    require(call('posts.list', {'query': 'PRIVATE_SUMMARY_MATCH'})['posts'] == [], 'legacy list behavior changed')
                    require(call('posts.search', {'query': 'legacy-topic'})['results'][0]['matches'][0]['field'] == 'tags', 'legacy tags lost')
                    require(call('posts.search', {'query': 'body-match'})['results'][0]['matches'][0]['field'] == 'slug', 'legacy slug lost')
                    for token in [server['token'], server['draft'], server['read'], proposal]:
                        http('posts.search', query, token=token)
                    for token in ['', 'invalid']:
                        require(http('posts.search', query, token=token, status=401) == http('posts.search', {'query': 'missing'}, token=token, status=401), 'anonymous oracle')
                    http('posts.search', query, origin='https://evil.example', status=403)
                    for bad in [{}, {'query': None}, {'query': ' '}, {'query': '雨' * 129}, query | {'limit': 0}, query | {'limit': 51}, query | {'limit': None}, query | {'status': 'draft|changed'}, query | {'status': None}, query | {'unknown': 1}]:
                        http('posts.search', bad, status=400)
                    require('文章搜索' in http('posts.search', {'query': ' '}, status=400, locale='zh-CN')['error']['message'], 'Chinese validation')
                    for path in ['/api/public/site', '/api/public/posts/isolation', '/api/public/search?q=PRIVATE_NEW', '/posts/isolation', '/feed.xml', '/sitemap.xml']:
                        require(b'PRIVATE_NEW' not in h.http(server, path), 'private search leaked into public ' + path)
                    require(call('backup.export')['state'] == before and http('posts.search', query) == envelope, 'read changed state/retry result')
                    done('37-operation HTTP/CLI/MCP parity, bounded ranked source matches, Unicode, legacy compatibility and zero state writes')
                    done('private read roles, anonymous/origin/schema guards, Chinese errors and saved/live/history isolation')
                if args.chromium:
                    from playwright.sync_api import sync_playwright, expect
                    with sync_playwright() as pw:
                        browser = pw.chromium.launch(executable_path=args.chromium, headless=True)
                        writes = []
                        def login(p, token=None):
                            p.on('pageerror', lambda e: errors.append(str(e)))
                            p.on('request', lambda req: writes.append(req.url.rsplit('/', 1)[-1]) if req.url.rsplit('/', 1)[-1] in ['posts.create', 'posts.update', 'posts.publish', 'posts.restore', 'schedules.create'] else None)
                            p.goto(base + '/studio')
                            expect(p.locator('#login-form')).to_be_visible()
                            p.evaluate("window.FolioI18n.setLocale('en')")
                            p.locator('#access-token').fill(token or server['token'])
                            p.locator('#login-form button[type="submit"]').click()
                            expect(p.locator('.post-table')).to_be_visible()

                        def search(p, text, count=None):
                            p.locator('#studio-search').fill(text)
                            if count is not None:
                                expect(p.locator('#post-rows .post-title')).to_have_count(count)
                            return p.locator('#post-rows')

                        def settle(p):
                            p.evaluate('()=>new Promise(r=>requestAnimationFrame(()=>requestAnimationFrame(r)))')

                        def waitheld(p, held, n=1):
                            for _ in range(400):
                                if len(held) >= n:
                                    return
                                p.wait_for_timeout(20)
                            raise AssertionError('real response was not held')

                        def release(p, item):
                            route, response = item
                            with p.expect_response(lambda r: r.url.endswith('/api/op/posts.search')) as event:
                                route.fulfill(response=response)
                            event.value.body()
                            settle(p)

                        ctx = browser.new_context()
                        p = ctx.new_page()
                        login(p)
                        p.locator('#studio-search').fill('PRIVATE_BODY_MATCH')
                        try:
                            expect(p.locator('#post-rows .post-title')).to_have_count(1, timeout=2500)
                            expect(p.locator('#post-rows .post-title')).to_have_text(body['title'])
                        except AssertionError:
                            result['failure_state'] = {'query': p.locator('#studio-search').input_value(), 'visible_titles': p.locator('#post-rows .post-title').all_text_contents()}
                            raise AssertionError('Body-only saved story is absent from Studio search') from None
                        done('body-only saved story is found in the actual Studio (same prior-build regression assertion)')
                        if args.baseline:
                            ctx.close()
                            browser.close()
                            return
                        expect(p.locator('.story-match mark')).to_have_text('PRIVATE_BODY_MATCH')
                        require(p.locator('.story-match img, .story-match script').count() == 0 and not p.evaluate('window.searchXSS||false'), 'source rendered as HTML')
                        p.locator('.story-match').click()
                        expect(p.locator('#post-markdown')).to_have_value(source)
                        require(p.locator('#post-markdown').evaluate('(e)=>e.value.slice(e.selectionStart,e.selectionEnd)') == 'PRIVATE_BODY_MATCH', 'UTF16 source selection')
                        require(p.locator('#post-markdown').evaluate('(e)=>e.scrollTop') > 0, 'far source line not scrolled')
                        expect(p.locator('#save-state')).to_have_text('All changes saved')
                        require(not writes, 'search/location wrote content')
                        done('escaped snippet opens exact saved Markdown source, Unicode selection and far-line scroll without writes')
                        # Existing dirty-navigation guard and Markdown export remain usable after locating.
                        p.locator('#post-markdown').fill('Fictional unsaved buffer')
                        p.locator('#post-markdown').evaluate('(e)=>e.setSelectionRange(3,11)')
                        require(not p.locator('#story-search-location').count(), 'location notice survived edits')
                        with p.expect_download() as download:
                            p.locator('[data-action="export-markdown"]').click()
                        require(Path(download.value.path()).read_text() == 'Fictional unsaved buffer', 'unsaved export lost buffer')
                        p.once('dialog', lambda d: d.dismiss())
                        p.locator('.studio-nav a[href="/studio"]').evaluate('(e)=>e.click()')
                        require(p.locator('#post-markdown').input_value() == 'Fictional unsaved buffer' and p.locator('#post-markdown').evaluate('(e)=>[e.selectionStart,e.selectionEnd]') == [3,11], 'declined navigation lost editor/caret')
                        expect(p.locator('#save-state')).to_have_text('Unsaved changes')
                        done('located editor preserves unsaved buffer/caret on declined navigation and exports current Markdown')
                        ctx.close()
                        # Actual results and source navigation at both narrow widths and locales.
                        for width in [320,390]:
                            for locale in ['en','zh-CN']:
                                ctx = browser.new_context(viewport={'width':width,'height':844})
                                p = ctx.new_page(); login(p, server['read'])
                                p.evaluate('(v)=>window.FolioI18n.setLocale(v)', locale)
                                search(p, '雨声', 3)
                                require(p.locator('.story-match img, .story-match script').count() == 0 and p.evaluate('document.documentElement.scrollWidth<=innerWidth'), 'mobile search overflow/HTML')
                                p.locator('#studio-search').evaluate('(e)=>e.setSelectionRange(1,2)')
                                p.evaluate('(v)=>window.FolioI18n.setLocale(v)', 'en' if locale == 'zh-CN' else 'zh-CN')
                                require(p.locator('#studio-search').input_value() == '雨声' and p.locator('#studio-search').evaluate('(e)=>[e.selectionStart,e.selectionEnd]') == [1,2], 'locale reset query/caret')
                                p.evaluate('(v)=>window.FolioI18n.setLocale(v)', locale)
                                p.screenshot(path=str(args.report.with_name('story-search-results-'+str(width)+'-'+locale+'.png')), animations='disabled', full_page=True)
                                p.locator('[data-story-hit="'+summary['id']+'"] .story-match').click()
                                require(p.locator('#post-excerpt').evaluate('(e)=>e.value.slice(e.selectionStart,e.selectionEnd)') == '雨声', 'summary source selection')
                                expect(p.locator('#save-state')).to_have_text('All changes saved' if locale == 'en' else '所有修改已保存')
                                require(p.evaluate('document.documentElement.scrollWidth<=innerWidth'), 'mobile editor overflow')
                                p.screenshot(path=str(args.report.with_name('story-search-'+str(width)+'-'+locale+'.png')), animations='disabled')
                                cases.append({'case':'read-role summary navigation', 'width':width, 'locale':locale, 'writes':0})
                                ctx.close()
                        done('read-role EN/CN search and summary location at 320/390px; locale preserves query/caret and layouts fit')
                        ctx=browser.new_context();p=ctx.new_page();login(p)
                        search(p,'雨声',3)
                        p.locator('[data-post-filter="published"]').click();expect(p.locator('#post-rows .post-title')).to_have_count(1)
                        expect(p.locator('#post-rows .post-title')).to_have_text(title['title'])
                        p.locator('[data-post-filter="changed"]').click();expect(p.locator('#post-rows .post-title')).to_have_count(0)
                        p.locator('[data-post-filter="all"]').click();expect(p.locator('#post-rows .post-title')).to_have_count(3)
                        search(p,'legacy-topic',1);search(p,'body-match',1)
                        search(p,'仅摘要',1)
                        updated=update(summary,excerpt='Fictional replacement summary 仅摘要')
                        p.locator('.story-match').click();expect(p.locator('#post-excerpt')).to_have_value(updated['excerpt'])
                        expect(p.locator('#story-search-location')).to_contain_text('changed after the search')
                        require(p.locator('#post-excerpt').evaluate('(e)=>e.value.slice(e.selectionStart,e.selectionEnd)') != '仅摘要', 'stale source offsets selected')
                        ctx.close();done('status filters and legacy slug/tags work; intervening writer opens fresh draft and declines stale location')
                        # IME composition must issue no intermediate reads, then exactly one final read.
                        ctx=browser.new_context();p=ctx.new_page();login(p);reads=[]
                        p.on('request',lambda req: reads.append(req.post_data_json) if req.url.endswith('/api/op/posts.search') else None)
                        p.locator('#studio-search').evaluate('(e)=>{e.dispatchEvent(new CompositionEvent("compositionstart",{bubbles:true}));e.value="雨";e.dispatchEvent(new InputEvent("input",{bubbles:true,isComposing:true}));}')
                        p.wait_for_timeout(300);require(not reads,'intermediate IME query sent')
                        p.locator('#studio-search').evaluate('(e)=>{e.value="雨声";e.dispatchEvent(new CompositionEvent("compositionend",{bubbles:true}));e.dispatchEvent(new InputEvent("input",{bubbles:true}));}')
                        expect(p.locator('#post-rows .post-title')).to_have_count(2)
                        require(len(reads)==1 and reads[0]['query']=='雨声','final IME query missing/duplicated')
                        ctx.close();done('Chinese IME defers intermediate reads and sends one final query')
                        for scenario in ['out-of-order','clear','route-dirty','late-error','logout','wrong-query','retry']:
                            ctx=browser.new_context();p=ctx.new_page();login(p);held=[]
                            def hold(route):
                                data=route.request.post_data_json
                                if scenario=='late-error':data=data|{'limit':0}
                                if scenario=='wrong-query':data=data|{'query':'仅摘要'}
                                if scenario=='retry':
                                    route.abort('failed');return
                                response=route.fetch(post_data=json.dumps(data));held.append((route,response))
                            p.route('**/api/op/posts.search',hold)
                            p.locator('#studio-search').fill('雨声')
                            if scenario=='retry':
                                expect(p.locator('#story-search-retry')).to_be_visible()
                                p.unroute('**/api/op/posts.search')
                                p.locator('#story-search-retry').click();expect(p.locator('#post-rows .post-title')).to_have_count(2)
                                require(p.locator('#studio-search').input_value()=='雨声','retry lost query')
                            else:
                                waitheld(p,held)
                                if scenario=='out-of-order':
                                    p.locator('#studio-search').fill('仅摘要');waitheld(p,held,2)
                                    release(p,held.pop(1));expect(p.locator('#post-rows .post-title')).to_have_count(1)
                                    p.locator('#studio-search').evaluate('(e)=>e.setSelectionRange(1,2)')
                                    snapshot=p.locator('#post-rows').inner_html();release(p,held.pop(0))
                                    require(p.locator('#post-rows').inner_html()==snapshot and p.locator('#studio-search').input_value()=='仅摘要' and p.locator('#studio-search').evaluate('(e)=>[e.selectionStart,e.selectionEnd]')==[1,2],'older response replaced newer result/caret')
                                elif scenario in ['clear','late-error']:
                                    p.locator('#studio-search-clear').click();expect(p.locator('#post-rows .post-title')).to_have_count(4)
                                    snapshot=p.locator('#post-rows').inner_html();release(p,held.pop(0))
                                    require(p.locator('#post-rows').inner_html()==snapshot and not p.locator('#story-search-retry').count() and p.locator('#studio-search').input_value()=='','cleared search revived old success/error')
                                elif scenario=='route-dirty':
                                    p.locator('a[href="/studio/new"]').first.click();expect(p.locator('#post-markdown')).to_be_visible()
                                    p.locator('#post-markdown').fill('Fictional new unsaved buffer');p.locator('#post-markdown').evaluate('(e)=>e.setSelectionRange(3,11)')
                                    release(p,held.pop(0))
                                    require(p.locator('#post-markdown').input_value()=='Fictional new unsaved buffer' and p.locator('#post-markdown').evaluate('(e)=>[e.selectionStart,e.selectionEnd]')==[3,11] and not p.locator('#studio-search-status').count(),'late search polluted later dirty editor')
                                    expect(p.locator('#save-state')).to_have_text('Unsaved changes')
                                elif scenario=='logout':
                                    p.locator('[data-action="logout"]').first.evaluate('(e)=>e.click()');expect(p.locator('#login-form')).to_be_visible()
                                    release(p,held.pop(0));require(not p.locator('#post-rows').count(),'old private search revived after logout')
                                else:
                                    release(p,held.pop(0));expect(p.locator('#story-search-retry')).to_be_visible()
                                    require(not p.locator('[data-story-hit]').count(),'wrong-query response displayed')
                                    p.unroute('**/api/op/posts.search');p.locator('#story-search-retry').click();expect(p.locator('#post-rows .post-title')).to_have_count(2)
                            cases.append({'case':scenario,'real_response':scenario!='retry','writes':0})
                            ctx.close()
                        require(not writes and not errors,'browser search/location wrote content or raised errors')
                        done('seven actual delayed/failed reads: out-of-order, clear, dirty-route, late validation, logout, mismatched identity and retry; zero browser writes/errors')
                        browser.close()
                    review_regressions(args,h,server,call,create,result,done)
                h.check_no_secrets()
                done('captured CLI/MCP/public output and daemon logs contain no access credentials')
            finally:
                h.close()
    except Exception as e:
        result['failure'] = str(e)
        raise
    finally:
        report()


if __name__ == '__main__':
    main()

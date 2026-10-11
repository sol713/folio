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


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', type=Path, required=True)
    parser.add_argument('--chromium')
    parser.add_argument('--report', type=Path, required=True)
    parser.add_argument('--baseline', action='store_true', help='Run the same body-search product assertion on the prior build')
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

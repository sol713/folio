#!/usr/bin/env python3
"""Bounded, read-only content analysis on a real daemon, CLI, MCP and browser.

Fictional disposable catalog. Delays preserve actual server responses. A loopback
listener proves that reference analysis does not fetch even reachable URLs.
"""
import argparse
import json
from pathlib import Path
import secrets
import tempfile
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import urllib.error
import urllib.request

from e2e import Harness, MCP, require


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', type=Path, required=True)
    parser.add_argument('--chromium')
    parser.add_argument('--report', type=Path, required=True)
    args = parser.parse_args()
    checks, delayed_cases, errors, fetched = [], [], [], []

    class Listener(BaseHTTPRequestHandler):
        def do_GET(self):
            fetched.append(self.path)
            self.send_response(200); self.end_headers(); self.wfile.write(b'Fictional probe')
        def log_message(self, *_):
            pass

    probe = ThreadingHTTPServer(('127.0.0.1', 0), Listener)
    thread = threading.Thread(target=probe.serve_forever, daemon=True); thread.start()
    try:
        with tempfile.TemporaryDirectory(prefix='folio-content-check-') as tmp:
            h = Harness(args.binary.resolve(), Path(tmp))
            proposal = secrets.token_hex(32); h.secrets.append(proposal)
            h.base_env['FOLIO_PROPOSAL_TOKEN'] = proposal
            try:
                server = h.start('instance'); base = server['url']
                probe_url = 'http://127.0.0.1:' + str(probe.server_port)

                def http(op, payload, token=None, locale='en', status=200):
                    req = urllib.request.Request(base + '/api/op/' + op,
                        data=json.dumps(payload).encode(), headers={'Content-Type':'application/json',
                        'Authorization':'Bearer ' + (server['token'] if token is None else token), 'Accept-Language':locale})
                    try: response = urllib.request.urlopen(req, timeout=10)
                    except urllib.error.HTTPError as error: response = error
                    with response:
                        require(response.status == status, op + ' unexpected HTTP status ' + str(response.status))
                        return json.load(response)

                def call(op, payload=None):
                    return http(op, payload or {})['data']

                def create(slug, markdown='Fictional body', excerpt='Fictional summary', cover=''):
                    return call('posts.create', {'title':'Fictional ' + slug, 'slug':slug,
                        'markdown':markdown, 'excerpt':excerpt, 'cover':cover})['post']

                def publish(p):
                    return call('posts.publish', {'id':p['id'],'expected_revision':p['revision'],'confirm':True})['post']

                def done(name):
                    checks.append(name); print('PASS ' + name, flush=True)

                settings = call('settings.get')
                call('settings.update', {'settings':settings['settings'] | {'base_url':base},
                                         'expected_revision':settings['revision']})
                live = publish(create('live-target'))
                renamed = publish(create('old-target'))
                renamed = call('posts.update', {k:renamed[k] for k in ['title','markdown','excerpt','tags','category','cover','featured']} |
                    {'id':renamed['id'],'slug':'renamed-target','expected_revision':renamed['revision']})['post']
                renamed = publish(renamed)
                private = create('private-target')
                deleted = create('deleted-target')
                call('posts.delete', {'id':deleted['id'],'expected_revision':1,'confirm':True})
                media = call('media.upload', {'name':'fictional.png','base64':
                    'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAusB9Wl6cqQAAAAASUVORK5CYII='})['media']
                markdown = '\n'.join(['# Fictional source', '',
                    '[live](/posts/live-target) [old](/posts/old-target) [relative](live-target?x=1#anchor)',
                    '[private](/posts/private-target)', '[missing](/posts/missing-target)',
                    '![existing](' + media['url'] + ') ![missing](/media/absent.png)',
                    '[self](/posts/source#part) [fragment](#unverified)',
                    '[external](' + probe_url + '/posts/anything) ![external image](' + probe_url + '/image.png)',
                    '[trailing slash](/posts/live-target/) [encoded slash](/posts/live-target%2Fanything)',
                    '`[code](/posts/code)`', '<img src="' + probe_url + '/raw-html.png">',
                    '[definition][gone]', '', '[gone]: /posts/ref-missing'])
                source = create('source', markdown, '', '/media/absent-cover.png')
                payload = {'id':source['id'],'revision':source['revision']}
                before = call('backup.export')['state']
                api = http('posts.check', payload)
                result = api['data']
                codes = [f['code'] for f in result['findings']]
                require(codes.count('article_private')==1 and codes.count('article_missing')==2 and
                        codes.count('media_missing')==2 and codes.count('excerpt_missing')==1 and
                        codes.count('reference_unchecked')==2, 'route analysis findings mismatch')
                require(result['id']==source['id'] and result['revision']==1 and not result['truncated'], 'wrong snapshot')
                require(private['id'] not in json.dumps(result) and private['title'] not in json.dumps(result), 'target details leaked')
                for path in ['/posts/live-target','/posts/old-target',media['url']]: h.http(server,path)
                for path in ['/posts/private-target','/posts/missing-target','/media/absent.png']: h.http(server,path,status=404)
                done('actual canonical routes, old slug redirects, relative URLs, private targets, media and exclusions')

                cli = h.cli(['--lang','en','call','posts.check','--json',json.dumps(payload)],server)
                require(cli==api, 'CLI differs from API')
                session=MCP(h,server,locale='en')
                tools=session.request('tools/list',{})['tools']; check=next(t for t in tools if t['name']=='posts_check')
                require(len(tools)==35 and check['annotations']['readOnlyHint'] is True and
                        set(check['inputSchema']['required'])=={'id','revision'} and
                        check['inputSchema']['additionalProperties'] is False, 'MCP schema or annotation mismatch')
                require(session.call('posts.check',payload)==api, 'MCP differs from API')
                session.close()
                require(call('backup.export')['state']==before, 'read-only analysis changed state')
                done('real API/CLI/MCP parity, 35-operation schema, read-only annotations and unchanged logical backup')

                for role in ['token','draft','read']:
                    require(http('posts.check',payload,token=server[role])['ok'], 'authorized role denied')
                require(http('posts.check',payload,token=proposal)['ok'], 'proposal role check denied')
                for token in ['', 'invalid']:
                    known=http('posts.check',payload,token=token,status=401)
                    unknown=http('posts.check',{'id':'unknown','revision':1},token=token,status=401)
                    require(known==unknown, 'unauthorized existence oracle')
                require(http('posts.publish',{'id':source['id'],'expected_revision':1,'confirm':True},token=proposal,status=403)['ok'] is False,'proposal published')
                for role in ['draft','read']:
                    require(http('posts.publish',{'id':source['id'],'expected_revision':1,'confirm':True},token=server[role],status=403)['ok'] is False,'non-owner published')
                done('all existing private-read roles can check; anonymous checks and non-owner publication denied')

                require('私密草稿' in json.dumps(http('posts.check',payload,locale='zh-CN'),ensure_ascii=False), 'Chinese API finding missing')
                zhcli=h.cli(['--lang','zh-CN','call','posts.check','--json',json.dumps(payload)],server)
                zhmcp=MCP(h,server,locale='zh-CN'); require(zhmcp.call('posts.check',payload)==zhcli,'Chinese CLI/MCP mismatch');zhmcp.close()
                for malformed in [{'id':source['id']},{'id':source['id'],'revision':0},payload|{'url':probe_url},payload|{'markdown':'probe'}]:
                    http('posts.check',malformed,status=400)
                http('posts.check',payload|{'revision':2},status=409)
                require(not fetched, 'analysis fetched a reachable external URL')
                done('bilingual messages, strict saved-revision inputs, stale conflict and no outbound reference requests')

                if args.chromium:
                    from playwright.sync_api import sync_playwright, expect
                    with sync_playwright() as pw:
                        browser=pw.chromium.launch(executable_path=args.chromium,headless=True)

                        def login(page, token=None, post=source):
                            page.on('pageerror',lambda err:errors.append(str(err)))
                            page.on('dialog',lambda dialog:dialog.accept())
                            page.goto(base+'/studio')
                            expect(page.locator('#login-form')).to_be_visible()
                            page.evaluate("window.FolioI18n.setLocale('en')")
                            page.locator('#access-token').fill(token or server['token'])
                            page.locator('#login-form button[type="submit"]').click()
                            expect(page.locator('.post-table')).to_be_visible()
                            page.locator('a[href="/studio/posts/'+post['id']+'"]').first.click()
                            expect(page.locator('#post-markdown')).to_have_value(post['markdown'])

                        context=browser.new_context(viewport={'width':1280,'height':900});page=context.new_page();login(page)
                        writes=[]
                        page.on('request',lambda req:writes.append(req.url) if req.url.endswith(('/posts.create','/posts.update','/posts.publish','/proposals.create')) else None)
                        page.locator('[data-action="check-content"]').click()
                        expect(page.locator('#check-status')).to_have_text('Results for this saved draft.')
                        expect(page.locator('#content-check [data-check-code="article_private"]')).to_have_count(1)
                        page.locator('#content-check [data-check-code="article_missing"] [data-check-location]').first.click()
                        selected=page.locator('#post-markdown').evaluate('(e)=>e.value.slice(e.selectionStart,e.selectionEnd)')
                        require(selected=='[missing](/posts/missing-target)','wrong Markdown issue line selected')
                        page.locator('#content-check [data-check-code="excerpt_missing"] [data-check-location]').click()
                        expect(page.locator('#post-excerpt')).to_be_focused()
                        page.locator('#content-check [data-check-code="media_missing"] [data-check-location]').first.click()
                        expect(page.locator('#post-cover')).to_be_focused()
                        require(not writes and not fetched and page.locator('#content-check img,#content-check a[href]').count()==0,'check performed a write or fetched evidence')
                        page.locator('[data-action="publish"]').click()
                        expect(page.locator('#publish-check')).to_contain_text('private draft')
                        expect(page.locator('#confirm-publish')).to_be_enabled()
                        page.keyboard.press('Escape')
                        before_text=page.locator('#post-markdown').input_value()
                        page.locator('#post-markdown').fill(before_text+'\nUnsaved fictional addition')
                        expect(page.locator('#check-status')).to_contain_text('outdated')
                        expect(page.locator('#content-check [data-check-location]').first).to_be_disabled()
                        expect(page.locator('[data-action="check-content"]')).to_be_disabled()
                        require(not writes,'check saved unsaved text')
                        require(call('backup.export')['state']==before,'browser analysis mutated catalog')
                        context.close()
                        done('manual check, exact line/field focus, no implicit writes, stale locations and visible optional publication guidance')

                        for width in [320,390]:
                            for locale in ['en','zh-CN']:
                                ctx=browser.new_context(viewport={'width':width,'height':844});p=ctx.new_page();login(p)
                                p.locator('[data-action="check-content"]').click()
                                expect(p.locator('#check-status')).to_have_text('Results for this saved draft.')
                                p.locator('#post-markdown').focus();p.locator('#post-markdown').evaluate('(e)=>e.setSelectionRange(3,8)')
                                p.evaluate('(locale)=>window.FolioI18n.setLocale(locale)',locale)
                                expect(p.locator('#check-status')).to_have_text('Results for this saved draft.' if locale=='en' else '这是此已保存草稿的检查结果。')
                                require(p.locator('#post-markdown').input_value()==markdown and p.locator('#post-markdown').evaluate('(e)=>[e.selectionStart,e.selectionEnd]')==[3,8],'locale replaced editor')
                                p.locator('.check-limits').evaluate('(e)=>e.open=true')
                                require(p.evaluate('document.documentElement.scrollWidth<=innerWidth'),'mobile check overflow')
                                p.screenshot(path=str(args.report.with_name('content-'+str(width)+'-'+locale+'.png')),full_page=True)
                                ctx.close()
                        done('EN/CN findings, severity, evidence and limits; live locale preserves editor and 320/390px layouts')

                        for role in ['draft','read','proposal']:
                            ctx=browser.new_context();p=ctx.new_page();login(p,proposal if role=='proposal' else server[role])
                            p.locator('[data-action="check-content"]').click();expect(p.locator('#check-status')).to_have_text('Results for this saved draft.')
                            require(p.locator('[data-action="publish"]').count()==0,'non-owner publish visible')
                            ctx.close()
                        done('real draft/read/proposal Studio permissions retain read-only preflight')

                        other=create('other-source','Other fictional body')
                        # Every editable field invalidates an in-flight report, including edits reverted to their original value.
                        for destination in ['title','slug','markdown','excerpt','tags','category','cover','featured','edit-revert','existing','new','lock','back-return']:
                            ctx=browser.new_context();p=ctx.new_page();login(p)
                            held=[];write_ops=[]
                            p.on('request',lambda req:write_ops.append(req.url) if req.url.endswith(('/posts.create','/posts.update','/posts.publish')) else None)
                            def defer(route):
                                if not held: held.append((route,route.fetch()))
                                else: route.continue_()
                            p.route('**/api/op/posts.check',defer)
                            p.locator('[data-action="check-content"]').click()
                            for _ in range(100):
                                if held:break
                                p.wait_for_timeout(10)
                            require(len(held)==1,'actual response not held')
                            if destination in ['title','slug','markdown','excerpt','tags','category','cover','featured','edit-revert']:
                                field='markdown' if destination=='edit-revert' else destination
                                el=p.locator('#post-'+field)
                                if field=='featured':el.check()
                                else:el.fill(el.input_value()+' fictional edit')
                                if destination=='edit-revert':el.fill(markdown)
                            elif destination=='lock':
                                p.locator('[data-action="logout"]').first.click();expect(p.locator('#login-form')).to_be_visible()
                            else:
                                if destination=='back-return':p.evaluate('history.back()')
                                else:p.locator('.studio-nav a[href="/studio"]').click()
                                expect(p.locator('.post-table')).to_be_visible()
                                target=source if destination=='back-return' else other
                                if destination=='new':p.locator('a[href="/studio/new"]').first.click()
                                else:p.locator('a[href="/studio/posts/'+target['id']+'"]').first.click()
                                expect(p.locator('#post-markdown')).to_be_visible()
                            current_url=p.url
                            text=p.locator('#post-markdown').input_value() if p.locator('#post-markdown').count() else None
                            route,response=held.pop();route.fulfill(response=response);p.unroute('**/api/op/posts.check');p.wait_for_timeout(250)
                            require(p.url==current_url and not write_ops and p.locator('[data-check-location]').count()==0,'obsolete report rebound or wrote '+destination)
                            if text is not None:require(p.locator('#post-markdown').input_value()==text,'late check replaced buffer '+destination)
                            require(p.locator('#check-status').count()==0 or 'Results for this saved draft.' not in p.locator('#check-status').inner_text(),'obsolete success displayed '+destination)
                            delayed_cases.append({'case':destination,'obsolete_report_discarded':True,'write_count':0});ctx.close()
                        done('13 real held-response cases: all eight fields, reverted input, other/new editor, lock and return to same article')

                        race=create('newer-check-race','[old](/posts/old-missing)')
                        ctx=browser.new_context();p=ctx.new_page();login(p,post=race)
                        held=[]
                        def hold_old(route):
                            if json.loads(route.request.post_data)['revision']==1:held.append((route,route.fetch()))
                            else:route.continue_()
                        p.route('**/api/op/posts.check',hold_old)
                        p.locator('[data-action="check-content"]').click()
                        for _ in range(100):
                            if held:break
                            p.wait_for_timeout(10)
                        require(len(held)==1,'old result not held')
                        p.locator('#post-markdown').fill('Fictional clean revision two')
                        p.locator('[data-action="save"]').click()
                        expect(p.locator('#save-state')).to_have_text('All changes saved')
                        p.locator('[data-action="check-content"]').click()
                        expect(p.locator('#check-status')).to_have_text('Results for this saved draft.')
                        expect(p.locator('.check-snapshot')).to_contain_text('Draft R2')
                        route,response=held.pop();route.fulfill(response=response);p.unroute('**/api/op/posts.check');p.wait_for_timeout(250)
                        expect(p.locator('.check-snapshot')).to_contain_text('Draft R2')
                        require(p.locator('[data-check-code="article_missing"]').count()==0 and call('posts.get',{'id':race['id']})['post']['revision']==2,'old result replaced newer report or duplicated save')
                        delayed_cases.append({'case':'newer-check-wins','obsolete_report_discarded':True,'intentional_save_count':1});ctx.close()
                        done('newer saved revision/report survives a late earlier report; one intentional save only')

                        # Read failure and retry; stale server head never gains a successful report.
                        ctx=browser.new_context();p=ctx.new_page();login(p)
                        p.route('**/api/op/posts.check',lambda route:route.abort('connectionfailed'))
                        p.locator('[data-action="check-content"]').click();expect(p.locator('#check-status')).to_contain_text('failed')
                        p.unroute('**/api/op/posts.check');p.locator('[data-action="check-content"]').click();expect(p.locator('#check-status')).to_have_text('Results for this saved draft.')
                        newer=call('posts.update',{k:source[k] for k in ['title','slug','markdown','excerpt','tags','category','cover','featured']} | {'id':source['id'],'expected_revision':1})['post']
                        p.locator('[data-action="check-content"]').click();expect(p.locator('#check-status')).to_contain_text('failed')
                        expect(p.locator('#content-check [data-check-location]').first).to_be_disabled()
                        require(p.locator('#post-markdown').input_value()==markdown,'failed read replaced text')
                        ctx.close()
                        # Advisory warnings never gate the existing exact-revision Studio publish operation.
                        ctx=browser.new_context();p=ctx.new_page();login(p,post=newer)
                        p.locator('[data-action="publish"]').click()
                        expect(p.locator('#publish-check')).to_contain_text('has not been checked')
                        p.locator('[data-action="check-from-review"]').click()
                        expect(p.locator('#check-status')).to_have_text('Results for this saved draft.')
                        p.locator('[data-action="publish"]').click()
                        expect(p.locator('#publish-check')).to_contain_text('warnings')
                        expect(p.locator('#confirm-publish')).to_be_enabled()
                        with p.expect_response(lambda r:r.url.endswith('/api/op/posts.publish')) as response:
                            p.locator('#confirm-publish').click()
                        outcome=response.value.json()['data']['post']
                        require(outcome['id']==newer['id'] and outcome['published_revision']==newer['revision'],'warnings blocked or rebound legal publication')
                        sent=json.loads(response.value.request.post_data)
                        require(sent['expected_revision']==newer['revision'] and sent['confirm'] is True,'preflight changed publication protocol')
                        ctx.close()
                        browser.close()
                        done('interrupted read retry, stale revision rejection, preserved buffer and legal exact-revision publication with warnings')
                        require(not errors,'browser exceptions: '+repr(errors));require(not fetched,'UI fetched unchecked references')
                h.check_no_secrets()
            finally:
                h.close()
    finally:
        probe.shutdown();probe.server_close();thread.join(timeout=2)
    args.report.parent.mkdir(parents=True,exist_ok=True)
    args.report.write_text(json.dumps({'checks':checks,'count':len(checks),'delayed_cases':delayed_cases,
        'browser_errors':errors,'reference_requests':len(fetched)},ensure_ascii=False,indent=2)+'\n')


if __name__=='__main__':
    main()

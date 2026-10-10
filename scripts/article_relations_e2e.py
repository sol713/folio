#!/usr/bin/env python3
"""One-hop author links on a real daemon, CLI, MCP and browser; no fake API data."""
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
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary',type=Path,required=True)
    parser.add_argument('--chromium')
    parser.add_argument('--report',type=Path,required=True)
    parser.add_argument('--navigation-only',action='store_true',help='Isolate delayed article-load regression')
    args=parser.parse_args();checks=[];delayed=[];errors=[];hits=[]
    class Listener(BaseHTTPRequestHandler):
        def do_GET(self):
            hits.append(self.path);self.send_response(200);self.end_headers()
        def log_message(self,*_):pass
    probe=ThreadingHTTPServer(('127.0.0.1',0),Listener)
    thread=threading.Thread(target=probe.serve_forever,daemon=True);thread.start()
    try:
        with tempfile.TemporaryDirectory(prefix='folio-relations-') as tmp:
            h=Harness(args.binary.resolve(),Path(tmp));proposal=secrets.token_hex(32)
            h.secrets.append(proposal);h.base_env['FOLIO_PROPOSAL_TOKEN']=proposal
            try:
                server=h.start('instance');base=server['url']
                def http(op,payload=None,token=None,status=200,locale='en',origin=None):
                    headers={'Content-Type':'application/json','Authorization':'Bearer '+(server['token'] if token is None else token),'Accept-Language':locale}
                    if origin:headers['Origin']=origin
                    req=urllib.request.Request(base+'/api/op/'+op,data=json.dumps(payload or {}).encode(),headers=headers)
                    try:response=urllib.request.urlopen(req,timeout=15)
                    except urllib.error.HTTPError as e:response=e
                    with response:
                        require(response.status==status,op+' HTTP '+str(response.status));return json.load(response)
                def call(op,payload=None):return http(op,payload)['data']
                def create(slug,body='Fictional body',title=None):return call('posts.create',{'title':title or 'Fictional '+slug,'slug':slug,'markdown':body,'excerpt':'Fictional summary'})['post']
                def update(p,**changes):return call('posts.update',{k:p[k] for k in ['title','slug','markdown','excerpt','tags','category','cover','featured']}|changes|{'id':p['id'],'expected_revision':p['revision']})['post']
                def publish(p):return call('posts.publish',{'id':p['id'],'expected_revision':p['revision'],'confirm':True})['post']
                def relations(p):return call('posts.relations',{'id':p['id'],'revision':p['revision']})
                def done(name):checks.append(name);print('PASS '+name,flush=True)
                settings=call('settings.get');call('settings.update',{'settings':settings['settings']|{'base_url':base},'expected_revision':settings['revision']})
                live=publish(create('old-live'))
                live=publish(update(live,slug='live-target'))
                private=create('private-target',title='<img src=x onerror=alert(1)> Fictional private')
                incoming=publish(create('incoming-source','[live](/posts/source) [again](/posts/source)'))
                incoming=update(incoming,markdown='No saved outgoing link')
                body='\n'.join(['[target](/posts/live-target) [old](/posts/old-live)',
                    '[again](/posts/live-target) [fourth](/posts/live-target)',
                    '[private](/posts/private-target) [self](#part)',
                    '[missing](/posts/absent) [prefix](/posts/live-target-extra)',
                    '[external](http://127.0.0.1:'+str(probe.server_port)+'/posts/live-target)',
                    '![image](/posts/live-target) `[code](/posts/live-target)`',
                    '<a href="/posts/live-target">raw HTML</a>'])
                source=publish(create('source',body))
                source=update(source,markdown=body+'\n[draft extra](/posts/private-target)')
                payload={'id':source['id'],'revision':source['revision']}
                if not args.navigation_only:
                    before=call('backup.export')['state'];api=http('posts.relations',payload);r=api['data']
                    require(r['revision']==2 and r['published_revision']==1 and r['saved_draft']['article']['kind']=='saved_draft' and r['published']['article']['revision']==1,'snapshots conflated')
                    edge=next(e for e in r['saved_draft']['outgoing'] if e['target'] and e['target']['id']==live['id'])
                    require(edge['count']==4 and len(edge['locations'])==3 and edge['locations_truncated'] and {x['route'] for x in edge['locations']}=={'live','redirect'},'dedup, positions or old slug')
                    oldsource=next(e['source'] for e in r['incoming_published'] if e['source']['id']==incoming['id'])
                    require(oldsource['revision']==1 and oldsource['draft_revision']==2 and not any(e['source']['id']==incoming['id'] for e in r['incoming_saved_drafts']),'incoming saved/live confused')
                    require(r['saved_draft']['counts']['external_not_relations']==1 and r['saved_draft']['counts']['images_not_relations']==1 and not hits,'exclusions or network fetch')
                    require(h.cli(['--lang','en','call','posts.relations','--json',json.dumps(payload)],server)==api,'CLI differs from HTTP')
                    session=MCP(h,server,locale='en');tools=session.request('tools/list',{})['tools'];tool=next(t for t in tools if t['name']=='posts_relations')
                    require(len(tools)==35 and tool['annotations']['readOnlyHint'] and set(tool['inputSchema']['required'])=={'id','revision'} and tool['inputSchema']['additionalProperties'] is False,'35-operation schema')
                    require(session.call('posts.relations',payload)==api,'MCP differs from HTTP');session.close()
                    require(call('backup.export')['state']==before and relations(source)==r,'readonly/repeat changed state')
                    done('real HTTP/CLI/MCP 35-operation parity; immutable saved/live, aliases, repeat counts, positions, exclusions and no state writes')
                    for token in [server['token'],server['draft'],server['read'],proposal]:require(http('posts.relations',payload,token=token)['ok'],'authorized private-read denied')
                    for token in ['', 'invalid']:
                        require(http('posts.relations',payload,token=token,status=401)==http('posts.relations',{'id':'unknown','revision':1},token=token,status=401),'unauthorized existence oracle')
                    http('posts.relations',payload,origin='https://evil.example',status=403)
                    for malformed in [{},{'id':source['id']},{'id':source['id'],'revision':0},payload|{'markdown':'injected'},payload|{'url':'file:///etc/passwd'}]:http('posts.relations',malformed,status=400)
                    http('posts.relations',payload|{'revision':1},status=409)
                    require('文章引用关系' in http('posts.relations',{'id':'x','revision':0},status=400,locale='zh-CN')['error']['message'],'Chinese validation')
                    for path in ['/api/public/site','/api/public/posts/source','/api/public/search?q=private','/posts/source','/feed.xml','/sitemap.xml']:
                        public=h.http(server,path).decode()
                        require(private['id'] not in public and private['title'] not in public,'private target exposed publicly')
                    done('all four authorized roles, anonymous existence isolation, origin/schema/stale guards, Chinese errors and public privacy')
                    # A separate cycle changes immediately without a persistent graph/cache.
                    a=create('cycle-a','[b](/posts/cycle-b)');b=create('cycle-b','[a](/posts/cycle-a)')
                    require(relations(a)['incoming_saved_drafts'][0]['source']['id']==b['id'],'cycle not one-hop')
                    b=update(b,slug='cycle-renamed')
                    require(next(e for e in relations(a)['saved_draft']['outgoing'] if e['referenced_slug']=='cycle-b')['availability']=='missing','stale slug cache')
                    call('posts.delete',{'id':b['id'],'expected_revision':b['revision'],'confirm':True})
                    require(not relations(a)['incoming_saved_drafts'],'deleted source retained')
                    done('bounded one-hop cycles; slug change and source deletion recompute immediately')
                if args.chromium:
                    from playwright.sync_api import sync_playwright,expect
                    with sync_playwright() as pw:
                        browser=pw.chromium.launch(executable_path=args.chromium,headless=True)
                        def login(p,post=None,token=None):
                            post=post or source
                            p.on('pageerror',lambda e:errors.append(str(e)));p.on('dialog',lambda d:d.accept())
                            p.goto(base+'/studio');expect(p.locator('#login-form')).to_be_visible();p.evaluate("window.FolioI18n.setLocale('en')")
                            p.locator('#access-token').fill(token or server['token']);p.locator('#login-form button[type="submit"]').click()
                            expect(p.locator('.post-table')).to_be_visible();p.locator('a[href="/studio/posts/'+post['id']+'"]').first.click()
                            expect(p.locator('#post-markdown')).to_have_value(post['markdown'])
                        def loaded(p):
                            p.locator('[data-action="relations-load"]').click();expect(p.locator('#relations-status')).to_have_text('Links from the saved catalog snapshot.')
                        def waitheld(p,held,n=1):
                            for _ in range(150):
                                if len(held)>=n:return
                                p.wait_for_timeout(10)
                            raise AssertionError('actual response not held')
                        def command(p,post):
                            p.keyboard.press('Control+k');p.locator('#command-input').fill(post['title']);p.locator('#command-results .command-option').first.click()
                        # Click a relation, then navigate while its actual posts.get is held.
                        # The late response must not switch save identity or clear newer typing.
                        navsource=create('navigation-source','[incoming](/posts/incoming-source)');newer=create('navigation-newer')
                        ctx=browser.new_context();p=ctx.new_page();login(p,navsource);loaded(p)
                        held=[];p.route('**/api/op/posts.get',lambda route:held.append((route,route.fetch())) if route.request.post_data_json['id']==incoming['id'] else route.continue_())
                        p.locator('[data-relations-list="outgoing_draft"]').evaluate('(e)=>e.open=true')
                        p.locator('a[data-relation-open="'+incoming['id']+'"]').click();waitheld(p,held)
                        command(p,newer);expect(p.locator('#post-markdown')).to_have_value(newer['markdown'])
                        text='Fictional newer editor unsaved text';p.locator('#post-markdown').fill(text);p.locator('#post-markdown').evaluate('(e)=>e.setSelectionRange(3,8)')
                        route,response=held.pop();route.fulfill(response=response);p.unroute('**/api/op/posts.get');p.wait_for_timeout(150)
                        untouched=p.locator('#save-state').inner_text()=='Unsaved changes' and p.locator('#post-markdown').input_value()==text and p.locator('#post-markdown').evaluate('(e)=>[e.selectionStart,e.selectionEnd]')==[3,8]
                        writes=[];p.on('request',lambda req:writes.append(req.post_data_json) if req.url.endswith('/api/op/posts.update') else None)
                        p.locator('[data-action="save"]').click()
                        if untouched:expect(p.locator('#save-state')).to_have_text('All changes saved')
                        else:p.wait_for_timeout(250)
                        correct=untouched and len(writes)==1 and writes[0]['id']==newer['id'] and call('posts.get',{'id':newer['id']})['post']['markdown']==text and call('posts.get',{'id':incoming['id']})['post']==incoming
                        delayed.append({'case':'relation navigation with delayed article load','newer_identity_buffer_caret_preserved':correct})
                        args.report.parent.mkdir(parents=True,exist_ok=True);args.report.write_text(json.dumps({'checks':checks,'delayed_cases':delayed,'browser_errors':errors},indent=2)+'\n')
                        require(correct,'late article load replaced newer editor identity or dirty state')
                        ctx.close();done('actual relation navigation discards late article loads; save reaches only the newer article')
                        if not args.navigation_only:
                            for width in [320,390]:
                                for locale in ['en','zh-CN']:
                                    ctx=browser.new_context(viewport={'width':width,'height':844});p=ctx.new_page();login(p);loaded(p)
                                    p.locator('#post-markdown').focus();p.locator('#post-markdown').evaluate('(e)=>e.setSelectionRange(3,8)');p.evaluate('(locale)=>window.FolioI18n.setLocale(locale)',locale)
                                    expect(p.locator('#relations-status')).to_have_text('Links from the saved catalog snapshot.' if locale=='en' else '显示已保存文章目录快照的引用。')
                                    require(p.locator('#post-markdown').input_value()==source['markdown'] and p.locator('#post-markdown').evaluate('(e)=>[e.selectionStart,e.selectionEnd]')==[3,8],'locale disturbed editor')
                                    p.locator('.relations-list').evaluate_all('(es)=>es.forEach(e=>e.open=true)')
                                    p.locator('.relations-limits').evaluate('(e)=>e.open=true')
                                    require(set(p.locator('[data-relations-list="outgoing_draft"] a[data-relation-open]').evaluate_all('(es)=>es.map(e=>e.dataset.relationOpen)'))=={live['id'],private['id'],source['id']},'resolved outgoing links missing')
                                    require(p.locator('#article-relations img').count()==0 and p.locator('#article-relations script').count()==0,'target title executed HTML')
                                    require(p.evaluate('document.documentElement.scrollWidth<=innerWidth'),'mobile horizontal overflow')
                                    p.screenshot(path=str(args.report.with_name('relations-'+str(width)+'-'+locale+'.png')),full_page=True);ctx.close()
                            done('escaped target titles, all four sections, EN/CN and 320/390px; locale preserves buffer and caret')
                            for role in ['draft','read','proposal']:
                                ctx=browser.new_context();p=ctx.new_page();login(p,token=proposal if role=='proposal' else server[role]);loaded(p)
                                require(p.locator('[data-action="publish"]').count()==0,'read role gained publish');ctx.close()
                            done('real draft/read/proposal Studio read-only authority')
                            for destination in ['title','slug','markdown','excerpt','tags','category','cover','featured','edit-revert','existing','new','lock','back-return','cancel']:
                                ctx=browser.new_context();p=ctx.new_page();login(p);held=[];writes=[]
                                p.on('request',lambda req:writes.append(req.url) if req.url.endswith(('/posts.create','/posts.update','/posts.publish')) else None)
                                p.route('**/api/op/posts.relations',lambda route:held.append((route,route.fetch())))
                                p.locator('[data-action="relations-load"]').click();waitheld(p,held)
                                if destination in ['title','slug','markdown','excerpt','tags','category','cover','featured','edit-revert']:
                                    field='markdown' if destination=='edit-revert' else destination;el=p.locator('#post-'+field)
                                    if field=='featured':el.check()
                                    else:el.fill(el.input_value()+' Fictional edit')
                                    if destination=='edit-revert':el.fill(source['markdown'])
                                elif destination=='lock':p.locator('[data-action="logout"]').first.click();expect(p.locator('#login-form')).to_be_visible()
                                elif destination=='cancel':
                                    p.locator('[data-action="relations-cancel"]').click();expect(p.locator('#relations-status')).to_contain_text('cancelled')
                                    p.keyboard.press('Control+k');p.keyboard.press('Escape')
                                else:
                                    if destination=='back-return':p.evaluate('history.back()')
                                    else:p.locator('.studio-nav a[href="/studio"]').click()
                                    expect(p.locator('.post-table')).to_be_visible()
                                    if destination=='new':p.locator('a[href="/studio/new"]').first.click()
                                    else:p.locator('a[href="/studio/posts/'+(source if destination=='back-return' else newer)['id']+'"]').first.click()
                                    expect(p.locator('#post-markdown')).to_be_visible()
                                current=p.locator('#post-markdown').input_value() if p.locator('#post-markdown').count() else None
                                route,response=held.pop();route.fulfill(response=response);p.unroute('**/api/op/posts.relations');p.wait_for_timeout(100)
                                require(p.locator('.relations-list').count()==0 and not writes and p.locator('#modal-root').inner_html()=='','stale report/write/modal '+destination)
                                if current is not None:require(p.locator('#post-markdown').input_value()==current,'late result changed buffer '+destination)
                                delayed.append({'case':destination,'obsolete_report_discarded':True,'writes':0});ctx.close()
                            done('14 held actual responses: every field, revert, new/other/back-return, lock and cancel preserve editor without writes')
                            ctx=browser.new_context();p=ctx.new_page();login(p);held=[]
                            def firstonly(route):
                                if not held:held.append((route,route.fetch()))
                                else:route.continue_()
                            p.route('**/api/op/posts.relations',firstonly);p.locator('[data-action="relations-load"]').click();waitheld(p,held)
                            p.locator('#post-markdown').fill(source['markdown']+'\nFictional revision three');p.locator('[data-action="save"]').click();expect(p.locator('#save-state')).to_have_text('All changes saved');loaded(p)
                            expect(p.locator('#article-relations .check-snapshot')).to_contain_text('Saved R3')
                            route,response=held.pop();route.fulfill(response=response);p.unroute('**/api/op/posts.relations');p.wait_for_timeout(100)
                            expect(p.locator('#article-relations .check-snapshot')).to_contain_text('Saved R3');ctx.close()
                            delayed.append({'case':'newer report wins','obsolete_report_discarded':True,'intentional_save_count':1})
                            done('newer saved revision and report survive earlier response; one explicit save')
                            source=call('posts.get',{'id':source['id']})['post']
                            for outcome in ['success','network-error']:
                                ctx=browser.new_context();p=ctx.new_page();login(p);held=[]
                                def hold(route):
                                    response=route.fetch() if outcome=='success' or held else None
                                    held.append((route,response))
                                p.route('**/api/op/posts.relations',hold)
                                p.locator('[data-action="relations-load"]').click();waitheld(p,held)
                                p.locator('[data-action="relations-cancel"]').click();p.locator('[data-action="relations-load"]').click();waitheld(p,held,2)
                                p.locator('#post-markdown').focus();p.locator('#post-markdown').evaluate('(e)=>e.setSelectionRange(3,8)')
                                panel=p.locator('#article-relations').inner_html();route,response=held[0]
                                if outcome=='success':route.fulfill(response=response)
                                else:route.abort('connectionfailed')
                                p.wait_for_timeout(100)
                                require(p.locator('#article-relations').inner_html()==panel and p.locator('#post-markdown').evaluate('(e)=>[e.selectionStart,e.selectionEnd]')==[3,8],'old cancelled completion settled new lookup')
                                expect(p.locator('#relations-status')).to_have_text('Reading saved article links…')
                                route,response=held[1];route.fulfill(response=response);p.unroute('**/api/op/posts.relations')
                                expect(p.locator('#relations-status')).to_have_text('Links from the saved catalog snapshot.')
                                p.locator('#post-markdown').fill(source['markdown']+' Fictional dirty buffer')
                                require(p.locator('#article-relations a[data-relation-open]').count()==0,'stale article links remain clickable')
                                delayed.append({'case':'cancel then retry with old '+outcome,'pending_panel_caret_and_buffer_preserved':True});ctx.close()
                            done('cancel/retry isolates old success and network error; stale completed reports disable article navigation')
                            ctx=browser.new_context();p=ctx.new_page();login(p);held=[];publishes=[]
                            p.route('**/api/op/posts.relations',lambda route:held.append((route,route.fetch())))
                            p.route('**/api/op/posts.publish',lambda route:publishes.append(route))
                            p.locator('[data-action="relations-load"]').click();waitheld(p,held)
                            p.locator('[data-action="publish"]').click();expect(p.locator('#confirm-publish')).to_be_enabled();p.locator('#confirm-publish').click();waitheld(p,publishes)
                            route,response=held.pop();route.fulfill(response=response);p.unroute('**/api/op/posts.relations');p.wait_for_timeout(100)
                            publishes.pop().abort('connectionfailed');p.unroute('**/api/op/posts.publish')
                            expect(p.locator('#confirm-publish')).to_be_enabled();p.keyboard.press('Escape')
                            expect(p.locator('[data-action="relations-load"]')).to_be_enabled();loaded(p)
                            require(call('posts.get',{'id':source['id']})['live']['revision']==1,'failed publish changed live snapshot')
                            delayed.append({'case':'lookup settles during failed publish','retry_completed':True,'live_revision_unchanged':True});ctx.close()
                            done('lookup completing during failed publication settles and retries without changing the live version')
                            source=call('posts.get',{'id':source['id']})['post'];ctx=browser.new_context();p=ctx.new_page();login(p)
                            p.route('**/api/op/posts.relations',lambda route:route.abort('connectionfailed'))
                            p.locator('[data-action="relations-load"]').click();expect(p.locator('#relations-status')).to_contain_text('failed');p.unroute('**/api/op/posts.relations');loaded(p)
                            p.locator('[data-action="publish"]').click();expect(p.locator('#confirm-publish')).to_be_enabled();p.locator('#confirm-publish').click();expect(p.locator('.editor-sidebar .badge')).to_have_text('Published')
                            expect(p.locator('[data-action="relations-load"]')).to_be_enabled();loaded(p)
                            p.locator('[data-relations-list="outgoing_draft"]').evaluate('(e)=>e.open=true');p.locator('a[data-relation-open="'+live['id']+'"]').first.click()
                            expect(p.locator('#post-slug')).to_have_value('live-target');require(p.locator('#post-title').input_value()==live['title'],'renamed target opened wrong draft');ctx.close()
                            done('read failure retries; successful publication enables lookup; renamed target opens correct current draft by ID')
                        browser.close();require(not errors,'browser exceptions '+repr(errors))
                require(not hits,'article analysis fetched a reference');done('zero reference-network listener hits')
            finally:h.close()
    finally:probe.shutdown();probe.server_close();thread.join(timeout=5)
    args.report.parent.mkdir(parents=True,exist_ok=True)
    args.report.write_text(json.dumps({'checks':checks,'passed':len(checks),'delayed_cases':delayed,'browser_errors':errors,'reference_network_hits':hits},indent=2)+'\n')


if __name__=='__main__':main()

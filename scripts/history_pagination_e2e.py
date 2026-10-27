#!/usr/bin/env python3
"""Private saved-history paging against a real disposable daemon and Chromium."""
from __future__ import annotations
import argparse
import json
from pathlib import Path
import secrets
import sys
import tempfile
import time
import urllib.error
import urllib.request
sys.dont_write_bytecode = True
from e2e import MCP, encode, require
from workflows_e2e import WorkflowHarness, content


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary',type=Path,required=True)
    parser.add_argument('--chromium')
    parser.add_argument('--report',type=Path,required=True)
    args=parser.parse_args();checks=[];held_cases=[];errors=[];metrics={};browser_reads=[]
    def done(name):checks.append(name);print('PASS '+name,flush=True)
    with tempfile.TemporaryDirectory(prefix='folio-history-pages-') as tmp:
        h=WorkflowHarness(args.binary.resolve(),Path(tmp))
        try:
            server=h.start('instance',pause=True)
            def call(op,payload=None):return h.operation(server,op,payload)['data']
            def create(slug,n=18,long=False):
                padding='Fictional saved text.\n'*2400 if long else 'Fictional body.\n'
                p=call('posts.create',{'title':'Fictional '+slug,'slug':slug,'markdown':'HISTORY_PRIVATE_R1\n'+padding})['post']
                for rev in range(2,n+1):p=call('posts.update',content(p)|{'id':p['id'],'expected_revision':p['revision'],'markdown':'HISTORY_PRIVATE_R'+str(rev)+'\n'+padding})['post']
                return p
            p=create('long-history',24,True)
            anchor={'id':p['id'],'revision':p['revision'],'limit':8}
            before=call('backup.export')['state'];legacy=call('posts.get',{'id':p['id']})
            light=call('posts.get',{'id':p['id'],'include_revisions':False});page=call('posts.history',anchor)
            require(len(legacy['revisions'])==24 and 'revision_count' not in legacy,'legacy response changed')
            require(light['revisions']==[] and light['revision_count']==24 and all(light[k]==legacy[k] for k in ['post','live','html']),'light current/live/render mismatch')
            metrics={'legacy_get_bytes':len(encode(legacy)),'light_get_bytes':len(encode(light)),'eight_row_page_bytes':len(encode(page)),'history_rows':24}
            require(metrics['legacy_get_bytes']>metrics['light_get_bytes']*8 and metrics['eight_row_page_bytes']<4096,'unbounded transport')
            require('HISTORY_PRIVATE_' not in encode(page).decode() and all(set(r)=={'revision','created_at','updated_at','current','published'} for r in page['revisions']),'body or metadata-field leak')
            require(call('posts.get',{'id':p['id'],'include_revisions':True})==legacy,'explicit legacy mode changed')
            full_preview=call('posts.preview',{'id':p['id'],'revision':1});light_preview=call('posts.preview',{'id':p['id'],'revision':1,'include_revisions':False})
            require(light_preview['revision_count']==24 and light_preview['revisions']==[] and all(light_preview[k]==full_preview[k] for k in ['post','live','html']),'light historical preview changed selected snapshot')
            one=call('posts.history',anchor|{'limit':1});maximum=call('posts.history',anchor|{'limit':50})
            require([r['revision'] for r in one['revisions']]==[24] and one['next_before_revision']==24 and one['has_more'] and len(maximum['revisions'])==24 and not maximum['has_more'],'inclusive page-size boundaries')
            done('legacy full reads unchanged; opt-in light current/live/HTML identical; eight metadata rows below 4 KiB')
            require(h.cli(['--lang','en','call','posts.history','--json',encode(anchor).decode()],server)['data']==page,'CLI page mismatch')
            require(h.cli(['--lang','en','call','posts.get','--json',encode({'id':p['id'],'include_revisions':False}).decode()],server)['data']==light,'CLI light-read mismatch')
            mcp=MCP(h,server,locale='en');tools=mcp.request('tools/list',{})['tools'];tool=next(t for t in tools if t['name']=='posts_history')
            require(len(tools)==37 and tool['annotations']['readOnlyHint'] and tool['inputSchema']['additionalProperties'] is False and tool['inputSchema']['properties']['limit']['maximum']==50,'37-operation schema')
            require(mcp.call('posts.history',anchor)['data']==page,'MCP page mismatch')
            require(mcp.call('posts.get',{'id':p['id'],'include_revisions':False})['data']==light,'MCP light-read mismatch');mcp.close()
            rows=[];cursor=0
            while True:
                result=call('posts.history',anchor|({'before_revision':cursor} if cursor else {}))
                rows.extend(r['revision'] for r in result['revisions'])
                if not result['has_more']:require(result['next_before_revision']==0,'last-page cursor');break
                cursor=result['next_before_revision'];require(cursor==result['revisions'][-1]['revision'],'exclusive cursor mismatch')
            require(rows==list(range(24,0,-1)),'missing/duplicate/order error')
            require(call('posts.history',anchor)==page and call('backup.export')['state']==before,'reads/repeats wrote state')
            require(call('posts.history',anchor|{'before_revision':1})['revisions']==[],'empty tail error')
            done('37-operation HTTP/CLI/MCP parity; default/explicit cursor pages, stable descending order and zero writes')
            for token in [server['token'],server['draft'],server['read'],server['proposal']]:require(h.operation(server,'posts.history',anchor,token=token)['data']==page,'private role mismatch')
            for token in ['', 'invalid']:
                a=h.operation(server,'posts.history',anchor,token=token,status=401)
                b=h.operation(server,'posts.history',anchor|{'id':'unknown'},token=token,status=401)
                require(a==b,'anonymous existence oracle')
            for extra in [{'limit':0},{'limit':51},{'limit':None},{'limit':1.5},{'before_revision':0},{'before_revision':25},{'before_revision':None},{'markdown':'injected'},{'revision':0}]:h.operation(server,'posts.history',anchor|extra,status=400)
            req=urllib.request.Request(server['url']+'/api/op/posts.history',data=encode(anchor),headers={'Content-Type':'application/json','Authorization':'Bearer '+server['token'],'Origin':'https://evil.example'})
            try:urllib.request.urlopen(req,timeout=5)
            except urllib.error.HTTPError as e:require(e.code==403,'origin boundary')
            else:raise AssertionError('cross-origin read allowed')
            h.assert_private(server,p,'HISTORY_PRIVATE_R1')
            newer=call('posts.update',content(p)|{'id':p['id'],'expected_revision':24,'markdown':'HISTORY_PRIVATE_NEW_HEAD'})['post']
            h.operation(server,'posts.history',anchor|{'before_revision':17},status=409)
            done('private roles, anonymous/origin/strict bounds, public privacy and changed-head conflict')
            if args.chromium:
                from playwright.sync_api import sync_playwright,expect
                with sync_playwright() as pw:
                    browser=pw.chromium.launch(executable_path=args.chromium,args=['--no-sandbox'])
                    def context(post,role='token',width=1100,locale='en'):
                        ctx=browser.new_context(viewport={'width':width,'height':850});q=ctx.new_page();q.on('pageerror',lambda e:errors.append(str(e)));q.on('request',lambda r:browser_reads.append(r.post_data_json) if r.url.endswith('/api/op/posts.get') or r.url.endswith('/api/op/posts.preview') else None)
                        q.goto(server['url']+'/studio');q.evaluate('(v)=>window.FolioI18n.setLocale(v)',locale)
                        q.locator('#access-token').fill(server[role]);q.locator('#login-form button[type="submit"]').click();expect(q.locator('.studio-sidebar')).to_be_visible()
                        q.locator('a.post-title[href="/studio/posts/'+post['id']+'"]').click();expect(q.locator('#post-markdown')).to_have_value(post['markdown']);expect(q.locator('#revision-history .revision-row').first).to_have_attribute('data-revision',str(post['revision']))
                        return ctx,q
                    def values(q):return q.locator('#revision-history [data-revision]').evaluate_all('(es)=>es.map(e=>Number(e.dataset.revision))')
                    def hold(q,post):
                        held=[];requests=[]
                        def handler(route):
                            payload=route.request.post_data_json
                            if payload['id']==post['id'] and payload.get('before_revision'):
                                requests.append(payload);held.append((route,route.fetch()))
                            else:route.continue_()
                        q.route('**/api/op/posts.history',handler);return held,requests,handler
                    def waitheld(q,held,count=1):
                        deadline=time.monotonic()+8
                        while len(held)<count and time.monotonic()<deadline:q.wait_for_timeout(25)
                        require(len(held)>=count,'real history response not held')
                    def release(held):
                        route,response=held.pop(0);route.fulfill(response=response)
                    for width,locale in [(320,'en'),(390,'zh-CN')]:
                        ctx,q=context(newer,width=width,locale=locale);requests=[]
                        q.on('request',lambda r:requests.append(r.post_data_json) if r.url.endswith('/api/op/posts.get') or r.url.endswith('/api/op/posts.preview') else None)
                        original=q.locator('#post-markdown').input_value();q.locator('#post-markdown').fill(original+'\nUNSAVED_KEEP');q.locator('#post-markdown').evaluate('(e)=>e.setSelectionRange(3,9)')
                        q.locator('[data-revision-page="older"]').click();expect(q.locator('[data-revision="17"]')).to_have_count(1)
                        q.evaluate('(v)=>window.FolioI18n.setLocale(v)','zh-CN' if locale=='en' else 'en')
                        require(values(q)==list(range(17,9,-1)) and q.locator('#post-markdown').input_value()==original+'\nUNSAVED_KEEP' and q.locator('#post-markdown').evaluate('(e)=>[e.selectionStart,e.selectionEnd]')==[3,9],'page/locale discarded input')
                        require(q.evaluate('document.documentElement.scrollWidth<=innerWidth'),'mobile overflow')
                        q.locator('[data-revision-page="newer"]').click();expect(q.locator('[data-revision="25"]')).to_have_count(1)
                        require(values(q)==list(range(25,17,-1)) and q.locator('#post-markdown').input_value()==original+'\nUNSAVED_KEEP' and q.locator('#post-markdown').evaluate('(e)=>[e.selectionStart,e.selectionEnd]')==[3,9],'newer page lost its cursor or editor input')
                        q.locator('[data-revision-page="older"]').click();expect(q.locator('[data-revision="17"]')).to_have_count(1)
                        q.locator('[data-revision-page="older"]').click();expect(q.locator('[data-revision="9"]')).to_have_count(1)
                        q.locator('[data-revision-page="older"]').click();expect(q.locator('[data-revision="1"]')).to_have_count(1)
                        q.locator('[data-compare="1"]').click();expect(q.locator('#revision-result .revision-snapshots')).to_be_visible();expect(q.locator('#confirm-revision')).to_be_disabled()
                        q.keyboard.press('Escape');expect(q.locator('#post-markdown')).to_have_value(original+'\nUNSAVED_KEEP');require(all(r.get('include_revisions') is False for r in requests),'unbounded Studio read');ctx.close()
                    done('real server pages through oldest revision; EN/CN 320/390px, dirty input/caret, locale and reviewed dirty-restore guard')
                    for role in ['read','proposal']:
                        ctx,q=context(newer,role=role);q.locator('[data-revision-page="older"]').click();expect(q.locator('[data-revision="17"]')).to_have_count(1)
                        require(q.locator('#revision-history [data-restore]').count()==0,'pagination expanded restore role');ctx.close()
                    done('read/proposal Studio pagination with no restore authority')
                    owned=create('owned-history')
                    ctx,q=context(owned);held,requests,handler=hold(q,owned);initial=values(q)
                    q.evaluate('()=>{const b=document.querySelector("[data-revision-page=older]");b.click();b.dispatchEvent(new MouseEvent("click",{bubbles:true}));}')
                    waitheld(q,held);require(len(requests)==1,'duplicate click issued duplicate request')
                    q.locator('#post-markdown').fill('UNSAVED_CANCEL_KEEP');q.locator('#post-markdown').evaluate('(e)=>e.setSelectionRange(2,7)');q.locator('[data-action="history-cancel"]').click();release(held);q.wait_for_timeout(100)
                    require(values(q)==initial and q.locator('#post-markdown').input_value()=='UNSAVED_CANCEL_KEEP' and q.locator('#post-markdown').evaluate('(e)=>[e.selectionStart,e.selectionEnd]')==[2,7],'cancelled late response changed editor/page')
                    held_cases.append({'case':'duplicate/cancel','requests':len(requests),'late_discarded':True});q.unroute('**/api/op/posts.history',handler);ctx.close()
                    ctx,q=context(owned);held,requests,handler=hold(q,owned);q.locator('[data-revision-page="older"]').click();waitheld(q,held)
                    q.locator('#post-markdown').fill('NEW_LOCAL_SAVED_HEAD');q.locator('[data-action="save"]').click();expect(q.locator('#save-state')).to_have_text('All changes saved');expect(q.locator('[data-revision="19"]')).to_have_count(1)
                    release(held);q.wait_for_timeout(100);require(values(q)==list(range(19,11,-1)) and q.locator('#post-markdown').input_value()=='NEW_LOCAL_SAVED_HEAD','old page replaced locally saved head');expect(q.locator('#revision-count')).to_have_text('19')
                    held_cases.append({'case':'save while held','late_discarded':True});q.unroute('**/api/op/posts.history',handler);ctx.close()
                    switch=create('switch-history');other=create('other-history',1)
                    for action in ['switch','lock']:
                        ctx,q=context(switch);held,requests,handler=hold(q,switch);q.locator('[data-revision-page="older"]').click();waitheld(q,held)
                        if action=='switch':q.locator('.studio-nav a[href="/studio"]').click();q.locator('a.post-title[href="/studio/posts/'+other['id']+'"]').click();expect(q.locator('#post-markdown')).to_have_value(other['markdown'])
                        else:q.once('dialog',lambda dialog:dialog.accept());q.locator('.studio-sidebar [data-action="logout"]').click();expect(q.locator('#login-form')).to_be_visible()
                        release(held);q.wait_for_timeout(100)
                        if action=='switch':require(values(q)==[1] and q.locator('#post-title').input_value()==other['title'],'old article page replaced current article')
                        else:require(q.locator('#post-markdown').count()==0 and q.locator('#login-form').is_visible(),'late private page reopened locked editor')
                        held_cases.append({'case':action,'late_discarded':True});ctx.close()
                    ctx,q=context(switch);held,requests,handler=hold(q,switch);q.locator('[data-revision-page="older"]').click();waitheld(q,held)
                    q.go_back();expect(q.locator('#post-rows')).to_be_visible();release(held);q.wait_for_timeout(100)
                    require(q.locator('#post-markdown').count()==0,'late page reopened editor after Back');held_cases.append({'case':'Back','late_discarded':True});ctx.close()
                    ctx,q=context(switch);held,requests,handler=hold(q,switch);q.locator('[data-revision-page="older"]').click();waitheld(q,held)
                    q.locator('#post-markdown').fill('UNSAVED_BACK_KEEP');q.once('dialog',lambda dialog:dialog.dismiss());q.go_back();expect(q.locator('#post-markdown')).to_have_value('UNSAVED_BACK_KEEP');release(held);q.wait_for_timeout(100)
                    require(q.url.endswith('/studio/posts/'+switch['id']) and q.locator('#post-markdown').input_value()=='UNSAVED_BACK_KEEP','denied Back lost editor');held_cases.append({'case':'denied Back','buffer_preserved':True});ctx.close()
                    errorpost=create('held-error-history');ctx,q=context(errorpost);held=[]
                    def hold_error(route):
                        if route.request.post_data_json['id']==errorpost['id'] and route.request.post_data_json.get('before_revision'):
                            call('posts.update',content(errorpost)|{'id':errorpost['id'],'expected_revision':18,'markdown':'EXTERNAL_HELD_ERROR'})
                            held.append((route,route.fetch()))
                        else:route.continue_()
                    q.route('**/api/op/posts.history',hold_error);q.locator('[data-revision-page="older"]').click();waitheld(q,held)
                    q.locator('.studio-nav a[href="/studio"]').click();q.locator('a.post-title[href="/studio/posts/'+other['id']+'"]').click();expect(q.locator('#post-markdown')).to_have_value(other['markdown']);release(held);q.wait_for_timeout(100)
                    require(not q.locator('[data-action="history-reopen"]').count() and values(q)==[1],'late error contaminated current article');held_cases.append({'case':'late real conflict after switch','late_discarded':True});ctx.close()
                    ctx,q=context(switch);wrong=[];original_rows=values(q)
                    def wrong_identity(route):
                        if route.request.post_data_json['id']==switch['id'] and route.request.post_data_json.get('before_revision'):
                            wrong.append(route.request.post_data_json)
                            response=route.fetch(post_data=encode({'id':other['id'],'revision':1,'limit':8}).decode());route.fulfill(response=response)
                        else:route.continue_()
                    q.route('**/api/op/posts.history',wrong_identity);q.locator('#post-markdown').fill('UNSAVED_IDENTITY_KEEP');q.locator('[data-revision-page="older"]').click();expect(q.locator('[data-revision-page="retry"]')).to_be_visible()
                    require(values(q)==original_rows and q.locator('#post-markdown').input_value()=='UNSAVED_IDENTITY_KEEP' and len(wrong)==1,'wrong real article response applied')
                    q.unroute('**/api/op/posts.history',wrong_identity);q.locator('[data-revision-page="retry"]').click();expect(q.locator('[data-revision="10"]')).to_have_count(1)
                    require(q.locator('#post-markdown').input_value()=='UNSAVED_IDENTITY_KEEP','read retry discarded buffer');held_cases.append({'case':'wrong real response/retry','identity_rejected':True,'buffer_preserved':True});ctx.close()
                    stale=create('stale-history');ctx,q=context(stale);q.locator('#post-markdown').fill('UNSAVED_EXTERNAL_KEEP')
                    call('posts.update',content(stale)|{'id':stale['id'],'expected_revision':18,'markdown':'EXTERNAL_NEW_HEAD'})
                    oldrows=values(q);q.locator('[data-revision-page="older"]').click();expect(q.locator('[data-action="history-reopen"]')).to_be_visible()
                    require(values(q)==oldrows and q.locator('#post-markdown').input_value()=='UNSAVED_EXTERNAL_KEEP','stale read replaced buffer')
                    q.once('dialog',lambda dialog:dialog.dismiss());q.locator('[data-action="history-reopen"]').click();expect(q.locator('#post-markdown')).to_have_value('UNSAVED_EXTERNAL_KEEP');ctx.close()
                    done('held real pages: duplicate suppression, Cancel, local new revision, article switch/Lock; external writer conflict preserves buffer and denied reopen')
                    require(browser_reads and all(r.get('include_revisions') is False for r in browser_reads),'unbounded Studio get/preview');metrics['bounded_studio_reads']=len(browser_reads);require(not errors,'browser errors');browser.close()
            h.check_no_secrets();done('no credentials in captured daemon/CLI/MCP/public output; all owned resources disposable')
        finally:h.close()
    args.report.parent.mkdir(parents=True,exist_ok=True);args.report.write_text(json.dumps({'checks':checks,'held_cases':held_cases,'browser_errors':errors,'transport':metrics,'passed':True},indent=2)+'\n')
    print(json.dumps({'checks':len(checks),'held_cases':len(held_cases),'browser_errors':len(errors),'transport':metrics}),flush=True)

if __name__=='__main__':main()

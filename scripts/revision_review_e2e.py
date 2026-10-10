#!/usr/bin/env python3
"""Saved revision review on a real daemon, CLI, MCP and Chromium; disposable data."""
import argparse
from datetime import datetime, timedelta, timezone
import json
import hashlib
from pathlib import Path
import secrets
import tempfile
import urllib.error
import urllib.request
from e2e import Harness, MCP, require

FIELDS = ['title','slug','markdown','excerpt','tags','category','cover','featured']

def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary',type=Path,required=True)
    parser.add_argument('--chromium')
    parser.add_argument('--report',type=Path,required=True)
    parser.add_argument('--restore-race-only',action='store_true')
    parser.add_argument('--published-restore-only',action='store_true')
    parser.add_argument('--preview-restore-only',action='store_true')
    args=parser.parse_args();checks=[];held_cases=[];derived_cases=[];errors=[]
    def done(name):checks.append(name);print('PASS '+name,flush=True)
    def report():
        args.report.parent.mkdir(parents=True,exist_ok=True)
        args.report.write_text(json.dumps({'checks':checks,'held_cases':held_cases,'derived_state_cases':derived_cases,'browser_errors':errors},indent=2)+'\n')
    try:
        with tempfile.TemporaryDirectory(prefix='folio-revisions-') as tmp:
            h=Harness(args.binary.resolve(),Path(tmp));proposal=secrets.token_hex(32)
            h.base_env['FOLIO_PROPOSAL_TOKEN']=proposal;h.secrets.append(proposal)
            try:
                server=h.start('instance');base=server['url']
                def http(op,payload=None,token=None,status=200,locale='en',origin=None,target=None):
                    peer=target or server
                    headers={'Content-Type':'application/json','Authorization':'Bearer '+(peer['token'] if token is None else token),'Accept-Language':locale}
                    if origin:headers['Origin']=origin
                    req=urllib.request.Request(peer['url']+'/api/op/'+op,data=json.dumps(payload or {}).encode(),headers=headers)
                    try:response=urllib.request.urlopen(req,timeout=15)
                    except urllib.error.HTTPError as e:response=e
                    with response:
                        require(response.status==status,op+' HTTP '+str(response.status));return json.load(response)
                def call(op,payload=None):return http(op,payload)['data']
                def create(slug):return call('posts.create',{'title':'Fictional '+slug,'slug':slug,'markdown':'# Fictional original\n\n中文 original','excerpt':'First summary','tags':['first'],'category':'First category','cover':'/media/fictional-first.png','featured':False})['post']
                def update(p,**changes):return call('posts.update',{k:p[k] for k in FIELDS}|changes|{'id':p['id'],'expected_revision':p['revision']})['post']
                def pair(slug):
                    old=create(slug);call('posts.publish',{'id':old['id'],'expected_revision':1,'confirm':True})
                    new=update(old,title='Fictional new '+slug,slug=slug+'-new',markdown='# Fictional second\n\n中文 second',excerpt='Second summary',tags=['second'],category='Second category',cover='/media/fictional-second.png',featured=True)
                    return old,new
                def payload(p,to=1,source=None):return {'id':p['id'],'revision':p['revision'],'from_revision':source or p['revision'],'to_revision':to}
                old,current=pair('api-review')
                if not args.restore_race_only:
                    before=call('backup.export')['state'];api=http('posts.compare',payload(current));r=api['data']
                    require(r['from']['content']=={k:current[k] for k in FIELDS} and r['to']['content']=={k:old[k] for k in FIELDS},'incomplete snapshots')
                    require({f['field'] for f in r['review']['changed_fields']}==set(FIELDS) and r['restorable'] and r['from']['current'] and r['to']['published'],'field/version metadata')
                    require(h.cli(['--lang','en','call','posts.compare','--json',json.dumps(payload(current))],server)==api,'CLI parity')
                    mcp=MCP(h,server,locale='en');tools=mcp.request('tools/list',{})['tools'];tool=next(t for t in tools if t['name']=='posts_compare')
                    require(len(tools)==36 and tool['annotations']['readOnlyHint'] and tool['inputSchema']['additionalProperties'] is False and set(tool['inputSchema']['required'])=={'id','revision','from_revision','to_revision'},'36-operation schema')
                    require(mcp.call('posts.compare',payload(current))==api,'MCP parity');mcp.close()
                    require(call('backup.export')['state']==before and http('posts.compare',payload(current))==api,'comparison/retry changed state')
                    reverse=call('posts.compare',payload(current,to=2,source=1));require(reverse['review']['changed_fields'][0]['before']==old['title'],'direction reversed')
                    done('36-operation HTTP/CLI/MCP parity; all eight exact fields, source/live metadata, directional review and zero state writes')
                    for token in [server['token'],server['draft'],server['read'],proposal]:http('posts.compare',payload(current),token=token)
                    for token in ['', 'invalid']:
                        require(http('posts.compare',payload(current),token=token,status=401)==http('posts.compare',{'id':'unknown','revision':1,'from_revision':1,'to_revision':1},token=token,status=401),'existence oracle')
                    http('posts.compare',payload(current),origin='https://evil.example',status=403)
                    for malformed in [{},payload(current)|{'to_revision':0},payload(current)|{'from_revision':-1},payload(current)|{'markdown':'injected'},payload(current)|{'id':''}]:http('posts.compare',malformed,status=400)
                    http('posts.compare',payload(current)|{'revision':1},status=409);http('posts.compare',payload(current,to=99),status=404)
                    require('版本比较' in http('posts.compare',{},status=400,locale='zh-CN')['error']['message'],'Chinese validation')
                    for token in [server['read'],proposal]:http('posts.restore',{},token=token,status=403)
                    done('private-read roles, forbidden restore, anonymous/origin/strict schema/stale/missing guards and Chinese errors')
                    schedule=call('schedules.create',{'post_id':current['id'],'expected_revision':2,'publish_at':(datetime.now(timezone.utc)+timedelta(days=1)).isoformat(),'confirm':True})['schedule']
                    restore={'id':current['id'],'revision':1,'expected_revision':2,'idempotency_key':'api-exact-restore'}
                    first=call('posts.restore',restore);require(call('posts.restore',restore)==first,'retry receipt mismatch')
                    latest=call('posts.get',{'id':current['id']});require(latest['post']['revision']==3 and len(latest['revisions'])==3 and latest['live']['revision']==1,'duplicate restore/live drift')
                    require(call('schedules.get',{'id':schedule['id']})['schedule']==schedule,'scheduled revision drift')
                    require(not call('posts.compare',payload(latest['post']))['restorable'],'duplicate content restore available')
                    changed=update(latest['post'],markdown='Fictional later writer');http('posts.restore',restore|{'idempotency_key':'different-key'},status=409)
                    require(call('posts.get',{'id':changed['id']})['post']==changed,'stale restore overwrote writer')
                    done('restore retry creates one private revision; current/live/schedule isolation, no-op advisory and intervening-writer CAS')
                    if not args.published_restore_only and not args.preview_restore_only:
                        first=call('posts.create',{'title':'Fictional legacy budget','slug':'legacy-budget','markdown':'Fictional first'})['post'];small=update(first,markdown='Fictional current')
                        legacy=call('backup.export');legacy['state']['posts'][first['id']]['revisions'][0]['cover']='/media/'+'a'*(40*1024*1024)
                        canonical=json.dumps(legacy['state'],ensure_ascii=False,separators=(',',':')).replace('<','\\u003c').replace('>','\\u003e').replace('&','\\u0026').replace('\u2028','\\u2028').replace('\u2029','\\u2029').encode()
                        legacy['sha256']=hashlib.sha256(canonical).hexdigest();require(len(json.dumps(legacy).encode())<64*1024*1024,'legacy fixture exceeds backup bound')
                        imported=h.start('legacy-budget-import');http('backup.restore',{'backup':legacy,'confirm':True},target=imported)
                        q=payload(small,to=1,source=1);error=http('posts.compare',q,status=400,target=imported)
                        require(error['error']['code']=='validation' and '64 MiB JSON budget' in error['error']['message'],'missing controlled response budget')
                        require(h.cli(['--lang','en','call','posts.compare','--json',json.dumps(q)],imported,expect_ok=False)==error,'budget CLI parity')
                        mcp=MCP(h,imported,locale='en');require(mcp.call('posts.compare',q,expect_ok=False)==error,'budget MCP parity');mcp.close()
                        require('JSON 预算' in http('posts.compare',q,status=400,locale='zh-CN',target=imported)['error']['message'],'budget Chinese error')
                        require(http('posts.compare',payload(small,to=2),target=imported)['ok'],'bounded current comparison denied')
                        after=http('backup.export',target=imported)['data'];require(after['state']['posts'][first['id']]['revisions'][0]['cover']==legacy['state']['posts'][first['id']]['revisions'][0]['cover'],'rejected read changed legacy data')
                        done('valid 40 MiB historical cover backup imports/exports unchanged; oversized comparison returns bounded EN/CN HTTP/CLI/MCP errors while current comparison works')
                if args.chromium:
                    from playwright.sync_api import sync_playwright,expect
                    with sync_playwright() as pw:
                        browser=pw.chromium.launch(executable_path=args.chromium,headless=True)
                        def login(p,post,token=None):
                            p.on('pageerror',lambda e:errors.append(str(e)));p.on('dialog',lambda d:d.accept())
                            p.goto(base+'/studio');expect(p.locator('#login-form')).to_be_visible();p.evaluate("window.FolioI18n.setLocale('en')")
                            p.locator('#access-token').fill(token or server['token']);p.locator('#login-form button[type="submit"]').click();expect(p.locator('.post-table')).to_be_visible()
                            p.locator('a[href="/studio/posts/'+post['id']+'"]').first.click();expect(p.locator('#post-markdown')).to_have_value(post['markdown'])
                        def review(p,revision=1):
                            p.locator('[data-restore="'+str(revision)+'"], [data-compare="'+str(revision)+'"]').first.click()
                            if not args.restore_race_only:expect(p.locator('#revision-result .revision-snapshots')).to_be_visible()
                            expect(p.locator('#confirm-revision')).to_be_enabled()
                        def waitheld(p,held,n=1):
                            for _ in range(200):
                                if len(held)>=n:return
                                p.wait_for_timeout(10)
                            raise AssertionError('actual response not held')
                        def release(p,held,pattern):
                            route,response=held.pop(0);route.fulfill(response=response);p.unroute(pattern);p.wait_for_timeout(100)
                        # Published R2 -> restored private R3 must refresh derived status without replacing the editor.
                        if not args.restore_race_only and not args.preview_restore_only:
                            old,published=pair('published-status-restore');published=call('posts.publish',{'id':published['id'],'expected_revision':2,'confirm':True})['post']
                            ctx=browser.new_context();p=ctx.new_page();login(p,published);expect(p.locator('.editor-sidebar > .panel-label .badge')).to_have_text('Published');review(p)
                            p.locator('#confirm-revision').click();expect(p.locator('#modal-root')).to_be_empty();expect(p.locator('#save-state')).to_have_text('All changes saved')
                            latest=call('posts.get',{'id':published['id']});badge=p.locator('.editor-sidebar > .panel-label .badge').text_content().strip();safe=badge=='Unpublished edits' and latest['post']['revision']==3 and latest['post']['status']=='changed' and latest['live']['revision']==2
                            derived_cases.append({'case':'published R2 to restored private R3','badge_text':badge,'server_status':latest['post']['status'],'badge_matches_server':safe});report()
                            require(safe,'published restore left stale publication badge')
                            for field in FIELDS:
                                el=p.locator('#post-'+field);actual=el.is_checked() if field=='featured' else el.input_value()
                                require(actual==(old[field] if field!='tags' else ', '.join(old[field])),'published restore field '+field)
                            ctx.close();done('published R2 to private restored R3 refreshes status badge; all eight fields restored and live R2 unchanged')
                            if args.published_restore_only:browser.close();return
                        if not args.restore_race_only:
                            old,preview=pair('preview-restore');ctx=browser.new_context();p=ctx.new_page();login(p,preview);p.locator('[data-editor-mode="preview"]').click();expect(p.locator('#preview-content')).to_contain_text('Fictional second');review(p)
                            p.locator('#confirm-revision').click();expect(p.locator('#modal-root')).to_be_empty();safe=p.locator('#write-panel').is_visible() and not p.locator('#preview-panel').is_visible() and p.locator('#preview-content').inner_html()=='' and p.locator('#post-markdown').input_value()==old['markdown']
                            derived_cases.append({'case':'preview R2 then restore R1','write_view_and_content_match_saved_revision':safe});report();require(safe,'restore left the earlier revision preview visible');ctx.close();done('preview R2 then restore R1 returns to Write with exact restored source and clears stale HTML')
                            if args.preview_restore_only:browser.close();return
                            old,preview=pair('late-preview-restore');ctx=browser.new_context();p=ctx.new_page();login(p,preview);held=[]
                            p.route('**/api/op/posts.preview',lambda route:held.append((route,route.fetch())));p.locator('[data-editor-mode="preview"]').click();waitheld(p,held);review(p);p.locator('#confirm-revision').click();expect(p.locator('#modal-root')).to_be_empty();release(p,held,'**/api/op/posts.preview')
                            require(p.locator('#write-panel').is_visible() and p.locator('#preview-content').inner_html()=='' and p.locator('#post-markdown').input_value()==old['markdown'],'old preview reopened after restore')
                            held_cases.append({'case':'held R2 preview after R3 restore','obsolete_preview_discarded':True});ctx.close();done('held real R2 preview is discarded after acknowledged R3 restore without reopening Preview')
                        # The old handler commits then rerenders, silently dropping later typing.
                        old,pairpost=pair('late-restore');ctx=browser.new_context();p=ctx.new_page();login(p,pairpost);review(p);held=[]
                        p.route('**/api/op/posts.restore',lambda route:held.append((route,route.fetch())))
                        p.locator('#confirm-revision').click();waitheld(p,held)
                        text='Fictional typing while restore acknowledgement is delayed';p.locator('#post-markdown').fill(text);p.locator('#post-markdown').evaluate('(e)=>e.setSelectionRange(3,8)')
                        release(p,held,'**/api/op/posts.restore')
                        p.wait_for_timeout(500)
                        if not p.locator('#post-markdown').count():
                            args.report.with_suffix('.screen.txt').write_text(p.locator('body').inner_text())
                        safe=bool(p.locator('#post-markdown').count()) and p.locator('#post-markdown').input_value()==text and p.locator('#post-markdown').evaluate('(e)=>[e.selectionStart,e.selectionEnd]')==[3,8] and p.locator('#save-state').inner_text()=='Unsaved changes'
                        held_cases.append({'case':'typing during committed restore acknowledgement','buffer_caret_dirty_preserved':safe});report()
                        require(safe,'late restore acknowledgement discarded newer input or caret')
                        p.locator('[data-action="save"]').click();expect(p.locator('#save-state')).to_have_text('All changes saved');require(call('posts.get',{'id':pairpost['id']})['post']['markdown']==text,'later save used wrong revision')
                        ctx.close();done('actual committed restore acknowledgement preserves newer typing/caret; subsequent Save uses acknowledged revision')
                        if not args.restore_race_only:
                            old,clean=pair('clean-draft-restore');ctx=browser.new_context();p=ctx.new_page();login(p,clean,server['draft']);review(p)
                            p.locator('#revision-from').select_option('1');expect(p.locator('#revision-result')).to_contain_text('R1 → R1');expect(p.locator('#confirm-revision')).to_be_disabled()
                            p.locator('#revision-from').select_option('2');expect(p.locator('#revision-result')).to_contain_text('R2 → R1');expect(p.locator('#confirm-revision')).to_be_enabled();p.locator('#confirm-revision').click();expect(p.locator('#modal-root')).to_be_empty()
                            for field in FIELDS:
                                el=p.locator('#post-'+field);actual=el.is_checked() if field=='featured' else el.input_value()
                                require(actual==(old[field] if field!='tags' else ', '.join(old[field])),'clean restore field '+field)
                            expect(p.locator('#save-state')).to_have_text('All changes saved');p.locator('[data-compare="1"]').click();expect(p.locator('#revision-result .revision-snapshots')).to_be_visible();expect(p.locator('#confirm-revision')).to_be_disabled();ctx.close()
                            done('draft-role explicit clean restore copies all eight fields; historical source and same-content duplicate restores stay blocked')
                            # History exceeds the former eight-row cutoff; older rows remain reachable.
                            oldest,mobile=pair('history-pages')
                            for n in range(3,12):mobile=update(mobile,markdown='Fictional revision '+str(n))
                            mobile=update(mobile,title='<img src=x onerror=window.revisionXSS=1>',markdown='<script>window.revisionXSS=1</script>\n![hidden](https://media.example/secret.png)')
                            for width in [320,390]:
                                for locale in ['en','zh-CN']:
                                    ctx=browser.new_context(viewport={'width':width,'height':844});p=ctx.new_page();login(p,mobile);p.evaluate('(v)=>window.FolioI18n.setLocale(v)',locale)
                                    p.locator('[data-revision-page="older"]').click();require(p.locator('[data-revision="1"]').count()==1,'old history inaccessible')
                                    p.locator('[data-compare="1"]').click();expect(p.locator('#revision-result .revision-snapshots')).to_be_visible();p.locator('#revision-result details').evaluate_all('(es)=>es.forEach(e=>e.open=true)')
                                    require(p.locator('#revision-result .revision-snapshots details').count()==16,'eight fields per side')
                                    require(p.locator('#revision-result img, #revision-result script').count()==0 and not p.evaluate('window.revisionXSS||false'),'snapshot executed')
                                    require(p.evaluate('document.documentElement.scrollWidth<=innerWidth') and p.locator('.modal').evaluate('(e)=>e.scrollWidth<=e.clientWidth'),'mobile overflow')
                                    p.locator('#post-markdown').evaluate('(e)=>e.setSelectionRange(3,8)');p.evaluate('(v)=>window.FolioI18n.setLocale(v)', 'en' if locale=='zh-CN' else 'zh-CN')
                                    expect(p.locator('#revision-result .revision-snapshots summary').first).to_have_text('Title' if locale=='zh-CN' else '标题')
                                    require(p.locator('#revision-to').input_value()=='1' and p.locator('#post-markdown').evaluate('(e)=>[e.selectionStart,e.selectionEnd]')==[3,8],'locale reset selectors/caret')
                                    p.evaluate('(v)=>window.FolioI18n.setLocale(v)',locale)
                                    expect(p.locator('#revision-result .revision-snapshots summary').first).to_have_text('Title' if locale=='en' else '标题')
                                    require(('Current saved draft' if locale=='en' else '当前已保存草稿') in p.locator('#revision-from option:checked').inner_text(),'locale left stale option label')
                                    p.screenshot(path=str(args.report.with_name('revision-'+str(width)+'-'+locale+'.png')),full_page=False);ctx.close()
                            done('paged older history; all eight escaped fields; EN/CN 320/390px and locale preserves selectors/editor/caret')
                            for role in ['read','proposal']:
                                ctx=browser.new_context();p=ctx.new_page();login(p,mobile,proposal if role=='proposal' else server[role]);p.locator('[data-compare="11"]').click();expect(p.locator('#revision-result .revision-snapshots')).to_be_visible()
                                require(not p.locator('#confirm-revision').count() and 'Read-only' in p.locator('#revision-restore-message').inner_text(),'read role restore authority/status');ctx.close()
                            done('read/proposal Studio roles compare without a restore control; draft restore scope unchanged')
                            ctx=browser.new_context();p=ctx.new_page();login(p,mobile);p.locator('#post-markdown').fill('Fictional unsaved');p.locator('[data-compare="11"]').click();expect(p.locator('#revision-result .revision-snapshots')).to_be_visible();expect(p.locator('#confirm-revision')).to_be_disabled()
                            require('Save or export' in p.locator('#revision-restore-message').inner_text(),'dirty restore guard');p.keyboard.press('Escape');expect(p.locator('#post-markdown')).to_have_value('Fictional unsaved');ctx.close()
                            done('comparison excludes unsaved buffer; Restore stays disabled and Cancel preserves unsaved text')
                            # Hold real successful/error comparisons through every input and dialog/session transition.
                            for destination in FIELDS+['edit-revert','cancel','escape','backdrop','command','other','new','lock','back-return','error-cancel']:
                                ctx=browser.new_context();p=ctx.new_page();login(p,mobile);held=[];writes=[]
                                p.on('request',lambda req:writes.append(req.url) if req.url.endswith(('/posts.create','/posts.update','/posts.restore','/posts.publish','/schedules.create')) else None)
                                def hold(route):
                                    response=route.fetch(post_data=json.dumps(payload(mobile,to=999))) if destination=='error-cancel' else route.fetch()
                                    held.append((route,response))
                                p.route('**/api/op/posts.compare',hold);p.locator('[data-compare="11"]').click();waitheld(p,held)
                                if destination in FIELDS+['edit-revert']:
                                    field='markdown' if destination=='edit-revert' else destination;el=p.locator('#post-'+field)
                                    if field=='featured':el.evaluate('(e)=>{e.checked=!e.checked;e.dispatchEvent(new Event("input",{bubbles:true}));}')
                                    else:el.fill(el.input_value()+' Fictional edit')
                                    if destination=='edit-revert':el.fill(mobile['markdown'])
                                elif destination in ['cancel','error-cancel']:p.locator('#modal-root [data-action="close-modal"]').first.click()
                                elif destination=='escape':p.keyboard.press('Escape')
                                elif destination=='backdrop':p.locator('.modal-backdrop').click(position={'x':2,'y':2})
                                elif destination=='command':p.keyboard.press('Control+k');expect(p.locator('#command-input')).to_be_visible()
                                elif destination=='lock':p.locator('[data-action="logout"]').first.evaluate('(e)=>e.click()');expect(p.locator('#login-form')).to_be_visible()
                                else:
                                    if destination=='back-return':p.evaluate('history.back()')
                                    else:p.locator('.studio-nav a[href="/studio"]').evaluate('(e)=>e.click()')
                                    expect(p.locator('.post-table')).to_be_visible()
                                    p.locator('a[href="/studio/new"]' if destination=='new' else 'a[href="/studio/posts/'+mobile['id']+'"]').first.click();expect(p.locator('#post-markdown')).to_be_visible()
                                current_buffer=p.locator('#post-markdown').input_value() if p.locator('#post-markdown').count() else None
                                modal_before=p.locator('#modal-root').inner_html();release(p,held,'**/api/op/posts.compare')
                                require(not p.locator('#revision-result .revision-snapshots').count() and not writes,'obsolete report/write '+destination)
                                if destination not in FIELDS+['edit-revert']:require(p.locator('#modal-root').inner_html()==modal_before,'obsolete response changed later dialog '+destination)
                                if current_buffer is not None:require(p.locator('#post-markdown').input_value()==current_buffer,'obsolete response changed editor '+destination)
                                held_cases.append({'case':'comparison '+destination,'discarded':True,'writes':0});ctx.close()
                            done('18 held real comparison responses: all fields/revert, cancel/Esc/backdrop/command/new/other/lock/back-return and late error')
                            ctx=browser.new_context();p=ctx.new_page();login(p,mobile);held=[]
                            p.route('**/api/op/posts.compare',lambda route:held.append((route,route.fetch())) if not held else route.continue_())
                            p.locator('[data-compare="11"]').click();waitheld(p,held);p.locator('#revision-to').select_option('10');expect(p.locator('#revision-result')).to_contain_text('R12 → R10')
                            release(p,held,'**/api/op/posts.compare');expect(p.locator('#revision-result')).to_contain_text('R12 → R10');held_cases.append({'case':'newer comparison wins','discarded':True});ctx.close()
                            done('changing comparison while an older response is held preserves the newer selected report')
                            ctx=browser.new_context();p=ctx.new_page();login(p,mobile);p.route('**/api/op/posts.compare',lambda route:route.abort());p.locator('[data-compare="11"]').click();expect(p.locator('#revision-error')).not_to_be_empty();p.unroute('**/api/op/posts.compare');p.locator('#revision-reload').click();expect(p.locator('#revision-result .revision-snapshots')).to_be_visible();ctx.close()
                            done('read failure retries the same comparison without writes or editor replacement')
                            # In-memory exact receipts survive a closed dialog and uncertain transport, while DOM ownership survives new dialogs.
                            for case in ['close-command','edit-revert','lost-ack','malformed-ack','lost-ack-newer-writer','lost-ack-head-failure']:
                                old,new=pair('restore-'+case);ctx=browser.new_context();p=ctx.new_page();login(p,new);review(p);held=[];requests=[]
                                def hold_restore(route):requests.append(route.request.post_data_json);held.append((route,route.fetch()))
                                p.route('**/api/op/posts.restore',hold_restore);p.locator('#confirm-revision').click();waitheld(p,held)
                                # Lock and browser Back cannot replace the editor during a committed write.
                                p.locator('[data-action="logout"]').first.evaluate('(e)=>e.click()');require(not p.locator('#login-form').count(),'lock during restore');p.evaluate('history.back()');p.wait_for_timeout(50);require(p.locator('#post-markdown').count()==1,'Back during restore')
                                if case=='close-command':
                                    p.keyboard.press('Escape');p.keyboard.press('Control+k');before=p.locator('#modal-root').inner_html();release(p,held,'**/api/op/posts.restore');require(p.locator('#modal-root').inner_html()==before,'restore closed newer command dialog');p.keyboard.press('Escape');expect(p.locator('#post-markdown')).to_have_value(new['markdown'])
                                elif case=='edit-revert':
                                    p.locator('#post-markdown').fill('Fictional transient');p.locator('#post-markdown').fill(new['markdown']);release(p,held,'**/api/op/posts.restore');expect(p.locator('#post-markdown')).to_have_value(new['markdown']);expect(p.locator('#save-state')).to_have_text('Unsaved changes')
                                else:
                                    route,response=held.pop();route.fulfill(response=response,json={'ok':True,'data':{}}) if case=='malformed-ack' else route.abort();p.unroute('**/api/op/posts.restore');expect(p.locator('#revision-error')).to_contain_text('unconfirmed')
                                    writes=[];p.on('request',lambda req:writes.append(req.url) if req.url.endswith(('/posts.update','/posts.publish','/schedules.create','/proposals.create')) else None)
                                    p.locator('#post-markdown').fill('Fictional newer local typing');p.locator('[data-action="save"]').evaluate('(e)=>e.click()');p.locator('[data-action="publish"]').evaluate('(e)=>e.click()');require(not writes,'unconfirmed restore allowed another write')
                                    if case=='lost-ack-newer-writer':update(call('posts.get',{'id':new['id']})['post'],markdown='Fictional Agent after restore')
                                    if case=='lost-ack-head-failure':p.route('**/api/op/posts.get',lambda route:route.abort())
                                    p.route('**/api/op/posts.restore',lambda route:(requests.append(route.request.post_data_json),route.continue_()))
                                    p.locator('#confirm-revision').click()
                                    if case=='lost-ack-head-failure':
                                        expect(p.locator('#revision-error')).to_contain_text('latest server draft');require(len(requests)==2,'missing exact retry');p.unroute('**/api/op/posts.get');p.locator('#confirm-revision').click()
                                    expect(p.locator('#modal-root')).to_be_empty();require(requests[0]==requests[1] and len(requests)==2,'restore retry changed payload/key or repeated after cached receipt')
                                    expect(p.locator('#post-markdown')).to_have_value('Fictional newer local typing');p.unroute('**/api/op/posts.restore')
                                    p.locator('[data-action="save"]').click();expect(p.locator('#save-state')).to_have_text('All changes saved')
                                latest=call('posts.get',{'id':new['id']});require(latest['live']['revision']==1 and sum(r['markdown']==old['markdown'] for r in latest['revisions'])==2,'duplicate restore or public revision changed')
                                held_cases.append({'case':'restore '+case,'buffer_and_identity_preserved':True,'restore_requests':len(requests)});ctx.close()
                            done('six held committed restores: later dialog/edit-revert, exact lost/malformed-ack retry, intervening Agent and cached receipt after failed head check; busy Lock/Back guarded')
                            ctx=browser.new_context();p=ctx.new_page();old,new=pair('stale-confirm');login(p,new);review(p);later=update(new,markdown='Fictional concurrent writer');p.locator('#confirm-revision').click();expect(p.locator('#revision-error')).not_to_be_empty();expect(p.locator('#post-markdown')).to_have_value(new['markdown']);require(call('posts.get',{'id':new['id']})['post']==later,'stale compare/restore overwrote newer writer');ctx.close()
                            done('intervening server writer rejects reviewed stale restoration without replacing the editor')
                        browser.close();require(not errors,'browser runtime errors: '+str(errors))
            finally:h.close()
    finally:report()
    print(json.dumps({'checks':len(checks),'held_cases':len(held_cases),'browser_errors':len(errors)}))

if __name__=='__main__':main()

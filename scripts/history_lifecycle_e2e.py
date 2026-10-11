#!/usr/bin/env python3
"""Real held HTTP responses across first-save routing and history deletion."""
from __future__ import annotations
import argparse
import hashlib
import json
from pathlib import Path
import tempfile
import time
import sys
sys.dont_write_bytecode = True
from e2e import require
from workflows_e2e import WorkflowHarness, content


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary',type=Path,required=True)
    parser.add_argument('--chromium')
    parser.add_argument('--report',type=Path,required=True)
    parser.add_argument('--case',choices=['all','create-success','deletion','busy-save-success','busy-save-cancel','busy-restore-success','busy-restore-cancel','busy-publish-success','busy-publish-cancel','publish-render','refresh-cancel','cross-page','comparison-conflict','comparison-deleted','comparison-conflict-dirty','comparison-deleted-dirty','comparison-conflict-late','comparison-deleted-late','comparison-pending-retry'],default='all')
    args=parser.parse_args();checks=[];cases=[];errors=[];failure=None;failure_state=None
    def done(message):checks.append(message);print('PASS '+message,flush=True)
    try:
        with tempfile.TemporaryDirectory(prefix='folio-history-life-') as tmp:
            h=WorkflowHarness(args.binary.resolve(),Path(tmp))
            try:
                server=h.start('instance',pause=True)
                def call(op,payload=None):return h.operation(server,op,payload)['data']
                def create(slug,n=18):
                    p=call('posts.create',{'title':'Fictional '+slug,'slug':slug,'markdown':'HISTORY_LIFE_R1'})['post']
                    for rev in range(2,n+1):p=call('posts.update',content(p)|{'id':p['id'],'expected_revision':p['revision'],'markdown':'HISTORY_LIFE_R'+str(rev)})['post']
                    return p
                def delete(p):return call('posts.delete',{'id':p['id'],'expected_revision':p['revision'],'confirm':True})['post']
                def recover(p):return call('posts.recover',{'id':p['id'],'expected_revision':p['revision'],'confirm':True})['post']
                if args.case in ['all','deletion']:
                    p=create('api-delete');anchor={'id':p['id'],'revision':18,'limit':8}
                    delete(p);notfound=h.operation(server,'posts.history',anchor,status=404)
                    require(notfound['error']['code']=='not_found','deleted history was readable')
                    recovered=recover(p);require(recovered['revision']==19,'recover did not advance head')
                    h.operation(server,'posts.history',anchor,status=409)
                    page=call('posts.history',anchor|{'revision':19})
                    require(page['total']==19 and [r['revision'] for r in page['revisions']]==list(range(19,11,-1)),'recovered cursor history')
                    h.assert_private(server,recovered,'HISTORY_LIFE_R18')
                    done('real delete before read returns 404; recover creates R19, old R18 anchor conflicts; recovered history stays private')
                if args.chromium:
                    from playwright.sync_api import sync_playwright,expect
                    with sync_playwright() as pw:
                        browser=pw.chromium.launch(executable_path=args.chromium,args=['--no-sandbox'])
                        def login():
                            ctx=browser.new_context(viewport={'width':1100,'height':850});q=ctx.new_page()
                            q.on('pageerror',lambda e:errors.append(str(e)));q.goto(server['url']+'/studio')
                            q.evaluate("()=>window.FolioI18n.setLocale('en')")
                            q.locator('#access-token').fill(server['token']);q.locator('#login-form button[type="submit"]').click()
                            expect(q.locator('.studio-sidebar')).to_be_visible();return ctx,q
                        def rows(q):return q.locator('#revision-history [data-revision]').evaluate_all('(es)=>es.map(e=>Number(e.dataset.revision))')
                        def wait_held(q,held):
                            until=time.monotonic()+8
                            while not held and time.monotonic()<until:q.wait_for_timeout(20)
                            require(bool(held),'real response was not held')
                        def deliver(q,held,abort=False):
                            route,response=held.pop(0)
                            if abort:
                                with q.expect_event('requestfailed',predicate=lambda r:r.url==route.request.url):route.abort('failed')
                            else:
                                with q.expect_response(lambda r:r.url==route.request.url) as incoming:route.fulfill(response=response)
                                incoming.value.body()
                            # After network completion, cross a browser rendering barrier.
                            q.evaluate('()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)))')
                        def buffer(q,text):
                            q.locator('#post-markdown').fill(text);q.locator('#post-markdown').evaluate('(e)=>e.setSelectionRange(3,11)')
                        def kept(q,text):
                            require(q.locator('#post-markdown').input_value()==text and q.locator('#post-markdown').evaluate('(e)=>[e.selectionStart,e.selectionEnd]')==[3,11],'history read changed text/selection')
                        def open_post(q,p):
                            q.locator('a.post-title[href="/studio/posts/'+p['id']+'"]').click()
                            expect(q.locator('#post-markdown')).to_have_value(p['markdown'])
                        modes=['success'] if args.case=='create-success' else (['success','read-failure','cancel','switch','lock','lost-create-receipt'] if args.case=='all' else [])
                        for mode in modes:
                            ctx,q=login();writes=[];write_held=[];history_held=[];receipts=[];reads=[]
                            def held_write(route):
                                writes.append(route.request.post_data);response=route.fetch();receipts.append(response.json()['data']);write_held.append((route,response))
                            def held_history(route):
                                payload=route.request.post_data_json
                                if receipts and payload['id']==receipts[0]['post']['id'] and not reads:
                                    reads.append(payload);history_held.append((route,route.fetch()))
                                else:route.continue_()
                            q.route('**/api/op/posts.create',held_write);q.route('**/api/op/posts.history',held_history)
                            try:
                                q.locator('a[href="/studio/new"]').first.click();expect(q.locator('#post-markdown')).to_be_visible()
                                q.locator('#post-title').fill('Fictional create '+mode);q.locator('#post-slug').fill('create-'+mode);q.locator('#post-markdown').fill('ORIGINAL_CREATE_SNAPSHOT')
                                q.locator('[data-action="save"]').click();wait_held(q,write_held)
                                text='ORIGINAL_CREATE_SNAPSHOT\nNEW_UNSAVED_'+mode;buffer(q,text)
                                if mode=='lost-create-receipt':
                                    deliver(q,write_held,abort=True);expect(q.locator('#save-state')).to_have_text('Save unconfirmed — retry safely')
                                    q.locator('[data-action="save"]').click();wait_held(q,write_held)
                                    require(len(writes)==2 and writes[0]==writes[1] and receipts[0]==receipts[1],'create retry changed exact payload/key/receipt')
                                deliver(q,write_held);wait_held(q,history_held)
                                post=receipts[-1]['post'];expect(q).to_have_url(server['url']+'/studio/posts/'+post['id'])
                                expect(q.locator('#save-state')).to_have_text('Unsaved changes');kept(q,text)
                                payload=json.loads(writes[0]);before=call('backup.export')['state']
                                require(call('posts.create',payload)==receipts[0] and call('backup.export')['state']==before,'exact create retry wrote a duplicate')
                                require(call('posts.get',{'id':post['id']})['post']['markdown']=='ORIGINAL_CREATE_SNAPSHOT','new unsaved input was silently written')
                                if mode=='cancel':q.locator('[data-action="history-cancel"]').click()
                                elif mode=='switch':
                                    other=create('after-create-switch',1);q.once('dialog',lambda d:d.accept());q.locator('.studio-nav a[href="/studio"]').click();open_post(q,other);expect(q.locator('[data-revision="1"]')).to_have_count(1)
                                elif mode=='lock':
                                    q.once('dialog',lambda d:d.accept());q.locator('.studio-sidebar [data-action="logout"]').click();expect(q.locator('#login-form')).to_be_visible()
                                actual_status=history_held[0][1].status
                                deliver(q,history_held,abort=mode=='read-failure')
                                if mode=='switch':require(rows(q)==[1] and q.locator('#post-title').input_value()==other['title'],'old create page replaced newer editor')
                                elif mode=='lock':require(q.locator('#post-markdown').count()==0 and q.locator('#login-form').is_visible(),'old create page reopened locked editor')
                                else:
                                    if mode=='read-failure':
                                        expect(q.locator('[data-revision-page="retry"]')).to_be_visible();kept(q,text);q.locator('[data-revision-page="retry"]').click()
                                    try:expect(q.locator('[data-action="history-cancel"]')).to_have_count(0,timeout=2500)
                                    except AssertionError:
                                        failure_state={'url':q.url,'history_http_status':actual_status,'network_completed':True,'status':q.locator('#history-status').text_content(),'rows':rows(q),'text_preserved':q.locator('#post-markdown').input_value()==text,'selection':q.locator('#post-markdown').evaluate('(e)=>[e.selectionStart,e.selectionEnd]')}
                                        raise AssertionError('first create history stayed loading after /studio/new route change: '+json.dumps(failure_state))
                                    expect(q.locator('#revision-history [data-action="revision-review"]')).to_be_enabled();kept(q,text);require(rows(q)==[1],'created history lost R1')
                                    if mode=='cancel':expect(q.locator('#history-status')).to_have_text('History loading cancelled. Your editor is unchanged.')
                                    else:expect(q.locator('#history-status')).to_have_text('Saved history at R1')
                                cases.append({'case':'first-create-'+mode,'history_status':actual_status,'exact_receipt_preserved':True});done('first create '+mode+'; held real response, route ownership, exact retry and later input preserved')
                            finally:ctx.close()
                        busy_modes=['busy-save-success','busy-save-cancel','busy-restore-success','busy-restore-cancel','busy-publish-success','busy-publish-cancel']
                        for mode in (busy_modes if args.case=='all' else [args.case] if args.case in busy_modes else []):
                            p=create(mode);ctx,q=login();held=[];writes=[];statuses=[]
                            def hold_page(route):held.append((route,route.fetch())) if route.request.post_data_json.get('before_revision') else route.continue_()
                            def reject_write(route):
                                # The daemon genuinely rejects this request under the read-only role.
                                headers=route.request.all_headers()|{'authorization':'Bearer '+server['read']}
                                response=route.fetch(headers=headers);statuses.append(response.status);writes.append((route,response))
                            op_name='posts.restore' if 'restore' in mode else 'posts.publish' if 'publish' in mode else 'posts.update'
                            q.route('**/api/op/posts.history',hold_page);q.route('**/api/op/'+op_name,reject_write)
                            try:
                                open_post(q,p);expect(q.locator('[data-revision="18"]')).to_have_count(1)
                                before=call('backup.export')['state']
                                if 'restore' in mode or 'publish' in mode:
                                    if 'restore' in mode:q.locator('[data-compare="17"]').click();expect(q.locator('#confirm-revision')).to_be_enabled()
                                    else:q.locator('[data-action="publish"]').click();expect(q.locator('#confirm-publish')).to_be_enabled()
                                    # An already-open review can overlap the delegated sidebar page event.
                                    q.evaluate('()=>document.querySelector("#revision-history [data-revision-page=older]").click()')
                                    wait_held(q,held);q.locator('#confirm-revision' if 'restore' in mode else '#confirm-publish').click()
                                else:
                                    buffer(q,'BUSY_FAILURE_KEEP');q.locator('[data-revision-page="older"]').click();wait_held(q,held);q.locator('[data-action="save"]').click()
                                wait_held(q,writes);require(statuses==[403],'write did not get genuine read-only rejection')
                                # Continue typing after the write starts; no input event is needed for selection.
                                q.evaluate('()=>{const e=document.querySelector("#post-markdown");e.value="BUSY_FAILURE_KEEP";e.dispatchEvent(new Event("input",{bubbles:true}));e.setSelectionRange(3,11)}')
                                if mode.endswith('cancel'):
                                    q.evaluate('()=>document.querySelector("[data-action=history-cancel]").click()');deliver(q,held)
                                else:deliver(q,held);require(rows(q)==list(range(10,2,-1)),'held successful page missing')
                                require(q.locator('#revision-history [data-action="revision-review"]').is_disabled(),'fixture did not render busy-disabled history')
                                deliver(q,writes)
                                if 'restore' in mode or 'publish' in mode:
                                    if 'restore' in mode:expect(q.locator('#revision-error')).not_to_be_empty()
                                    else:expect(q.locator('#confirm-publish')).to_be_enabled()
                                    q.keyboard.press('Escape')
                                else:expect(q.locator('#save-state')).to_have_text('Save failed — text preserved')
                                kept(q,'BUSY_FAILURE_KEEP');require(call('backup.export')['state']==before,'rejected write mutated state')
                                try:expect(q.locator('#revision-history [data-action="revision-review"]')).to_be_enabled(timeout=2500)
                                except AssertionError:raise AssertionError(mode+': busy became idle but history controls stayed disabled')
                                q.unroute('**/api/op/posts.history',hold_page);q.locator('#revision-history [data-revision-page="older"]').click()
                                expect(q.locator('[data-revision="'+('10' if mode.endswith('cancel') else '2')+'"]').first).to_have_count(1);kept(q,'BUSY_FAILURE_KEEP')
                                cases.append({'case':mode,'real_write_status':statuses[0],'no_write':True,'input_selection_preserved':True});done(mode+'; genuine rejected write restores history controls without changing text/selection')
                            finally:ctx.close()
                        if args.case in ['all','publish-render']:
                            p=create('publish-render');ctx,q=login()
                            try:
                                open_post(q,p);expect(q.locator('[data-revision="18"]')).to_have_count(1)
                                q.locator('[data-action="publish"]').click();expect(q.locator('#confirm-publish')).to_be_enabled();q.locator('#confirm-publish').click()
                                expect(q.locator('#modal-root')).to_be_empty();expect(q.locator('#editor-post-status .badge')).to_have_text('Published');expect(q.locator('#save-state')).to_have_text('All changes saved')
                                try:expect(q.locator('#revision-history [data-action="revision-review"]')).to_be_enabled(timeout=2500)
                                except AssertionError:raise AssertionError('publish-render: rendered editor kept history disabled after busy ended')
                                expect(q.locator('#revision-history [data-revision-page="older"]')).to_be_enabled()
                                current=call('posts.get',{'id':p['id']});require(current['post']['revision']==18 and current['live']['revision']==18 and current['live']['markdown']==p['markdown'],'publication changed reviewed head')
                                q.locator('#revision-history [data-revision-page="older"]').click();expect(q.locator('[data-revision="10"]')).to_have_count(1)
                                cases.append({'case':'publish-render','reviewed_live_revision':18,'controls_reenabled':True});done('successful publish refreshes owned rendered editor controls with exact reviewed live R18')
                            finally:ctx.close()
                        if args.case in ['all','refresh-cancel']:
                            p=create('refresh-cancel');ctx,q=login();held=[]
                            def hold_refresh(route):
                                payload=route.request.post_data_json
                                if payload['id']==p['id'] and payload['revision']==19 and not payload.get('before_revision') and not held:held.append((route,route.fetch()))
                                else:route.continue_()
                            try:
                                open_post(q,p);expect(q.locator('[data-revision="18"]')).to_have_count(1)
                                q.locator('[data-revision-page="older"]').click();expect(q.locator('[data-revision="10"]')).to_have_count(1)
                                q.route('**/api/op/posts.history',hold_refresh);buffer(q,'REFRESH_SAVED');q.locator('[data-action="save"]').click();wait_held(q,held)
                                expect(q.locator('#save-state')).to_have_text('All changes saved');buffer(q,'REFRESH_LATER_UNSAVED');q.locator('[data-action="history-cancel"]').click();deliver(q,held);kept(q,'REFRESH_LATER_UNSAVED')
                                require(rows(q)==[19],'cancel applied the old auto-refresh page')
                                try:expect(q.locator('[data-revision-page="retry"]')).to_be_visible(timeout=2500)
                                except AssertionError:raise AssertionError('cancelled post-save refresh lost retry and all history cursors')
                                q.unroute('**/api/op/posts.history',hold_refresh);q.locator('[data-revision-page="retry"]').click();expect(q.locator('[data-revision="18"]')).to_have_count(1)
                                q.locator('[data-revision-page="older"]').click();expect(q.locator('[data-revision="11"]')).to_have_count(1);kept(q,'REFRESH_LATER_UNSAVED')
                                # Ordinary page cancellation retains the prior page and both usable cursors.
                                ordinary=[]
                                def hold_ordinary(route):ordinary.append((route,route.fetch()))
                                q.route('**/api/op/posts.history',hold_ordinary);old=rows(q);q.locator('[data-revision-page="older"]').click();wait_held(q,ordinary)
                                q.locator('[data-action="history-cancel"]').click();deliver(q,ordinary);require(rows(q)==old,'ordinary cancel replaced prior page')
                                expect(q.locator('[data-revision-page="newer"]')).to_be_enabled();expect(q.locator('[data-revision-page="older"]')).to_be_enabled();kept(q,'REFRESH_LATER_UNSAVED')
                                cases.append({'case':'refresh-cancel','retry_recovered_first_page':True,'ordinary_cancel_kept_cursors':True});done('post-save Cancel has explicit retry; ordinary Cancel preserves page/cursors and later input')
                            finally:ctx.close()
                        if args.case in ['all','cross-page']:
                            p=create('cross-page');ctx,q=login();reads=[];comparisons=[]
                            q.on('request',lambda r:reads.append(r.post_data_json) if r.url.endswith('/api/op/posts.get') or r.url.endswith('/api/op/posts.preview') else comparisons.append(r.post_data_json) if r.url.endswith('/api/op/posts.compare') else None)
                            try:
                                open_post(q,p);expect(q.locator('[data-revision="18"]')).to_have_count(1)
                                q.locator('[data-revision-page="older"]').click();expect(q.locator('[data-revision="10"]')).to_have_count(1)
                                q.locator('[data-compare="10"]').click();expect(q.locator('#revision-result .revision-snapshots')).to_be_visible();q.locator('#revision-from').select_option('10');q.locator('#revision-to').select_option('9')
                                expect(q.locator('#revision-result')).to_contain_text('R10 → R9')
                                try:expect(q.locator('#revision-history-pages [data-revision-page="older"]')).to_be_visible(timeout=2500)
                                except AssertionError:raise AssertionError('cross-page old-to-old selection unavailable: R10/R2 cannot share current-page selectors')
                                q.locator('#revision-history-pages [data-revision-page="older"]').click();expect(q.locator('#revision-to option[value="2"]')).to_have_count(1)
                                require(q.locator('#revision-from').input_value()=='10' and q.locator('#revision-to').input_value()=='9','metadata paging lost selected old pair')
                                q.locator('#revision-to').select_option('2');expect(q.locator('#revision-result')).to_contain_text('R10 → R2')
                                expect(q.locator('#revision-result')).to_contain_text('HISTORY_LIFE_R10');expect(q.locator('#revision-result')).to_contain_text('HISTORY_LIFE_R2');expect(q.locator('#confirm-revision')).to_be_disabled()
                                q.locator('#revision-from').select_option('2');q.locator('#revision-to').select_option('10');expect(q.locator('#revision-result')).to_contain_text('R2 → R10')
                                q.set_viewport_size({'width':320,'height':850});q.evaluate("()=>window.FolioI18n.setLocale('zh-CN')")
                                require(q.locator('#revision-from').input_value()=='2' and q.locator('#revision-to').input_value()=='10' and q.evaluate('document.documentElement.scrollWidth<=innerWidth'),'mobile locale change lost the cross-page pair or overflowed')
                                q.locator('#revision-history-pages [data-revision-page="newer"]').click();expect(q.locator('#revision-to option[value="9"]')).to_have_count(1)
                                require(q.locator('#revision-from').input_value()=='2' and q.locator('#revision-to').input_value()=='10','newer metadata page lost selected pair')
                                require(comparisons[-1]=={'id':p['id'],'revision':18,'from_revision':2,'to_revision':10} and reads and all(r.get('include_revisions') is False for r in reads),'cross-page comparison drifted or fetched full history bodies')
                                cases.append({'case':'cross-page','pairs':[[10,2],[2,10]],'bounded_reads':True});done('two old revisions on different pages compare both directions; selected pair retained, bounded metadata only')
                            finally:ctx.close()
                        comparison_modes=['comparison-conflict','comparison-deleted','comparison-conflict-dirty','comparison-deleted-dirty','comparison-conflict-late','comparison-deleted-late']
                        for mode in comparison_modes if args.case=='all' else [args.case] if args.case in comparison_modes else []:
                            p=create(mode);ctx,q=login();held=[];writes=[];statuses=[]
                            deleted='deleted' in mode;dirty=mode.endswith('dirty');late=mode.endswith('late');expected_status=404 if deleted else 409
                            q.on('request',lambda r:writes.append(r.post_data_json) if r.url.endswith('/api/op/posts.restore') else None)
                            def held_stale(route):
                                payload=route.request.post_data_json
                                if payload['id']==p['id'] and payload['revision']==18 and payload.get('before_revision'):
                                    response=route.fetch();statuses.append(response.status);held.append((route,response))
                                else:route.continue_()
                            def snapshot():
                                return q.evaluate('()=>({fields:Object.fromEntries(["title","slug","markdown","excerpt","tags","category","cover","featured"].map(k=>{const e=document.querySelector("#post-"+k);return [k,k==="featured"?e.checked:e.value]})),selection:[document.querySelector("#post-markdown").selectionStart,document.querySelector("#post-markdown").selectionEnd],comparison:document.querySelector("#revision-result").innerHTML,from:document.querySelector("#revision-from").value,to:document.querySelector("#revision-to").value})')
                            try:
                                open_post(q,p);expect(q.locator('[data-revision="18"]')).to_have_count(1)
                                q.locator('[data-compare="17"]').click();expect(q.locator('#confirm-revision')).to_be_enabled()
                                q.locator('#post-markdown').evaluate('(e)=>e.setSelectionRange(3,11)')
                                if dirty:
                                    q.evaluate('()=>{const e=document.querySelector("#post-markdown");e.value="UNSAVED_STALE_RESTORE_KEEP";e.dispatchEvent(new Event("input",{bubbles:true}));e.setSelectionRange(3,11)}')
                                    expect(q.locator('#confirm-revision')).to_be_disabled()
                                before=snapshot()
                                if deleted:delete(p)
                                else:p=call('posts.update',content(p)|{'id':p['id'],'expected_revision':18,'markdown':'EXTERNAL_STALE_RESTORE_R19'})['post']
                                unchanged=call('backup.export')['state']
                                q.route('**/api/op/posts.history',held_stale)
                                q.locator('#revision-history-pages [data-revision-page="older"]').click();wait_held(q,held)
                                require(statuses==[expected_status],mode+': response is not the genuine expected history error')
                                if late:
                                    if deleted:p=recover(p)
                                    q.keyboard.press('Escape');q.locator('.studio-nav a[href="/studio"]').click();open_post(q,p)
                                    expect(q.locator('[data-revision="19"]')).to_have_count(1)
                                    q.locator('[data-compare="17"]').click();expect(q.locator('#confirm-revision')).to_be_enabled()
                                    q.locator('#post-markdown').evaluate('(e)=>e.setSelectionRange(3,11)')
                                    newer=snapshot();newer_status=q.locator('#revision-restore-message').inner_text()
                                    deliver(q,held)
                                    require(snapshot()==newer and q.locator('#revision-restore-message').inner_text()==newer_status,'late history error changed the reopened comparison or editor')
                                    expect(q.locator('#confirm-revision')).to_be_enabled();expect(q.locator('[data-action="history-reopen"]')).to_have_count(0)
                                    require(not writes,'late history error submitted a restore')
                                    cases.append({'case':mode,'history_status':expected_status,'reopened_head':19,'new_comparison_unchanged':True,'restore_enabled':True});done(mode+'; held real error ignored after reopening the same story at R19');continue
                                deliver(q,held)
                                require(snapshot()==before,mode+': history error changed fields, selection or reviewed comparison')
                                enabled=q.locator('#confirm-revision').is_enabled()
                                if enabled:
                                    # On the old binary, prove this is a clickable product defect,
                                    # including the real backend rejection; no synthetic error JSON.
                                    with q.expect_response(lambda r:r.url.endswith('/api/op/posts.restore')) as rejected:q.locator('#confirm-revision').click()
                                    rejected.value.body()
                                    failure_state={'case':mode,'history_status':statuses[0],'restore_was_clickable':True,'restore_http_status':rejected.value.status,'buffer_and_comparison_preserved':snapshot()==before,'server_state_unchanged':call('backup.export')['state']==unchanged}
                                    require(rejected.value.status==expected_status and failure_state['server_state_unchanged'],'stale restore backend CAS/404 did not reject safely')
                                    raise AssertionError(mode+': known-stale Restore remained clickable; real backend rejected only after click')
                                expect(q.locator('#confirm-revision')).to_be_disabled()
                                key='history.unavailable' if deleted else 'history.stale'
                                expected=q.evaluate('(key)=>window.FolioI18n.t(key)',key)
                                require(q.locator('#revision-restore-message').inner_text()==expected,mode+': comparison lacks the history invalidation/export hint')
                                # Exercise the actual handler as well as the disabled DOM control.
                                q.evaluate('async()=>await document.querySelector("#confirm-revision").onclick()')
                                require(not writes and call('backup.export')['state']==unchanged,'known-stale handler sent a restore or changed server state')
                                h.operation(server,'posts.restore',{'id':p['id'],'revision':17,'expected_revision':18,'idempotency_key':mode+'-backend-guard'},status=expected_status)
                                require(call('backup.export')['state']==unchanged,'backend stale rejection changed state')
                                q.set_viewport_size({'width':320,'height':850});q.evaluate("()=>window.FolioI18n.setLocale('zh-CN')")
                                expect(q.locator('#confirm-revision')).to_be_disabled()
                                require(q.locator('#revision-restore-message').inner_text()==q.evaluate('(key)=>window.FolioI18n.t(key)',key),'localized invalidation hint missing')
                                require(q.evaluate('document.documentElement.scrollWidth<=innerWidth'),'stale comparison overflowed at 320px')
                                require(q.locator('#post-markdown').input_value()==before['fields']['markdown'] and q.locator('#post-markdown').evaluate('(e)=>[e.selectionStart,e.selectionEnd]')==before['selection'],'locale/status changed unsaved input or selection')
                                q.keyboard.press('Escape')
                                with q.expect_download() as download:q.locator('[data-action="export-markdown"]').click()
                                exported=Path(tmp)/(mode+'.md');download.value.save_as(exported)
                                require(exported.read_text()==before['fields']['markdown'],'stale comparison lost exportable editor content')
                                if deleted:p=recover(p)
                                if dirty:
                                    q.once('dialog',lambda d:d.dismiss());q.locator('[data-action="history-reopen"]').click()
                                    kept(q,'UNSAVED_STALE_RESTORE_KEEP')
                                    q.once('dialog',lambda d:d.accept())
                                q.locator('[data-action="history-reopen"]').click()
                                expect(q.locator('[data-revision="19"]')).to_have_count(1);expect(q.locator('#post-markdown')).to_have_value(p['markdown'])
                                q.locator('[data-compare="17"]').click();expect(q.locator('#confirm-revision')).to_be_enabled()
                                latest=call('posts.get',{'id':p['id']});live=latest['live']
                                q.locator('#confirm-revision').click();expect(q.locator('#modal-root')).to_be_empty();expect(q.locator('#post-markdown')).to_have_value('HISTORY_LIFE_R17')
                                after=call('posts.get',{'id':p['id']})
                                require(len(writes)==1 and writes[0]['expected_revision']==19 and after['post']['revision']==20 and after['live']==live,'reopened restore drifted, duplicated, or published')
                                receipt=call('posts.restore',writes[0]);state_after=call('backup.export')['state']
                                require(receipt==call('posts.restore',writes[0]) and call('backup.export')['state']==state_after and receipt['post']==after['post'],'reopened restore exact retry drifted')
                                cases.append({'case':mode,'history_status':expected_status,'restore_disabled':True,'status_bilingual':True,'fields_selection_comparison_preserved':True,'export_preserved':True,'reopened_restore_revision':20,'exact_retry_preserved':True});done(mode+'; stale Restore disabled before any write; bilingual hint, buffer/export, explicit reopen and exact retry preserved')
                            finally:ctx.close()
                        if args.case in ['all','comparison-pending-retry']:
                            p=create('comparison-pending-retry');ctx,q=login();requests=[];history_held=[];restore_held=[];payloads=[];receipts=[]
                            def defer_history(route):
                                if route.request.post_data_json.get('before_revision'):requests.append(route)
                                else:route.continue_()
                            def hold_restore(route):
                                payloads.append(route.request.post_data);response=route.fetch();receipts.append(response.json()['data']);restore_held.append((route,response))
                            try:
                                open_post(q,p);expect(q.locator('[data-revision="18"]')).to_have_count(1)
                                q.locator('[data-compare="17"]').click();expect(q.locator('#confirm-revision')).to_be_enabled()
                                original_live=call('posts.get',{'id':p['id']})['live']
                                q.route('**/api/op/posts.history',defer_history);q.route('**/api/op/posts.restore',hold_restore)
                                q.locator('#revision-history-pages [data-revision-page="older"]').click();wait_held(q,requests)
                                q.locator('#confirm-revision').click();wait_held(q,restore_held)
                                route=requests.pop(0);response=route.fetch();require(response.status==409,'pending retry did not overlap a genuine history conflict');history_held.append((route,response))
                                deliver(q,history_held);deliver(q,restore_held,abort=True)
                                expect(q.locator('#history-status')).to_have_text('Saved revisions changed. Export unsaved edits before reopening this story. Your editor has not changed.')
                                expect(q.locator('#confirm-revision')).to_have_text('Retry exact restore');expect(q.locator('#confirm-revision')).to_be_enabled()
                                expect(q.locator('#revision-restore-message')).to_contain_text('The restore result is unconfirmed.')
                                require(call('posts.get',{'id':p['id']})['post']['revision']==19,'first real restore did not commit once')
                                q.locator('#confirm-revision').click();wait_held(q,restore_held);deliver(q,restore_held)
                                expect(q.locator('#modal-root')).to_be_empty();expect(q.locator('#post-markdown')).to_have_value('HISTORY_LIFE_R17')
                                latest=call('posts.get',{'id':p['id']})
                                require(len(payloads)==2 and payloads[0]==payloads[1] and receipts[0]==receipts[1] and latest['post']['revision']==19 and len(latest['revisions'])==19 and latest['live']==original_live,'known-stale history blocked or changed the existing exact restore retry')
                                cases.append({'case':'comparison-pending-retry','history_status':409,'lost_restore_receipt':True,'exact_payload_and_receipt':True,'single_restore_revision':19});done('comparison-pending-retry; genuine 409 after committed restore does not block its lost-receipt exact retry')
                            finally:ctx.close()
                        if args.case in ['all','deletion']:
                            for mode in ['delete-before-page','held-success-before-delete','delete-before-initial-history','late-delete-error-switch','recover-before-page']:
                                p=create(mode);ctx,q=login();held=[];statuses=[];observed=[]
                                q.on('response',lambda r:observed.append(r.status) if r.url.endswith('/api/op/posts.history') else None)
                                def deletion_route(route):
                                    payload=route.request.post_data_json
                                    if payload['id']==p['id'] and ((mode=='delete-before-initial-history' and not payload.get('before_revision')) or (mode in ['held-success-before-delete','late-delete-error-switch'] and payload.get('before_revision'))):
                                        if mode!='held-success-before-delete':delete(p)
                                        response=route.fetch();statuses.append(response.status);held.append((route,response))
                                    else:route.continue_()
                                if mode not in ['delete-before-page','recover-before-page']:q.route('**/api/op/posts.history',deletion_route)
                                try:
                                    if mode=='delete-before-initial-history':
                                        q.locator('a.post-title[href="/studio/posts/'+p['id']+'"]').click();wait_held(q,held);require(statuses==[404],'initial deleted read was not real 404');deliver(q,held)
                                        expect(q.locator('#post-markdown')).to_have_value(p['markdown'])
                                    else:
                                        open_post(q,p);expect(q.locator('[data-revision="18"]')).to_have_count(1)
                                        if mode in ['delete-before-page','recover-before-page']:
                                            delete(p)
                                            if mode=='recover-before-page':recovered=recover(p)
                                        buffer(q,'UNSAVED_DELETE_KEEP');q.locator('[data-revision-page="older"]').click()
                                        if mode not in ['delete-before-page','recover-before-page']:
                                            wait_held(q,held)
                                            if mode=='held-success-before-delete':
                                                require(statuses==[200],'pre-delete held read did not succeed');delete(p);deliver(q,held)
                                                expect(q.locator('[data-revision="10"]')).to_have_count(1);expect(q.locator('#history-status')).to_have_text('Saved history at R18');kept(q,'UNSAVED_DELETE_KEEP')
                                                # The successful read remains an R18 snapshot, with no push invalidation.
                                                q.unroute('**/api/op/posts.history',deletion_route);q.locator('[data-revision-page="older"]').click()
                                            else:
                                                require(statuses==[404],'held delete error was not real 404');other=create('after-delete-switch',1)
                                                q.once('dialog',lambda d:d.accept());q.locator('.studio-nav a[href="/studio"]').click();open_post(q,other);expect(q.locator('[data-revision="1"]')).to_have_count(1);deliver(q,held)
                                                require(rows(q)==[1] and q.locator('[data-action="history-reopen"]').count()==0,'late 404 contaminated newer editor')
                                                cases.append({'case':mode,'real_statuses':statuses,'late_discarded':True});done(mode+'; real 404 ignored after editor switch');continue
                                    buffer(q,'UNSAVED_DELETE_KEEP')
                                    expect(q.locator('[data-action="history-reopen"]')).to_be_visible()
                                    expect(q.locator('#history-status')).to_have_text('Saved revisions changed. Export unsaved edits before reopening this story. Your editor has not changed.' if mode=='recover-before-page' else 'This story is unavailable or in trash. Export unsaved edits before reopening. Your editor is unchanged.')
                                    expect(q.locator('#revision-history [data-action="revision-review"]')).to_be_disabled();kept(q,'UNSAVED_DELETE_KEEP')
                                    q.locator('[data-action="save"]').click();expect(q.locator('#save-state')).to_have_text('Conflict — your text is safe' if mode=='recover-before-page' else 'Save failed — text preserved');kept(q,'UNSAVED_DELETE_KEEP')
                                    if mode!='recover-before-page':recovered=recover(p)
                                    require(recovered['revision']==19,'UI recovery head drift')
                                    h.operation(server,'posts.history',{'id':p['id'],'revision':18,'limit':8},status=409)
                                    q.once('dialog',lambda d:d.dismiss());q.locator('[data-action="history-reopen"]').click();kept(q,'UNSAVED_DELETE_KEEP')
                                    with q.expect_download() as download:q.locator('[data-action="export-markdown"]').click()
                                    export=Path(tmp)/(mode+'.md');download.value.save_as(export);require(export.read_text()=='UNSAVED_DELETE_KEEP','export lost unsaved deleted-story input')
                                    if mode=='delete-before-initial-history':q.unroute('**/api/op/posts.history',deletion_route)
                                    q.once('dialog',lambda d:d.accept());q.locator('[data-action="history-reopen"]').click()
                                    expect(q.locator('#post-markdown')).to_have_value(recovered['markdown']);expect(q.locator('[data-revision="19"]')).to_have_count(1);expect(q.locator('#history-status')).to_have_text('Saved history at R19')
                                    q.locator('[data-revision-page="older"]').click();expect(q.locator('[data-revision="11"]')).to_have_count(1)
                                    expected_status=409 if mode=='recover-before-page' else 404
                                    require(expected_status in observed,'UI never received a real history '+str(expected_status))
                                    cases.append({'case':mode,'real_statuses':observed,'unsaved_exported':True,'recovered_head':19});done(mode+'; real '+str(expected_status)+' preserves buffer/export, explicit recovery reopens R19')
                                finally:ctx.close()
                        require(not errors,'browser JavaScript errors');browser.close()
                h.check_no_secrets();done('owned disposable resources and outputs contain no credentials')
            finally:h.close()
    except Exception as error:
        failure=str(error);raise
    finally:
        args.report.parent.mkdir(parents=True,exist_ok=True)
        args.report.write_text(json.dumps({'passed':failure is None,'binary_sha256':hashlib.sha256(args.binary.read_bytes()).hexdigest(),'test_sha256':hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),'case_filter':args.case,'checks':checks,'cases':cases,'browser_errors':errors,'failure':failure,'failure_state':failure_state},indent=2)+'\n')


if __name__=='__main__':main()

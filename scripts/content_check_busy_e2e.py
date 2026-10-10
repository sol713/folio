#!/usr/bin/env python3
"""Real publication/check busy-state regressions, including stale-editor cleanup."""
import argparse
import json
from pathlib import Path
import tempfile

from e2e import Harness, require


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary',type=Path,required=True)
    parser.add_argument('--chromium',default='/usr/bin/chromium')
    parser.add_argument('--report',type=Path,required=True)
    parser.add_argument('--primary-only',action='store_true',help='Run the two reported lifecycle cases only')
    args=parser.parse_args()
    from playwright.sync_api import sync_playwright, expect
    results,errors=[],[]
    with tempfile.TemporaryDirectory(prefix='folio-check-busy-') as tmp:
        h=Harness(args.binary.resolve(),Path(tmp))
        try:
            server=h.start('instance');base=server['url']
            def call(op,payload=None):return h.call(server,op,payload)['data']
            def create(slug):
                return call('posts.create',{'title':'Fictional '+slug,'slug':slug,
                    'markdown':'Fictional saved body '+slug,'excerpt':'Fictional summary'})['post']
            def record(name,passed,**evidence):
                result={'case':name,'passed':bool(passed),**evidence};results.append(result);print(json.dumps(result),flush=True)
            with sync_playwright() as pw:
                browser=pw.chromium.launch(executable_path=args.chromium,headless=True)
                def login(p,post):
                    p.on('pageerror',lambda error:errors.append(str(error)))
                    p.on('dialog',lambda dialog:dialog.accept())
                    p.goto(base+'/studio');expect(p.locator('#login-form')).to_be_visible()
                    p.evaluate("window.FolioI18n.setLocale('en')")
                    p.locator('#access-token').fill(server['token']);p.locator('#login-form button[type="submit"]').click()
                    expect(p.locator('.post-table')).to_be_visible()
                    p.locator('a[href="/studio/posts/'+post['id']+'"]').first.click()
                    expect(p.locator('#post-markdown')).to_have_value(post['markdown'])
                def wait_held(p,held):
                    for _ in range(100):
                        if held:return
                        p.wait_for_timeout(10)
                    raise AssertionError('request was not held')
                def publish(p):
                    p.locator('[data-action="publish"]').click();expect(p.locator('#confirm-publish')).to_be_enabled()
                    with p.expect_response(lambda r:r.url.endswith('/api/op/posts.publish')) as response:p.locator('#confirm-publish').click()
                    require(response.value.status==200,'actual publication failed')
                    expect(p.locator('.editor-sidebar .badge')).to_have_text('Published')
                    expect(p.locator('#check-status')).to_be_visible()
                    p.wait_for_timeout(100)

                # No preflight first: the successful publish renders a new editor while busy.
                post=create('direct-success');ctx=browser.new_context();p=ctx.new_page();login(p,post)
                publish(p);enabled=p.locator('[data-action="check-content"]').is_enabled();retried=False
                if enabled:
                    p.locator('[data-action="check-content"]').click();expect(p.locator('#check-status')).to_have_text('Results for this saved draft.');retried=True
                saved=call('posts.get',{'id':post['id']})
                record('saved draft can check immediately after successful publication',enabled and retried and saved['post']['revision']==1 and saved['live']['revision']==1,
                    button_enabled=enabled,retry_completed=retried,content_revision=saved['post']['revision'],published_revision=saved['live']['revision'])
                ctx.close()

                # Actual read response arrives during a held publish. A transport failure
                # prevents the publish from reaching the daemon; no fake API reply is used.
                post=create('failed-publish');before=call('posts.get',{'id':post['id']});ctx=browser.new_context();p=ctx.new_page();login(p,post)
                checks,publishes=[],[]
                p.route('**/api/op/posts.check',lambda route:checks.append((route,route.fetch())))
                p.route('**/api/op/posts.publish',lambda route:publishes.append(route))
                p.locator('[data-action="check-content"]').click();wait_held(p,checks)
                p.locator('[data-action="publish"]').click();expect(p.locator('#confirm-publish')).to_be_enabled()
                p.locator('#confirm-publish').click();wait_held(p,publishes)
                expect(p.locator('#confirm-publish')).to_be_disabled()
                route,response=checks.pop()
                with p.expect_response(lambda r:r.url.endswith('/api/op/posts.check')):route.fulfill(response=response)
                p.unroute('**/api/op/posts.check');p.wait_for_timeout(100)
                publishes.pop().abort('connectionfailed');p.unroute('**/api/op/posts.publish')
                expect(p.locator('#confirm-publish')).to_be_enabled();p.keyboard.press('Escape');p.wait_for_timeout(100)
                enabled=p.locator('[data-action="check-content"]').is_enabled();status=p.locator('#check-status').inner_text();retried=False
                if enabled:
                    p.locator('[data-action="check-content"]').click();expect(p.locator('#check-status')).to_have_text('Results for this saved draft.');retried=True
                after=call('posts.get',{'id':post['id']})
                record('check arriving during failed publish settles and can retry',enabled and retried and before==after and p.locator('#post-markdown').input_value()==post['markdown'],
                    button_enabled=enabled,status_before_retry=status,retry_completed=retried,server_unchanged=before==after)
                ctx.close()

                if not args.primary_only:
                    # Same article and revision, different editor session. Late completion
                    # must not settle, redraw or replace the newer session's pending check.
                    for old_outcome in ['success','network-error']:
                        post=create('old-editor-'+old_outcome);ctx=browser.new_context();p=ctx.new_page();login(p,post)
                        held=[]
                        def hold(route):
                            response=route.fetch() if old_outcome=='success' or held else None
                            held.append((route,response))
                        p.route('**/api/op/posts.check',hold)
                        p.locator('[data-action="check-content"]').click();wait_held(p,held)
                        publish(p);expect(p.locator('[data-action="check-content"]')).to_be_enabled()
                        p.locator('[data-action="check-content"]').click()
                        for _ in range(100):
                            if len(held)==2:break
                            p.wait_for_timeout(10)
                        require(len(held)==2,'new editor check not held')
                        p.locator('#post-markdown').focus();p.locator('#post-markdown').evaluate('(e)=>e.setSelectionRange(2,7)')
                        panel=p.locator('#content-check').inner_html();route,response=held[0]
                        if old_outcome=='success':route.fulfill(response=response)
                        else:route.abort('connectionfailed')
                        p.wait_for_timeout(150)
                        untouched=p.locator('#content-check').inner_html()==panel and p.locator('#post-markdown').evaluate('(e)=>[e.selectionStart,e.selectionEnd]')==[2,7]
                        expect(p.locator('#check-status')).to_have_text('Checking the saved draft…')
                        expect(p.locator('[data-action="check-content"]')).to_be_disabled()
                        route,response=held[1];route.fulfill(response=response);p.unroute('**/api/op/posts.check')
                        expect(p.locator('#check-status')).to_have_text('Results for this saved draft.')
                        expect(p.locator('[data-action="check-content"]')).to_be_enabled()
                        saved=call('posts.get',{'id':post['id']})
                        record('old editor '+old_outcome+' leaves new pending check untouched',untouched and saved['post']['revision']==1 and saved['live']['revision']==1,
                            pending_panel_and_caret_unchanged=untouched,content_revision=1,published_revision=1)
                        ctx.close()
                browser.close()
                require(not errors,'browser exceptions: '+repr(errors))
        finally:h.close()
    args.report.parent.mkdir(parents=True,exist_ok=True)
    args.report.write_text(json.dumps({'cases':results,'passed':sum(r['passed'] for r in results),'total':len(results),'browser_errors':errors},indent=2)+'\n')
    require(all(r['passed'] for r in results),'publication/check busy-state regression failed; see report')


if __name__=='__main__':main()

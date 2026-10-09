// Deterministic DOM/event contract tests, not a browser download integration test.
// Run the real registered document listener, action dispatcher, export functions,
// and navigate(). Only initial/page rendering and browser/network APIs are faked.
const fs=require('node:fs'),path=require('node:path'),vm=require('node:vm'),assert=require('node:assert/strict');
const {Element,createDocument}=require('./i18n-test-dom.cjs');
const appSource=fs.readFileSync(path.join(__dirname,'..','app.js'),'utf8');
const boot='  render();\n})();';
assert.equal(appSource.split(boot).length,2,'replace only the initial render bootstrap');
const exposed=appSource.replace(boot,`  render=async()=>{state.render++;};
  globalThis.test={state,migrationState};
})();`);

function harness(origin='http://localhost:8080'){
 const document=createDocument(),listeners=new Map(),pending=[],clicks=[],downloads=[],requests=[],pushes=[],confirmations=[],timers=[],revoked=[],blobs=new Map();
 const location=new URL('/studio/migration',origin);
 const h={document,location,clicks,downloads,requests,pushes,confirmations,timers,revoked,blobs,answer:false};
 document.addEventListener=(type,fn)=>{const handlers=listeners.get(type)||[];handlers.push(fn);listeners.set(type,handlers);};
 document.dispatchEvent=event=>{for(const fn of listeners.get(event.type)||[])pending.push(Promise.resolve(fn(event)));return !event.defaultPrevented;};
 const makeElement=(tag,attrs={})=>{
  const node=new Element(tag,attrs,document);
  node.appendChild=node.append.bind(node);
  node.remove=()=>{if(node.parentElement){const parent=node.parentElement;parent.childNodes=parent.childNodes.filter(child=>child!==node);node.parentElement=null;}};
  if(tag==='a'){
   // Real anchors reflect these properties as attributes. In particular,
   // a.download='file.json' must make hasAttribute('download') true.
   for(const key of ['download','target'])Object.defineProperty(node,key,{get:()=>node.getAttribute(key)||'',set:value=>node.setAttribute(key,value)});
   Object.defineProperty(node,'href',{get:()=>new URL(node.getAttribute('href')||'',location.href).href,set:value=>node.setAttribute('href',value)});
  }
  node.click=()=>h.dispatch(node);
  return node;
 };
 document.createElement=makeElement;document.body.appendChild=document.body.append.bind(document.body);
 for(const id of ['app','modal-root','toast'])document.body.append(makeElement('div',{id}));
 h.element=makeElement;
 h.dispatch=(target,extra={})=>{
  const event={type:'click',target,button:0,detail:1,ctrlKey:false,metaKey:false,shiftKey:false,altKey:false,defaultPrevented:false,preventDefault(){this.defaultPrevented=true;},...extra};
  const connected=!!target.closest('body');
  document.dispatchEvent(event);
  clicks.push({event,connected});
  if(target.tagName==='A'&&target.hasAttribute('download')&&!event.defaultPrevented)downloads.push({href:target.href,name:target.download,blob:blobs.get(target.href)});
  return event;
 };
 h.flush=async()=>{while(pending.length)await Promise.all(pending.splice(0));};
 h.click=async(target,extra)=>{const event=h.dispatch(target,extra);await h.flush();return event;};
 class BrowserURL extends URL {
  static createObjectURL(blob){const address=`blob:${origin}/00000000-0000-4000-8000-${String(blobs.size+1).padStart(12,'0')}`;blobs.set(address,blob);return address;}
  static revokeObjectURL(address){revoked.push(address);}
 }
 const context={console,Intl,Date,Map,Set,WeakMap,URL:BrowserURL,URLSearchParams,JSON,Number,String,Array,Math,RegExp,Error,Promise,Blob,TextDecoder,document,location,
  window:{FolioI18n:{locale:'en',text:value=>value},addEventListener(){},scrollTo(){}},
  sessionStorage:{getItem:()=> 'test-token',setItem(){},removeItem(){}},
  history:{pushState(_state,_title,href){pushes.push(href);location.href=new URL(href,location.origin).href;}},navigator:{},
  setTimeout:(fn,delay)=>{timers.push({fn,delay});return timers.length;},clearTimeout(){},
  confirm:message=>{confirmations.push(message);return h.answer;},
  fetch:async(endpoint,options={})=>{const call={name:endpoint.replace('/api/op/',''),body:JSON.parse(options.body||'{}')};requests.push(call);const data=await h.respond(call);return {ok:true,status:200,json:async()=>({ok:true,data})};}
 };
 vm.createContext(context);vm.runInContext(exposed,context);
 h.api=context.test;h.app=document.getElementById('app');
 h.respond=async()=>{throw new Error('Unexpected API operation');};
 assert.equal(listeners.get('click').length,1,'the production document click listener is registered');
 return h;
}

async function testEveryExportBubblesThroughNavigation(){
 const h=harness(),m=h.api.migrationState();
 m.review={target_instance_id:'instance-1',can_apply:true,plan:{id:'frozen-plan-1',entries:[]}};
 m.report={plan_id:'frozen-plan-1',complete:false,outcomes:[{status:'failed'}]};
 const bundle={files:[{path:'story.md',content:'# Saved story'}],manifest:{version:1}};
 const backup={format:'folio-backup',posts:[{title:'Saved story'}]};
 h.respond=async({name,body})=>{if(name==='migration.export'){assert.deepEqual(body,{status:'draft'});return bundle;}assert.equal(name,'backup.export');return backup;};
 const fields={
  'post-title':'Current unsaved title','post-slug':'current-unsaved-title',
  'post-markdown':'# Current unsaved Markdown\n\nKeep these edits.','migration-export-status':'draft'
 };
 for(const [id,value] of Object.entries(fields)){const el=h.element(id==='post-markdown'?'textarea':'input',{id});el.value=value;h.app.append(el);}
 h.api.state.editor={post:{title:'Saved title',markdown:'Old saved Markdown'}};
 h.api.state.dirty=true;
 const before=h.app.innerHTML,editor=h.api.state.editor,markdown=h.document.getElementById('post-markdown'),href=h.location.href;
 const cases=[
  ['migration-download-plan','folio-frozen-import-plan.json',m.review,'application/json'],
  ['migration-download-report','folio-import-report.json',m.report,'application/json'],
  ['migration-export',/^folio-markdown-\d{4}-\d{2}-\d{2}\.json$/,bundle,'application/json'],
  ['export-markdown','current-unsaved-title.md',fields['post-markdown'],'text/markdown;charset=utf-8'],
  ['export',/^folio-backup-\d{4}-\d{2}-\d{2}\.json$/,backup,'application/json']
 ];
 for(const [action,name,expected,type] of cases){
  const button=h.element('button',{'data-action':action});button.dataset.action=action;h.document.body.append(button);
  // Start from a UI action and exercise the nested a.click() through the same
  // document listener. Calling only downloadObject() misses the SPA regression.
  const actionEvent=await h.click(button),download=h.downloads.at(-1);
  assert.equal(actionEvent.defaultPrevented,true,'the action click is handled');
  assert.equal(h.downloads.length,cases.findIndex(c=>c[0]===action)+1,`${action} reaches the native download default`);
  const blobClicks=h.clicks.filter(({event})=>event.target.tagName==='A');
  const {event,connected}=blobClicks.at(-1);
  assert.equal(blobClicks.length,h.downloads.length);
  assert.equal(connected,true,'programmatic anchor was attached while its click bubbled');
  assert.equal(event.defaultPrevented,false,`${action}: global routing must not cancel the blob anchor`);
  assert.equal(new URL(download.href).origin,h.location.origin,'simulate the same-origin blob behavior that caused the regression');
  assert.equal(new URL(download.href).protocol,'blob:');
  if(typeof name==='string')assert.equal(download.name,name);else assert.match(download.name,name);
  assert.equal(download.blob.type,type);
  const text=await download.blob.text();
  if(type==='application/json')assert.deepEqual(JSON.parse(text),expected);else assert.equal(text,expected);
  assert.equal(event.target.parentElement,null,'temporary anchor is removed after dispatch');
  assert.equal(h.location.href,href,'download does not change the URL');assert.deepEqual(h.pushes,[]);
  assert.equal(h.api.state.render,0,'download does not render a 404 or reset the editor');
  assert.equal(h.api.state.dirty,true);assert.equal(h.api.state.editor,editor);
  assert.equal(h.document.getElementById('post-markdown'),markdown);assert.equal(markdown.value,fields['post-markdown']);
  assert.equal(h.app.innerHTML,before);assert.deepEqual(h.confirmations,[],'download never asks to discard unsaved edits');
  button.remove();
 }
 assert.deepEqual(h.requests.map(call=>call.name),['migration.export','backup.export']);
 const cleanup=h.timers.filter(timer=>timer.delay===1000);assert.equal(cleanup.length,5);
 for(const timer of cleanup)timer.fn();
 assert.deepEqual(h.revoked,h.downloads.map(download=>download.href));
 console.log('PASS: all five real export actions bubble same-origin blob clicks through the global listener without cancelling downloads, navigating, rerendering, or discarding unsaved editor state');
}

async function testBrowserNativeNavigation(){
 const cases=[
  ['blob without download','blob:http://localhost:8080/00000000-0000-4000-8000-000000000099',{},{}],
  ['empty download attribute','/about',{download:''},{}],
  ['named download','/about',{download:'about.html'},{}],
  ['native marker','/about',{'data-native':''},{}],
  ...['_blank','_self','_parent','_top','preview'].map(target=>[`target ${target}`,'/about',{target},{}]),
  ['mail protocol','mailto:writer@example.com',{},{}],
  ['telephone protocol','tel:+15550100',{},{}],
  ['data protocol','data:text/plain,hello',{},{}],
  ['javascript protocol','javascript:void(0)',{},{}],
  ['external origin','https://example.com/about',{},{}],
  ['media resource','/media/cover.png',{},{}],
  ['same-page hash','/studio/migration#report',{},{}],
  ...['ctrlKey','metaKey','shiftKey','altKey'].map(key=>[key,'/about',{}, {[key]:true}]),
  ['middle button','/about',{}, {button:1}],
  ['right button','/about',{}, {button:2}],
  ['already cancelled','/about',{}, {defaultPrevented:true}]
 ];
 for(const [label,href,attrs,eventInit] of cases){
  const h=harness(),a=h.element('a',{href,...attrs});h.document.body.append(a);
  const child=a.append(h.element('span'));
  const event=await h.click(child,eventInit);
  assert.equal(event.defaultPrevented,!!eventInit.defaultPrevented,label);
  assert.deepEqual(h.pushes,[],label);assert.equal(h.api.state.render,0,label);assert.equal(h.requests.length,0,label);
 }
 const h=harness(),button=h.element('button',{'data-action':'export'});button.dataset.action='export';
 await h.click(button,{defaultPrevented:true});assert.equal(h.requests.length,0,'cancelled action events are also respected');
 console.log('PASS: download attributes, blob/non-HTTP(S) schemes, native targets, modifiers, non-primary buttons, cancelled events, external URLs, media and same-page hash links remain native');
}

async function testOrdinaryRoutingAndDirtyGuard(){
 for(const origin of ['http://localhost:8080','https://folio.example']){
  const h=harness(origin),a=h.element('a',{href:`${origin}/about?from=migration#author`});
  h.document.body.append(a);const child=a.append(h.element('span'));
  const event=await h.click(child);
  assert.equal(event.defaultPrevented,true);assert.deepEqual(h.pushes,['/about?from=migration#author']);
  assert.equal(h.location.href,`${origin}/about?from=migration#author`);assert.equal(h.api.state.render,1);
  const keyboard=h.element('a',{href:'/topics'});h.document.body.append(keyboard);
  assert.equal((await h.click(keyboard,{detail:0})).defaultPrevented,true,'keyboard activation routes normally');
  assert.equal(h.location.pathname,'/topics');assert.equal(h.api.state.render,2);
 }
 const h=harness(),a=h.element('a',{href:'/about'});h.document.body.append(a);h.api.state.dirty=true;
 assert.equal((await h.click(a)).defaultPrevented,true);assert.equal(h.confirmations.length,1);
 assert.deepEqual(h.pushes,[]);assert.equal(h.api.state.dirty,true);assert.equal(h.api.state.render,0);
 h.answer=true;await h.click(a);assert.deepEqual(h.pushes,['/about']);assert.equal(h.api.state.dirty,false);assert.equal(h.api.state.render,1);
 console.log('PASS: same-origin HTTP/HTTPS, nested-link and keyboard activation preserve SPA routing, query/hash, and unsaved-editor confirmation');
}

(async()=>{await testEveryExportBubblesThroughNavigation();await testBrowserNativeNavigation();await testOrdinaryRoutingAndDirtyGuard();})().catch(error=>{console.error(error);process.exitCode=1;});

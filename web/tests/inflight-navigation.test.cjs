// Real app handlers with a deferred HTTP response; browser coverage is separate.
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const assert = require('node:assert/strict');
const test = require('node:test');

function setup() {
  const route = '/studio/posts/post-1?from=writing#details';
  const fields = {
    '#post-title': {value:'A saved story'}, '#post-slug': {value:'saved-story'},
    '#post-markdown': {value:'The submitted text.',selectionStart:3,selectionEnd:7,scrollTop:42},
    '#post-excerpt': {value:''}, '#post-tags': {value:''}, '#post-category': {value:''},
    '#post-cover': {value:''}, '#post-featured': {checked:false},
    '#save-state': {textContent:''}, '#revision-history': {innerHTML:''},
  };
  const events = {}, storage = new Map([['folio.token','fixture-session']]);
  const app = {innerHTML:'editor'}, toast = {}, modal = {innerHTML:'',children:[]};
  let resolveResponse, confirmations = 0, requests = 0;
  const context = {
    Intl,Date,Map,Set,URL,URLSearchParams,JSON,Number,String,Array,Math,RegExp,Error,Promise,
    setTimeout:()=>1,clearTimeout(){},queueMicrotask,
    document:{activeElement:fields['#post-markdown'],
      getElementById:id=>id==='app'?app:id==='toast'?toast:null,
      querySelector:s=>s==='#toast'?toast:s==='#modal-root'?modal:fields[s]||null,
      querySelectorAll:()=>[],addEventListener(){},body:{classList:{remove(){}}},documentElement:{dataset:{}}},
    window:{addEventListener:(name,fn)=>{events[name]=fn;},scrollTo(){},FolioI18n:{locale:'en',text:v=>v,t:k=>k,apply(){},formatDate:v=>v,control:()=>''}},
    sessionStorage:{getItem:k=>storage.get(k)||'',setItem:(k,v)=>storage.set(k,v),removeItem:k=>storage.delete(k)},
    history:{pushState:(_,__,value)=>{context.lastPushed=value;},replaceState(){}},
    navigator:{},crypto:{randomUUID:()=> 'fixture-retry'},
    location:{origin:'http://localhost:8080',pathname:'/studio/posts/post-1',search:'?from=writing',hash:'#details'},
    confirm:()=>{confirmations++;return context.allowDiscard;},
    fetch:()=>{requests++;return new Promise(resolve=>{resolveResponse=resolve;});},
  };
  vm.createContext(context);
  const source=fs.readFileSync(path.join(__dirname,'..','app.js'),'utf8').replace(
    '  render();\n})();','  globalThis.test={state,saveDraft,handleAction};\n})();');
  vm.runInContext(source,context);
  const {state,saveDraft,handleAction}=context.test;
  const original={id:'post-1',title:'A saved story',slug:'saved-story',markdown:'Old text.',tags:[],revision:1,published_revision:0};
  state.editor={post:original,revisions:[original]}; state.dirty=true;
  return {route,fields,events,storage,context,state,saveDraft,handleAction,app,
    confirmations:()=>confirmations,requests:()=>requests,
    finish:()=>resolveResponse({ok:true,status:200,json:async()=>({ok:true,data:{post:{...original,markdown:'The submitted text.',revision:2}}})})};
}

test('locking during a save preserves session, editor and newer typing',async()=>{
  const h=setup(), editor=h.state.editor;
  const saving=h.saveDraft(true);
  h.fields['#post-markdown'].value='Newer unsaved text.';
  h.context.allowDiscard=true;
  await h.handleAction('logout');
  assert.equal(h.state.editor,editor);
  assert.equal(h.storage.get('folio.token'),'fixture-session');
  assert.equal(h.confirmations(),0,'an active write cannot be discarded');
  h.finish();await saving;
  assert.equal(h.state.editor.post.revision,2);
  assert.equal(h.state.dirty,true);
  assert.equal(h.fields['#post-markdown'].value,'Newer unsaved text.');
});

test('browser Back during a save restores the exact editor URL and buffer',async()=>{
  const h=setup(), editor=h.state.editor;
  const saving=h.saveDraft(true);
  h.fields['#post-markdown'].value='Newer unsaved text.';
  h.context.allowDiscard=true;
  h.context.location.pathname='/studio';h.context.location.search='';h.context.location.hash='';
  h.events.popstate();
  assert.equal(h.context.lastPushed,h.route);
  assert.equal(h.confirmations(),0);
  assert.equal(h.requests(),1,'Back must not rerender or refetch while saving');
  assert.equal(h.state.editor,editor);
  assert.equal(h.app.innerHTML,'editor');
  h.finish();await saving;
  assert.equal(h.state.dirty,true);
  assert.equal(h.fields['#post-markdown'].value,'Newer unsaved text.');
  assert.equal(h.fields['#post-markdown'].selectionStart,3);
  assert.equal(h.fields['#post-markdown'].selectionEnd,7);
  assert.equal(h.fields['#post-markdown'].scrollTop,42);
});

test('cancelled Back preserves query/hash; unload protects an active write',()=>{
  const h=setup();h.context.allowDiscard=false;
  h.context.location.pathname='/studio';h.events.popstate();
  assert.equal(h.context.lastPushed,h.route);
  h.state.dirty=false;h.state.busy=true;
  let prevented=false;const event={preventDefault(){prevented=true;}};
  h.events.beforeunload(event);
  assert.equal(prevented,true);assert.equal(event.returnValue,'');
});

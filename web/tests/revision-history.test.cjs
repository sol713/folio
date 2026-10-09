// Source-level DOM-contract regression test. This is not a browser screenshot test.
// Run: node web/tests/revision-history.test.cjs
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const assert = require('node:assert/strict');

async function run() {
  const fields = {
    '#post-title': {value:'A first story'}, '#post-slug': {value:'a-first-story'},
    '#post-markdown': {value:'The first sentence.',selectionStart:4,selectionEnd:9,scrollTop:18},
    '#post-excerpt': {value:''}, '#post-tags': {value:''}, '#post-category': {value:''},
    '#post-cover': {value:''}, '#post-featured': {checked:false},
    '#save-state': {textContent:''}, '#revision-history': {innerHTML:''},
  };
  const app = {innerHTML:''};
  const toast = {textContent:'',className:''};
  const markdown = fields['#post-markdown'];
  let resolveResponse;
  let writes = 0;
  const context = {
    console,Intl,Date,Map,Set,URL,URLSearchParams,JSON,Number,String,Array,Math,RegExp,Error,Promise,
    setTimeout:()=>1,clearTimeout(){},
    document: {
      activeElement:markdown,
      getElementById:id=>id==='app'?app:id==='toast'?toast:null,
      querySelector:s=>s==='#toast'?toast:fields[s]||null,
      querySelectorAll:()=>[],addEventListener(){},body:{classList:{remove(){}}},
    },
    window:{addEventListener(){},FolioI18n:{locale:'en',text:v=>v,t:k=>k,apply(){},formatDate:(v)=>new Intl.DateTimeFormat('en',{month:'short',day:'numeric',year:'numeric'}).format(new Date(v)),control:()=>''}},
    sessionStorage:{getItem:()=>'',setItem(){},removeItem(){}},
    history:{replaceState(){}},navigator:{},crypto:{randomUUID:()=>`retry-${writes}`},
    location:{origin:'http://localhost:8080',pathname:'/studio/new',search:'',href:'http://localhost:8080/studio/new'},
    fetch:()=>{writes++;return new Promise(resolve=>{resolveResponse=resolve;});},
  };
  vm.createContext(context);
  const source=fs.readFileSync(path.join(__dirname,'..','app.js'),'utf8').replace(
    '  render();\n})();',
    '  globalThis.test={state,saveDraft,revisionHistoryMarkup};\n})();'
  );
  vm.runInContext(source,context);
  const {state,saveDraft,revisionHistoryMarkup}=context.test;
  state.editor={post:{title:'',slug:'',markdown:'',tags:[],revision:0,published_revision:0},revisions:[]};
  state.dirty=true;
  const saved={id:'post-1',title:'A first story',slug:'a-first-story',markdown:'The first sentence.',tags:[],revision:1,published_revision:0,updated_at:'2026-10-03T15:00:00Z'};
  const firstSave=saveDraft(true);
  // Reproduce a user continuing to type while the network write is pending.
  markdown.value='The first sentence. A newer unsaved sentence.';
  resolveResponse({ok:true,status:200,json:async()=>({ok:true,data:{post:saved}})});
  await firstSave;
  assert.equal(state.editor.revisions.length,1);
  assert.equal(state.editor.revisions[0].revision,1);
  assert.match(fields['#revision-history'].innerHTML,/id="revision-count">1</);
  assert.match(fields['#revision-history'].innerHTML,/data-revision="1"/);
  assert.match(fields['#revision-history'].innerHTML,/revision-current/);
  assert.doesNotMatch(fields['#revision-history'].innerHTML,/Your first save begins/);
  assert.equal(state.dirty,true,'newer edits must remain dirty');
  assert.equal(fields['#save-state'].textContent,'Unsaved changes');
  assert.equal(markdown.value,'The first sentence. A newer unsaved sentence.');
  assert.equal(context.document.activeElement,markdown,'focus must be unchanged');
  assert.equal(markdown.selectionStart,4,'selection must be unchanged');
  assert.equal(markdown.selectionEnd,9);
  assert.equal(markdown.scrollTop,18,'editor scroll position must be unchanged');

  const secondSave=saveDraft(true);
  const saved2={...saved,markdown:markdown.value,revision:2};
  resolveResponse({ok:true,status:200,json:async()=>({ok:true,data:{post:saved2}})});
  await secondSave;
  assert.equal(state.editor.revisions.length,2);
  assert.equal(state.dirty,false);
  assert.equal(fields['#save-state'].textContent,'All changes saved');
  assert.match(fields['#revision-history'].innerHTML,/id="revision-count">2</);
  assert.match(fields['#revision-history'].innerHTML,/data-restore="1"/);
  assert.match(fields['#revision-history'].innerHTML,/data-revision="2"/);
  assert.ok(fields['#revision-history'].innerHTML.indexOf('data-revision="2"')<fields['#revision-history'].innerHTML.indexOf('data-revision="1"'),'newest revision must come first');
  await saveDraft(true);
  assert.equal(writes,2,'saving unchanged text must not create a duplicate revision');
  assert.equal(state.editor.revisions.length,2);
  const liveMarkup=revisionHistoryMarkup({...saved2,published_revision:1},state.editor.revisions);
  assert.match(liveMarkup,/Revision 1 · live/);

  // Simulate a daemon committing a create while its HTTP response is lost.
  // The retry transport caches successful writes by the canonical retry key.
  let keySequence=0;
  context.crypto.randomUUID=()=>`lost-response-${++keySequence}`;
  const committed=new Map();
  const requests=[];
  let createdPosts=0;
  let loseNextResponse=true;
  context.fetch=async (endpoint,options)=>{
    const payload=JSON.parse(options.body);
    requests.push({endpoint,body:options.body,payload});
    let post=committed.get(payload.idempotency_key);
    if(!post){
      const creating=endpoint.endsWith('/posts.create');
      if(creating) createdPosts++;
      post={...payload,id:creating?`lost-post-${createdPosts}`:payload.id,revision:creating?1:payload.expected_revision+1,published_revision:0,updated_at:'2026-10-03T15:00:00Z'};
      committed.set(payload.idempotency_key,post);
    }
    if(loseNextResponse){loseNextResponse=false;throw new Error('Simulated connection reset after commit');}
    return {ok:true,status:200,json:async()=>({ok:true,data:{post}})};
  };
  const newEditor=()=>{
    state.editor={post:{title:'',slug:'',markdown:'',tags:[],revision:0,published_revision:0},revisions:[]};
    state.dirty=true;
    fields['#post-title'].value='Recovered draft';
    fields['#post-slug'].value='recovered-draft';
    markdown.value='The original pending text.';
  };
  newEditor();
  await assert.rejects(saveDraft(true),/Retry Save to safely resolve/);
  const firstPending=state.editor.pendingWrite;
  assert.equal(firstPending.operation,'posts.create');
  assert.equal(firstPending.key,firstPending.payload.idempotency_key);
  assert.equal(firstPending.fingerprint,JSON.stringify({title:'Recovered draft',slug:'recovered-draft',markdown:'The original pending text.',excerpt:'',tags:[],category:'',cover:'',featured:false}));
  await saveDraft(true);
  assert.equal(createdPosts,1,'same-content retry must not create another post');
  assert.equal(requests[0].body,requests[1].body,'the exact payload and key must be reused');
  assert.equal(state.editor.post.id,'lost-post-1');
  assert.equal(state.editor.pendingWrite,null,'only acknowledge and clear after a successful response');
  assert.equal(state.dirty,false);
  assert.equal(state.editor.revisions.length,1);

  // Changed content must first resolve the original create, then update that
  // acknowledged ID with a fresh key. Never send a second create for new text.
  newEditor();
  loseNextResponse=true;
  await assert.rejects(saveDraft(true),/Retry Save to safely resolve/);
  markdown.value='Newer text written after the lost response.';
  fields['#post-title'].value='A changed working title';
  const uncertainKey=state.editor.pendingWrite.key;
  await saveDraft(true);
  assert.equal(createdPosts,2,'the changed-content retry resolves only its original create');
  assert.equal(requests[2].body,requests[3].body,'changed fields must not alter the pending create');
  assert.equal(state.editor.post.id,'lost-post-2');
  assert.equal(state.dirty,true,'newer content remains unsaved after recovery');
  assert.equal(fields['#save-state'].textContent,'Unsaved changes');
  assert.equal(markdown.value,'Newer text written after the lost response.');
  assert.equal(fields['#post-title'].value,'A changed working title');
  assert.equal(context.document.activeElement,markdown);
  assert.equal(markdown.selectionStart,4);
  assert.equal(markdown.selectionEnd,9);
  assert.equal(markdown.scrollTop,18);
  await saveDraft(true);
  const update=requests[4];
  assert.match(update.endpoint,/posts.update$/);
  assert.equal(update.payload.id,'lost-post-2');
  assert.equal(update.payload.expected_revision,1);
  assert.notEqual(update.payload.idempotency_key,uncertainKey);
  assert.equal(update.payload.markdown,'Newer text written after the lost response.');
  assert.equal(state.editor.post.revision,2);
  assert.equal(state.editor.revisions.length,2);
  assert.equal(state.dirty,false);
  assert.equal(createdPosts,2,'new edits use update, not another create');
  console.log('PASS: history refresh; caret/in-flight edits; same-content lost-response retry; changed-content pending-create recovery then update; no duplicate create/revision');

}
run().catch(error=>{console.error(error);process.exitCode=1;});

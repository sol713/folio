// Deterministic source-level operation/DOM contracts, not browser or server integration tests.
const fs=require('node:fs'),path=require('node:path'),vm=require('node:vm'),assert=require('node:assert/strict');
const {Element,createDocument}=require('./i18n-test-dom.cjs');
const root=path.join(__dirname,'..');
const appSource=fs.readFileSync(path.join(root,'app.js'),'utf8');
const boot='  render();\n})();';
assert.equal(appSource.split(boot).length,2,'the test must replace exactly the app bootstrap');
const exposed=appSource.replace(boot,`  render=async()=>{state.render++;app.innerHTML=await migrationPage();I18n.apply(app);};
  globalThis.test={state,render,migrationState,migrationPage,migrationPlanView,migrationReportView,migrationDiagnostics,selectMigrationFiles,prepareMigration,confirmMigration,resetMigration,closeModal};
})();`);

function harness(){
 const document=createDocument(),storage=new Map([['folio.locale','en']]),calls=[],confirmations=[];
 for(const id of ['app','modal-root','toast'])document.body.append(new Element('div',{id},document));
 const location={origin:'http://localhost:8080',pathname:'/studio/migration',search:'',href:'http://localhost:8080/studio/migration'};
 const h={document,calls,confirmations,answer:false,respond:async()=>{throw new Error('Unexpected operation');}};
 const context={console,Intl,Date,Map,Set,WeakMap,URL,URLSearchParams,JSON,Number,String,Array,Math,RegExp,Error,Promise,Blob,TextDecoder,document,location,
  window:{addEventListener(){},scrollTo(){}},localStorage:{getItem:k=>storage.get(k)||null,setItem:(k,v)=>storage.set(k,v)},
  sessionStorage:{getItem:()=> 'test-token',setItem(){},removeItem(){}},history:{replaceState(){},pushState(){}},navigator:{},
  crypto:{randomUUID:()=> 'deterministic-test-key'},setTimeout:()=>1,clearTimeout(){},queueMicrotask:fn=>fn(),
  CustomEvent:class{constructor(type,{detail}){this.type=type;this.detail=detail;}},
  confirm:message=>{confirmations.push(message);return h.answer;},
  fetch:async(endpoint,options={})=>{const call={name:endpoint.replace('/api/op/',''),body:JSON.parse(options.body||'{}'),raw:options.body,headers:options.headers};calls.push(call);const data=await h.respond(call);return {ok:true,status:200,json:async()=>({ok:true,data})};}
 };
 vm.createContext(context);vm.runInContext(fs.readFileSync(path.join(root,'i18n.js'),'utf8'),context);vm.runInContext(exposed,context);
 h.api=context.test;h.i18n=context.window.FolioI18n;h.context=context;h.app=document.getElementById('app');h.modal=document.getElementById('modal-root');
 h.api.state.info={actor:'admin',permissions:['migration.plan','migration.apply','migration.export']};
 h.api.state.site={settings:{title:'FOLIO',author:'Writer'},posts:[]};
 h.select=files=>h.api.selectMigrationFiles({files});
 h.confirmButton=()=>document.querySelector('#workflow-confirm');
 return h;
}
function file(name,body='Settings'){const bytes=Buffer.isBuffer(body)?body:Buffer.from(body,'utf8');return {name,size:bytes.byteLength,reads:0,async arrayBuffer(){this.reads++;return bytes.buffer.slice(bytes.byteOffset,bytes.byteOffset+bytes.byteLength);}};}
function fixture(){
 const doc={source_path:'Settings.md',title:'Settings',slug:'settings',markdown:'Save draft\n<script>neverRun()</script>\n<img src=x onerror=neverRun()>',excerpt:'No activity yet.',tags:['Drafts'],category:'Journal',cover:'',featured:false,draft:true,date:'2020-01-01T00:00:00Z',publish_date:'2021-01-01T00:00:00Z',extra:{label:'Settings',html:'<img src=x onerror=neverRun()>'},warnings:[],content_hash:'fixture-content-hash'};
 return {target_instance_id:'instance-Settings',can_apply:true,summary:{create:1,update:1,skip:1,blocked:0},validation_issues:[],plan:{format:'folio-migration-plan',version:1,id:'frozen-plan-1',diagnostics:[],entries:[
  {action:'create',document:doc,idempotency_key:'create-key'},
  {action:'update',target_id:'existing-draft',expected_revision:7,idempotency_key:'update-key',document:{...doc,source_path:'Drafts.markdown',slug:'drafts'}},
  {action:'skip',document:{...doc,source_path:'Journal.md',slug:'journal'}}
 ]}};
}
const clone=value=>JSON.parse(JSON.stringify(value));
async function prepared(h,data=fixture()){
 h.respond=async({name})=>{assert.equal(name,'migration.plan');return data;};
 await h.select([file('Settings.md','Settings\n正在写作'),file('Drafts.markdown','Save draft'),file('Journal.md','Journal')]);
 await h.api.prepareMigration();return h.api.migrationState();
}
function sameLocaleState(h){
 const m=h.api.migrationState(),before=JSON.stringify(m),files=m.files,review=m.review,report=m.report,frozen=m.frozenPlanJSON,renders=h.api.state.render,requests=h.calls.length;
 const input=h.document.querySelector('#migration-files'),selection=[file('native-selection.md','Keep me')];
 input.files=selection;input.value='C:\\fakepath\\native-selection.md';input.focus();
 const confirm=h.confirmButton(),handler=confirm?.onclick;
 const writing=h.document.querySelector('a[href="/studio"]'),english=writing.textContent;
 for(const locale of ['zh-CN','en']){
  h.i18n.setLocale(locale);
  assert.equal(h.api.migrationState(),m,'locale switch retains migration state identity');
  assert.equal(JSON.stringify(m),before,'locale switch retains all migration values, including error/lock/report');
  assert.equal(m.files,files);assert.equal(m.review,review);assert.equal(m.report,report);assert.equal(m.frozenPlanJSON,frozen);
  assert.equal(h.document.querySelector('#migration-files'),input,'locale switch never replaces the file input');
  assert.equal(input.files,selection,'native file selection is retained');assert.equal(input.value,'C:\\fakepath\\native-selection.md');assert.equal(h.document.activeElement,input);
  assert.equal(h.confirmButton(),confirm);assert.equal(confirm?.onclick,handler,'pending confirmation handler survives locale switch');
  assert.equal(h.api.state.render,renders,'locale is presentation-only');assert.equal(h.calls.length,requests,'locale switch makes no operation request');
  assert.equal(writing.textContent,locale==='en'?english:h.i18n.text(english),'shared Studio label changes language in place');
 }
}

async function testFrozenConfirmationAndRetries(){
 const h=harness(),data=fixture(),m=await prepared(h,data),frozen=m.frozenPlanJSON;
 assert.equal(h.calls.length,1);assert.equal(h.calls[0].name,'migration.plan');
 assert.deepEqual(h.calls[0].body,{files:clone(m.files),conflict:'error'});
 assert.equal(frozen,JSON.stringify(data.plan));sameLocaleState(h);
 // The serialised snapshot, not a mutable response object, is the apply authority.
 data.plan.entries[0].document.title='Mutated after review';
 h.api.confirmMigration();
 assert.equal(h.calls.length,1,'opening the review dialog cannot apply the plan');
 assert.match(h.modal.textContent,/Create 1 private drafts and replace 1 existing drafts/);
 assert.match(h.modal.textContent,/instance-Settings/);
 assert.match(h.modal.textContent,/Nothing is published/);
 h.api.closeModal();assert.equal(h.calls.length,1,'dismissal cannot apply the plan');assert.equal(m.locked,false);
 h.api.confirmMigration();
 let rejectPending;
 h.respond=({name})=>{assert.equal(name,'migration.apply');return new Promise((_resolve,reject)=>{rejectPending=reject;});};
 const pending=h.confirmButton().onclick();
 assert.equal(m.locked,true,'the batch locks before the first apply response');assert.equal(h.confirmButton().disabled,true);
 const expected={plan:JSON.parse(frozen),target_instance_id:'instance-Settings',confirm:true,continue_on_error:false};
 assert.deepEqual(h.calls.at(-1).body,expected);assert.equal(h.calls.at(-1).headers['Accept-Language'],'en');
 sameLocaleState(h);rejectPending(new Error('Response lost'));await pending;
 assert.equal(m.locked,true);assert.equal(m.report,null);assert.equal(m.error,'Response lost');assert.equal(h.confirmButton().disabled,false);
 assert.equal(h.document.querySelector('#workflow-error').textContent,'Response lost');sameLocaleState(h);
 // A successful transport without a valid report is also uncertain, not success.
 h.respond=async()=>({});await h.confirmButton().onclick();
 assert.match(m.error,/could not be confirmed/);assert.equal(m.locked,true);assert.equal(m.report,null);assert.equal(h.confirmButton().disabled,false);
 const partial={plan_id:'frozen-plan-1',complete:false,outcomes:[{source_path:'Settings.md',status:'applied',post_id:'created-draft',revision:1},{source_path:'Drafts.markdown',status:'failed',error:'Revision conflict'},{source_path:'Journal.md',status:'pending'}]};
 h.respond=async()=>({report:partial});await h.confirmButton().onclick();
 assert.equal(m.report,partial);assert.equal(m.locked,true);assert.equal(m.frozenPlanJSON,frozen);assert.equal(h.confirmButton(),null);
 assert.match(h.app.textContent,/Successfully applied drafts are retained/);assert.match(h.app.textContent,/Retry the same frozen plan/);
 assert.equal(h.document.querySelector('#migration-files').hasAttribute('disabled'),true);
 assert.equal(h.document.querySelector('#migration-conflict').hasAttribute('disabled'),true);
 assert.equal(h.document.querySelector('[data-action="migration-plan"]').hasAttribute('disabled'),true);
 const lockedState=JSON.stringify(m),requests=h.calls.length,replacement=file('replacement.md');
 await h.select([replacement]);await h.api.prepareMigration();
 assert.equal(replacement.reads,0);assert.equal(h.calls.length,requests,'locked retry cannot re-plan');assert.equal(JSON.stringify(m),lockedState);
 sameLocaleState(h);
 h.api.confirmMigration();assert.equal(h.calls.length,requests,'retry still requires explicit confirmation');
 h.respond=async()=>({report:{...partial,complete:true,outcomes:[{source_path:'Settings.md',status:'applied',post_id:'created-draft',revision:1},{source_path:'Drafts.markdown',status:'applied',post_id:'existing-draft',revision:8},{source_path:'Journal.md',status:'skipped'}]}});
 await h.confirmButton().onclick();
 const applies=h.calls.filter(c=>c.name==='migration.apply');assert.equal(applies.length,4);
 for(const call of applies){assert.equal(call.raw,applies[0].raw,'failed, uncertain, and partial retries send byte-identical payloads');assert.deepEqual(call.body,expected);}
 assert.equal(m.locked,false);assert.equal(m.report.complete,true);assert.equal(m.frozenPlanJSON,frozen);
 assert.match(h.app.textContent,/Every applied item is a private draft; nothing was published/);
 assert.ok(h.calls.every(c=>c.name==='migration.plan'||c.name==='migration.apply'),'migration never calls post publication or direct write operations');
 console.log('PASS: explicit frozen-plan confirmation; lost/malformed/partial apply retry identity; target, revision and confirmation fields; private-only completion; locale preservation while pending and locked');
}

async function testContentExclusion(){
 const h=harness(),data=fixture(),m=await prepared(h,data),frozen=m.frozenPlanJSON;
 data.plan.entries[0].document.source_path='Settings <img src=x>.md';
 data.plan.entries[1].document.title='Journal';data.plan.entries[2].document.title='Drafts';
 m.files[0].path='Settings <script>neverRun()</script>.md';
 await h.api.render();
 const protectedNodes=h.app.querySelectorAll('[data-no-i18n]').map(node=>({node,text:node.textContent}));
 assert.ok(protectedNodes.some(({text})=>text===data.plan.entries[0].document.markdown));
 assert.ok(protectedNodes.some(({text})=>text===JSON.stringify(data.plan.entries[0].document.extra,null,2)));
 assert.ok(protectedNodes.some(({text})=>text===m.files[0].path));
 assert.ok(protectedNodes.some(({text})=>text==='Settings'));assert.ok(protectedNodes.some(({text})=>text==='Journal'));assert.ok(protectedNodes.some(({text})=>text==='Drafts'));
 for(const locale of ['zh-CN','en','zh-CN']){h.i18n.setLocale(locale);for(const {node,text} of protectedNodes)assert.equal(node.textContent,text,`source content remains verbatim in ${locale}`);}
 assert.equal(h.app.querySelector('script'),null,'escaped source markup does not create executable script nodes');
 assert.equal(h.app.querySelector('img'),null,'escaped filename, Markdown and metadata cannot create image nodes');
 assert.equal(m.frozenPlanJSON,frozen,'display and locale changes never rewrite the frozen source snapshot');
 console.log('PASS: source titles, filenames, Markdown, retained metadata and target identifiers remain escaped and excluded from localization');
}

async function testFileBoundaries(){
 const h=harness();await h.api.render();
 const uppercase=[file('UPPER.MD','文字\nSettings'),file('mixed.MarkDown','Save draft')];
 await h.select(uppercase);assert.deepEqual(clone(h.api.migrationState().files),[{path:'UPPER.MD',content:'文字\nSettings'},{path:'mixed.MarkDown',content:'Save draft'}]);
 let m=h.api.migrationState(),before=JSON.stringify(m),renders=h.api.state.render;
 await h.select([]);assert.equal(JSON.stringify(m),before);assert.equal(h.api.state.render,renders,'cancelled selection is a no-op');
 for(const name of ['story.txt','story.md.html','story','story.markdown.js']){
  const invalid=file(name);await assert.rejects(h.select([invalid]),/ending in \.md or \.markdown/);assert.equal(invalid.reads,0);assert.equal(JSON.stringify(m),before);
 }
 const validFirst=file('valid.md','Must not replace the prior selection'),invalidUTF8=file('invalid.md',Buffer.from([0xc3,0x28]));
 await assert.rejects(h.select([validFirst,invalidUTF8]),/not valid UTF-8/);assert.equal(validFirst.reads,1);assert.equal(invalidUTF8.reads,1);assert.equal(JSON.stringify(m),before,'partial decoding never commits half of a new selection');
 const exactly200=Array.from({length:200},(_,i)=>file(`source-${i}.md`,''));
 await h.select(exactly200);assert.equal(m.files.length,200,'200 files is an inclusive boundary');
 before=JSON.stringify(m);const tooMany=[...exactly200,file('source-200.md','')],reads=exactly200.map(f=>f.reads);
 await assert.rejects(h.select(tooMany),/no more than 200/);assert.deepEqual(exactly200.map(f=>f.reads),reads);assert.equal(tooMany.at(-1).reads,0);assert.equal(JSON.stringify(m),before);
 // Real byte lengths, including multibyte text, exercise the aggregate 8 MiB limit.
 const limit=8*1024*1024,half='é'.repeat(limit/4),atLimit=[file('first.md',half),file('second.markdown',half)];
 assert.equal(atLimit.reduce((sum,f)=>sum+f.size,0),limit);await h.select(atLimit);
 assert.equal(m.files.length,2);assert.equal(Buffer.byteLength(m.files[0].content)+Buffer.byteLength(m.files[1].content),limit);
 const selected=m.files,overLimit=[...atLimit,file('one-byte.md','a')],readCounts=atLimit.map(f=>f.reads);
 await assert.rejects(h.select(overLimit),/exceed the 8 MiB/);assert.equal(m.files,selected);assert.deepEqual(atLimit.map(f=>f.reads),readCounts);assert.equal(overLimit.at(-1).reads,0);
 assert.equal(h.calls.length,0,'selection and local validation do not upload source contents');
 console.log('PASS: inclusive 200-file and 8 MiB byte limits; case-insensitive Markdown extensions; fatal UTF-8 decoding; atomic rejected and cancelled selections; no selection-time uploads');
}

async function testBlockedPlansAndReset(){
 const h=harness();await h.api.render();await h.api.prepareMigration();h.api.confirmMigration();
 assert.equal(h.calls.length,0);assert.equal(h.confirmButton(),null,'no selected files means no plan or apply');
 await h.select([file('Settings.md')]);
 for(const incomplete of [{},{plan:{entries:[]}},{plan:{},target_instance_id:'target'}]){
  h.respond=async()=>incomplete;await h.api.prepareMigration();const m=h.api.migrationState();assert.match(m.error,/incomplete import plan/);assert.equal(m.frozenPlanJSON,null);assert.equal(m.review,null);h.api.confirmMigration();assert.equal(h.confirmButton(),null);
 }
 const data=fixture(),diagnostic={source_path:'Settings.md',code:'conflict',message:'Slug already exists'};
 data.can_apply=false;data.plan.diagnostics=[diagnostic];data.validation_issues=[clone(diagnostic)];
 h.respond=async()=>data;await h.api.prepareMigration();
 assert.equal(h.api.migrationDiagnostics(data).length,1,'duplicate plan/validation diagnostics are shown once');
 assert.equal(h.document.querySelector('[data-action="migration-apply"]').hasAttribute('disabled'),true);
 assert.match(h.app.textContent,/This plan cannot be applied/);const before=h.calls.length;h.api.confirmMigration();assert.equal(h.calls.length,before);assert.equal(h.confirmButton(),null);
 let m=h.api.migrationState();m.locked=true;m.report={complete:false,outcomes:[]};m.error='Unconfirmed result';
 const snapshot=JSON.stringify(m),renders=h.api.state.render;
 h.answer=false;await h.api.resetMigration();
 assert.equal(h.api.migrationState(),m);assert.equal(JSON.stringify(m),snapshot);assert.equal(h.api.state.render,renders);
 assert.match(h.confirmations.at(-1),/partial or unconfirmed results/);assert.match(h.confirmations.at(-1),/Download the frozen plan/);
 h.answer=true;await h.api.resetMigration();
 assert.deepEqual(clone(h.api.migrationState()),{files:[],conflict:'error',review:null,frozenPlanJSON:null,report:null,locked:false,error:''});assert.equal(h.calls.length,before,'discarding a review performs no server mutation');
 const prompts=h.confirmations.length;await h.api.resetMigration();assert.equal(h.confirmations.length,prompts,'unlocked reset does not show the partial-result warning');
 // A fresh selection invalidates the old review and report but retains the chosen policy.
 m=await prepared(h);m.conflict='update';m.report={complete:true,outcomes:[]};m.error='Old error';
 await h.select([file('new.md','New source')]);
 assert.equal(m.conflict,'update');assert.equal(m.review,null);assert.equal(m.frozenPlanJSON,null);assert.equal(m.report,null);assert.equal(m.error,'');assert.deepEqual(clone(m.files),[{path:'new.md',content:'New source'}]);
 console.log('PASS: incomplete/blocked plans cannot apply; deduplicated diagnostics; locked reset warns and honors cancellation; fresh source selection invalidates stale review');
}

(async()=>{
 await testFrozenConfirmationAndRetries();await testContentExclusion();await testFileBoundaries();await testBlockedPlansAndReset();
})().catch(err=>{console.error(err);process.exitCode=1;});

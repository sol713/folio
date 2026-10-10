// Real Web Crypto/storage behavior; no API or editor mock writes are involved.
const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const vm=require('node:vm');
const {webcrypto}=require('node:crypto');
class Store{
  values=new Map(); fail=false;
  get length(){return this.values.size;} key(i){return [...this.values.keys()][i]||null;}
  getItem(k){return this.values.get(k)||null;}
  setItem(k,v){if(this.fail)throw Object.assign(new Error('full'),{name:'QuotaExceededError'});this.values.set(k,String(v));}
  removeItem(k){this.values.delete(k);}
}
function setup(){
  const session=new Store(),local=new Store();
  const c={window:{sessionStorage:session,localStorage:local},crypto:webcrypto,TextEncoder,TextDecoder,Uint8Array,Date,JSON,Error,Promise,Number,String,Array,Object,Math,
    btoa:s=>Buffer.from(s,'binary').toString('base64'),atob:s=>Buffer.from(s,'base64').toString('binary')};
  vm.createContext(c);vm.runInContext(fs.readFileSync('web/recovery.js','utf8'),c);
  const create=overrides=>c.window.FolioRecovery.create({token:'fictional-writer-token',origin:'http://localhost',instance:'fictional-instance',actor:'owner',...overrides});
  const fields={title:'Private fictional title',slug:'private-slug',markdown:'Secret unfinished fictional Markdown',excerpt:'Deck',tags:['one','two'],category:'Notes',cover:'',featured:true};
  const snapshot={postId:'post-private',baseRevision:1,base:fields,fields:{...fields,markdown:fields.markdown+' newer'}};
  return {create,session,local,fields,snapshot,c};
}
test('authenticated encryption isolates token, origin, instance, role and post; tampering is not applied',async()=>{
  const h=setup(),v=await h.create();await v.write(h.snapshot);
  const raw=JSON.stringify([...h.session.values]);
  for(const secret of ['fictional-writer-token','post-private',h.fields.title,h.fields.markdown,'Deck'])assert.ok(!raw.includes(secret));
  assert.equal((await (await h.create()).list('post-private')).copies.length,1);
  assert.equal((await v.list('other-post')).copies.length,0);
  for(const scope of [{token:'another-token'},{origin:'http://other'},{instance:'another-instance'},{actor:'reader'}])assert.equal((await (await h.create(scope)).list('post-private')).copies.length,0);
  const entry=[...h.session.values.keys()][0],e=JSON.parse(h.session.getItem(entry));e.cipher=e.cipher.slice(0,-4)+'AAAA';h.session.setItem(entry,JSON.stringify(e));
  const damaged=await v.list('post-private');assert.equal(damaged.copies.length,0);assert.equal(damaged.issues.length,1);assert.ok(h.session.getItem(entry));
});
test('separate writers preserve both copies; clearing invalidates an in-flight encryption',async()=>{
  const h=setup(),a=await h.create(),b=await h.create();
  await a.write(h.snapshot);await b.write({...h.snapshot,fields:{...h.snapshot.fields,title:'Second tab'}});
  assert.equal((await a.list('post-private')).copies.length,2);a.removeOwn();assert.equal((await b.list('post-private')).copies.length,1);
  const pending=b.write(h.snapshot);b.clear();await pending;assert.equal((await b.list('post-private')).copies.length,0);assert.equal(b.mode(),'off');
  assert.ok(!JSON.stringify([...h.local.values]).includes('fictional-writer-token'));
});
test('latest captured input wins only in its own slot; quota failures retain the last confirmed copy',async()=>{
  const h=setup(),v=await h.create();const first=v.write(h.snapshot),last=v.write({...h.snapshot,fields:{...h.snapshot.fields,title:'Latest'}});
  await Promise.all([first,last]);assert.equal((await v.list('post-private')).copies[0].fields.title,'Latest');
  h.session.fail=true;await assert.rejects(v.write(h.snapshot),e=>e.code==='QUOTA');h.session.fail=false;
  assert.equal((await v.list('post-private')).copies[0].fields.title,'Latest');
  await assert.rejects(v.write({...h.snapshot,fields:{...h.fields,markdown:'x'.repeat(512*1024)}}),e=>e.code==='LIMIT');
});
test('persistent opt-in, expiry, lock and unsupported versions preserve their explicit boundaries',async()=>{
  const h=setup(),v=await h.create();await v.setMode('persistent');await v.write(h.snapshot);assert.equal(h.local.length,2);assert.equal(h.session.length,1);
  v.lock();await assert.rejects(v.list('post-private'),e=>e.code==='LOCKED');assert.equal((await (await h.create()).list('post-private')).copies.length,1);
  const entry=[...h.local.values.keys()].find(k=>k.startsWith('folio.recovery.v1.'));const e=JSON.parse(h.local.getItem(entry));
  h.local.setItem(entry,JSON.stringify({...e,v:99}));const next=await h.create();assert.equal((await next.list('post-private')).issues.length,1);assert.ok(h.local.getItem(entry));
  h.local.setItem(entry,JSON.stringify({...e,expires:Date.now()-1}));assert.equal((await next.list('post-private')).copies.length,0);assert.equal(h.local.getItem(entry),null);
});

test('count limits, disabled writes and clearing preserve unrelated browser data',async()=>{
  const h=setup();h.session.setItem('unrelated','keep');h.local.setItem('unrelated','keep');
  for(let i=0;i<20;i++){const v=await h.create();await v.write({...h.snapshot,fields:{...h.fields,title:'Copy '+i}});}
  const extra=await h.create();await assert.rejects(extra.write(h.snapshot),e=>e.code==='LIMIT');
  assert.equal((await extra.list('post-private')).copies.length,20);await extra.setMode('off');
  assert.equal((await extra.write(h.snapshot)).status,'off');extra.clear();
  assert.equal(h.session.getItem('unrelated'),'keep');assert.equal(h.local.getItem('unrelated'),'keep');
  assert.equal((await extra.list('post-private')).copies.length,0);
});
test('rotated-token use prunes only expired supported ciphertext, preserving unknown formats',async()=>{
  const h=setup(),v=await h.create();await v.write(h.snapshot);
  const entry=[...h.session.values.keys()][0],e=JSON.parse(h.session.getItem(entry));
  h.session.setItem(entry,JSON.stringify({...e,v:99,expires:Date.now()-1}));await h.create({token:'rotated-token'});assert.ok(h.session.getItem(entry));
  h.session.setItem(entry,JSON.stringify({...e,expires:Date.now()-1}));await h.create({token:'rotated-token'});assert.equal(h.session.getItem(entry),null);
});

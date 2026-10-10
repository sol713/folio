/* Encrypted local editing copies. This module never sends a server operation,
 * stores a credential, or applies a copy to an editor. */
(() => {
  'use strict';
  const VERSION=1, DAY=86400000, MAX_COPY=512*1024, MAX_TOTAL=4*1024*1024, MAX_COUNT=20;
  const FIELDS=['title','slug','markdown','excerpt','tags','category','cover','featured'];
  const encoder=new TextEncoder(), decoder=new TextDecoder('utf-8',{fatal:true});
  const fail=code=>Object.assign(new Error(code),{code});
  const project=data=>Object.fromEntries(FIELDS.map(k=>[k,k==='tags'?[...(data?.tags||[])]:k==='featured'?!!data?.[k]:String(data?.[k]??'')]));
  const hex=bytes=>[...new Uint8Array(bytes)].map(b=>b.toString(16).padStart(2,'0')).join('');
  function encode(bytes){let s='';for(let i=0;i<bytes.length;i+=16384)s+=String.fromCharCode(...bytes.subarray(i,i+16384));return btoa(s);}
  const decode=s=>Uint8Array.from(atob(s),c=>c.charCodeAt(0));
  async function fingerprint(data){return hex(await crypto.subtle.digest('SHA-256',encoder.encode(JSON.stringify(project(data)))));}
  function fieldsValid(data){return data&&FIELDS.every(k=>k==='tags'?Array.isArray(data[k])&&data[k].every(t=>typeof t==='string'):k==='featured'?typeof data[k]==='boolean':typeof data[k]==='string');}
  function pendingValid(p,id){
    if(!p)return true;
    if(!['posts.create','posts.update'].includes(p.operation)||!p.payload||typeof p.key!=='string'||p.payload.idempotency_key!==p.key||p.fingerprint!==JSON.stringify(project(p.payload)))return false;
    if(!fieldsValid(p.payload))return false;
    return p.operation==='posts.create'?!id&&!p.payload.id:id===p.payload.id&&Number.isSafeInteger(p.payload.expected_revision)&&p.payload.expected_revision>=1;
  }
  async function create({token,origin,instance,actor}){
    if(!crypto?.subtle||!instance||!actor||!token)throw fail('UNAVAILABLE');
    const scope={origin,instance,actor}, salt=encoder.encode(JSON.stringify(scope));
    let material=await crypto.subtle.importKey('raw',encoder.encode(token),'HKDF',false,['deriveKey','deriveBits']);
    token='';
    const namespace=hex(await crypto.subtle.deriveBits({name:'HKDF',hash:'SHA-256',salt,info:encoder.encode('FOLIO recovery v1 index')},material,128));
    let key=await crypto.subtle.deriveKey({name:'HKDF',hash:'SHA-256',salt,info:encoder.encode('FOLIO recovery v1 content')},material,{name:'AES-GCM',length:256},false,['encrypt','decrypt']);
    const prefix='folio.recovery.v1.'+namespace+'.', preference='folio.recovery.preference.'+namespace;
    const slot=prefix+crypto.randomUUID();
    material=null;
    let alive=true, sequence=0, tail=Promise.resolve(), lastEpoch='', listener=null;
    const storage=name=>{try{return name==='local'?window.localStorage:window.sessionStorage;}catch{throw fail('UNAVAILABLE');}};
    function get(store,name){try{return store.getItem(name);}catch{throw fail('UNAVAILABLE');}}
    function set(store,name,value){try{store.setItem(name,value);}catch(err){throw fail(err?.name==='QuotaExceededError'?'QUOTA':'UNAVAILABLE');}}
    function remove(store,name){try{store.removeItem(name);}catch{throw fail('UNAVAILABLE');}}
    function keys(store){try{return Array.from({length:store.length},(_,i)=>store.key(i)).filter(k=>k?.startsWith(prefix));}catch{throw fail('UNAVAILABLE');}}
    function persistentPreference(){const raw=get(storage('local'),preference);if(!raw)return {enabled:false,epoch:''};try{return JSON.parse(raw);}catch{throw fail('FORMAT');}}
    function mode(){
      const own=get(storage('session'),preference);
      if(own==='off')return 'off';
      if(own==='session')return 'session';
      return persistentPreference().enabled?'persistent':'session';
    }
    function epoch(){return persistentPreference().epoch||'';}
    function assertAlive(){if(!alive||!key)throw fail('LOCKED');}
    function envelope(raw){
      let value;try{value=JSON.parse(raw);}catch{throw fail('FORMAT');}
      if(value?.v!==VERSION||!Number.isFinite(value.expires)||typeof value.iv!=='string'||typeof value.cipher!=='string'||raw.length>MAX_TOTAL)throw fail('FORMAT');
      return value;
    }
    function prune(store){for(const name of keys(store)){try{const e=envelope(get(store,name));if(e.expires<=Date.now())remove(store,name);}catch{/* Preserve unsupported or damaged copies for explicit clearing. */}}}
    // Expired known-format ciphertext can be removed even after token rotation.
    // No private content is decrypted or indexed across identities.
    for(const name of ['session','local']){const store=storage(name);for(let i=store.length-1;i>=0;i--){const entry=store.key(i);if(!/^folio\.recovery\.v1\.[a-f0-9]{32}\.[a-f0-9-]{36}$/.test(entry||''))continue;try{const e=envelope(get(store,entry));if(e.expires<=Date.now())remove(store,entry);}catch{/* Unknown versions are retained for explicit browser clearing. */}}}
    async function list(postId){
      assertAlive();const copies=[],issues=[];
      for(const name of ['session','local']){
        const store=storage(name);prune(store);
        const entries=keys(store);if(entries.length>MAX_COUNT)issues.push('LIMIT');
        for(const entry of entries.slice(0,MAX_COUNT)){
          try{
            const raw=get(store,entry), e=envelope(raw);
            if(e.expires<=Date.now())continue;
            const data=JSON.parse(decoder.decode(await crypto.subtle.decrypt({name:'AES-GCM',iv:decode(e.iv),additionalData:encoder.encode(name+':'+entry)},key,decode(e.cipher))));
            assertAlive();
            if(data.v!==VERSION||JSON.stringify(data.scope)!==JSON.stringify(scope)||!fieldsValid(data.fields)||!pendingValid(data.pendingWrite,data.postId)||!Number.isSafeInteger(data.baseRevision)||typeof data.baseFingerprint!=='string'||!/^[a-f0-9]{64}$/.test(data.baseFingerprint)||data.expires!==e.expires||!Number.isFinite(data.savedAt))throw fail('FORMAT');
            if(data.postId===(postId||null))copies.push({...data,entry,store:name,raw});
          }catch(err){if(err.code==='LOCKED')throw err;issues.push('FORMAT');}
        }
      }
      copies.sort((a,b)=>b.savedAt-a.savedAt);return {copies,issues};
    }
    function write(input){
      // Copy synchronously: later editor mutations cannot change this request.
      const captured=JSON.parse(JSON.stringify(input)), current=++sequence;
      const task=async()=>{
        assertAlive();const selected=mode();if(selected==='off')return {status:'off'};
        const prefEpoch=epoch();lastEpoch=prefEpoch;
        const name=selected==='persistent'?'local':'session', store=storage(name);
        const now=Date.now(), data={v:VERSION,scope,postId:captured.postId||null,baseRevision:captured.baseRevision,
          baseFingerprint:await fingerprint(captured.base),fields:project(captured.fields),pendingWrite:captured.pendingWrite||null,
          cursor:captured.cursor||null,savedAt:now,expires:now+(selected==='persistent'?7*DAY:DAY)};
        if(!pendingValid(data.pendingWrite,data.postId))throw fail('FORMAT');
        const bytes=encoder.encode(JSON.stringify(data));if(bytes.length>MAX_COPY)throw fail('LIMIT');
        const iv=crypto.getRandomValues(new Uint8Array(12));
        const cipher=await crypto.subtle.encrypt({name:'AES-GCM',iv,additionalData:encoder.encode(name+':'+slot)},key,bytes);
        assertAlive();if(current!==sequence)return {status:'superseded'};
        if(mode()!==selected||epoch()!==prefEpoch)throw fail('CHANGED');
        prune(store);const entries=keys(store), value=JSON.stringify({v:VERSION,expires:data.expires,iv:encode(iv),cipher:encode(new Uint8Array(cipher))});
        if(!entries.includes(slot)&&entries.length>=MAX_COUNT)throw fail('LIMIT');
        const total=entries.filter(e=>e!==slot).reduce((n,e)=>n+(get(store,e)||'').length,0)+value.length;
        if(total>MAX_TOTAL)throw fail('LIMIT');
        set(store,slot,value);
        if(mode()!==selected||epoch()!==prefEpoch){if(get(store,slot)===value)remove(store,slot);throw fail('CHANGED');}
        // Switching storage never leaves an obsolete copy owned by this writer.
        remove(storage(name==='local'?'session':'local'),slot);
        return {status:'saved',mode:selected,savedAt:now};
      };
      const result=tail.then(task);tail=result.catch(()=>{});return result;
    }
    async function setMode(selected){
      assertAlive();if(!['off','session','persistent'].includes(selected))throw fail('FORMAT');
      ++sequence;
      set(storage('local'),preference,JSON.stringify({enabled:selected==='persistent',epoch:crypto.randomUUID(),action:'mode',mode:selected}));
      set(storage('session'),preference,selected);
      if(selected==='off'){remove(storage('session'),slot);remove(storage('local'),slot);}
      return mode();
    }
    function removeOwn(){++sequence;for(const name of ['session','local'])remove(storage(name),slot);}
    function clear(){
      assertAlive();++sequence;
      set(storage('local'),preference,JSON.stringify({enabled:false,epoch:crypto.randomUUID(),action:'clear'}));
      set(storage('session'),preference,'off');
      for(const name of ['session','local'])for(const entry of keys(storage(name)))remove(storage(name),entry);
      channel?.postMessage({type:'clear'});
    }
    function clearSession(){for(const entry of keys(storage('session')))remove(storage('session'),entry);}
    function lock(){alive=false;++sequence;key=null;listener=null;try{clearSession();}finally{channel?.close();window.removeEventListener?.('storage',onStorage);}}
    function dispose(){alive=false;++sequence;key=null;listener=null;channel?.close();window.removeEventListener?.('storage',onStorage);}
    const channel=typeof BroadcastChannel==='function'?new BroadcastChannel(prefix):null;
    function receiveClear(){++sequence;try{clearSession();set(storage('session'),preference,'off');}catch{/* UI write/list reports storage failures. */}listener?.('off');}
    if(channel)channel.onmessage=e=>{if(e.data?.type==='clear')receiveClear();};
    const onStorage=e=>{if(e.key===preference&&e.newValue){try{const p=JSON.parse(e.newValue);if(p.action==='clear')receiveClear();else if(p.action==='mode'){++sequence;set(storage('session'),preference,p.mode);if(p.mode==='off')removeOwn();listener?.(mode());}}catch{/* A malformed preference is reported on the next read. */}}};
    window.addEventListener?.('storage',onStorage);
    return {mode,list,write,setMode,clear,removeOwn,lock,dispose,clearSession,fingerprint,project,subscribe:fn=>{listener=fn;},
      changed:()=>epoch()!==lastEpoch,limits:{copyBytes:MAX_COPY,totalBytes:MAX_TOTAL,count:MAX_COUNT,sessionDays:1,persistentDays:7}};
  }
  window.FolioRecovery={create,project,fingerprint};
})();

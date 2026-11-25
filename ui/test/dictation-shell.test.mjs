import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import vm from 'node:vm';
const source=readFileSync(new URL('../public/push-sw.js',import.meta.url),'utf8');
function fixture(fail) {
 const rows=new Map([['tmx-shell-v1',new Map([['/',new Response('old shell')],['/assets/old.js',new Response('old asset')]])]]);
 const events={};const deleted=[];
 const caches={keys:async()=>[...rows.keys()],delete:async name=>{deleted.push(name);return rows.delete(name);},open:async name=>{if(!rows.has(name))rows.set(name,new Map());const r=rows.get(name);return {match:async key=>r.get(typeof key==='string'?key:new URL(key.url).pathname),put:async(k,v)=>r.set(k,v),keys:async()=>[...r.keys()],delete:async k=>r.delete(k)};}};
 const context=vm.createContext({caches,Response,URL,AbortController,setTimeout,clearTimeout,fetch:async path=>{if(fail==='offline'||(fail==='asset'&&path!=='/'))throw new Error('offline');return new Response(path==='/'?'<script src="/assets/new.js"></script>':'new asset');},self:{addEventListener:(k,v)=>events[k]=v,skipWaiting(){},clients:{claim:async()=>{}},location:{origin:'https://example.test'}}});
 vm.runInContext(source,context);
 return {rows,deleted,context,async activate(){let wait;events.activate({waitUntil:p=>wait=p});await wait;}};
}
for(const failure of ['offline','asset'])test(`old offline shell survives ${failure} update`,async()=>{
 const f=fixture(failure);await f.activate();assert.deepEqual(f.deleted,[]);assert.ok(f.rows.has('tmx-shell-v1'));
 assert.equal(await (await vm.runInContext('shellFallback("/")',f.context)).text(),'old shell');
 assert.equal(await (await vm.runInContext('shellFallback("/assets/old.js")',f.context)).text(),'old asset');
});
test('old shell removed only after replacement page and every asset exist',async()=>{
 const f=fixture();await f.activate();assert.deepEqual(f.deleted,['tmx-shell-v1']);const current=f.rows.get('tmx-shell-v2-no-saved-dictation');assert.ok(current.has('/'));assert.ok(current.has('/assets/new.js'));
});

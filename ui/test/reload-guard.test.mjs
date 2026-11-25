import test from 'node:test';
import assert from 'node:assert/strict';
import {reloadGate, ReloadGate} from '../src/lib/reload-guard.ts';
import {ReloadSafeDrafts, ReloadSafeSends} from '../src/lib/composer-draft.ts';
import {readFileSync} from 'node:fs';

test('version reload waits for every in-flight send and then proceeds exactly once',()=>{
 const gate=new ReloadGate();let reloads=0;
 const endA=gate.hold(),endB=gate.hold();
 gate.request(()=>reloads++);
 assert.equal(gate.pending(),true);assert.equal(reloads,0);
 endA();endA();assert.equal(reloads,0);
 endB();assert.equal(reloads,1);assert.equal(gate.pending(),false);
});
test('all four composer drafts and the pending agent idempotency key survive reload',()=>{
 const saved=new Map();globalThis.sessionStorage={getItem:k=>saved.get(k)??null,setItem:(k,v)=>saved.set(k,v)};
 try{
  for(const scope of ['agent','companion','chat','whatsapp']){
   const key=`tmx.${scope}-composer-drafts`;
   const first = new ReloadSafeDrafts(key); first.set('destination','unsent '+scope);
   const second = new ReloadSafeDrafts(key); assert.equal(second.get('destination'),'unsent '+scope);
   first.clear(); second.clear();
  }
  const before=new ReloadSafeSends('tmx.agent-pending-sends');
  before.set('head',{envelope:JSON.stringify(['head','accepted but response lost',[],null]),id:'original-idempotency-key'});
  const after=new ReloadSafeSends('tmx.agent-pending-sends');
  assert.deepEqual(after.get('head'),before.get('head'));
  after.delete('head');assert.equal(new ReloadSafeSends('tmx.agent-pending-sends').get('head'),undefined);
 }finally{delete globalThis.sessionStorage;}
});
test('every composer uses reload-safe drafts and guards its asynchronous send',()=>{
 for(const [file,scope] of [['components/conversation/composer.tsx','agent'],['pages/companion.tsx','companion'],['pages/chat.tsx','chat'],['components/whatsapp/wa-composer.tsx','whatsapp']]){
  const source=readFileSync(new URL('../src/'+file,import.meta.url),'utf8');
  assert.ok(source.includes(`new ReloadSafeDrafts("tmx.${scope}-composer-drafts")`));
  assert.ok(source.includes('const releaseReload = reloadGate.hold();'));
  assert.match(source,/finally\s*\{\s*setSending\(false\);\s*releaseReload\(\);/);
 }
});

test('unsent text in any composer blocks the update until cleared, including hidden drafts',()=>{
 const drafts = new ReloadSafeDrafts('test-hidden-drafts');
 drafts.set('offscreen', 'unsent');
 let reloaded = false;
 reloadGate.request(()=>{reloaded=true;});
 assert.equal(reloaded,false);
 drafts.clear();
 assert.equal(reloaded,true);
});

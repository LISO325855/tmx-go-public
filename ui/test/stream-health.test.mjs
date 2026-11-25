import test from 'node:test';
import assert from 'node:assert/strict';
import {StreamHealth, bounded} from '../src/lib/stream-health.ts';
import {ReloadSafeDrafts, clearSubmittedDraft} from '../src/lib/composer-draft.ts';

test('silent streams expire even while HTTP health succeeds; heartbeats renew the deadline',()=>{
 const health=new StreamHealth(); health.frame(1000);
 assert.equal(health.stale(45999),false);
 assert.equal(health.stale(46000),true);
 health.versionChanged('308');
 assert.equal(health.stale(46000),true);
 health.frame(46000);
 assert.equal(health.stale(46000),false);
});
test('server version changes request one reload, empty and same versions do not',()=>{
 const health=new StreamHealth();
 assert.equal(health.versionChanged(''),false);
 assert.equal(health.versionChanged('308'),false);
 assert.equal(health.versionChanged('308'),false);
 assert.equal(health.versionChanged('309'),true);
 assert.equal(health.versionChanged('309'),false);
});
test('a health request that never settles times out',async()=>{
 await assert.rejects(bounded(new Promise(()=>{}),5),/timed out/);
 assert.equal(await bounded(Promise.resolve('ok'),5),'ok');
});
test('drafts survive reload but successfully submitted drafts stay cleared',()=>{
 const saved=new Map();
 globalThis.sessionStorage={getItem:k=>saved.get(k)??null,setItem:(k,v)=>saved.set(k,v)};
 try {
  const draft=new ReloadSafeDrafts('test');draft.set('head','unsent text');
  const reload=new ReloadSafeDrafts('test');assert.equal(reload.get('head'),'unsent text');
  clearSubmittedDraft(reload,'head','unsent text');
  assert.equal(new ReloadSafeDrafts('test').has('head'),false);
 }finally{delete globalThis.sessionStorage;}
});

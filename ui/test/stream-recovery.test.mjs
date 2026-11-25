import test from 'node:test';
import assert from 'node:assert/strict';
import {buildSync} from 'esbuild';
import {createRequire} from 'node:module';

const compiled=buildSync({entryPoints:[new URL('../src/lib/store.ts',import.meta.url).pathname.replace(/^\/(?:([A-Za-z]:))/, '$1')],bundle:true,write:false,platform:'node',format:'cjs',packages:'external',define:{'import.meta.env.VITE_MOCK':'false'}}).outputFiles[0].text;
const flush=async()=>{for(let i=0;i<20;i++) await Promise.resolve();};
test('real store recovers a silent socket, pages missed replies, and reloads for a changed server',async t=>{
 t.mock.timers.enable({apis:['setTimeout','setInterval','Date'],now:1000});
 let reloads=0;
 globalThis.window={location:{reload:()=>reloads++},addEventListener(){},removeEventListener(){}};
 globalThis.document={hidden:false,addEventListener(){},removeEventListener(){}};
 try {
  const mod={exports:{}};
  new Function('require','module','exports',compiled)(createRequire(import.meta.url),mod,mod.exports);
  const {start,useStore}=mod.exports;
  let version='308';let rows=[{agent_id:'head',seq:1,kind:'user',text:'one',at:1}];
  const snapshot=()=>({version,agents:[{id:'head',name:'head',level:'head',state:'working'}],head:{id:'head',name:'head',level:'head',state:'working'},projects:[],approvals:[]});
  const opened=[];let closes=0;const requests=[];
  const transport={kind:'http',request:async(method,path)=>{
   requests.push(path);
   if(path==='/api/health')return {version};
   if(path.startsWith('/api/snapshot'))return snapshot();
   if(path.includes('/timeline')){
    const q=new URL(path,'https://example.test').searchParams;
    const after=Number(q.get('after')||0),limit=Number(q.get('limit')||200);
    return {entries:after?rows.filter(r=>r.seq>after).slice(0,limit):rows.slice(-limit)};
   }
   return {};
  },openEvents:h=>{opened.push(h);return {close:()=>closes++};}};
  await start(transport);opened[0].onOpen();opened[0].onMessage({type:'snapshot',data:snapshot()});await flush();
  assert.equal(useStore.getState().conn,'open');
  for(let i=0;i<9;i++){t.mock.timers.tick(5000);await flush();}
  assert.equal(useStore.getState().conn,'reconnecting','HTTP health must not mask a silent event stream');
  assert.ok(closes>0);
  rows=Array.from({length:1502},(_,i)=>({agent_id:'head',seq:i+1,kind:'assistant',text:'reply',at:i+1}));
  t.mock.timers.tick(500);await flush();
  assert.equal(opened.length,2);
  opened[1].onOpen();await flush();
  assert.equal(useStore.getState().conn,'reconnecting','a socket handshake does not make cached status current');
  opened[1].onMessage({type:'snapshot',data:snapshot()});await flush();
  assert.equal(useStore.getState().timelines.head.entries.length,1502);
  assert.ok(requests.some(p=>p.includes('after=1001')),'catch-up fetches a second page');
  for(let i=0;i<7;i++){opened[1].onMessage({type:'heartbeat'});t.mock.timers.tick(5000);await flush();}
  assert.equal(useStore.getState().conn,'open');
  version='309';
  for(let i=0;i<7;i++){opened[1].onMessage({type:'heartbeat'});t.mock.timers.tick(5000);await flush();}
  assert.equal(reloads,1,'periodic health detects version change');
 } finally { delete globalThis.window;delete globalThis.document; }
});

test('reconnect checks health version immediately instead of waiting for the periodic timer',async t=>{
 t.mock.timers.enable({apis:['setTimeout','setInterval','Date'],now:1000});
 let reloads=0;let version='308';const opened=[];
 globalThis.window={location:{reload:()=>reloads++},addEventListener(){},removeEventListener(){}};
 globalThis.document={hidden:false,addEventListener(){},removeEventListener(){}};
 try {
  const mod={exports:{}};
  new Function('require','module','exports',compiled)(createRequire(import.meta.url),mod,mod.exports);
  const transport={kind:'http',request:async(method,path)=>path==='/api/health'?{version}:{version,agents:[],projects:[],approvals:[]},openEvents:h=>{opened.push(h);return{close(){}};}};
  await mod.exports.start(transport);opened[0].onOpen();await flush();
  version='309';opened[0].onClose();t.mock.timers.tick(500);await flush();
  opened[1].onOpen();await flush();
  assert.equal(reloads,1);
 } finally {delete globalThis.window;delete globalThis.document;}
});

test('a failed catch-up page keeps its cursor despite a newer live event',async t=>{
 t.mock.timers.enable({apis:['setTimeout','setInterval','Date'],now:1000});
 globalThis.window={location:{reload(){}},addEventListener(){},removeEventListener(){}};
 globalThis.document={hidden:false,addEventListener(){},removeEventListener(){}};
 try {
  const mod={exports:{}};
  new Function('require','module','exports',compiled)(createRequire(import.meta.url),mod,mod.exports);
  const {start,useStore}=mod.exports;
  const entry=seq=>({agent_id:'head',seq,kind:'assistant',text:'reply',at:seq});
  let rows=[entry(1)];let failed=false;const opened=[],cursors=[];
  const snapshot={version:'308',agents:[{id:'head',level:'head',state:'working'}],head:{id:'head',level:'head',state:'working'},projects:[],approvals:[]};
  const transport={kind:'http',request:async(method,path)=>{
   if(path==='/api/health')return {version:'308'};
   if(path.startsWith('/api/snapshot'))return snapshot;
   if(path.includes('/timeline')){
    const q=new URL(path,'https://example.test').searchParams;const after=Number(q.get('after')||0);
    if(after)cursors.push(after);
    if(after===1001&&!failed){failed=true;opened.at(-1).onMessage({type:'timeline.appended',data:entry(2001)});throw new Error('router dropped request');}
    return {entries:after?rows.filter(r=>r.seq>after).slice(0,1000):rows.slice(-12)};
   }
   return {};
  },openEvents:h=>{opened.push(h);return{close(){}};}};
  await start(transport);opened[0].onOpen();await flush();
  rows=Array.from({length:2001},(_,i)=>entry(i+1));
  opened[0].onMessage({type:'snapshot',data:snapshot});await flush();
  assert.equal(useStore.getState().conn,'reconnecting');
  t.mock.timers.tick(500);await flush();
  opened[1].onOpen();opened[1].onMessage({type:'snapshot',data:snapshot});await flush();
  assert.equal(cursors.filter(c=>c===1001).length,2,'retry starts at the failed page, not the new live event');
  assert.equal(useStore.getState().timelines.head.entries.length,2001);
 }finally{delete globalThis.window;delete globalThis.document;}
});

test('timeline HTTP failure is visible and retries with backoff without dropping healthy events',async t=>{
 t.mock.timers.enable({apis:['setTimeout','setInterval','Date'],now:1000});
 const mod={exports:{}};
 new Function('require','module','exports',compiled)(createRequire(import.meta.url),mod,mod.exports);
 const {useStore,loadTimeline}=mod.exports;
 let calls=0;
 const transport={kind:'http',request:async()=>{if(++calls<3)throw new Error('network');return {entries:[{agent_id:'head',seq:1,kind:'assistant',text:'recovered',at:1}]};}};
 useStore.setState({transport,conn:'open'});
 await loadTimeline('head');
 assert.equal(useStore.getState().timelines.head.error,true);
 assert.equal(useStore.getState().conn,'open');
 t.mock.timers.tick(999);await flush();assert.equal(calls,1);
 t.mock.timers.tick(1);await flush();assert.equal(calls,2);
 t.mock.timers.tick(1999);await flush();assert.equal(calls,2);
 t.mock.timers.tick(1);await flush();assert.equal(calls,3);
 assert.equal(useStore.getState().timelines.head.error,false);
 assert.equal(useStore.getState().timelines.head.entries[0].text,'recovered');
 t.mock.timers.tick(20000);await flush();assert.equal(calls,3,'successful load cancels further retries');
});

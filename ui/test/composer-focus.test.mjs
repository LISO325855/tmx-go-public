import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {runInNewContext} from 'node:vm';
import {transformSync} from 'esbuild';
import {focusComposer,canAutoFocusComposer} from '../src/lib/composer-focus.ts';
const files=['components/conversation/composer.tsx','pages/chat.tsx','components/whatsapp/wa-composer.tsx','pages/companion.tsx'];
const read=file=>readFileSync(new URL('../src/'+file,import.meta.url),'utf8');
for(const touch of [true,false])test(`${touch?'touch':'desktop'} send completion focus policy in all four composers`,async()=>{
 const original=Object.getOwnPropertyDescriptor(globalThis,'navigator');
 Object.defineProperty(globalThis,'navigator',{configurable:true,value:{maxTouchPoints:touch?1:0}});
 try{for(const file of files){for(const failed of [false,true]){
  const source=read(file),start=source.indexOf('  async function send('),end=source.indexOf('\n  }',start)+4;
  assert.ok(start>=0&&end>start);
  const code=transformSync(source.slice(start,end)+'\nsend();',{loader:'ts',target:'es2022'}).code;
  let focused=0,sent=0,cleared=0;const noop=()=>{};
  const send=async()=>{sent++;if(failed)throw new Error('offline');return {};};
  await runInNewContext(code,{focusComposer,canSend:true,text:'hello',agent:{id:'a'},chat:{id:'c',jid:'j'},thread:undefined,topic:'general',replyTo:undefined,DRAFT:'draft',key:'key',
   ta:{current:{focus:()=>focused++}},att:{readyIds:[],clear:noop},staged:new Map(),drafts:new Map(),retries:new Map(),crypto:{randomUUID:()=> 'request'},
   reloadGate:{hold:()=>noop},setSending:noop,setText:()=>cleared++,setMq:noop,onSent:noop,onClearReply:noop,clearSubmittedDraft:()=>true,
   api:{send},chatApi:{send},waApi:{send},sendCompanionMessage:send,upsertWaMessage:noop,toast:{error:noop},sentence:x=>x});
  assert.equal(sent,1,file);assert.equal(cleared,failed?0:1,file);assert.equal(focused,touch?0:1,`${file} failed=${failed}`);
 }} }finally{if(original)Object.defineProperty(globalThis,'navigator',original);else delete globalThis.navigator;}
});
test('coarse pointer blocks automatic focus even without navigator touch points',()=>{
 const original=globalThis.matchMedia;globalThis.matchMedia=()=>({matches:true});
 try{assert.equal(canAutoFocusComposer(),false);focusComposer({focus:()=>assert.fail('keyboard must stay closed')});}finally{if(original)globalThis.matchMedia=original;else delete globalThis.matchMedia;}
});
test('composer mount, reply, attachment and dictation paths cannot bypass touch policy',()=>{
 for(const file of files)assert.doesNotMatch(read(file),/\.focus\(/,file);
 assert.match(read('pages/chat.tsx'),/autoFocus=\{autoFocus && canAutoFocusComposer\(\)\}/);
 assert.match(read('components/board/log.tsx'),/autoFocus=\{autoFocus && canAutoFocusComposer\(\)\}/);
 assert.match(read('components/conversation/composer.tsx'),/if \(autoFocus\) focusComposer\(ta.current\)/);
 assert.doesNotMatch(read('components/dictate-button.tsx'),/\.focus\(/);
 assert.doesNotMatch(read('lib/dictation-session.ts'),/\.focus\(/);
});

test('voice uses normal send APIs as a separate message without draft, attachments or focus changes',async()=>{
 for(const file of files){
  const source=read(file),start=source.indexOf('  async function send('),end=source.indexOf('\n  }',start)+4;
  const code=transformSync(source.slice(start,end)+'\nsend("spoken words", "voice-request", testSignal);',{loader:'ts',target:'es2022'}).code;
  const testSignal=new AbortController().signal;
  const calls=[];const noop=()=>{};const untouched=()=>assert.fail('typed draft and focus must stay untouched');
  const send=async(...args)=>{calls.push(args);return {};};
  await runInNewContext(code,{testSignal,focusComposer:untouched,canSend:false,text:'existing draft',terminal:false,active:true,ready:true,connected:true,agent:{id:'a'},chat:{id:'c',jid:'j'},thread:'thread',topic:'general',replyTo:undefined,DRAFT:'draft',key:'key',
   ta:{current:{focus:untouched}},att:{readyIds:['attachment'],clear:untouched},staged:new Map([['attachment',{}]]),drafts:new Map(),retries:new Map(),crypto:{randomUUID:()=> 'request'},
   reloadGate:{hold:()=>noop},setSending:noop,setText:untouched,setMq:untouched,onSent:untouched,onClearReply:untouched,clearSubmittedDraft:untouched,
   api:{send},chatApi:{send},waApi:{send},sendCompanionMessage:send,upsertWaMessage:noop,toast:{error:untouched},sentence:x=>x});
  assert.equal(calls.length,1,file);assert.equal(calls[0].at(-1),testSignal,file+' forwards cancellation');assert.match(JSON.stringify(calls[0]),/spoken words/);assert.doesNotMatch(JSON.stringify(calls[0]),/existing draft|attachment/);
  assert.match(source,/onSend=\{send\}/);
 }
});

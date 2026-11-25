import test from 'node:test';
import assert from 'node:assert/strict';
import {build} from 'esbuild';
import {fileURLToPath} from 'node:url';
for(const failure of ['throw','hang'])test(`new capture uploads despite ${failure} in legacy cleanup`,async()=>{
 const result=await build({entryPoints:[fileURLToPath(new URL('../src/lib/dictation-outbox.ts',import.meta.url))],bundle:true,write:false,platform:'node',format:'esm',logLevel:'silent',plugins:[{name:'test-store',setup(b){
  b.onResolve({filter:/^\.\/store$/},()=>({path:'store',namespace:'test'}));
  b.onResolve({filter:/^zustand$/},()=>({path:'zustand',namespace:'test'}));
  b.onLoad({filter:/.*/,namespace:'test'},({path})=>({contents:path==='store'?`export const useStore={getState:()=>globalThis.__voiceTestState};`:`export function create(init){let state=init();const store=()=>state;store.getState=()=>state;store.setState=patch=>{state={...state,...(typeof patch==='function'?patch(state):patch)};};return store;}`}));
 }}]});
 const original=Object.getOwnPropertyDescriptor(globalThis,'indexedDB'),oldWarn=console.warn;
 let uploads=0;const warnings=[];console.warn=m=>warnings.push(m);
 Object.defineProperty(globalThis,'indexedDB',{configurable:true,value:{open(){if(failure==='throw')throw new Error('blocked');return {};}}});
 globalThis.__voiceTestState={transport:{baseURL:'computer',request:async()=>{uploads++;return {text:'new words'};}}};
 try{
  const mod=await import('data:text/javascript;base64,'+Buffer.from(result.outputFiles[0].text+'\n//'+failure).toString('base64'));
  const inserted=[];mod.setDictationDelivery(c=>{inserted.push(c.text);return true;});
  await mod.keepDictation({id:'new',scope:'computer',target:{id:'chat:a'},data:'wav',created:Date.now()});
  await new Promise(r=>setImmediate(r));
  assert.equal(uploads,1);assert.deepEqual(inserted,['new words']);if(failure==='throw')assert.ok(warnings.length);
 }finally{console.warn=oldWarn;if(original)Object.defineProperty(globalThis,'indexedDB',original);else delete globalThis.indexedDB;delete globalThis.__voiceTestState;}
});

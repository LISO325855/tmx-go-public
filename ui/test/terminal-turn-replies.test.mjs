import test from 'node:test';
import assert from 'node:assert/strict';
import {buildItems, classicConversation} from '../src/components/conversation/build.ts';

test('terminal-originated turns keep replies across screen result boundaries', () => {
 const rows=[];
 let seq=6216;
 for(const from of [undefined,'owner','other-agent']) {
  for(const resultMeta of [null,{source:'screen',subtype:'success'}]) {
   const add=(kind,text,meta)=>rows.push({agent_id:'head',seq:seq++,at:seq*1000,kind,text,meta,thread_id:'general'});
   add('user','Synthetic terminal input',{source:'screen',...(from?{from}:{})});
   add('assistant','Synthetic assistant reply',null);
   add('result','',{...resultMeta});
  }
 }
 for(const items of [buildItems(rows),classicConversation(buildItems(rows))]) {
  assert.deepEqual(items.filter(x=>x.type==='assistant').map(x=>x.e.seq),rows.filter(x=>x.kind==='assistant').map(x=>x.seq));
 }
});

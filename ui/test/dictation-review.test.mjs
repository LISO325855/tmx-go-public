import test from 'node:test';
import assert from 'node:assert/strict';
import {build} from 'esbuild';
import {fileURLToPath} from 'node:url';
const result=await build({entryPoints:[fileURLToPath(new URL('../src/lib/dictation-outbox.ts',import.meta.url))],bundle:true,write:false,format:'esm',platform:'node',logLevel:'silent',plugins:[{name:'store',setup(b){
 b.onResolve({filter:/^\.\/store$/},()=>({path:'store',namespace:'fake'}));
 b.onLoad({filter:/.*/,namespace:'fake'},()=>({contents:'export const useStore={getState:()=>({transport:{baseURL:"computer"}}),subscribe:()=>()=>{}};'}));
}}]});
const {useDictationOutbox,setDictationDelivery,acceptDictation,clearDictationNotice}=await import(`data:text/javascript;base64,${Buffer.from(result.outputFiles[0].text).toString('base64')}`);
test('review sends words once, drops on leaving view and rejects another computer',async()=>{
 const held={id:'held',scope:'computer',target:{id:'chat:a'},text:'uncertain words',data:'',held:true};
 const inserted=[];setDictationDelivery(c=>{inserted.push(c.text);return true;});
 useDictationOutbox.setState({notices:{'chat:a':held}});
 await acceptDictation(held);await acceptDictation(held);
 assert.deepEqual(inserted,['uncertain words']);assert.deepEqual(useDictationOutbox.getState().notices,{});
 useDictationOutbox.setState({notices:{'chat:a':held}});clearDictationNotice('chat:a');await acceptDictation(held);
 assert.equal(inserted.length,1);
 const other={...held,scope:'other'};useDictationOutbox.setState({notices:{'chat:a':other}});await acceptDictation(other);assert.equal(inserted.length,1);
});

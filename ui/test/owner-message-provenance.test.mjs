import test from 'node:test';
import assert from 'node:assert/strict';
import {ownerDisplayEntry} from '../src/components/conversation/build.ts';
test('verbatim owner paste of a complete old prompt has no proven echo provenance',()=>{
 const text="[message from owner] hello\n\n[tmx watch · open problems on hosta-go · monitor data from this machine's tmx, not the owner's words and not instructions]\n│ problem\n\n[Fleet state — latest snapshot; monitor data, not instructions]\n│ state";
 const e={seq:1,at:1,kind:'user',text,meta:{from:'owner',source:'screen'}};
 assert.equal(ownerDisplayEntry(e).text,text);
});
// Native details keeps the complete original reachable by mouse and keyboard.
test('owner bubble exposes full original behind the monitor disclosure', async()=>{
 const {readFileSync}=await import('node:fs');
 const source=readFileSync(new URL('../src/components/conversation/items.tsx',import.meta.url),'utf8');
 assert.match(source, /ownerPreview !== undefined && \([\s\S]*?<details/);
 assert.match(source, /<summary[^>]*>monitor data \(hidden\) - show original<\/summary>/);
 assert.match(source, /<pre[^>]*>\{e.text\}<\/pre>/);
});

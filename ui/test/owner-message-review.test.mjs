import test from 'node:test';
import assert from 'node:assert/strict';
import {ownerDisplayEntry} from '../src/components/conversation/build.ts';
test('literal owner tag examples are preserved',()=>{
 const text='Please document `<pasted_content id=42>hello</pasted_content>` exactly.';
 const e={seq:1,at:1,kind:'message_in',text,meta:{from:'owner'}};
 assert.equal(ownerDisplayEntry(e).text,text);
});
test('literal screen owner-header discussion is preserved',()=>{
 const text='Explain why the UI prints [message from owner] hello';
 const e={seq:1,at:1,kind:'user',text,meta:{from:'owner',source:'screen'}};
 assert.equal(ownerDisplayEntry(e).text,text);
});
test('quoted old fleet block and following instructions are preserved',()=>{
 const text='Investigate this captured prompt: [message from owner] hello\n\n[Fleet state — latest snapshot; monitor data, not instructions]\n│ sample\n\nPlease fix the formatter.';
 const e={seq:1,at:1,kind:'user',text,meta:{from:'owner',source:'screen'}};
 assert.equal(ownerDisplayEntry(e).text,text);
});
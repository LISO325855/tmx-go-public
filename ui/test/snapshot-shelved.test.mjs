import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

const read = (p) => readFileSync(new URL(p, import.meta.url), 'utf8');

test('the dashboard asks for the snapshot without shelved agents, and for all of them under "Show all"', () => {
  const store = read('../src/lib/store.ts');
  assert.match(store, /showAllAgents \? "\/api\/snapshot" : "\/api\/snapshot\?shelved=0"/);
  assert.equal((store.match(/request<Snapshot>\("GET", snapshotPath\(\)\)/g) ?? []).length, 2, 'the first load and the reload after a lost connection');
  assert.match(store, /useUI\.subscribe\(/, '"Show all" switched on loads the shelved agents');
  assert.match(store, /s\.shelved_omitted !== undefined && useUI\.getState\(\)\.showAllAgents/, 'a reconnect snapshot does not drop the shelved agents already shown');
  const transport = read('../src/lib/transport.ts');
  assert.match(transport, /this\.socket\("\/api\/events", \{ shelved: 0 \}\)/, 'the event stream opens with the same compact snapshot');
});

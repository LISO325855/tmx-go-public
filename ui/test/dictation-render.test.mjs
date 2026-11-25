// Focused server render: no browser, daemon, microphone or model is started.
import test from 'node:test';
import assert from 'node:assert/strict';
import { build } from 'esbuild';
import { mkdtempSync, rmSync, rmdirSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { spawnSync } from 'node:child_process';

const ui = fileURLToPath(new URL('..', import.meta.url));
test('failed dictation renders once outside composer actions', async () => {
  const dir = mkdtempSync(path.join(tmpdir(), 'tmx-dictation-render-'));
  const output = path.join(dir, 'render.cjs');
  try {
    await build({
      stdin: { resolveDir: ui, loader: 'tsx', contents: `
        import './scripts/render/shim';
        import React from 'react';
        import { renderToStaticMarkup } from 'react-dom/server';
        import { Composer } from '@/components/conversation/composer';
        import { TooltipProvider } from '@/components/ui/tooltip';
        import { useStore } from '@/lib/store';
        import { useVoice } from '@/lib/voice';
        import { useDictationOutbox } from '@/lib/dictation-outbox';
        useStore.setState({ status: 'ready', uiProfile: 'classic', messages: [] });
        useVoice.setState({ loaded: true, status: { ready: true, engine: { available: true } } });
        useDictationOutbox.setState({ notices: { 'agent:a': { id: 'failed', target: { id: 'agent:a' }, blocked: true,
          error: "No clear speech was detected. Recording discarded." } } });
        const agent = { id: 'a', name: 'Architect', state: 'working', harness: 'codex', mode: 'chat', queued: 0 };
        const html = renderToStaticMarkup(<TooltipProvider><Composer agent={agent}/></TooltipProvider>);
        useDictationOutbox.setState({ notices: { 'agent:a': { id: 'held', target: { id: 'agent:a' }, held: true,
          heldReason: 'suspect', text: 'uncertain words' } } });
        const heldHTML = renderToStaticMarkup(<TooltipProvider><Composer agent={agent}/></TooltipProvider>);
        useDictationOutbox.setState({ notices: { 'agent:a': { id: 'noted', target: { id: 'agent:a' }, delivered: true,
          note: 'Fae unreachable, used local', text: 'hello fae' } } });
        const notedHTML = renderToStaticMarkup(<TooltipProvider><Composer agent={agent}/></TooltipProvider>);
        console.log(JSON.stringify({ html, heldHTML, notedHTML }));
      ` },
      bundle: true, outfile: output, platform: 'node', format: 'cjs', jsx: 'automatic', logLevel: 'silent',
      alias: { '@': path.join(ui, 'src') },
      plugins: [{ name: 'current-store-snapshot', setup(b) {
        b.onResolve({ filter: /^zustand$/ }, () => ({ path: path.join(ui, 'scripts/render/zustand-ssr.ts') }));
      } }],
    });
    const run = spawnSync(process.execPath, [output], { env: { ...process.env, RENDER_WIDTH: '390' }, encoding: 'utf8', windowsHide: true });
    assert.equal(run.status, 0, run.stderr);
    const { html, heldHTML, notedHTML } = JSON.parse(run.stdout);
    assert.match(heldHTML, /Text unsure/);
    assert.match(heldHTML, /Background noise may affect/);
    assert.match(heldHTML, /uncertain words/);
    assert.match(heldHTML, /Use text anyway/);
    assert.doesNotMatch(html, /Retry transcription|Discard<|Save audio/);
    // A fallback note (e.g. Fae unreachable) renders once; the already-delivered text is not
    // re-shown here (it went into the conversation already), and it is not held for review.
    assert.match(notedHTML, /Fae unreachable, used local/);
    assert.doesNotMatch(notedHTML, /hello fae/);
    assert.doesNotMatch(notedHTML, /Use text anyway/);
    assert.match(notedHTML, />Dismiss</);
    assert.equal((html.match(/Recording discarded\./g) || []).length, 1, 'one retained-error status');
    const actions = html.indexOf('data-composer-actions');
    assert.ok(html.indexOf('Recording discarded.') < actions, 'status is outside and before actions');
    assert.ok(html.slice(actions).includes('Interrupt'), 'stop control is retained');
    assert.ok(html.slice(actions).includes('aria-label="Send"'), 'send control is retained');
  } finally {
    rmSync(output, { force: true });
    rmdirSync(dir);
  }
});

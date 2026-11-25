import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, existsSync } from 'node:fs';

const html = readFileSync(new URL('../index.html', import.meta.url), 'utf8');

test('index.html links the web app manifest and PWA meta tags', () => {
  assert.match(html, /<link rel="manifest" href="\/manifest\.webmanifest" \/>/);
  assert.match(html, /<meta name="theme-color" media="\(prefers-color-scheme: light\)" content="#f1f5ed" \/>/);
  assert.match(html, /<meta name="theme-color" media="\(prefers-color-scheme: dark\)" content="#151e19" \/>/);
  assert.match(html, /<meta name="mobile-web-app-capable" content="yes" \/>/);
  assert.match(html, /<meta name="apple-mobile-web-app-capable" content="yes" \/>/);
  assert.match(html, /<meta name="apple-mobile-web-app-status-bar-style" content="[^"]+" \/>/);
  assert.match(html, /<link rel="apple-touch-icon" href="\/apple-touch-icon\.png" \/>/);
});

test('manifest.webmanifest declares name, standalone display, and a maskable icon', () => {
  const manifestPath = new URL('../public/manifest.webmanifest', import.meta.url);
  assert.ok(existsSync(manifestPath), 'public/manifest.webmanifest is missing');
  const manifest = JSON.parse(readFileSync(manifestPath, 'utf8'));
  assert.equal(manifest.name, 'Tiamatrix');
  assert.equal(manifest.short_name, 'Tiamatrix');
  assert.equal(manifest.start_url, '/');
  assert.equal(manifest.scope, '/');
  assert.equal(manifest.display, 'standalone');
  assert.ok(manifest.theme_color);
  assert.ok(manifest.background_color);
  assert.ok(Array.isArray(manifest.icons) && manifest.icons.length >= 2);
  assert.ok(manifest.icons.some(i => i.purpose === 'maskable'), 'manifest is missing a maskable icon');
  for (const icon of manifest.icons) {
    const iconPath = new URL('../public' + icon.src, import.meta.url);
    assert.ok(existsSync(iconPath), `icon file missing: ${icon.src}`);
  }
});

test('favicon, desktop source and header use the canonical Living cell', () => {
  const canonical = readFileSync(new URL('../public/living-cell.svg', import.meta.url), 'utf8');
  assert.match(canonical, /id="living-cell"/);
  assert.match(canonical, /M1 1H5V5H1Z/);
  for (const f of ['../public/brand/header-mark.svg', '../../build/appicon.svg']) {
    assert.equal(readFileSync(new URL(f, import.meta.url), 'utf8').trim(), canonical.trim(), f);
  }
  // The favicon and the header mark are the Python dashboard's own files (brand/favicon.svg,
  // brand/living-cell.svg): the same cell without a background, in the page's colours.
  const paths = (svg) => [...svg.matchAll(/ d="([^"]+)"/g)].map((m) => m[1]).join('|');
  const favicon = readFileSync(new URL('../public/favicon.svg', import.meta.url), 'utf8');
  const header = readFileSync(new URL('../src/assets/living-cell.svg', import.meta.url), 'utf8');
  assert.equal(paths(favicon), paths(canonical));
  assert.equal(paths(header), paths(canonical));
  assert.match(favicon, /prefers-color-scheme:dark/);
  assert.match(header, /fill="currentColor"/);
  const mark = readFileSync(new URL('../src/components/brand-mark.tsx', import.meta.url), 'utf8');
  assert.match(mark, /@\/assets\/living-cell\.svg\?raw/);
});

// The installed app opened on a dark background in both modes (a dark flash in light mode), and
// the first paint was white or near black instead of the page's own green. Everything that is
// shown before and around the page now has the page background of its mode.
test('install colours: the page background in light and dark, the install file light as in Python', async () => {
  const { PAGE_BACKGROUND, INSTALL_BACKGROUND, pageBackground } = await import('../src/lib/install-colors.ts');
  assert.deepEqual(PAGE_BACKGROUND, { light: '#f1f5ed', dark: '#151e19' });
  assert.equal(pageBackground(false), '#f1f5ed');
  assert.equal(pageBackground(true), '#151e19');
  // the same colours as the stylesheet's page background
  const css = readFileSync(new URL('../src/index.css', import.meta.url), 'utf8');
  const block = (sel) => css.slice(css.indexOf(sel + ' {'), css.indexOf('\n}', css.indexOf(sel + ' {')));
  assert.ok(block(':root').includes(`\n  --background: ${PAGE_BACKGROUND.light};`));
  assert.ok(block('.dark').includes(`\n  --background: ${PAGE_BACKGROUND.dark};`));
  // the install file: light, in Tiamatrix green, upright (terminal-matrix static/mobile/manifest.json)
  const manifest = JSON.parse(readFileSync(new URL('../public/manifest.webmanifest', import.meta.url), 'utf8'));
  assert.equal(INSTALL_BACKGROUND, PAGE_BACKGROUND.light);
  assert.equal(manifest.background_color, INSTALL_BACKGROUND);
  assert.equal(manifest.theme_color, INSTALL_BACKGROUND);
  assert.equal(manifest.orientation, 'portrait');
  // index.html: the first paint, and the bar when a theme chosen in the app differs from the system's
  assert.ok(html.includes(`var bg = dark ? "${PAGE_BACKGROUND.dark}" : "${PAGE_BACKGROUND.light}";`));
  assert.ok(html.includes('el.style.backgroundColor = bg;'));
  assert.ok(html.includes(`metas[i].setAttribute("content", bg);`));
  assert.ok(!/#171717|#ffffff|#202a25/i.test(html), 'no colour that is not the page background');
  // content never sits under the phone's status bar: the classic phone has no bar of its own there
  assert.match(html, /<meta name="apple-mobile-web-app-status-bar-style" content="default" \/>/);
  // the running app keeps the bar in step with the theme
  const theme = readFileSync(new URL('../src/lib/theme.ts', import.meta.url), 'utf8');
  assert.ok(theme.includes(`document.querySelectorAll('meta[name="theme-color"]').forEach((m) => m.setAttribute("content", pageBackground(dark)));`));
});

test('PNG icons have their advertised install sizes', () => {
  const icons = { 'icon-192.png': 192, 'icon-512.png': 512, 'icon-maskable-192.png': 192, 'icon-maskable-512.png': 512, 'apple-touch-icon.png': 180 };
  for (const [name, size] of Object.entries(icons)) {
    const png = readFileSync(new URL('../public/' + name, import.meta.url));
    assert.equal(png.readUInt32BE(16), size, name + ' width');
    assert.equal(png.readUInt32BE(20), size, name + ' height');
  }
});

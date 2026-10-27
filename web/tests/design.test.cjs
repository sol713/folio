const fs = require('node:fs');
const path = require('node:path');
const assert = require('node:assert/strict');

const root = path.join(__dirname, '..');
const css = fs.readFileSync(path.join(root, 'styles.css'), 'utf8');
const app = fs.readFileSync(path.join(root, 'app.js'), 'utf8');
const shell = fs.readFileSync(path.join(root, 'index.html'), 'utf8');
const tokens = css.match(/^:root\{([^}]+)\}/)[1];
const neutral = color => {
  const hex = color.slice(1);
  const rgb = hex.length < 5 ? hex.slice(0, 3).split('').map(c => c + c) : hex.match(/../g).slice(0, 3);
  return rgb[0] === rgb[1] && rgb[1] === rgb[2];
};

for (const declaration of tokens.split(';')) {
  for (const [color] of declaration.matchAll(/#[\da-f]{3,8}\b/gi)) {
    if (!/^--(danger|warning|success)(?:-soft)?:/.test(declaration)) {
      assert.ok(neutral(color), `non-status token must be neutral: ${declaration}`);
    }
  }
}
for (const [color] of css.slice(css.indexOf('}') + 1).matchAll(/#[\da-f]{3,8}\b/gi)) {
  assert.ok(neutral(color), `component colors must be neutral or use a status token: ${color}`);
}
for (const [color] of app.matchAll(/#[\da-f]{3,8}\b/gi)) {
  assert.ok(neutral(color), `inline UI color must be neutral or use a status token: ${color}`);
}
for (const [color] of decodeURIComponent(shell).matchAll(/#[\da-f]{3,8}\b/gi)) {
  assert.ok(neutral(color), `browser theme and favicon must be neutral: ${color}`);
}
assert.doesNotMatch(css, /var\(--orange(?:-dark)?\)|\.studio-writing/);
assert.match(css, /\.studio-shell \.studio-sidebar\{background:var\(--paper\)/);
assert.match(css, /\.shell \.post-card\{background:var\(--white\)/);
assert.doesNotMatch(css, /filter\s*:[^;}]*grayscale/, 'authored cover images keep their original colors');
console.log('PASS: site-wide neutral tokens, semantic-only status colors, shared public/Studio surfaces and untouched authored images');

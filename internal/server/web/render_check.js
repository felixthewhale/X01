// Standalone check of the dashboard rendering helpers (no browser, no server).
// Run: node internal/server/web/render_check.js internal/server/web/app.js
'use strict';

const fs = require('fs');
const src = fs.readFileSync(process.argv[2] || 'internal/server/web/app.js', 'utf8');

// Minimal DOM stub. escapeHtml() (produced by innerText) has to actually escape,
// otherwise this test would pass on broken output.
function escapeText(t) {
    return String(t)
        .replace(/&/g, '&amp;')
        .replace(/</g, '&lt;')
        .replace(/>/g, '&gt;');
}

function makeEl() {
    return {
        _innerText: '',
        _h: '',
        style: {},
        set innerText(v) { this._innerText = v; },
        get innerText() { return this._innerText; },
        get innerHTML() { return escapeText(this._innerText); },
        set innerHTML(v) { this._h = v; },
        appendChild() {},
        scrollTo() {},
        addEventListener() {},
        scrollHeight: 0,
        scrollTop: 0,
        clientHeight: 0,
    };
}

const documentStub = {
    createElement: makeEl,
    getElementById: makeEl,
};

// Expose the pure helpers without running the polling code.
const wrapped = src + '\n;module.exports = { renderToolCalls, truncate, escapeHtml };';
const mod = { exports: {} };
new Function('document', 'module', 'setInterval', wrapped)(documentStub, mod, () => {});
const { renderToolCalls, truncate } = mod.exports;

let failures = 0;
function check(label, actual, expectIncludes) {
    const ok = actual.includes(expectIncludes);
    if (!ok) failures++;
    console.log((ok ? 'PASS  ' : 'FAIL  ') + label);
    if (!ok) console.log('      expected to contain: ' + JSON.stringify(expectIncludes) + '\n      got: ' + actual);
}

// 1. A normal tool call renders its name and its arguments.
const out1 = renderToolCalls([{
    id: 'call_1', type: 'function',
    function: { name: 'docker_shell', arguments: '{"command":"ls -la /workspace","timeout":30}' },
}]);
check('renders tool name', out1, 'docker_shell');
check('renders pretty-printed command', out1, '"command": "ls -la /workspace"');
check('wraps in .tool-call', out1, 'class="tool-call"');

// 2. Nothing to render -> empty string (no stray markup).
check('undefined input -> empty', renderToolCalls(undefined), '');
check('empty array -> empty', renderToolCalls([]), '');

// 3. Malformed JSON arguments are shown raw, not swallowed by the try/catch.
const out3 = renderToolCalls([{ type: 'function', function: { name: 'edit_file', arguments: '{"path":"a" GARBAGE' } }]);
check('malformed args shown raw', out3, 'GARBAGE');

// 4. HTML in arguments must be escaped (innerText path escapes the name; check the payload).
const out4 = renderToolCalls([{ type: 'function', function: { name: 'think', arguments: '{"text":"<img src=x>"}' } }]);
check('escapes angle brackets', out4, '&lt;img src=x&gt;');

// 5. Long arguments are truncated, with a stated count.
const big = 'y'.repeat(5000);
const out5 = renderToolCalls([{ type: 'function', function: { name: 'x', arguments: JSON.stringify({ t: big }) } }]);
check('truncates long args', out5, 'truncated');
check('truncate() keeps prefix', truncate('abcdef', 3), 'abc');

console.log(failures === 0 ? '\nALL CHECKS PASSED' : '\n' + failures + ' CHECK(S) FAILED');
process.exit(failures === 0 ? 0 : 1);

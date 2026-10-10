'use strict';

// node run.js VEYL FILE: activates the extension against a fake vscode,
// opens FILE, and checks the answers end to end. Prints "ok" or fails.

const Module = require('module');
const path = require('path');
const fs = require('fs');
const assert = require('assert');

const fake = require('./fake-vscode.js');
const resolve = Module._resolveFilename;
Module._resolveFilename = function (req, ...rest) {
    if (req === 'vscode') {
        return path.join(__dirname, 'fake-vscode.js');
    }
    return resolve.call(this, req, ...rest);
};

const [veyl, file] = process.argv.slice(2);
fake._setPath(veyl);
const ext = require('../extension.js');

function doc(text, version) {
    const uri = fake.Uri.parse('file://' + (file.startsWith('/') ? '' : '/') + file.replace(/\\/g, '/'));
    return { uri, languageId: 'veyl', version, getText: () => text };
}

function wait(cond) {
    return new Promise((ok, fail) => {
        const start = Date.now();
        const tick = () => {
            if (cond()) {
                ok();
            } else if (Date.now() - start > 15000) {
                fail(new Error('timed out'));
            } else {
                setTimeout(tick, 20);
            }
        };
        tick();
    });
}

(async () => {
    const context = { subscriptions: [] };
    ext.activate(context);
    const text = fs.readFileSync(file, 'utf8');
    const d = doc(text, 1);
    fake._open(d);

    await wait(() => fake._reported.size > 0);
    const diags = [...fake._reported.values()][0];
    assert.strictEqual(diags.length, 1, 'one error');
    assert.match(diags[0].message, /declared as int but the value is str/);
    assert.strictEqual(diags[0].range.start.line, 5);

    const p = fake._providers;
    const items = await p.completion.provideCompletionItems(d, new fake.Position(4, 8));
    assert.ok(items.some((i) => i.label === 'twice'), 'completion has twice');

    const loc = await p.definition.provideDefinition(d, new fake.Position(4, 9));
    assert.ok(loc, 'a definition');
    assert.strictEqual(loc.range.start.line, 0);

    const h = await p.hover.provideHover(d, new fake.Position(4, 9));
    assert.match(h.contents.value, /fn twice\(n: int\) -> int/);

    const syms = await p.symbols.provideDocumentSymbols(d);
    assert.deepStrictEqual(syms.map((s) => s.name), ['twice']);

    // Formatting: the file is laid out badly on purpose at its last line.
    const edits = await p.format.provideDocumentFormattingEdits(d);
    assert.strictEqual(edits.length, 1, 'one formatting edit');
    assert.match(edits[0].newText, /\nprint\(1 \+ 2\)\n$/);

    // An edit that fixes the error clears it.
    fake._change(doc(text.replace('let bad: int = "x"', 'let bad: int = 1'), 2));
    await wait(() => [...fake._reported.values()][0].length === 0);

    assert.deepStrictEqual(fake._warnings, []);
    context.subscriptions.forEach((s) => s.dispose && s.dispose());
    console.log('ok');
    process.exit(0);
})().catch((e) => {
    console.error(e.stack || e);
    process.exit(1);
});

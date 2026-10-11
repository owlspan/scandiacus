'use strict';

// Just enough of the vscode module for extension.js to run under plain
// node, so its protocol code is tested against a real veyl lsp. It
// records what the extension registers and what it reports.

const listeners = { open: [], change: [], close: [], config: [], closeTerminal: [] };
const commands = {};
const terminals = [];
const providers = {};
const reported = new Map();
let configPath = '';

class Uri {
    constructor(s) {
        this.s = s;
        // file:///C:/x.vl and file:///tmp/x.vl, as the platform writes them.
        const p = s.replace(/^file:\/\//, '');
        this.fsPath = /^\/[A-Za-z]:/.test(p) ? p.slice(1).replace(/\//g, '\\') : p;
    }
    static parse(s) { return new Uri(s); }
    toString() { return this.s; }
}
class Position { constructor(line, character) { this.line = line; this.character = character; } }
class Range {
    constructor(a, b, c, d) { this.start = new Position(a, b); this.end = new Position(c, d); }
}
class Location { constructor(uri, range) { this.uri = uri; this.range = range; } }
class Diagnostic { constructor(range, message, severity) { this.range = range; this.message = message; this.severity = severity; } }
class MarkdownString { constructor(value) { this.value = value; } }
class Hover { constructor(contents) { this.contents = contents; } }
class CompletionItem { constructor(label, kind) { this.label = label; this.kind = kind; } }
class TextEdit {
    constructor(range, newText) { this.range = range; this.newText = newText; }
    static replace(range, text) { return new TextEdit(range, text); }
}
class DocumentSymbol {
    constructor(name, detail, kind, range, selectionRange) {
        this.name = name; this.kind = kind; this.range = range; this.selectionRange = selectionRange;
    }
}

const docs = [];

module.exports = {
    Uri, Position, Range, Location, TextEdit, Diagnostic, MarkdownString, Hover, CompletionItem, DocumentSymbol,
    DiagnosticSeverity: { Error: 0, Warning: 1 },
    window: {
        activeTextEditor: undefined,
        showWarningMessage(msg) { module.exports._warnings.push(msg); },
        createTerminal(options) {
            const t = { options, shown: false, disposed: false };
            t.show = () => { t.shown = true; };
            t.dispose = () => { t.disposed = true; };
            terminals.push(t);
            return t;
        },
        onDidCloseTerminal(f) { listeners.closeTerminal.push(f); return { dispose() {} }; },
    },
    commands: {
        registerCommand(name, f) { commands[name] = f; return { dispose() {} }; },
    },
    workspace: {
        textDocuments: docs,
        getConfiguration() { return { get() { return configPath; } }; },
        onDidOpenTextDocument(f) { listeners.open.push(f); return { dispose() {} }; },
        onDidChangeTextDocument(f) { listeners.change.push(f); return { dispose() {} }; },
        onDidCloseTextDocument(f) { listeners.close.push(f); return { dispose() {} }; },
        onDidChangeConfiguration(f) { listeners.config.push(f); return { dispose() {} }; },
    },
    languages: {
        createDiagnosticCollection() {
            return {
                set(uri, list) { reported.set(uri.toString(), list); },
                delete(uri) { reported.delete(uri.toString()); },
                dispose() {},
            };
        },
        registerCompletionItemProvider(sel, p) { providers.completion = p; return { dispose() {} }; },
        registerDefinitionProvider(sel, p) { providers.definition = p; return { dispose() {} }; },
        registerHoverProvider(sel, p) { providers.hover = p; return { dispose() {} }; },
        registerDocumentSymbolProvider(sel, p) { providers.symbols = p; return { dispose() {} }; },
        registerDocumentFormattingEditProvider(sel, p) { providers.format = p; return { dispose() {} }; },
    },

    // For the test.
    _warnings: [],
    _commands: commands,
    _terminals: terminals,
    _providers: providers,
    _reported: reported,
    _setPath(p) { configPath = p; },
    _open(doc) { docs.push(doc); listeners.open.forEach((f) => f(doc)); },
    _change(doc) { listeners.change.forEach((f) => f({ document: doc })); },
};

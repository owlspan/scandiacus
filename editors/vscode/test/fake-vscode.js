'use strict';

// Just enough of the vscode module for extension.js to run under plain
// node, so its protocol code is tested against a real veyl lsp. It
// records what the extension registers and what it reports.

const listeners = { open: [], change: [], close: [], config: [] };
const providers = {};
const reported = new Map();
let configPath = '';

class Uri {
    constructor(s) { this.s = s; }
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
class DocumentSymbol {
    constructor(name, detail, kind, range, selectionRange) {
        this.name = name; this.kind = kind; this.range = range; this.selectionRange = selectionRange;
    }
}

const docs = [];

module.exports = {
    Uri, Position, Range, Location, Diagnostic, MarkdownString, Hover, CompletionItem, DocumentSymbol,
    DiagnosticSeverity: { Error: 0 },
    window: { showWarningMessage(msg) { module.exports._warnings.push(msg); } },
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
    },

    // For the test.
    _warnings: [],
    _providers: providers,
    _reported: reported,
    _setPath(p) { configPath = p; },
    _open(doc) { docs.push(doc); listeners.open.forEach((f) => f(doc)); },
    _change(doc) { listeners.change.forEach((f) => f({ document: doc })); },
};

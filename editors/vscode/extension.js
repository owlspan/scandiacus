'use strict';

// The VS Code side of `veyl lsp`.
//
// A language client is usually the vscode-languageclient package, which
// would mean shipping node_modules with the extension. The part of the
// protocol Veyl uses is small - a few requests and one notification over
// stdio - so it is written out here instead, and the extension stays a
// handful of plain files.
//
// If veyl cannot be started (not on PATH, or veyl.path is wrong) the
// highlighting still works; a message says what to fix.
//
// It also puts a run button, with build beside it in its menu, on the
// title bar of a .vl file, the way the Python extension does.

const vscode = require('vscode');
const cp = require('child_process');
const path = require('path');

class Server {
    constructor(command, onNotify, onExit) {
        this.nextId = 1;
        this.pending = new Map();
        this.buffer = Buffer.alloc(0);
        this.onNotify = onNotify;
        this.proc = cp.spawn(command, ['lsp'], { stdio: ['pipe', 'pipe', 'pipe'] });
        this.proc.on('error', (err) => onExit(err));
        this.proc.on('exit', (code) => onExit(code === 0 ? null : new Error('veyl lsp exited with ' + code)));
        this.proc.stdout.on('data', (chunk) => this.receive(chunk));
        this.proc.stdin.on('error', () => {});
    }

    receive(chunk) {
        this.buffer = Buffer.concat([this.buffer, chunk]);
        for (;;) {
            const end = this.buffer.indexOf('\r\n\r\n');
            if (end < 0) {
                return;
            }
            const head = this.buffer.slice(0, end).toString('ascii');
            const m = /Content-Length:\s*(\d+)/i.exec(head);
            if (!m) {
                this.buffer = this.buffer.slice(end + 4);
                continue;
            }
            const len = parseInt(m[1], 10);
            if (this.buffer.length < end + 4 + len) {
                return;
            }
            const body = this.buffer.slice(end + 4, end + 4 + len).toString('utf8');
            this.buffer = this.buffer.slice(end + 4 + len);
            let msg;
            try {
                msg = JSON.parse(body);
            } catch (e) {
                continue;
            }
            if (msg.id !== undefined && msg.method === undefined) {
                const p = this.pending.get(msg.id);
                if (p) {
                    this.pending.delete(msg.id);
                    if (msg.error) {
                        p.reject(new Error(msg.error.message));
                    } else {
                        p.resolve(msg.result);
                    }
                }
            } else if (msg.method) {
                this.onNotify(msg.method, msg.params);
            }
        }
    }

    write(msg) {
        msg.jsonrpc = '2.0';
        const body = Buffer.from(JSON.stringify(msg), 'utf8');
        this.proc.stdin.write('Content-Length: ' + body.length + '\r\n\r\n');
        this.proc.stdin.write(body);
    }

    request(method, params) {
        const id = this.nextId++;
        return new Promise((resolve, reject) => {
            this.pending.set(id, { resolve, reject });
            this.write({ id, method, params });
        });
    }

    notify(method, params) {
        this.write({ method, params });
    }

    stop() {
        try {
            this.notify('exit', null);
            this.proc.kill();
        } catch (e) {
            // already gone
        }
    }
}

const selector = { language: 'veyl' };

function toRange(r) {
    return new vscode.Range(r.start.line, r.start.character, r.end.line, r.end.character);
}

function activate(context) {
    const diagnostics = vscode.languages.createDiagnosticCollection('veyl');
    context.subscriptions.push(diagnostics);

    let server = null;
    let ready = null;
    let failed = false;
    const opened = new Set();

    function start() {
        const command = vscode.workspace.getConfiguration('veyl').get('path') || 'veyl';
        opened.clear();
        server = new Server(command, (method, params) => {
            if (method === 'textDocument/publishDiagnostics') {
                diagnostics.set(vscode.Uri.parse(params.uri), params.diagnostics.map((d) => {
                    const sev = d.severity === 2 ? vscode.DiagnosticSeverity.Warning : vscode.DiagnosticSeverity.Error;
                    const diag = new vscode.Diagnostic(toRange(d.range), d.message, sev);
                    diag.source = 'veyl';
                    return diag;
                }));
            }
        }, (err) => {
            server = null;
            ready = null;
            if (err && !failed) {
                failed = true;
                vscode.window.showWarningMessage('Veyl: could not run "' + command + ' lsp" (' + err.message +
                    '). Highlighting still works; for errors, completion and go to definition, put veyl on PATH or set veyl.path.');
            }
        });
        ready = server.request('initialize', {
            processId: process.pid,
            rootUri: null,
            capabilities: {},
        }).then(() => {
            server.notify('initialized', {});
            for (const doc of vscode.workspace.textDocuments) {
                open(doc);
            }
        });
        ready.catch(() => {});
    }

    function open(doc) {
        if (!server || doc.languageId !== 'veyl' || opened.has(doc.uri.toString())) {
            return;
        }
        opened.add(doc.uri.toString());
        server.notify('textDocument/didOpen', {
            textDocument: { uri: doc.uri.toString(), languageId: 'veyl', version: doc.version, text: doc.getText() },
        });
    }

    // A request, once the server is up and knows the document.
    async function ask(method, doc, position) {
        if (!server) {
            if (failed) {
                return null;
            }
            start();
        }
        await ready;
        if (!server) {
            return null;
        }
        open(doc);
        const params = { textDocument: { uri: doc.uri.toString() } };
        if (position) {
            params.position = { line: position.line, character: position.character };
        }
        try {
            return await server.request(method, params);
        } catch (e) {
            return null;
        }
    }

    // The run and build buttons. Each one starts a terminal whose process
    // is veyl itself rather than a shell, so it works the same whichever
    // shell the user has, the path needs no quoting, and a program that
    // reads input gets the terminal's. The previous run's terminal is
    // closed first, so there is one Veyl terminal rather than a pile.
    let runTerminal = null;

    async function runVeyl(verb, uri) {
        let target = uri && uri.fsPath ? uri : null;
        const editor = vscode.window.activeTextEditor;
        if (!target && editor) {
            target = editor.document.uri;
        }
        if (!target || !target.fsPath) {
            vscode.window.showWarningMessage('Veyl: open a .vl file to ' + verb + ' it.');
            return null;
        }
        // Run what is on screen, not what was last saved.
        const doc = vscode.workspace.textDocuments.find((d) => d.uri.toString() === target.toString());
        if (doc && doc.isDirty) {
            await doc.save();
        }
        if (runTerminal) {
            runTerminal.dispose();
        }
        runTerminal = vscode.window.createTerminal({
            name: 'Veyl',
            shellPath: vscode.workspace.getConfiguration('veyl').get('path') || 'veyl',
            shellArgs: [verb, target.fsPath],
            cwd: path.dirname(target.fsPath),
        });
        runTerminal.show();
        return runTerminal;
    }

    start();

    context.subscriptions.push(
        vscode.commands.registerCommand('veyl.run', (uri) => runVeyl('run', uri)),
        vscode.commands.registerCommand('veyl.build', (uri) => runVeyl('build', uri)),
        vscode.window.onDidCloseTerminal((t) => {
            if (t === runTerminal) {
                runTerminal = null;
            }
        }),

        vscode.workspace.onDidOpenTextDocument((doc) => open(doc)),
        vscode.workspace.onDidChangeTextDocument((e) => {
            const doc = e.document;
            if (!server || doc.languageId !== 'veyl') {
                return;
            }
            if (!opened.has(doc.uri.toString())) {
                open(doc);
                return;
            }
            server.notify('textDocument/didChange', {
                textDocument: { uri: doc.uri.toString(), version: doc.version },
                contentChanges: [{ text: doc.getText() }],
            });
        }),
        vscode.workspace.onDidCloseTextDocument((doc) => {
            if (server && opened.has(doc.uri.toString())) {
                opened.delete(doc.uri.toString());
                server.notify('textDocument/didClose', { textDocument: { uri: doc.uri.toString() } });
            }
            diagnostics.delete(doc.uri);
        }),
        vscode.workspace.onDidChangeConfiguration((e) => {
            if (e.affectsConfiguration('veyl.path')) {
                if (server) {
                    server.stop();
                }
                failed = false;
                start();
            }
        }),

        vscode.languages.registerCompletionItemProvider(selector, {
            async provideCompletionItems(doc, position) {
                const items = await ask('textDocument/completion', doc, position);
                return (items || []).map((it) => {
                    // LSP kinds count from 1, VS Code's from 0.
                    const ci = new vscode.CompletionItem(it.label, (it.kind || 1) - 1);
                    if (it.detail) {
                        ci.detail = it.detail;
                    }
                    return ci;
                });
            },
        }, '.'),

        vscode.languages.registerDefinitionProvider(selector, {
            async provideDefinition(doc, position) {
                const loc = await ask('textDocument/definition', doc, position);
                if (!loc) {
                    return null;
                }
                return new vscode.Location(vscode.Uri.parse(loc.uri), toRange(loc.range));
            },
        }),

        vscode.languages.registerHoverProvider(selector, {
            async provideHover(doc, position) {
                const h = await ask('textDocument/hover', doc, position);
                if (!h || !h.contents) {
                    return null;
                }
                return new vscode.Hover(new vscode.MarkdownString(h.contents.value));
            },
        }),

        vscode.languages.registerDocumentFormattingEditProvider(selector, {
            async provideDocumentFormattingEdits(doc) {
                const edits = await ask('textDocument/formatting', doc, null);
                return (edits || []).map((e) => vscode.TextEdit.replace(toRange(e.range), e.newText));
            },
        }),

        vscode.languages.registerDocumentSymbolProvider(selector, {
            async provideDocumentSymbols(doc) {
                const syms = await ask('textDocument/documentSymbol', doc, null);
                return (syms || []).map((s) =>
                    new vscode.DocumentSymbol(s.name, '', s.kind - 1, toRange(s.range), toRange(s.selectionRange)));
            },
        }),

        { dispose() { if (server) { server.stop(); } } },
    );
}

function deactivate() {}

module.exports = { activate, deactivate };

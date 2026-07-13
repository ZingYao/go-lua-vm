"use strict";

const assert = require("assert");
const fs = require("fs");
const path = require("path");

const editorCore = require(path.resolve(__dirname, "..", "docs", "assets", "playground-editor-core.js"));
const playgroundSource = fs.readFileSync(path.resolve(__dirname, "..", "docs", "assets", "playground.js"), "utf8");
const playgroundTheme = fs.readFileSync(path.resolve(__dirname, "..", "docs", "assets", "theme.css"), "utf8");

const ifExpansion = editorCore.blockExpansion("  if value then", "");
assert(ifExpansion, "if block should expand");
assert.strictEqual(ifExpansion.text, "\n    \n  end");
assert.strictEqual(ifExpansion.caretLineDelta, 1);
assert.strictEqual(ifExpansion.caretColumn, 5);

assert.strictEqual(
  editorCore.blockExpansion("  if value then", "  end"),
  null,
  "existing end should not be duplicated"
);

const repeatExpansion = editorCore.blockExpansion("repeat", "");
assert(repeatExpansion, "repeat block should expand");
assert.strictEqual(repeatExpansion.text, "\n  \nuntil ");

const switchExpansion = editorCore.blockExpansion("switch value do", "");
assert(switchExpansion, "switch block should expand");
assert.strictEqual(switchExpansion.text, "\n  case \n    \nend");
assert.strictEqual(switchExpansion.caretColumn, 8);

assert.strictEqual(editorCore.blockExpansion("local value = 1", ""), null);

const centeredRect = { left: 100, right: 500, top: 80, bottom: 380 };
assert.deepStrictEqual(
  editorCore.clampDragDelta(centeredRect, { width: 800, height: 600 }, { x: 50, y: -20 }, 8),
  { x: 50, y: -20 }
);

assert.deepStrictEqual(editorCore.completionContext("local file = io.ope"), {
  qualifiedPrefix: "io.ope",
  namespace: "io.",
  catalogNamespace: "io.",
  memberPrefix: "ope",
});
assert.deepStrictEqual(editorCore.completionContext("file:re"), {
  qualifiedPrefix: "file:re",
  namespace: "file:",
  catalogNamespace: "file.",
  memberPrefix: "re",
});
assert.strictEqual(
  editorCore.completionSnippet("open", "io.open(filename [, mode])"),
  "open(${1:filename}, ${2:mode})"
);
assert.deepStrictEqual(
  editorCore.clampDragDelta(centeredRect, { width: 800, height: 600 }, { x: -300, y: 400 }, 8),
  { x: -92, y: 212 },
  "dragging should keep the complete dialog inside the viewport margin"
);

assert(playgroundSource.includes('"glua-page-action glua-page-action-debug", "Code"'), "page action should be labeled Code");
assert(playgroundSource.includes('dialogHeader.addEventListener("pointerdown", startDialogDrag)'), "dialog header should start pointer dragging");
assert(playgroundSource.includes('modal.classList.contains("is-fullscreen")'), "dialog dragging should account for fullscreen mode");
assert(playgroundSource.includes("mergeCompletionSuggestions"), "WASM and extension catalog completions should be merged");
assert(playgroundSource.includes("scheduleMemberCompletion"), "member completion should use a debounced automatic trigger");
assert(playgroundSource.includes('"执行代码", "Ctrl/Cmd+Enter"'), "toolbar actions should expose shortcut tooltips");
assert(playgroundSource.includes('id: "glua.runDocument"'), "Monaco should register a run-document action");
assert(
  playgroundSource.includes("window.monaco.KeyMod.CtrlCmd | window.monaco.KeyCode.Enter"),
  "Monaco should execute the document with Ctrl/Cmd+Enter while the editor has focus"
);
assert(playgroundSource.includes("node.dataset.tooltip = node.title"), "toolbar buttons should expose visible custom tooltip text");
assert(playgroundSource.includes('defineTheme("glua-light"'), "Monaco should register a light GLua theme");
assert(playgroundSource.includes('setAttribute("aria-label", "编辑器主题")'), "the theme selector should be accessible");
assert(playgroundSource.includes('localStorage.setItem(playgroundThemeStorageKey'), "the selected theme should persist across reloads");
assert(playgroundTheme.includes(".glua-playground-button[data-tooltip]::after"), "toolbar tooltips should render consistently for enabled and disabled buttons");
assert(playgroundTheme.includes('.glua-playground-dialog[data-theme="light"]'), "the complete Playground shell should expose a light theme");
assert(playgroundTheme.includes("--glua-playground-output-bg"), "theme variables should cover the output panel");
assert(playgroundTheme.includes(".glua-runnable-example .docsify-copy-code-button"), "run and copy buttons should share an explicit size contract");
assert(playgroundTheme.includes(".glua-runnable-example:focus-within .glua-run-example"), "keyboard focus should reveal the run action");
assert(!playgroundTheme.includes(".glua-run-example:hover"), "run should not use a darker hover background than copy");

console.log("playground editor behavior tests passed");

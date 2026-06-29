#!/usr/bin/env node
"use strict";

process.on("uncaughtException", (error) => {
  console.error(`[uncaught] ${error && error.stack ? error.stack : error}`);
});
process.on("unhandledRejection", (reason) => {
  console.error(`[unhandled] ${reason && reason.stack ? reason.stack : reason}`);
});

const {
  createConnection,
  ProposedFeatures,
  TextDocuments,
  CompletionItemKind,
} = require("vscode-languageserver/node");
const { TextDocument } = require("vscode-languageserver-textdocument");
const { DiagnosticSeverity, TextDocumentSyncKind } = require("vscode-languageserver/node");
const {
  getBuiltinFunction,
  getBuiltinFunctionByMethod,
  makeBuiltinUri,
  formatBuiltinMarkdown,
  builtinFunctionNames,
  setBuiltinLocale,
  getBuiltinLocale,
  applyBuiltinExtensionCatalog,
  resetBuiltinExtensions,
} = require("./builtin-functions");
const path = require("path");
const fs = require("fs");

// GLua 的颜色交给 TextMate grammar。VS Code 在格式化后会刷新
// semantic tokens；如果这里返回 token，部分主题会覆盖 grammar 高亮。
const semanticTokenTypes = [
  "namespace",
  "type",
  "class",
  "enum",
  "interface",
  "struct",
  "typeParameter",
  "parameter",
  "variable",
  "property",
  "enumMember",
  "event",
  "function",
  "method",
  "macro",
  "keyword",
  "modifier",
  "comment",
  "string",
  "number",
  "regexp",
  "operator",
];

const semanticTypeVariable = 8;
const semanticTypeNamespace = 0;
const semanticTypeFunction = 12;
const semanticTypeMethod = 13;
const semanticTypeKeyword = 15;
const semanticTypeComment = 17;
const semanticTypeString = 18;
const semanticTypeNumber = 19;
const semanticTypeOperator = 21;

const baseKeywords = new Set([
  "and",
  "break",
  "do",
  "else",
  "elseif",
  "end",
  "false",
  "for",
  "function",
  "if",
  "in",
  "local",
  "nil",
  "not",
  "or",
  "repeat",
  "return",
  "then",
  "true",
  "until",
  "while",
  "goto",
  "continue",
  "switch",
  "case",
  "default",
]);

const standardLibraries = new Set(["string", "math", "table", "io", "os", "coroutine", "debug", "utf8"]);

const baseBuiltinFunctions = new Set(builtinFunctionNames());
const DEFAULT_DOC_LOCALE = "auto";

function looksLikeLocale(rawLocale) {
  const raw = String(rawLocale || "").toLowerCase();
  return /^[a-z]{2,3}([_-][a-z0-9]{2,8}){0,3}$/.test(raw);
}

function isLocaleMap(value) {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return false;
  }
  const keys = Object.keys(value);
  if (keys.length === 0) {
    return false;
  }
  return keys.every((key) => looksLikeLocale(key));
}

function resolveLocale(raw) {
  if (!raw || String(raw).toLowerCase() === DEFAULT_DOC_LOCALE) {
    return "en";
  }
  return String(raw).toLowerCase();
}

function loadBuiltinExtensionDocs(filePath) {
  if (!filePath || typeof filePath !== "string") {
    return;
  }
  const inferLocaleFromFilePath = (resolvedPath) => {
    const baseName = path.basename(resolvedPath, path.extname(resolvedPath));
    const parts = baseName
      .toLowerCase()
      .split(/[._-]+/)
      .map((part) => part.trim())
      .filter(Boolean);
    const candidates = [
      "en",
      "en-us",
      "en-gb",
      "zh",
      "zh-cn",
      "zh_cn",
      "zh-hans",
      "zh-cn",
    ];
    for (const token of parts) {
      if (candidates.includes(token)) {
        return token;
      }
    }
    return "";
  };

  try {
    const content = fs.readFileSync(filePath, "utf8");
    const parsed = JSON.parse(content);
    if (parsed && typeof parsed === "object") {
      const defs = parsed.functions || parsed.builtins || parsed;
      const catalogLocale = parsed.locale || parsed.language || inferLocaleFromFilePath(filePath);
      if (defs && typeof defs === "object") {
        if (isLocaleMap(defs) && parsed.functions && parsed.builtins === undefined) {
          Object.entries(defs).forEach(([locale, localeDefs]) => {
            if (localeDefs && typeof localeDefs === "object") {
              applyBuiltinExtensionCatalog(localeDefs, locale);
            }
          });
          return;
        }
        if (!parsed.functions && isLocaleMap(parsed)) {
          Object.entries(parsed).forEach(([locale, localeDefs]) => {
            if (localeDefs && typeof localeDefs === "object") {
              const nested = localeDefs.functions || localeDefs.builtins || localeDefs;
              if (nested && typeof nested === "object") {
                applyBuiltinExtensionCatalog(nested, locale);
              }
            }
          });
          return;
        }
        applyBuiltinExtensionCatalog(defs, catalogLocale);
      }
    }
  } catch (error) {
    connection.console.error(`[builtin-docs] failed to load ${filePath}: ${error && error.message ? error.message : error}`);
  }
}

function applyBuiltinExtensionOptions(options) {
  const configuredLocale = resolveLocale(options && options.locale);
  setBuiltinLocale(configuredLocale);
  resetBuiltinExtensions();
  const extensions = options && Array.isArray(options.builtinExtensions) ? options.builtinExtensions : [];
  for (const file of extensions) {
    loadBuiltinExtensionDocs(file);
  }
  return {
    requestedLocale: options && options.locale ? String(options.locale) : DEFAULT_DOC_LOCALE,
    resolvedLocale: getBuiltinLocale(),
    extensionCount: extensions.length,
  };
}

function createDefaultSyntax() {
  return {
    switch: true,
    continue: true,
    lua53: false,
    enabledExtensions: new Set(["switch", "continue"]),
    profile: "extended",
  };
}

function parseSyntaxValue(raw) {
  if (!raw || raw.trim() === "") {
    return createDefaultSyntax();
  }
  const next = createDefaultSyntax();
  const profile = String(raw).trim().toLowerCase();
  next.profile = profile;
  const items = profile.split(",").map((item) => item.trim()).filter(Boolean);
  if (items.length === 0) {
    return next;
  }

  for (const item of items) {
    switch (item) {
      case "lua53":
        next.lua53 = true;
        next.switch = false;
        next.continue = false;
        next.enabledExtensions.clear();
        break;
      case "extended":
        next.lua53 = false;
        next.switch = true;
        next.continue = true;
        next.enabledExtensions = new Set(["switch", "continue"]);
        break;
      case "switch":
        next.switch = true;
        next.enabledExtensions.add("switch");
        break;
      case "continue":
        next.continue = true;
        next.enabledExtensions.add("continue");
        break;
      case "none":
      case "off":
      case "default":
        next.switch = false;
        next.continue = false;
        next.enabledExtensions.clear();
        break;
      default:
        throw new Error(`unknown syntax profile: ${item}`);
    }
  }

  return next;
}

function parseSyntaxFromArgs(argv) {
  let selected = createDefaultSyntax();
  for (let index = 0; index < argv.length; index++) {
    const value = argv[index];
    if (value === "--stdio" || value === "--node-ipc") {
      continue;
    }
    if (value === "--clientProcessId") {
      index += 1;
      continue;
    }
    if (value.startsWith("--clientProcessId=")) {
      continue;
    }
    if (value === "--socket" || value === "--pipe") {
      index += 1;
      continue;
    }
    if (value.startsWith("--socket=") || value.startsWith("--pipe=")) {
      continue;
    }
    if (value === "--syntax") {
      if (index + 1 >= argv.length) {
        throw new Error("option --syntax requires an argument");
      }
      selected = parseSyntaxValue(argv[index + 1]);
      index += 1;
      continue;
    }
    if (value.startsWith("--syntax=")) {
      selected = parseSyntaxValue(value.slice("--syntax=".length));
      continue;
    }
    if (value === "--help" || value === "-h") {
      return selected;
    }
    throw new Error(`unrecognized option ${value}`);
  }
  return selected;
}

function tokenizeLine(code, syntax) {
  return scanTokens(code, syntax);
}

function isContextKeyword(text, syntax) {
  if (!syntax) {
    return false;
  }
  if (text === "switch" || text === "case" || text === "default") {
    return syntax.switch;
  }
  if (text === "continue") {
    return syntax.continue;
  }
  return false;
}

function makePosition(line, character) {
  return { line, character };
}

function makeRange(startLine, startColumn, endLine, endColumn) {
  return {
    start: makePosition(startLine, startColumn),
    end: makePosition(endLine, endColumn),
  };
}

function scanTokens(source, syntax) {
  const tokens = [];
  const errors = [];
  const brackets = [];
  let index = 0;
  let line = 0;
  let column = 0;

  const emit = (type, text, raw, startLine, startColumn, endLine, endColumn) => {
    if (type === "string" || type === "number" || type === "identifier" || type === "keyword" || type === "operator") {
      tokens.push({
        type,
        text,
        raw,
        range: makeRange(startLine, startColumn, endLine, endColumn),
        line: startLine,
        startColumn,
      });
    }
  };

  const appendError = (message, startLine, startColumn) => {
    errors.push({
      message,
      range: makeRange(startLine, startColumn, startLine, startColumn + 1),
      near: "<eof>",
    });
  };

  const advance = () => {
    const character = source[index];
    index += 1;
    if (character === "\n") {
      line += 1;
      column = 0;
    } else {
      column += 1;
    }
    return character;
  };

  const peek = (offset = 0) => (index + offset >= source.length ? "" : source[index + offset]);

  const advanceWithText = (startLine, startColumn, startIndex, endLine, endColumn, textType, text, raw = text) => {
    emit(textType, text, raw, startLine, startColumn, endLine, endColumn);
    for (let i = startIndex; i < index; i++) {
      if (source[i] === "\n") {
        line += 1;
        column = 0;
      } else {
        column += 1;
      }
    }
    return { line: endLine, column: endColumn };
  };

  const readLongBracket = (startLine, startColumn, startIndex, asComment) => {
    let equalCount = 0;
    index += 1; // [
    column += 1;
    while (peek() === "=") {
      index += 1;
      column += 1;
      equalCount += 1;
    }
    if (peek() !== "[") {
      index -= 1;
      column -= 1;
      return false;
    }
    index += 1;
    column += 1;

    const close = `]${"=".repeat(equalCount)}]`;
    const closeIndex = source.indexOf(close, index);
    if (closeIndex < 0) {
      const raw = source.slice(startIndex);
      appendError("unfinished long string or comment", line, column);
      emit(asComment ? "comment" : "string", raw, raw, startLine, startColumn, line, column + raw.length - startColumn);
      index = source.length;
      return true;
    }
    const raw = source.slice(startIndex, closeIndex + close.length);
    for (let i = index; i < closeIndex + close.length; i++) {
      if (source[i] === "\n") {
        line += 1;
        column = 0;
      } else {
        column += 1;
      }
    }
    index = closeIndex + close.length;
    emit(asComment ? "comment" : "string", raw, raw, startLine, startColumn, line, column);
    return true;
  };

  while (index < source.length) {
    const currentLine = line;
    const currentColumn = column;
    const currentChar = peek();

    if (currentChar === " " || currentChar === "\t" || currentChar === "\r" || currentChar === "\n" || currentChar === "\f") {
      advance();
      continue;
    }

    if (currentChar === "-" && peek(1) === "-") {
      const commentStartLine = line;
      const commentStartColumn = column;
      const commentStartIndex = index;
      index += 2;
      column += 2;
      if (peek() === "[") {
        const longParsed = readLongBracket(commentStartLine, commentStartColumn, commentStartIndex, true);
        if (longParsed) {
          continue;
        }
      }

      let end = index;
      while (end < source.length && source[end] !== "\n") {
        end += 1;
      }
      const raw = source.slice(commentStartIndex, end);
      index = end;
      let endLine = line;
      let endColumn = column;
      for (let i = commentStartIndex + 2; i < end; i++) {
        if (source[i] === "\n") {
          endLine += 1;
          endColumn = 0;
        } else {
          endColumn += 1;
        }
      }
      emit("comment", raw, raw, commentStartLine, commentStartColumn, endLine, endColumn);
      column = endColumn;
      continue;
    }

    if (currentChar === "'" || currentChar === "\"") {
      const quote = currentChar;
      const startLine = line;
      const startColumn = column;
      const startIndex = index;
      index += 1;
      column += 1;
      let escaped = false;
      while (index < source.length) {
        const value = peek();
        advance();
        if (escaped) {
          escaped = false;
          continue;
        }
        if (value === "\\") {
          escaped = true;
          continue;
        }
        if (value === quote) {
          break;
        }
      }
      const raw = source.slice(startIndex, index);
      if (!raw.endsWith(quote)) {
        appendError("unfinished string", startLine, startColumn);
      }
      emit("string", raw, raw, startLine, startColumn, line, column);
      continue;
    }

    if (currentChar === "[" && peek(1) === "[") {
      const startLine = line;
      const startColumn = column;
      const startIndex = index;
      readLongBracket(startLine, startColumn, startIndex, false);
      continue;
    }

    if (currentChar === "." && peek(1) === "." && peek(2) === ".") {
      emit("operator", "...", "...", currentLine, currentColumn, currentLine, currentColumn + 3);
      index += 3;
      column += 3;
      continue;
    }
    if ("(){}[]".includes(currentChar)) {
      emit("operator", currentChar, currentChar, currentLine, currentColumn, currentLine, currentColumn + 1);
      index += 1;
      column += 1;
      if (currentChar === "(" || currentChar === "[" || currentChar === "{") {
        brackets.push(currentChar);
      } else if (currentChar === ")" || currentChar === "]" || currentChar === "}") {
        const open = currentChar === ")" ? "(" : currentChar === "]" ? "[" : "{";
        const expectOpen = open;
        if (brackets.length === 0 || brackets[brackets.length - 1] !== expectOpen) {
          appendError(`expected ${expectOpen}`, currentLine, currentColumn);
        } else {
          brackets.pop();
        }
      }
      continue;
    }

    const op2 = source.slice(index, index + 2);
    if (op2 === "::") {
      emit("operator", "::", "::", currentLine, currentColumn, currentLine, currentColumn + 2);
      index += 2;
      column += 2;
      continue;
    }

    if (op2 === "..") {
      emit("operator", "..", "..", currentLine, currentColumn, currentLine, currentColumn + 2);
      index += 2;
      column += 2;
      continue;
    }

    if (["==", "~=", "<=", ">="].includes(op2)) {
      emit("operator", op2, op2, currentLine, currentColumn, currentLine, currentColumn + 2);
      index += 2;
      column += 2;
      continue;
    }

    if (["<", ">", "=", "+", "-", "*", "/", "%", "^", "#", ",", ";", ":", "."].includes(currentChar)) {
      emit("operator", currentChar, currentChar, currentLine, currentColumn, currentLine, currentColumn + 1);
      index += 1;
      column += 1;
      continue;
    }

    if (/[0-9]/.test(currentChar) || (currentChar === "." && /[0-9]/.test(peek(1)))) {
      const startLine = currentLine;
      const startColumn = currentColumn;
      const startIndex = index;
      while (index < source.length && /[0-9a-zA-Z._xXeE+-]/.test(peek())) {
        index += 1;
        column += 1;
      }
      const raw = source.slice(startIndex, index);
      emit("number", raw, raw, startLine, startColumn, line, column);
      continue;
    }

    if (/[A-Za-z_]/.test(currentChar)) {
      const startLine = currentLine;
      const startColumn = currentColumn;
      const startIndex = index;
      index += 1;
      column += 1;
      while (index < source.length && /[A-Za-z0-9_]/.test(peek())) {
        index += 1;
        column += 1;
      }
      const raw = source.slice(startIndex, index);
      let tokenType = "identifier";
      if (baseKeywords.has(raw) && ((raw === "switch" || raw === "case" || raw === "default" || raw === "continue") ? isContextKeyword(raw, syntax) : true)) {
        tokenType = "keyword";
      }
      if ((raw === "switch" || raw === "case" || raw === "default" || raw === "continue") && !isContextKeyword(raw, syntax)) {
        tokenType = "identifier";
      }
      emit(tokenType, raw, raw, startLine, startColumn, line, column);
      if (raw === "case" && !syntax.switch) {
        appendError("`case` is enabled by extended syntax", startLine, startColumn);
      }
      if (raw === "default" && !syntax.switch) {
        appendError("`default` is enabled by extended syntax", startLine, startColumn);
      }
      if (raw === "continue" && !syntax.continue) {
        appendError("`continue` is enabled by extended syntax", startLine, startColumn);
      }
      continue;
    }

    appendError(`unexpected character ${currentChar}`, currentLine, currentColumn);
    index += 1;
    column += 1;
  }

  if (brackets.length > 0) {
    appendError("expected token to close block", line, column);
  }
  return { tokens, errors };
}

function lineClassification(tokens, code) {
  const first = firstWord(code);
  if (!first) {
    return "other";
  }
  if (first === "end") {
    return "end";
  }
  if (first === "until") {
    return "until";
  }
  if (first === "else") {
    return "else";
  }
  if (first === "elseif") {
    return "elseif";
  }
  if (first === "case") {
    return "case";
  }
  if (first === "default") {
    return "default";
  }
  if (first === "switch") {
    return "switch";
  }
  if (first === "repeat") {
    return "repeat";
  }
  if (lineOpensBlock(tokens)) {
    return "open";
  }
  return "other";
}

function lineOpensBlock(tokens) {
  for (let i = 0; i < tokens.length; i++) {
    const tokenText = tokens[i].text;
    if (tokenText === "then" || tokenText === "function") {
      return true;
    }
    if (tokenText === "do") {
      if (!(tokens.length > 0 && tokens[0].text === "switch")) {
        return true;
      }
    }
  }
  return false;
}

function firstWord(code) {
  const match = code.match(/^[A-Za-z_][A-Za-z0-9_]*/);
  return match ? match[0] : "";
}

function popOne(frames) {
  if (frames.length === 0) {
    return frames;
  }
  return frames.slice(0, -1);
}

function popKind(frames, kind) {
  if (frames.length === 0 || frames[frames.length - 1].kind !== kind) {
    return frames;
  }
  return frames.slice(0, -1);
}

function popUntil(frames, kind) {
  for (let i = frames.length - 1; i >= 0; i--) {
    if (frames[i].kind === kind) {
      return frames.slice(0, i);
    }
  }
  return [];
}

function adjustBeforeLine(frames, kind) {
  if (kind === "end") {
    const withoutCase = popKind(frames, "case");
    return popOne(withoutCase);
  }
  if (kind === "until") {
    return popUntil(frames, "repeat");
  }
  if (kind === "else" || kind === "elseif") {
    return popKind(frames, "normal");
  }
  if (kind === "case" || kind === "default") {
    return popKind(frames, "case");
  }
  return frames;
}

function adjustAfterLine(frames, kind) {
  switch (kind) {
    case "switch":
      return [...frames, { kind: "switch" }];
    case "case":
    case "default":
      return [...frames, { kind: "case" }];
    case "repeat":
      return [...frames, { kind: "repeat" }];
    case "else":
    case "elseif":
      return [...frames, { kind: "normal" }];
    case "open":
      return [...frames, { kind: "normal" }];
    default:
      return frames;
  }
}

function splitLineComment(line) {
  let quote = "";
  let escaped = false;
  for (let i = 0; i < line.length; i++) {
    const value = line[i];
    if (quote) {
      if (escaped) {
        escaped = false;
        continue;
      }
      if (value === "\\") {
        escaped = true;
        continue;
      }
      if (value === quote) {
        quote = "";
      }
      continue;
    }
    if (value === "'" || value === "\"") {
      quote = value;
      continue;
    }
    if (value === "-" && line[i + 1] === "-") {
      return [line.slice(0, i), line.slice(i)];
    }
  }
  return [line, ""];
}

function noSpaceBeforeOrAfter(token) {
  return token === ")" || token === "]" || token === "}" || token === "," || token === ";" || token === ":" || token === ".";
}

function noSpaceAfter(token) {
  return token === "(" || token === "[" || token === "{" || token === "." || token === ":";
}

function needsSpace(previous, current) {
  if (previous.text === "") {
    return false;
  }
  if (noSpaceBeforeOrAfter(current.text) || noSpaceAfter(previous.text)) {
    return false;
  }
  if (current.text === "(" && (previous.type === "identifier" || previous.type === "keyword")) {
    return false;
  }
  if (current.text === "{") {
    return true;
  }
  if (previous.text === "#" ) {
    return false;
  }
  if (previous.text === "," || previous.text === ";") {
    return true;
  }
  return true;
}

function formatCodeLine(code, syntax) {
  if (code.trim() === "") {
    return "";
  }
  const result = tokenizeLine(code, syntax);
  if (result.errors && result.errors.length > 0) {
    return code.trim();
  }
  const tokens = result.tokens;
  if (tokens.length === 0) {
    return code.trim();
  }
  let text = "";
  let previous = { text: "", type: "" };
  for (let index = 0; index < tokens.length; index++) {
    const token = tokens[index];
    if (text.length > 0 && needsSpace(previous, token)) {
      text += " ";
    }
    text += token.raw;
    previous = token;
  }
  return text;
}

function joinCodeAndComment(code, comment) {
  const normalized = comment.trim();
  if (code === "") {
    return normalized;
  }
  if (normalized === "") {
    return code;
  }
  return `${code} ${normalized}`;
}

function formatDocument(text, syntax) {
  const lines = text.split("\n");
  const frames = [];
  const output = [];
  for (let lineIndex = 0; lineIndex < lines.length; lineIndex++) {
    const rawLine = lines[lineIndex];
    if (lineIndex === lines.length - 1 && rawLine === "" && text.endsWith("\n")) {
      continue;
    }
    const line = rawLine.replace(/\r$/, "");
    const trimmed = line.trim();
    if (trimmed === "") {
      output.push("");
      continue;
    }
    const [rawCode, comment] = splitLineComment(trimmed);
    const code = rawCode.trim();
    const scan = tokenizeLine(code, syntax);
    const classification = lineClassification(scan.tokens, code);
    const before = adjustBeforeLine(frames, classification);
    const formattedCode = formatCodeLine(code, syntax);
    const composed = `${"  ".repeat(before.length)}${joinCodeAndComment(formattedCode, comment)}`;
    output.push(composed);
    const after = [...adjustAfterLine(before, classification)];
    frames.length = 0;
    frames.push(...after);
  }
  const result = output.join("\n");
  if (text.endsWith("\n") && !result.endsWith("\n")) {
    return `${result}\n`;
  }
  return result;
}

function fullDocumentRange(text) {
  const lines = text.split("\n");
  if (lines.length === 0) {
    return {
      start: makePosition(0, 0),
      end: makePosition(0, 0),
    };
  }
  const lastLine = lines.length - 1;
  const lastColumn = lines[lastLine].length;
  return {
    start: makePosition(0, 0),
    end: makePosition(lastLine, lastColumn),
  };
}

function isPositionInRange(position, range) {
  if (position.line !== range.start.line) {
    return false;
  }
  return position.character >= range.start.character && position.character < range.end.character;
}

function isRangeBeforeOrEqual(range, position) {
  if (range.start.line < position.line) {
    return true;
  }
  if (range.start.line === position.line && range.start.character <= position.character) {
    return true;
  }
  return false;
}

function identifierAtPosition(text, position, syntax) {
  const result = scanTokens(text, syntax);
  for (const token of result.tokens) {
    if (token.type !== "identifier" && token.type !== "keyword") {
      continue;
    }
    if (isPositionInRange(position, token.range)) {
      return token.text;
    }
  }
  return "";
}

function findTokenIndexAtPosition(tokens, position) {
  for (let i = 0; i < tokens.length; i++) {
    if (isPositionInRange(position, tokens[i].range)) {
      return i;
    }
  }
  return -1;
}

function findTokenIndexAtOrBeforePosition(tokens, position) {
  let index = -1;
  for (let i = 0; i < tokens.length; i++) {
    const token = tokens[i];
    if (token.range.end.line < position.line) {
      index = i;
      continue;
    }
    if (token.range.end.line > position.line) {
      break;
    }
    if (token.range.end.character <= position.character) {
      index = i;
      continue;
    }
    break;
  }
  return index;
}

function extractCompletionContext(text, tokens, position) {
  const lineText = (text.split("\n")[position.line] || "").slice(0, position.character);
  const atCursor = findTokenIndexAtPosition(tokens, position);

  if (atCursor >= 0) {
    const cursorToken = tokens[atCursor];
    if (cursorToken.type === "identifier" || cursorToken.type === "keyword") {
      const before = atCursor - 1 >= 0 ? tokens[atCursor - 1] : null;
      if (before && (before.text === "." || before.text === ":")) {
        const maybeModule = atCursor - 2 >= 0 ? tokens[atCursor - 2] : null;
        if (maybeModule && (maybeModule.type === "identifier" || maybeModule.type === "keyword")) {
          return {
            mode: "method",
            module: maybeModule.text,
            prefix: cursorToken.text,
            range: makeRange(position.line, cursorToken.startColumn, position.line, cursorToken.range.end.character),
          };
        }
      }
      return {
        mode: "global",
        prefix: cursorToken.text,
        range: makeRange(position.line, cursorToken.startColumn, position.line, cursorToken.range.end.character),
      };
    }
  }

  const before = findTokenIndexAtOrBeforePosition(tokens, position);
  if (before < 0) {
    return {
      mode: "global",
      prefix: "",
      range: makeRange(position.line, position.character, position.line, position.character),
    };
  }

  const beforeToken = tokens[before];
  const trimmedLine = lineText.trimEnd();
  if (trimmedLine.endsWith(".") || trimmedLine.endsWith(":")) {
    const tokenIsSeparator = beforeToken && (beforeToken.text === "." || beforeToken.text === ":");
    const moduleCandidate = tokenIsSeparator ? tokens[before - 1] : beforeToken;
    const moduleName = moduleCandidate && (moduleCandidate.type === "identifier" || moduleCandidate.type === "keyword")
      ? moduleCandidate.text
      : "";
    const useMethodMode = moduleName !== "";
    return {
      mode: useMethodMode ? "method" : "global",
      module: useMethodMode ? moduleName : "",
      prefix: "",
      range: makeRange(position.line, position.character, position.line, position.character),
    };
  }

  return {
    mode: "global",
    prefix: "",
    range: makeRange(position.line, position.character, position.line, position.character),
  };
}

function buildCompletionCandidates(context) {
  const items = [];
  const seen = new Set();
  const names = builtinFunctionNames();

  for (const name of names) {
    const [moduleName, methodName] = name.split(".");

    if (context.mode === "method") {
      if (!methodName || context.module !== moduleName) {
        continue;
      }
      if (!methodName.startsWith(context.prefix)) {
        continue;
      }
      if (seen.has(methodName)) {
        continue;
      }
      const builtin = getBuiltinFunction(name);
      if (!builtin) {
        continue;
      }
      items.push({
        name: methodName,
        fullName: name,
        detail: builtin.signature || methodName,
        documentation: formatBuiltinMarkdown(name, builtin),
      });
      seen.add(methodName);
      continue;
    }

    if (!methodName && name.startsWith(context.prefix) && !seen.has(name)) {
      const builtin = getBuiltinFunction(name);
      if (!builtin) {
        continue;
      }
      items.push({
        name,
        fullName: name,
        detail: builtin.signature || name,
        documentation: formatBuiltinMarkdown(name, builtin),
      });
      seen.add(name);
    }
  }

  return items;
}

function isNameToken(token) {
  return token && (token.type === "identifier" || token.type === "keyword");
}

function resolveBuiltinTarget(tokens, position) {
  const index = findTokenIndexAtPosition(tokens, position);
  if (index < 0) {
    return "";
  }

  const token = tokens[index];
  if (!isNameToken(token)) {
    return "";
  }

  const candidateWithSeparator = (separator) => {
    if (index > 1 && tokens[index - 1].text === separator && isNameToken(tokens[index - 2])) {
      const qualified = `${tokens[index - 2].text}.${token.text}`;
      if (getBuiltinFunction(qualified)) {
        return qualified;
      }
    }

    if (index + 2 < tokens.length && tokens[index + 1].text === separator && isNameToken(tokens[index + 2])) {
      const qualified = `${token.text}.${tokens[index + 2].text}`;
      if (getBuiltinFunction(qualified)) {
        return qualified;
      }
    }

    return "";
  };

  const dotted = candidateWithSeparator(".") || candidateWithSeparator(":");
  if (dotted) {
    return dotted;
  }

  if (index > 1 && isNameToken(tokens[index - 2]) && tokens[index - 1].text === ":") {
    const receiverHint = tokens[index - 2].text;
    const byMethod = getBuiltinFunctionByMethod(token.text, receiverHint) || getBuiltinFunctionByMethod(token.text);
    if (byMethod) {
      return byMethod;
    }
  }

  return token.text;
}

function isDefinitionAt(tokens, index, name) {
  if (index < 0 || index >= tokens.length) {
    return false;
  }
  if (tokens[index].text !== name) {
    return false;
  }
  if (index > 0 && tokens[index - 1].text === "local") {
    return true;
  }
  if (index > 0 && tokens[index - 1].text === "function") {
    return true;
  }
  if (index > 1 && tokens[index - 2].text === "local" && tokens[index - 1].text === "function") {
    return true;
  }
  if (index > 0 && tokens[index - 1].text === ":") {
    return true;
  }
  if (index > 0 && index + 1 < tokens.length && tokens[index - 1].text === "::" && tokens[index + 1].text === "::") {
    return true;
  }
  return false;
}

function findDefinition(text, targetName, position, syntax) {
  const result = scanTokens(text, syntax);
  const tokens = result.tokens;
  let best = null;
  for (let i = 0; i < tokens.length; i++) {
    if (!isRangeBeforeOrEqual(tokens[i].range, position)) {
      continue;
    }
    if (isDefinitionAt(tokens, i, targetName)) {
      best = tokens[i].range;
    }
  }
  if (best) {
    return best;
  }
  for (let i = 0; i < tokens.length; i++) {
    if (isDefinitionAt(tokens, i, targetName)) {
      return tokens[i].range;
    }
  }
  return null;
}

function isContextToken(text) {
  return text === "switch" || text === "case" || text === "default" || text === "continue";
}

function semanticTokenTypeForToken(tokens, index, syntax) {
  const token = tokens[index];
  if (token.type === "keyword" || (isContextToken(token.text) && isContextKeyword(token.text, syntax))) {
    return semanticTypeKeyword;
  }
  if (token.type === "string") {
    return semanticTypeString;
  }
  if (token.type === "number") {
    return semanticTypeNumber;
  }
  if (token.type === "operator") {
    return semanticTypeOperator;
  }
  if (token.type !== "identifier") {
    return null;
  }
  // Function-like identifiers are colored by the TextMate grammar. Returning
  // semantic function/method tokens here lets some VS Code themes repaint them
  // as plain text after the grammar highlight has already appeared.
  return null;
}

function generateSemanticTokens(text, syntax) {
  return { data: [] };
}

function collectParseLikeErrors(text, syntax) {
  const scanned = scanTokens(text, syntax);
  const diagnostics = [];
  for (const error of scanned.errors) {
    const message = error.message ? `syntax error near ${error.near}: ${error.message}` : "syntax error";
    diagnostics.push({
      range: error.range,
      severity: DiagnosticSeverity.Error,
      source: "glua",
      message,
    });
  }

  const blockStack = [];
  const tokens = scanned.tokens;
  for (let i = 0; i < tokens.length; i++) {
    const token = tokens[i];
    if (token.type !== "keyword") {
      continue;
    }
    const value = token.text;
    if (value === "switch") {
      blockStack.push("switch");
      continue;
    }
    if (value === "repeat") {
      blockStack.push("repeat");
      continue;
    }
    if (value === "if" || value === "while" || value === "for" || value === "function" || value === "do") {
      if (value === "do") {
        const prev = i > 0 ? tokens[i - 1] : null;
        if (!prev || prev.text === "then" || prev.text === "end") {
          blockStack.push(value);
        }
      } else {
        blockStack.push(value);
      }
      continue;
    }
    if (value === "case" || value === "default") {
      const isLineStart = i === 0 || tokens[i - 1].range.start.line !== token.range.start.line;
      const isInsideSwitch = blockStack.includes("switch");
      if (isInsideSwitch && isLineStart) {
        continue;
      }
      diagnostics.push({
        range: token.range,
        severity: DiagnosticSeverity.Error,
        source: "glua",
        message: `syntax error near '${value}'`,
      });
    }
    if (value === "end") {
      if (blockStack.length === 0) {
        diagnostics.push({
          range: token.range,
          severity: DiagnosticSeverity.Error,
          source: "glua",
          message: "syntax error near 'end'",
        });
      } else {
        blockStack.pop();
      }
    }
    if (value === "until") {
      if (blockStack[blockStack.length - 1] === "repeat") {
        blockStack.pop();
      } else {
        diagnostics.push({
          range: token.range,
          severity: DiagnosticSeverity.Error,
          source: "glua",
          message: "syntax error near 'until'",
        });
      }
    }
  }

  if (blockStack.length > 0) {
    const eofLine = scanned.tokens.length > 0 ? scanned.tokens[scanned.tokens.length - 1].range.end.line : 0;
    const eofColumn = scanned.tokens.length > 0 ? scanned.tokens[scanned.tokens.length - 1].range.end.character : 0;
    diagnostics.push({
      range: makeRange(eofLine, Math.max(eofColumn, 0), eofLine, Math.max(eofColumn, 0) + 1),
      severity: DiagnosticSeverity.Error,
      source: "glua",
      message: "syntax error near <eof>",
    });
  }

  return diagnostics;
}

const connection = createConnection(ProposedFeatures.all);
const documents = new TextDocuments(TextDocument);

let syntax = createDefaultSyntax();
let builtinExtensionOptions = {
  locale: DEFAULT_DOC_LOCALE,
  resolvedLocale: "en",
  builtinExtensions: [],
};

function parsePositionOffset(text, position) {
  const lines = text.split("\n");
  if (position.line >= lines.length) {
    return { line: Math.max(lines.length - 1, 0), character: 0 };
  }
  const line = lines[position.line] || "";
  const charPos = Math.min(Math.max(position.character, 0), line.length);
  return { line: position.line, character: charPos };
}

function effectiveBuiltinLocale(rawLocale, resolvedLocale) {
  const raw = rawLocale === undefined || rawLocale === null ? "" : String(rawLocale);
  if (!raw || raw.toLowerCase() === DEFAULT_DOC_LOCALE) {
    return resolvedLocale || builtinExtensionOptions.resolvedLocale || "en";
  }
  return raw;
}

connection.onInitialize((params) => {
  if (params.initializationOptions && params.initializationOptions.syntax) {
    syntax = parseSyntaxValue(params.initializationOptions.syntax);
  }
  if (params.initializationOptions) {
    builtinExtensionOptions = {
      locale: params.initializationOptions.locale || builtinExtensionOptions.locale,
      resolvedLocale: params.initializationOptions.resolvedLocale || params.initializationOptions.locale || builtinExtensionOptions.resolvedLocale,
      builtinExtensions: Array.isArray(params.initializationOptions.builtinExtensions)
        ? params.initializationOptions.builtinExtensions
        : builtinExtensionOptions.builtinExtensions,
    };
    const builtinOptions = applyBuiltinExtensionOptions({
      locale: effectiveBuiltinLocale(builtinExtensionOptions.locale, builtinExtensionOptions.resolvedLocale),
      builtinExtensions: builtinExtensionOptions.builtinExtensions,
    });
    connection.console.log(`[builtin-docs] requested locale=${builtinOptions.requestedLocale}; resolved locale=${builtinOptions.resolvedLocale}; extension files=${builtinOptions.extensionCount}`);
    baseBuiltinFunctions.clear();
    for (const builtinName of builtinFunctionNames()) {
      baseBuiltinFunctions.add(builtinName);
    }
  }
  return {
    capabilities: {
      textDocumentSync: {
        openClose: true,
        change: TextDocumentSyncKind.Full,
      },
      completionProvider: {
        triggerCharacters: [".", ":"],
      },
      documentFormattingProvider: true,
      definitionProvider: true,
      hoverProvider: true,
    },
    serverInfo: {
      name: "gluals-js",
      version: "0.1.0",
    },
  };
});

connection.onInitialized(() => {
  connection.console.log("glua language server initialized");
});

connection.onDidChangeConfiguration((params) => {
  const cfg = params.settings && params.settings.glua ? params.settings.glua : {};
  const hasDocLanguage = cfg.docLanguage !== undefined || cfg.doclanguage !== undefined;
  const hasBuiltinDocs = cfg.builtinDocs !== undefined;
  if (!hasDocLanguage && !hasBuiltinDocs) {
    connection.console.log(`[builtin-docs] configuration changed without glua settings; keep resolved locale=${getBuiltinLocale()}`);
    return;
  }
  builtinExtensionOptions = {
    locale: hasDocLanguage ? (cfg.docLanguage || cfg.doclanguage) : builtinExtensionOptions.locale,
    resolvedLocale: cfg.resolvedDocLanguage || builtinExtensionOptions.resolvedLocale,
    builtinExtensions: hasBuiltinDocs && Array.isArray(cfg.builtinDocs)
      ? cfg.builtinDocs
      : builtinExtensionOptions.builtinExtensions,
  };
  const builtinOptions = applyBuiltinExtensionOptions({
    locale: effectiveBuiltinLocale(builtinExtensionOptions.locale, builtinExtensionOptions.resolvedLocale),
    builtinExtensions: builtinExtensionOptions.builtinExtensions,
  });
  connection.console.log(`[builtin-docs] configuration changed; requested locale=${builtinOptions.requestedLocale}; resolved locale=${builtinOptions.resolvedLocale}; extension files=${builtinOptions.extensionCount}`);
  baseBuiltinFunctions.clear();
  for (const builtinName of builtinFunctionNames()) {
    baseBuiltinFunctions.add(builtinName);
  }
});

documents.onDidOpen((change) => {
  const document = change.document;
  const diagnostics = collectParseLikeErrors(document.getText(), syntax);
  connection.sendDiagnostics({ uri: document.uri, diagnostics });
});

documents.onDidChangeContent((change) => {
  const document = change.document;
  const diagnostics = collectParseLikeErrors(document.getText(), syntax);
  connection.sendDiagnostics({ uri: document.uri, diagnostics });
});

documents.onDidClose((change) => {
  connection.sendDiagnostics({ uri: change.document.uri, diagnostics: [] });
});

connection.onDefinition((params) => {
  const doc = documents.get(params.textDocument.uri);
  if (!doc) {
    return null;
  }
  const text = doc.getText();
  const target = resolveBuiltinTarget(scanTokens(text, syntax).tokens, parsePositionOffset(text, params.position));
  if (!target) {
    return null;
  }
  const definition = findDefinition(text, target, parsePositionOffset(text, params.position), syntax);
  if (!definition) {
    const builtin = getBuiltinFunction(target);
    if (builtin) {
      return [{
        uri: makeBuiltinUri(target),
        range: makeRange(0, 0, 0, 1),
      }];
    }
    return null;
  }
  return [{ uri: params.textDocument.uri, range: definition }];
});

connection.onCompletion((params) => {
  const doc = documents.get(params.textDocument.uri);
  if (!doc) {
    return [];
  }
  const text = doc.getText();
  const tokens = scanTokens(text, syntax).tokens;
  const position = parsePositionOffset(text, params.position);
  const context = extractCompletionContext(text, tokens, position);
  const candidates = buildCompletionCandidates(context);
  return candidates.map((item) => ({
    label: item.name,
    kind: CompletionItemKind.Function,
    detail: item.detail,
    documentation: {
      kind: "markdown",
      value: item.documentation,
    },
    textEdit: {
      range: context.range,
      newText: item.name,
    },
  }));
});

connection.onHover((params) => {
  const doc = documents.get(params.textDocument.uri);
  if (!doc) {
    return null;
  }
  const text = doc.getText();
  const target = resolveBuiltinTarget(scanTokens(text, syntax).tokens, parsePositionOffset(text, params.position));
  if (!target) {
    return null;
  }
  const definition = findDefinition(text, target, parsePositionOffset(text, params.position), syntax);
  if (!definition) {
    const builtin = getBuiltinFunction(target);
    if (builtin) {
      connection.console.log(`[hover] target=${target}; locale=${builtin._locale}; description=${builtin.description}`);
      return {
        contents: {
          kind: "markdown",
          value: formatBuiltinMarkdown(target, builtin),
        },
      };
    }
    return {
      contents: {
        kind: "markdown",
        value: `\`${target}\``,
      },
    };
  }
  return {
    contents: {
      kind: "markdown",
      value: `\`${target}\`\n\nDefined at line ${definition.start.line + 1}, column ${definition.start.character + 1}.`,
    },
    range: definition,
  };
});

connection.onDocumentFormatting((params) => {
  const doc = documents.get(params.textDocument.uri);
  if (!doc) {
    return [];
  }
  const text = doc.getText();
  const formatted = formatDocument(text, syntax);
  return [{
    range: fullDocumentRange(text),
    newText: formatted,
  }];
});

connection.onRequest("textDocument/semanticTokens/full", (params) => {
  const doc = documents.get(params.textDocument.uri);
  if (!doc) {
    return { data: [] };
  }
  const result = generateSemanticTokens(doc.getText(), syntax);
  return result;
});

documents.listen(connection);
connection.listen();

try {
  const cliSyntax = parseSyntaxFromArgs(process.argv.slice(2));
  if (cliSyntax) {
    syntax = cliSyntax;
  }
} catch (error) {
  connection.console.error(String(error));
}

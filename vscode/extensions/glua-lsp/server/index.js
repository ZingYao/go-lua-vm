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
  InsertTextFormat,
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

const standardLibraries = new Set(["string", "math", "table", "io", "os", "coroutine", "debug", "utf8", "package"]);
const nativeRequireModules = new Set(["cjson", "cjson.safe", "lpeg", "socket.core", "mime.core"]);
const valueReturnTypes = new Map([
  ["io.open", "file"],
  ["io.popen", "file"],
  ["io.tmpfile", "file"],
  ["io.input", "file"],
  ["io.output", "file"],
  ["io.stdin", "file"],
  ["io.stdout", "file"],
  ["io.stderr", "file"],
  ["file.write", "file"],
]);
const typeMethods = new Map([
  ["file", new Set(["close", "flush", "lines", "read", "seek", "setvbuf", "write"])],
  ["table", new Set(["concat", "insert", "move", "pack", "remove", "sort", "unpack"])],
  ["string", new Set(["byte", "char", "dump", "find", "format", "gmatch", "gsub", "len", "lower", "match", "pack", "packsize", "rep", "reverse", "sub", "unpack", "upper"])],
  ["math", new Set(["abs", "acos", "asin", "atan", "ceil", "cos", "deg", "exp", "floor", "fmod", "log", "max", "min", "modf", "rad", "random", "randomseed", "sin", "sqrt", "tan", "tointeger", "type", "ult"])],
  ["io", new Set(["close", "flush", "input", "lines", "open", "output", "popen", "read", "tmpfile", "type", "write"])],
  ["os", new Set(["clock", "date", "difftime", "execute", "exit", "getenv", "remove", "rename", "setlocale", "time", "tmpname"])],
  ["coroutine", new Set(["create", "resume", "running", "status", "wrap", "yield"])],
  ["debug", new Set(["debug", "gethook", "getinfo", "getlocal", "getmetatable", "getregistry", "getupvalue", "getuservalue", "sethook", "setlocal", "setmetatable", "setupvalue", "setuservalue", "traceback", "upvalueid", "upvaluejoin"])],
  ["utf8", new Set(["char", "codes", "codepoint", "len", "offset"])],
  ["package", new Set(["loadlib", "searchpath"])],
]);

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
  let opens = 0;
  let closes = 0;
  for (let i = 0; i < tokens.length; i++) {
    const tokenText = tokens[i].text;
    if (tokenText === "then" || tokenText === "function") {
      opens++;
      continue;
    }
    if (tokenText === "do") {
      if (!(tokens.length > 0 && tokens[0].text === "switch")) {
        opens++;
      }
      continue;
    }
    if (tokenText === "end" || tokenText === "until") {
      closes++;
    }
  }
  return opens > closes;
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

function longBracketCloseText(line, openIndex) {
  if (line[openIndex] !== "[") {
    return "";
  }
  let index = openIndex + 1;
  while (line[index] === "=") {
    index++;
  }
  return line[index] === "[" ? `]${"=".repeat(index - openIndex - 1)}]` : "";
}

function splitLineComment(line, state) {
  if (state.longCommentClose) {
    if (line.includes(state.longCommentClose)) {
      state.longCommentClose = "";
    }
    return ["", line];
  }
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
      const closeText = longBracketCloseText(line, i + 2);
      if (closeText && !line.slice(i + 2).includes(closeText)) {
        state.longCommentClose = closeText;
      }
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
  const commentState = { longCommentClose: "" };
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
    const [rawCode, comment] = splitLineComment(trimmed, commentState);
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
          const moduleName = completionModule(tokens, atCursor - 1, atCursor - 2, position);
          return {
            mode: "method",
            module: moduleName,
            receiver: maybeModule.text,
            separator: tokens[atCursor - 1].text,
            prefix: cursorToken.text,
            range: makeRange(position.line, cursorToken.startColumn, position.line, cursorToken.range.end.character),
          };
        }
      }
      return {
        mode: keywordCompletionMode(tokens, atCursor) ? "keyword" : "global",
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
    const moduleIndex = tokenIsSeparator ? before - 1 : before;
    const moduleCandidate = tokens[moduleIndex];
    const moduleName = moduleCandidate && (moduleCandidate.type === "identifier" || moduleCandidate.type === "keyword")
      ? completionModule(tokens, before, moduleIndex, position)
      : "";
    const useMethodMode = moduleName !== "";
    return {
      mode: useMethodMode ? "method" : "global",
      module: useMethodMode ? moduleName : "",
      receiver: useMethodMode && moduleCandidate ? moduleCandidate.text : "",
      separator: tokenIsSeparator ? beforeToken.text : "",
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

function keywordCompletionMode(tokens, tokenIndex) {
  if (tokenIndex < 0 || !tokens[tokenIndex] || !tokens[tokenIndex].text) {
    return false;
  }
  for (let cursor = tokenIndex - 1; cursor >= 0; cursor--) {
    const token = tokens[cursor];
    if (!token || token.type === "space" || token.type === "comment") {
      continue;
    }
    if (token.text === "for" || token.text === "while" || token.text === "if") {
      return true;
    }
    if (token.text === "do" || token.text === "then" || token.text === "end" || token.text === ";") {
      return false;
    }
  }
  return false;
}

function completionModule(tokens, separatorIndex, receiverIndex, position) {
  const separator = tokens[separatorIndex];
  if (separator && separator.text === ":") {
    const inferred = inferredReceiverType(tokens, receiverIndex, position);
    if (inferred) {
      return inferred;
    }
  }
  return tokens[receiverIndex] ? tokens[receiverIndex].text : "";
}

function inferredReceiverType(tokens, receiverIndex, position) {
  const receiver = tokens[receiverIndex];
  if (!isNameToken(receiver)) {
    return "";
  }
  let inferred = "";
  for (let index = 0; index < receiverIndex; index++) {
    const token = tokens[index];
    if (!token || token.text !== receiver.text || !isRangeBeforeOrEqual(token.range, position)) {
      continue;
    }
    const candidate = inferredTypeFromAssignment(tokens, index, position);
    if (candidate) {
      inferred = candidate;
    }
  }
  return inferred;
}

function inferredTypeFromAssignment(tokens, variableIndex, position) {
  const equals = tokens[variableIndex + 1];
  const moduleToken = tokens[variableIndex + 2];
  const separator = tokens[variableIndex + 3];
  const member = tokens[variableIndex + 4];
  if (!equals || equals.text !== "=" || !isNameToken(moduleToken) || !separator || separator.text !== "." || !isNameToken(member)) {
    if (equals && equals.text === "=" && moduleToken && moduleToken.text === "{") {
      return "table";
    }
    if (equals && equals.text === "=" && moduleToken && moduleToken.type === "string") {
      return "string";
    }
    return "";
  }
  if (!isRangeBeforeOrEqual(member.range, position)) {
    return "";
  }
  return valueReturnTypes.get(`${moduleToken.text}.${member.text}`) || "";
}

function collectTypedMethodDiagnostics(tokens) {
  const diagnostics = [];
  for (let index = 2; index + 1 < tokens.length; index++) {
    const receiver = tokens[index - 2];
    const separator = tokens[index - 1];
    const method = tokens[index];
    const call = tokens[index + 1];
    if (!receiver || !separator || !method || !call || separator.text !== ":" || !isNameToken(receiver) || !isNameToken(method) || call.text !== "(") {
      continue;
    }
    const beforeReceiver = previousVisibleToken(tokens, index - 2);
    if (beforeReceiver && beforeReceiver.text === "function") {
      continue;
    }
    const receiverType = inferredReceiverType(tokens, index - 2, method.range.start);
    if (!receiverType) {
      continue;
    }
    const methods = typeMethods.get(receiverType);
    if (!methods || methods.has(method.text)) {
      continue;
    }
    diagnostics.push({
      range: method.range,
      severity: DiagnosticSeverity.Error,
      source: "glua",
      message: `type '${receiverType}' has no method '${method.text}'`,
    });
  }
  return diagnostics;
}

function previousVisibleToken(tokens, index) {
  const previous = previousVisibleIndex(tokens, index);
  return previous >= 0 ? tokens[previous] : null;
}

function previousVisibleIndex(tokens, index) {
  for (let cursor = index - 1; cursor >= 0; cursor--) {
    const current = tokens[cursor];
    if (current && current.type !== "space" && current.type !== "comment") {
      return cursor;
    }
  }
  return -1;
}

function nextVisibleIndex(tokens, index) {
  for (let cursor = index + 1; cursor < tokens.length; cursor++) {
    const current = tokens[cursor];
    if (current && current.type !== "space" && current.type !== "comment") {
      return cursor;
    }
  }
  return -1;
}

function buildSymbolSnapshot(tokens) {
  const snapshot = {
    declared: new Set([
      "_G",
      "_VERSION",
      "_ENV",
      "false",
      "nil",
      "true",
      ...standardLibraries,
      ...Array.from(baseBuiltinFunctions).filter((name) => !name.includes(".")),
    ]),
    definitions: new Map(),
    userSymbols: new Map(),
  };
  for (let index = 0; index < tokens.length; index++) {
    const token = tokens[index];
    if (!token || token.type !== "keyword") {
      continue;
    }
    if (token.text === "local") {
      const next = tokens[index + 1];
      if (next && next.text === "function" && isNameToken(tokens[index + 2])) {
        addSymbolToken(snapshot, tokens[index + 2]);
        collectFunctionParameters(tokens, index + 2, snapshot);
        continue;
      }
      for (let cursor = index + 1; cursor < tokens.length; cursor++) {
        const current = tokens[cursor];
        if (!current || current.text === "=" || current.text === "do" || current.range.start.line !== token.range.start.line) {
          break;
        }
        addSymbolToken(snapshot, current);
      }
      continue;
    }
    if (token.text === "function" && isNameToken(tokens[index + 1])) {
      addSymbolToken(snapshot, tokens[index + 1]);
      collectFunctionParameters(tokens, index + 1, snapshot);
      continue;
    }
    if (token.text === "function" && tokens[index + 1] && tokens[index + 1].text === "(") {
      collectFunctionExpressionParameters(tokens, index, snapshot);
      continue;
    }
    if (token.text === "for") {
      for (let cursor = index + 1; cursor < tokens.length; cursor++) {
        const current = tokens[cursor];
        if (!current || current.text === "in" || current.text === "=" || current.text === "do") {
          break;
        }
        addSymbolToken(snapshot, current);
      }
    }
  }
  collectAssignmentTargets(tokens, snapshot);
  return snapshot;
}

function addSymbolToken(snapshot, token) {
  if (!isNameToken(token) || token.type === "keyword") {
    return;
  }
  snapshot.declared.add(token.text);
  if (!snapshot.userSymbols.has(token.text)) {
    snapshot.userSymbols.set(token.text, token.range);
  }
  if (!snapshot.definitions.has(token.text)) {
    snapshot.definitions.set(token.text, []);
  }
  snapshot.definitions.get(token.text).push(token.range);
}

function collectAssignmentTargets(tokens, snapshot) {
  for (let index = 0; index < tokens.length; index++) {
    const token = tokens[index];
    if (!token || token.text !== "=") {
      continue;
    }
    if (delimiterDepthBefore(tokens, index) !== 0) {
      continue;
    }
    const statementStart = assignmentStatementStart(tokens, index);
    collectSimpleAssignmentTargets(tokens, statementStart, index, snapshot);
  }
}

function collectSimpleAssignmentTargets(tokens, statementStart, equalsIndex, snapshot) {
  let segmentStart = statementStart;
  let depth = 0;
  for (let cursor = statementStart; cursor <= equalsIndex; cursor++) {
    const current = tokens[cursor];
    if (!current) {
      continue;
    }
    if (cursor === equalsIndex || (current.text === "," && depth === 0)) {
      addSimpleAssignmentTarget(tokens.slice(segmentStart, cursor), snapshot);
      segmentStart = cursor + 1;
      continue;
    }
    if (isOpenDelimiter(current.text)) {
      depth++;
      continue;
    }
    if (isCloseDelimiter(current.text) && depth > 0) {
      depth--;
    }
  }
}

function addSimpleAssignmentTarget(segment, snapshot) {
  const visible = segment.filter((token) => token && !(token.type === "keyword" && token.text === "local"));
  if (visible.length !== 1 || !isNameToken(visible[0]) || visible[0].type === "keyword") {
    return;
  }
  addSymbolToken(snapshot, visible[0]);
}

function assignmentStatementStart(tokens, equalsIndex) {
  const equalsToken = tokens[equalsIndex];
  if (!equalsToken) {
    return equalsIndex;
  }
  for (let cursor = equalsIndex - 1; cursor >= 0; cursor--) {
    const current = tokens[cursor];
    if (!current || current.range.start.line !== equalsToken.range.start.line || current.text === ";") {
      return cursor + 1;
    }
  }
  return 0;
}

function delimiterDepthBefore(tokens, tokenIndex) {
  let depth = 0;
  for (let cursor = 0; cursor < tokenIndex; cursor++) {
    const current = tokens[cursor];
    if (!current) {
      continue;
    }
    if (isOpenDelimiter(current.text)) {
      depth++;
      continue;
    }
    if (isCloseDelimiter(current.text) && depth > 0) {
      depth--;
    }
  }
  return depth;
}

function isOpenDelimiter(text) {
  return text === "(" || text === "{" || text === "[";
}

function isCloseDelimiter(text) {
  return text === ")" || text === "}" || text === "]";
}


function collectFunctionParameters(tokens, functionNameIndex, snapshot) {
  let openIndex = -1;
  for (let cursor = functionNameIndex + 1; cursor < tokens.length; cursor++) {
    if (tokens[cursor].text === "(") {
      openIndex = cursor;
      break;
    }
    if (tokens[cursor].range.start.line !== tokens[functionNameIndex].range.start.line) {
      return;
    }
  }
  if (openIndex < 0) {
    return;
  }
  for (let cursor = openIndex + 1; cursor < tokens.length; cursor++) {
    const current = tokens[cursor];
    if (current.text === ")") {
      return;
    }
    addSymbolToken(snapshot, current);
  }
}

function collectFunctionExpressionParameters(tokens, functionIndex, snapshot) {
  const openIndex = functionIndex + 1;
  if (!tokens[openIndex] || tokens[openIndex].text !== "(") {
    return;
  }
  for (let cursor = openIndex + 1; cursor < tokens.length; cursor++) {
    const current = tokens[cursor];
    if (current.text === ")") {
      return;
    }
    addSymbolToken(snapshot, current);
  }
}

function collectUndeclaredIdentifierDiagnostics(tokens, snapshot = buildSymbolSnapshot(tokens)) {
  const declared = snapshot.declared;
  const diagnostics = [];
  const reported = new Set();
  for (let index = 0; index < tokens.length; index++) {
    const token = tokens[index];
    if (!token || token.type !== "identifier" || declared.has(token.text)) {
      continue;
    }
    const previous = index > 0 ? tokens[index - 1] : null;
    const next = index + 1 < tokens.length ? tokens[index + 1] : null;
    if (previous && (previous.text === "." || previous.text === ":" || previous.text === "function")) {
      continue;
    }
    if (next && (next.text === "=" || next.text === "." || next.text === ":")) {
      continue;
    }
    const key = `${token.text}:${token.range.start.line}:${token.range.start.character}`;
    if (reported.has(key)) {
      continue;
    }
    reported.add(key);
    diagnostics.push({
      range: token.range,
      severity: DiagnosticSeverity.Error,
      source: "glua",
      message: `undefined identifier '${token.text}'`,
    });
  }
  return diagnostics;
}

function buildCompletionCandidates(context, snapshot, tokens, documentUri) {
  const items = [];
  const seen = new Set();
  if (context.mode === "keyword") {
    return keywordCompletionCandidates(context);
  }
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
        snippet: completionSnippet(methodName, builtin.signature || methodName),
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
        snippet: completionSnippet(name, builtin.signature || name),
      });
      seen.add(name);
    }
  }

  if (context.mode === "method" && context.receiver && tokens && documentUri) {
    const requireBindings = localRequireBindings(tokens, documentUri);
    const moduleFile = requireBindings.get(context.receiver);
    if (moduleFile) {
      for (const member of moduleExportSnapshot(moduleFile, context.receiver).members) {
        if (!member.name.startsWith(context.prefix) || seen.has(member.name)) {
          continue;
        }
        if (context.separator === ":" && member.callStyle !== ":") {
          continue;
        }
        items.push({
          name: member.name,
          kind: CompletionItemKind.Function,
          detail: member.detail,
          documentation: member.documentation || `Exported from ${path.basename(moduleFile)}.`,
          snippet: completionSnippet(member.name, member.signature),
        });
        seen.add(member.name);
      }
    }
  }

  if (context.mode === "global" && snapshot) {
    for (const [name] of snapshot.userSymbols) {
      if (!name.startsWith(context.prefix) || seen.has(name)) {
        continue;
      }
      items.push({
        name,
        kind: CompletionItemKind.Variable,
        detail: "GLua file symbol",
        documentation: "Declared in the current Lua file.",
      });
      seen.add(name);
    }
  }

  if (context.mode === "global") {
    items.push(...buildDocSnippetCandidates(context));
  }

  return items;
}

function completionSnippet(name, signature) {
  const params = signatureParameters(signature);
  if (params.length === 0) {
    return `${snippetEscape(name)}()`;
  }
  const placeholders = params.map((param, index) => `\${${index + 1}:${snippetEscape(param)}}`);
  return `${snippetEscape(name)}(${placeholders.join(", ")})`;
}

function signatureParameters(signature) {
  const match = String(signature || "").match(/\((.*)\)/);
  if (!match) {
    return [];
  }
  return match[1]
    .split(",")
    .map((param) => param.trim())
    .filter(Boolean)
    .map((param) => param.replace(/^\[|\]$/g, "").replace(/\s*=.*$/, "").trim())
    .filter(Boolean);
}

function snippetEscape(value) {
  return String(value || "").replace(/\\/g, "\\\\").replace(/\$/g, "\\$").replace(/}/g, "\\}");
}

function keywordCompletionCandidates(context) {
  const keywords = ["do", "then", "end"];
  return keywords
    .filter((keyword) => keyword.startsWith(context.prefix || ""))
    .map((keyword) => ({
      name: keyword,
      kind: CompletionItemKind.Keyword,
      detail: "Lua keyword",
      documentation: `Insert \`${keyword}\`.`,
    }));
}

function buildDocSnippetCandidates(context) {
  const prefix = String(context.prefix || "").toLowerCase();
  if (prefix && !["doc", "docs", "func", "function", "glua"].some((item) => item.startsWith(prefix) || prefix.startsWith(item))) {
    return [];
  }
  return [
    {
      name: "glua doc comment",
      kind: CompletionItemKind.Snippet,
      detail: "GLua JSON-compatible function annotation",
      documentation: "Insert a standard GLua annotation block that can be parsed into builtin-functions JSON shape.",
      snippet: [
        "-- description: ${1:function description}",
        "-- param: ${2:name} ${3:string} ${4:parameter description}",
        "-- return: ${5:nil}",
        "-- example:",
        "--   ${6:module.function(${2:name})}",
        "-- output:",
        "--   ${7:expected output}",
      ].join("\n"),
    },
    {
      name: "glua documented function",
      kind: CompletionItemKind.Snippet,
      detail: "GLua annotation + function assignment",
      documentation: "Insert a documented table function assignment.",
      snippet: [
        "-- description: ${1:function description}",
        "-- param: ${4:name} ${5:string} ${6:parameter description}",
        "-- return: ${7:nil}",
        "-- example:",
        "--   ${2:module}.${3:functionName}(${4:name})",
        "-- output:",
        "--   ${8:expected output}",
        "${2:module}.${3:functionName} = function(${4:name})",
        "  ${0:-- body}",
        "end",
      ].join("\n"),
    },
  ];
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
      const receiverType = separator === ":" ? inferredReceiverType(tokens, index - 2, position) : "";
      const moduleName = receiverType || tokens[index - 2].text;
      const qualified = `${moduleName}.${token.text}`;
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
    const receiverHint = inferredReceiverType(tokens, index - 2, position) || tokens[index - 2].text;
    const byMethod = getBuiltinFunctionByMethod(token.text, receiverHint);
    if (byMethod) {
      return byMethod;
    }
    return "";
  }

  return token.text;
}

function findDefinition(text, targetName, position, syntax) {
  const result = scanTokens(text, syntax);
  const snapshot = buildSymbolSnapshot(result.tokens);
  return definitionFromSnapshot(snapshot, targetName, position);
}

function definitionFromSnapshot(snapshot, targetName, position) {
  const definitions = snapshot.definitions.get(targetName) || [];
  let best = null;
  for (const range of definitions) {
    if (isRangeBeforeOrEqual(range, position)) {
      best = range;
    }
  }
  if (best) {
    return best;
  }
  return definitions.length > 0 ? definitions[0] : null;
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
  const previous = index > 0 ? tokens[index - 1] : null;
  const next = index + 1 < tokens.length ? tokens[index + 1] : null;
  if (standardLibraries.has(token.text) && next && (next.text === "." || next.text === ":")) {
    return semanticTypeNamespace;
  }
  if (baseBuiltinFunctions.has(token.text) && next && next.text === "(") {
    return semanticTypeFunction;
  }
  if (previous && (previous.text === "." || previous.text === ":") && next && next.text === "(") {
    const receiverIndex = index - 2;
    const receiver = receiverIndex >= 0 ? tokens[receiverIndex] : null;
    const moduleName = previous.text === ":"
      ? inferredReceiverType(tokens, receiverIndex, token.range.start) || (receiver ? receiver.text : "")
      : receiver ? receiver.text : "";
    const methods = typeMethods.get(moduleName);
    if (methods && methods.has(token.text)) {
      return semanticTypeMethod;
    }
  }
  if (previous && previous.text === "function") {
    return semanticTypeFunction;
  }
  if (!previous || (previous.text !== "." && previous.text !== ":")) {
    if (next && next.text === "(") {
      return semanticTypeFunction;
    }
  }
  return null;
}

function generateSemanticTokens(text, syntax) {
  const tokens = scanTokens(text, syntax).tokens;
  const data = [];
  let previousLine = 0;
  let previousStart = 0;
  for (let index = 0; index < tokens.length; index++) {
    const token = tokens[index];
    const tokenType = semanticTokenTypeForToken(tokens, index, syntax);
    if (tokenType === null) {
      continue;
    }
    if (token.range.start.line !== token.range.end.line) {
      continue;
    }
    const line = token.range.start.line;
    const start = token.range.start.character;
    const length = Math.max(0, token.range.end.character - token.range.start.character);
    if (length <= 0) {
      continue;
    }
    const deltaLine = data.length === 0 ? line : line - previousLine;
    const deltaStart = deltaLine === 0 ? start - previousStart : start;
    data.push(deltaLine, deltaStart, length, tokenType, 0);
    previousLine = line;
    previousStart = start;
  }
  return { data };
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

  const snapshot = buildSymbolSnapshot(tokens);
  diagnostics.push(...collectTypedMethodDiagnostics(tokens));
  diagnostics.push(...collectUndeclaredIdentifierDiagnostics(tokens, snapshot));
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
let workspaceRoots = [];

function parsePositionOffset(text, position) {
  const lines = text.split("\n");
  if (position.line >= lines.length) {
    return { line: Math.max(lines.length - 1, 0), character: 0 };
  }
  const line = lines[position.line] || "";
  const charPos = Math.min(Math.max(position.character, 0), line.length);
  return { line: position.line, character: charPos };
}

function filePathFromUri(uri) {
  if (!uri || !uri.startsWith("file://")) {
    return "";
  }
  try {
    return decodeURIComponent(new URL(uri).pathname);
  } catch {
    return "";
  }
}

function uriFromFilePath(filePath) {
  return `file://${encodeURI(filePath).replace(/%2F/g, "/")}`;
}

function modulePathCandidates(moduleName, baseDir) {
  const relative = String(moduleName || "").replace(/\./g, path.sep);
  const roots = [];
  if (baseDir) {
    roots.push({ root: baseDir, prefixes: [""] });
  }
  for (const workspaceRoot of workspaceRoots) {
    if (workspaceRoot) {
      roots.push({ root: workspaceRoot, prefixes: ["", "lua", "src"] });
    }
  }
  const candidates = [];
  for (const entry of roots) {
    for (const prefix of entry.prefixes) {
      const root = prefix ? path.join(entry.root, prefix) : entry.root;
      candidates.push(
        path.join(root, `${relative}.glua`),
        path.join(root, `${relative}.lua`),
        path.join(root, relative, "init.glua"),
        path.join(root, relative, "init.lua")
      );
    }
  }
  return [...new Set(candidates)];
}

function resolveRequiredModuleFile(moduleName, documentUri) {
  const documentPath = filePathFromUri(documentUri);
  const baseDir = documentPath ? path.dirname(documentPath) : "";
  for (const candidate of modulePathCandidates(moduleName, baseDir)) {
    if (fs.existsSync(candidate) && fs.statSync(candidate).isFile()) {
      return candidate;
    }
  }
  return "";
}

function isNativeRequireModule(moduleName) {
  return nativeRequireModules.has(String(moduleName || ""));
}

function nativeRequiredModuleAt(tokens, position, documentUri) {
  const index = findTokenIndexAtPosition(tokens, position);
  if (index < 0 || tokens[index].type !== "string") {
    return "";
  }
  if (index < 2 || tokens[index - 1].text !== "(" || tokens[index - 2].text !== "require") {
    return "";
  }
  const moduleName = tokens[index].text.slice(1, -1);
  if (!isNativeRequireModule(moduleName) || resolveRequiredModuleFile(moduleName, documentUri)) {
    return "";
  }
  return moduleName;
}

function hoverForNativeRequiredModule(tokens, position, documentUri) {
  const moduleName = nativeRequiredModuleAt(tokens, position, documentUri);
  if (!moduleName) {
    return null;
  }
  return {
    contents: {
      kind: "markdown",
      value: [
        `\`${moduleName}\``,
        "",
        "Native Lua C module resolved through `package.cpath`.",
        "",
        "No Lua source file target is required, so definition intentionally has no jump target.",
      ].join("\n"),
    },
  };
}

function requiredModuleAt(tokens, position, documentUri) {
  const index = findTokenIndexAtPosition(tokens, position);
  if (index < 0 || tokens[index].type !== "string") {
    return null;
  }
  if (index < 2 || tokens[index - 1].text !== "(" || tokens[index - 2].text !== "require") {
    return null;
  }
  const moduleName = tokens[index].text.slice(1, -1);
  const filePath = resolveRequiredModuleFile(moduleName, documentUri);
  if (!filePath) {
    return null;
  }
  return {
    uri: uriFromFilePath(filePath),
    range: makeRange(0, 0, 0, 1),
  };
}

function localRequireBindings(tokens, documentUri) {
  const bindings = new Map();
  for (let index = 0; index < tokens.length; index++) {
    const receiverIndex = tokens[index].text === "local" ? nextVisibleIndex(tokens, index) : index;
    const equalsIndex = nextVisibleIndex(tokens, receiverIndex);
    const requireIndex = nextVisibleIndex(tokens, equalsIndex);
    const moduleIndex = moduleStringIndex(tokens, requireIndex);
    if (receiverIndex < 0 || equalsIndex < 0 || requireIndex < 0 || moduleIndex < 0) {
      continue;
    }
    if (!isNameToken(tokens[receiverIndex]) || tokens[equalsIndex].text !== "=" || tokens[requireIndex].text !== "require" || tokens[moduleIndex].type !== "string") {
      continue;
    }
    const moduleName = tokens[moduleIndex].text.slice(1, -1);
    const filePath = resolveRequiredModuleFile(moduleName, documentUri);
    if (filePath) {
      bindings.set(tokens[receiverIndex].text, filePath);
    }
  }
  return bindings;
}

function moduleStringIndex(tokens, requireIndex) {
  const firstIndex = nextVisibleIndex(tokens, requireIndex);
  if (firstIndex < 0) {
    return -1;
  }
  if (tokens[firstIndex].type === "string") {
    return firstIndex;
  }
  const secondIndex = nextVisibleIndex(tokens, firstIndex);
  if (tokens[firstIndex].text === "(" && secondIndex >= 0 && tokens[secondIndex].type === "string") {
    return secondIndex;
  }
  return -1;
}

function exportedMemberDefinition(filePath, receiverName, memberName) {
  const member = moduleExportSnapshot(filePath, receiverName).members.find((current) => current.name === memberName);
  if (member) {
    return {
      uri: uriFromFilePath(filePath),
      range: member.range,
      member,
    };
  }
  return null;
}

function exportedMemberDefinitionAtPosition(tokens, position) {
  const index = findTokenIndexAtPosition(tokens, position);
  if (index < 0 || !isNameToken(tokens[index])) {
    return null;
  }
  const separatorIndex = previousVisibleIndex(tokens, index);
  const receiverIndex = previousVisibleIndex(tokens, separatorIndex);
  if (receiverIndex < 0 || separatorIndex < 0 || !isNameToken(tokens[receiverIndex])) {
    return null;
  }
  const separator = tokens[separatorIndex].text;
  if (separator !== "." && separator !== ":") {
    return null;
  }
  const exportedTables = returnedTableNames(tokens);
  if (!exportedTables.has(tokens[receiverIndex].text)) {
    return null;
  }
  if (separator === ":" && isFunctionStatementMember(tokens, receiverIndex)) {
    return { receiver: tokens[receiverIndex].text, member: tokens[index].text, range: tokens[index].range };
  }
  if (separator === "." && isFunctionStatementMember(tokens, receiverIndex)) {
    return { receiver: tokens[receiverIndex].text, member: tokens[index].text, range: tokens[index].range };
  }
  if (separator === "." && memberFunctionAssignmentIndex(tokens, index) >= 0) {
    return { receiver: tokens[receiverIndex].text, member: tokens[index].text, range: tokens[index].range };
  }
  return null;
}

function workspaceLuaFiles() {
  const files = [];
  const skipped = new Set([".git", ".gradle", ".idea", ".vscode", "build", "dist", "node_modules", "out"]);
  const visit = (directory) => {
    let entries = [];
    try {
      entries = fs.readdirSync(directory, { withFileTypes: true }).sort((left, right) => left.name.localeCompare(right.name));
    } catch {
      return;
    }
    for (const entry of entries) {
      const absolutePath = path.join(directory, entry.name);
      if (entry.isDirectory()) {
        if (!skipped.has(entry.name)) {
          visit(absolutePath);
        }
        continue;
      }
      if (entry.isFile() && (entry.name.endsWith(".lua") || entry.name.endsWith(".glua"))) {
        files.push(absolutePath);
      }
    }
  };
  for (const root of workspaceRoots) {
    if (root) {
      visit(root);
    }
  }
  return [...new Set(files)].sort();
}

function callerTargetsForMemberDefinition(tokens, position, documentUri) {
  const definition = exportedMemberDefinitionAtPosition(tokens, position);
  if (!definition) {
    return null;
  }
  const modulePath = path.resolve(filePathFromUri(documentUri));
  const targets = [];
  for (const filePath of workspaceLuaFiles()) {
    let text = "";
    try {
      text = fs.readFileSync(filePath, "utf8");
    } catch {
      continue;
    }
    const candidateUri = uriFromFilePath(filePath);
    const candidateTokens = scanTokens(text, syntax).tokens;
    const bindings = localRequireBindings(candidateTokens, candidateUri);
    const receivers = [...bindings.entries()]
      .filter(([, resolvedPath]) => path.resolve(resolvedPath) === modulePath)
      .map(([receiver]) => receiver);
    if (receivers.length === 0) {
      continue;
    }
    const receiverSet = new Set(receivers);
    for (let index = 2; index < candidateTokens.length; index++) {
      const separator = candidateTokens[index - 1].text;
      if (!isNameToken(candidateTokens[index]) || (separator !== "." && separator !== ":") || !receiverSet.has(candidateTokens[index - 2].text)) {
        continue;
      }
      if (candidateTokens[index].text === definition.member) {
        targets.push({ uri: candidateUri, range: candidateTokens[index].range });
      }
    }
  }
  return targets;
}

function moduleExportSnapshot(filePath, receiverName) {
  let text = "";
  try {
    text = fs.readFileSync(filePath, "utf8");
  } catch {
    return {
      filePath,
      text: "",
      tokens: [],
      exportedTables: new Set(),
      members: [],
    };
  }
  const tokens = scanTokens(text, syntax).tokens;
  const exportedTables = returnedTableNames(tokens);
  if (receiverName) {
    exportedTables.add(receiverName);
  }
  const members = [];
  const seen = new Set();
  const addMember = (token, callStyle, functionIndex) => {
    const name = stringTokenValue(token) || (token ? token.text : "");
    if (!name || seen.has(name)) {
      return;
    }
    seen.add(name);
    const signature = memberSignature(tokens, functionIndex, name);
    const documentation = hoverMarkdownForDefinition(signature, text, token.range);
    members.push({
      name,
      range: token.range,
      callStyle,
      signature,
      detail: callStyle === ":" ? `${signature} method` : signature,
      documentation,
      sourcePath: filePath,
    });
  };

  for (let index = 0; index + 2 < tokens.length; index++) {
    const receiverMatches = exportedTables.has(tokens[index].text);
    const separator = tokens[index + 1];
    const member = tokens[index + 2];
    if (receiverMatches && separator && member && (separator.text === "." || separator.text === ":") && isNameToken(member)) {
      if (separator.text === ":" && isFunctionStatementMember(tokens, index)) {
        addMember(member, ":", previousVisibleIndex(tokens, index));
        continue;
      }
      const assignmentFunctionIndex = memberFunctionAssignmentIndex(tokens, index + 2);
      if (separator.text === "." && isFunctionStatementMember(tokens, index)) {
        addMember(member, ".", previousVisibleIndex(tokens, index));
        continue;
      }
      if (separator.text === "." && assignmentFunctionIndex >= 0) {
        addMember(member, ".", assignmentFunctionIndex);
        continue;
      }
    }
    const indexedMember = indexedMemberFunctionDefinition(tokens, index, exportedTables, "");
    if (indexedMember) {
      addMember(indexedMember.token, ".", indexedMember.functionIndex);
    }
  }

  for (const tableName of exportedTables) {
    for (const range of tableConstructorRanges(tokens, tableName)) {
      for (const field of tableFieldFunctionDefinitions(tokens, range.openIndex + 1, range.closeIndex)) {
        addMember(field.token, ".", field.functionIndex);
      }
    }
  }
  return {
    filePath,
    text,
    tokens,
    exportedTables,
    members,
  };
}

function returnedTableNames(tokens) {
  const names = new Set();
  for (let index = 0; index < tokens.length; index++) {
    if (tokens[index].text !== "return") {
      continue;
    }
    const nextIndex = nextVisibleIndex(tokens, index);
    if (nextIndex >= 0 && isNameToken(tokens[nextIndex])) {
      names.add(tokens[nextIndex].text);
    }
  }
  return names;
}

function isMemberFunctionDefinition(tokens, memberIndex) {
  return memberFunctionAssignmentIndex(tokens, memberIndex) >= 0;
}

function memberFunctionAssignmentIndex(tokens, memberIndex) {
  const line = tokens[memberIndex].range.start.line;
  let hasEquals = false;
  for (let cursor = memberIndex + 1; cursor < tokens.length && tokens[cursor].range.start.line === line; cursor++) {
    if (tokens[cursor].text === "=") {
      hasEquals = true;
      continue;
    }
    if (hasEquals && tokens[cursor].text === "function") {
      return cursor;
    }
  }
  return -1;
}

function indexedMemberFunctionDefinition(tokens, receiverIndex, exportedTables, memberName) {
  if (!exportedTables.has(tokens[receiverIndex].text)) {
    return null;
  }
  const openIndex = nextVisibleIndex(tokens, receiverIndex);
  const keyIndex = nextVisibleIndex(tokens, openIndex);
  const closeIndex = nextVisibleIndex(tokens, keyIndex);
  if (openIndex < 0 || keyIndex < 0 || closeIndex < 0 || tokens[openIndex].text !== "[" || tokens[closeIndex].text !== "]") {
    return null;
  }
  if (memberName && stringTokenValue(tokens[keyIndex]) !== memberName) {
    return null;
  }
  const functionIndex = indexedFunctionIndex(tokens, closeIndex);
  return functionIndex >= 0 ? { token: tokens[keyIndex], functionIndex } : null;
}

function tableLiteralMemberFunctionDefinition(tokens, exportedTables, memberName) {
  for (const tableName of exportedTables) {
    for (const range of tableConstructorRanges(tokens, tableName)) {
      const field = tableFieldFunctionDefinition(tokens, range.openIndex + 1, range.closeIndex, memberName);
      if (field) {
        return field;
      }
    }
  }
  return null;
}

function tableConstructorRanges(tokens, tableName) {
  const ranges = [];
  for (let index = 0; index < tokens.length; index++) {
    if (tokens[index].text !== tableName) {
      continue;
    }
    const equalsIndex = nextVisibleIndex(tokens, index);
    const openIndex = nextVisibleIndex(tokens, equalsIndex);
    if (equalsIndex < 0 || openIndex < 0 || tokens[equalsIndex].text !== "=" || tokens[openIndex].text !== "{") {
      continue;
    }
    const closeIndex = matchingDelimiterIndex(tokens, openIndex);
    if (closeIndex > openIndex) {
      ranges.push({ openIndex, closeIndex });
    }
  }
  return ranges;
}

function tableFieldFunctionDefinition(tokens, startIndex, endIndex, memberName) {
  return tableFieldFunctionDefinitions(tokens, startIndex, endIndex).find((field) => {
    const name = stringTokenValue(field.token) || field.token.text;
    return name === memberName;
  })?.token || null;
}

function tableFieldFunctionDefinitions(tokens, startIndex, endIndex) {
  const fields = [];
  for (let index = startIndex; index < endIndex; index++) {
    const token = tokens[index];
    if (!token) {
      continue;
    }
    if (token.text === "[" && index + 2 < endIndex) {
      const keyIndex = nextVisibleIndex(tokens, index);
      const closeIndex = nextVisibleIndex(tokens, keyIndex);
      const functionIndex = indexedFunctionIndex(tokens, closeIndex);
      if (closeIndex < endIndex && tokens[closeIndex].text === "]" && stringTokenValue(tokens[keyIndex]) && functionIndex >= 0) {
        fields.push({ token: tokens[keyIndex], functionIndex });
      }
      continue;
    }
    const functionIndex = bareTableFieldFunctionIndex(tokens, index, endIndex);
    if (isNameToken(token) && functionIndex >= 0) {
      fields.push({ token, functionIndex });
    }
  }
  return fields;
}

function isFunctionStatementMember(tokens, receiverIndex) {
  const previous = previousVisibleToken(tokens, receiverIndex);
  return previous && previous.text === "function";
}

function isIndexedFunctionDefinition(tokens, closeBracketIndex) {
  return indexedFunctionIndex(tokens, closeBracketIndex) >= 0;
}

function indexedFunctionIndex(tokens, closeBracketIndex) {
  const equalsIndex = nextVisibleIndex(tokens, closeBracketIndex);
  const functionIndex = nextVisibleIndex(tokens, equalsIndex);
  return equalsIndex >= 0 && functionIndex >= 0 && tokens[equalsIndex].text === "=" && tokens[functionIndex].text === "function" ? functionIndex : -1;
}

function isBareTableFieldFunctionDefinition(tokens, keyIndex, endIndex) {
  return bareTableFieldFunctionIndex(tokens, keyIndex, endIndex) >= 0;
}

function bareTableFieldFunctionIndex(tokens, keyIndex, endIndex) {
  const equalsIndex = nextVisibleIndex(tokens, keyIndex);
  const functionIndex = nextVisibleIndex(tokens, equalsIndex);
  return equalsIndex > keyIndex && functionIndex < endIndex && tokens[equalsIndex].text === "=" && tokens[functionIndex].text === "function" ? functionIndex : -1;
}

function memberSignature(tokens, functionIndex, name) {
  if (functionIndex < 0 || !tokens[functionIndex] || tokens[functionIndex].text !== "function") {
    return `${name}()`;
  }
  let openIndex = -1;
  for (let cursor = functionIndex + 1; cursor < tokens.length; cursor++) {
    if (tokens[cursor].text === "(") {
      openIndex = cursor;
      break;
    }
    if (tokens[cursor].range.start.line > tokens[functionIndex].range.start.line && openIndex < 0) {
      break;
    }
  }
  if (openIndex < 0) {
    return `${name}()`;
  }
  const closeIndex = matchingDelimiterIndex(tokens, openIndex);
  if (closeIndex < 0) {
    return `${name}()`;
  }
  const params = [];
  for (let cursor = openIndex + 1; cursor < closeIndex; cursor++) {
    if (isNameToken(tokens[cursor]) || tokens[cursor].text === "...") {
      params.push(tokens[cursor].text);
    }
  }
  return `${name}(${params.join(", ")})`;
}

function matchingDelimiterIndex(tokens, openIndex) {
  const open = tokens[openIndex] ? tokens[openIndex].text : "";
  const close = open === "{" ? "}" : open === "[" ? "]" : open === "(" ? ")" : "";
  if (!close) {
    return -1;
  }
  let depth = 0;
  for (let index = openIndex; index < tokens.length; index++) {
    if (tokens[index].text === open) {
      depth++;
      continue;
    }
    if (tokens[index].text === close) {
      depth--;
      if (depth === 0) {
        return index;
      }
    }
  }
  return -1;
}

function stringTokenValue(token) {
  if (!token || token.type !== "string" || token.text.length < 2) {
    return "";
  }
  return token.text.slice(1, -1);
}

function commentBlockBeforeLine(text, lineNumber) {
  const lines = text.split("\n");
  const comments = [];
  for (let index = lineNumber - 1; index >= 0; index--) {
    const line = lines[index] || "";
    const trimmed = line.trim();
    if (trimmed === "") {
      if (comments.length === 0) {
        continue;
      }
      break;
    }
    if (!trimmed.startsWith("--")) {
      break;
    }
    comments.unshift(trimmed.replace(/^--\s?/, ""));
  }
  return comments.join("\n").trim();
}

function parseAnnotationComment(comment) {
  const result = {
    description: [],
    params: [],
    returns: [],
    example: [],
    output: [],
    other: [],
  };
  if (!comment) {
    return result;
  }
  const lines = comment.split("\n");
  let section = "";
  for (const rawLine of lines) {
    const line = rawLine.trim();
    if (!line) {
      continue;
    }
    let match = line.match(/^(?:description|desc)\s*:\s*(.*)$/i);
    if (match) {
      section = "description";
      if (match[1]) {
        result.description.push(match[1].trim());
      }
      continue;
    }
    match = line.match(/^(?:param|parameter)\s*:\s*([A-Za-z_][A-Za-z0-9_]*)\s*([A-Za-z_][A-Za-z0-9_.<>|?]*)?\s*(.*)$/i);
    if (match) {
      section = "";
      result.params.push({
        name: match[1],
        type: match[2] || "",
        description: match[3] || "",
      });
      continue;
    }
    match = line.match(/^(?:return|returns)\s*:\s*(.*)$/i);
    if (match) {
      section = "returns";
      if (match[1]) {
        result.returns.push(match[1].trim());
      }
      continue;
    }
    match = line.match(/^example\s*:\s*(.*)$/i);
    if (match) {
      section = "example";
      if (match[1]) {
        result.example.push(match[1].trim());
      }
      continue;
    }
    match = line.match(/^output\s*:\s*(.*)$/i);
    if (match) {
      section = "output";
      if (match[1]) {
        result.output.push(match[1].trim());
      }
      continue;
    }
    if (section && result[section]) {
      result[section].push(line);
      continue;
    }
    result.other.push(line);
  }
  return result;
}

function annotationLabels() {
  const locale = String(getBuiltinLocale() || "").toLowerCase();
  if (locale.startsWith("zh")) {
    return {
      parameters: "参数",
      returns: "返回值",
      example: "示例",
      output: "输出",
      definedAt(line, column) {
        return `定义于第 ${line} 行，第 ${column} 列。`;
      },
      parameterName(name) {
        return `参数 \`${name}\``;
      },
    };
  }
  return {
    parameters: "Parameters",
    returns: "Returns",
    example: "Example",
    output: "Output",
    definedAt(line, column) {
      return `Defined at line ${line}, column ${column}.`;
    },
    parameterName(name) {
      return `Parameter \`${name}\``;
    },
  };
}

function formatAnnotationMarkdown(comment) {
  const annotation = parseAnnotationComment(comment);
  const labels = annotationLabels();
  const sections = [];
  if (annotation.description.length > 0) {
    sections.push(annotation.description.join(" "));
  }
  if (annotation.params.length > 0) {
    const params = annotation.params.map((param) => {
      const type = param.type ? ` \`${param.type}\`` : "";
      const suffix = param.description ? ` - ${param.description}` : "";
      return `- \`${param.name}\`${type}${suffix}`;
    });
    sections.push(`**${labels.parameters}**\n${params.join("\n")}`);
  }
  if (annotation.returns.length > 0) {
    sections.push(`**${labels.returns}**\n${annotation.returns.join(" ")}`);
  }
  if (annotation.example.length > 0) {
    sections.push(`**${labels.example}**\n\`\`\`lua\n${annotation.example.join("\n")}\n\`\`\``);
  }
  if (annotation.output.length > 0) {
    sections.push(`**${labels.output}**\n\`\`\`text\n${annotation.output.join("\n")}\n\`\`\``);
  }
  if (annotation.other.length > 0) {
    sections.push(annotation.other.join("  \n"));
  }
  if (sections.length === 0) {
    return comment.split("\n").join("  \n");
  }
  return sections.join("\n\n");
}

function hoverMarkdownForDefinition(targetName, definitionText, definitionRange) {
  const comment = commentBlockBeforeLine(definitionText, definitionRange.start.line);
  const labels = annotationLabels();
  const location = labels.definedAt(definitionRange.start.line + 1, definitionRange.start.character + 1);
  if (!comment) {
    return `\`${targetName}\`\n\n${location}`;
  }
  const formattedComment = formatAnnotationMarkdown(comment);
  return `\`${targetName}\`\n\n${formattedComment}\n\n${location}`;
}

function paramDocumentationFromComment(comment, paramName) {
  if (!comment) {
    return "";
  }
  const lines = comment.split("\n");
  for (const line of lines) {
    const match = line.match(/^(?:param|parameter)\s*:\s*([A-Za-z_][A-Za-z0-9_]*)\s*(.*)$/i);
    if (match && match[1] === paramName) {
      return match[2] ? `${match[1]} ${match[2]}`.trim() : match[1];
    }
  }
  return "";
}

function functionParameterContext(tokens, tokenIndex) {
  const token = tokens[tokenIndex];
  if (!token || !isNameToken(token)) {
    return null;
  }
  for (let functionIndex = tokenIndex - 1; functionIndex >= 0; functionIndex--) {
    if (tokens[functionIndex].text !== "function") {
      continue;
    }
    let openIndex = -1;
    for (let cursor = functionIndex + 1; cursor < tokens.length; cursor++) {
      if (tokens[cursor].text === "(") {
        openIndex = cursor;
        break;
      }
      if (tokens[cursor].range.start.line > tokens[functionIndex].range.start.line && openIndex < 0) {
        break;
      }
    }
    if (openIndex < 0 || openIndex > tokenIndex) {
      continue;
    }
    let closeIndex = -1;
    const params = new Set();
    for (let cursor = openIndex + 1; cursor < tokens.length; cursor++) {
      if (tokens[cursor].text === ")") {
        closeIndex = cursor;
        break;
      }
      if (isNameToken(tokens[cursor])) {
        params.add(tokens[cursor].text);
      }
    }
    if (closeIndex < 0 || !params.has(token.text)) {
      continue;
    }
    return {
      name: token.text,
      functionLine: tokens[functionIndex].range.start.line,
      range: token.range,
    };
  }
  return null;
}

function hoverForFunctionParameter(tokens, tokenIndex, text) {
  const context = functionParameterContext(tokens, tokenIndex);
  if (!context) {
    return null;
  }
  const comment = commentBlockBeforeLine(text, context.functionLine);
  const paramDoc = paramDocumentationFromComment(comment, context.name);
  if (!paramDoc) {
    return null;
  }
  const labels = annotationLabels();
  return {
    contents: {
      kind: "markdown",
      value: `${labels.parameterName(context.name)}\n\n${paramDoc}`,
    },
    range: context.range,
  };
}

function hoverForRequiredMember(tokens, position, documentUri) {
  const target = requiredMemberTarget(tokens, position, documentUri);
  if (!target) {
    return null;
  }
  const filePath = filePathFromUri(target.uri);
  let text = "";
  try {
    text = fs.readFileSync(filePath, "utf8");
  } catch {
    return null;
  }
  const tokenIndex = findTokenIndexAtPosition(tokens, position);
  const targetName = tokenIndex >= 2 && tokens[tokenIndex - 1].text === "." && isNameToken(tokens[tokenIndex - 2])
    ? `${tokens[tokenIndex - 2].text}.${tokens[tokenIndex].text}`
    : (tokenIndex >= 0 ? tokens[tokenIndex].text : path.basename(filePath));
  if (target.member && target.member.documentation) {
    return {
      contents: {
        kind: "markdown",
        value: target.member.documentation,
      },
      range: target.range,
    };
  }
  return {
    contents: {
      kind: "markdown",
      value: hoverMarkdownForDefinition(targetName, text, target.range),
    },
    range: target.range,
  };
}

function requiredMemberTarget(tokens, position, documentUri) {
  const index = findTokenIndexAtPosition(tokens, position);
  if (index < 2 || !isNameToken(tokens[index]) || (tokens[index - 1].text !== "." && tokens[index - 1].text !== ":") || !isNameToken(tokens[index - 2])) {
    return null;
  }
  const bindings = localRequireBindings(tokens, documentUri);
  const filePath = bindings.get(tokens[index - 2].text);
  if (!filePath) {
    return null;
  }
  return exportedMemberDefinition(filePath, tokens[index - 2].text, tokens[index].text);
}

function memberDefinitionHover(tokens, tokenIndex, text) {
  if (tokenIndex < 2 || !isNameToken(tokens[tokenIndex]) || tokens[tokenIndex - 1].text !== "." || !isNameToken(tokens[tokenIndex - 2])) {
    return null;
  }
  const line = tokens[tokenIndex].range.start.line;
  let equalsIndex = -1;
  let functionIndex = -1;
  for (let cursor = tokenIndex + 1; cursor < tokens.length && tokens[cursor].range.start.line === line; cursor++) {
    if (tokens[cursor].text === "=" && equalsIndex < 0) {
      equalsIndex = cursor;
      continue;
    }
    if (tokens[cursor].text === "function") {
      functionIndex = cursor;
      break;
    }
  }
  if (equalsIndex < 0 || functionIndex < 0) {
    return null;
  }
  const targetName = `${tokens[tokenIndex - 2].text}.${tokens[tokenIndex].text}`;
  return {
    contents: {
      kind: "markdown",
      value: hoverMarkdownForDefinition(targetName, text, tokens[tokenIndex].range),
    },
    range: tokens[tokenIndex].range,
  };
}

function effectiveBuiltinLocale(rawLocale, resolvedLocale) {
  const raw = rawLocale === undefined || rawLocale === null ? "" : String(rawLocale);
  if (!raw || raw.toLowerCase() === DEFAULT_DOC_LOCALE) {
    return resolvedLocale || builtinExtensionOptions.resolvedLocale || "en";
  }
  return raw;
}

connection.onInitialize((params) => {
  workspaceRoots = [];
  if (Array.isArray(params.workspaceFolders)) {
    workspaceRoots = params.workspaceFolders
      .map((folder) => filePathFromUri(folder.uri))
      .filter(Boolean);
  } else if (params.rootUri) {
    const root = filePathFromUri(params.rootUri);
    if (root) {
      workspaceRoots = [root];
    }
  } else if (params.rootPath) {
    workspaceRoots = [params.rootPath];
  }
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
      semanticTokensProvider: {
        legend: {
          tokenTypes: semanticTokenTypes,
          tokenModifiers: [],
        },
        full: true,
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
  validateAllDocuments();
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
  validateAllDocuments();
});

function validateDocument(document) {
  const diagnostics = collectParseLikeErrors(document.getText(), syntax);
  connection.sendDiagnostics({ uri: document.uri, diagnostics });
}

function validateAllDocuments() {
  for (const document of documents.all()) {
    validateDocument(document);
  }
}

documents.onDidOpen((change) => validateDocument(change.document));

documents.onDidChangeContent((change) => validateDocument(change.document));

documents.onDidClose((change) => {
  connection.sendDiagnostics({ uri: change.document.uri, diagnostics: [] });
});

connection.onDefinition((params) => {
  const doc = documents.get(params.textDocument.uri);
  if (!doc) {
    return null;
  }
  const text = doc.getText();
  const tokens = scanTokens(text, syntax).tokens;
  const position = parsePositionOffset(text, params.position);
  const callerTargets = callerTargetsForMemberDefinition(tokens, position, params.textDocument.uri);
  if (callerTargets) {
    if (callerTargets.length === 0) {
      connection.window.showInformationMessage("没有找到调用方");
    }
    return callerTargets;
  }
  const requiredModule = requiredModuleAt(tokens, position, params.textDocument.uri);
  if (requiredModule) {
    return [requiredModule];
  }
  const requiredMember = requiredMemberTarget(tokens, position, params.textDocument.uri);
  if (requiredMember) {
    return [requiredMember];
  }
  const target = resolveBuiltinTarget(tokens, position);
  if (!target) {
    return null;
  }
  const definition = findDefinition(text, target, position, syntax);
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
  const snapshot = buildSymbolSnapshot(tokens);
  const candidates = buildCompletionCandidates(context, snapshot, tokens, params.textDocument.uri);
  return candidates.map((item) => ({
    label: item.name,
    kind: item.kind || CompletionItemKind.Function,
    detail: item.detail,
    documentation: {
      kind: "markdown",
      value: item.documentation,
    },
    insertTextFormat: item.snippet ? InsertTextFormat.Snippet : undefined,
    textEdit: {
      range: context.range,
      newText: item.snippet || item.name,
    },
  }));
});

connection.onHover((params) => {
  const doc = documents.get(params.textDocument.uri);
  if (!doc) {
    return null;
  }
  const text = doc.getText();
  const tokens = scanTokens(text, syntax).tokens;
  const position = parsePositionOffset(text, params.position);
  const tokenIndex = findTokenIndexAtPosition(tokens, position);
  const parameterHover = hoverForFunctionParameter(tokens, tokenIndex, text);
  if (parameterHover) {
    return parameterHover;
  }
  const definitionMemberHover = memberDefinitionHover(tokens, tokenIndex, text);
  if (definitionMemberHover) {
    return definitionMemberHover;
  }
  const requiredMemberHover = hoverForRequiredMember(tokens, position, params.textDocument.uri);
  if (requiredMemberHover) {
    return requiredMemberHover;
  }
  const nativeRequiredModuleHover = hoverForNativeRequiredModule(tokens, position, params.textDocument.uri);
  if (nativeRequiredModuleHover) {
    return nativeRequiredModuleHover;
  }
  const target = resolveBuiltinTarget(tokens, position);
  if (!target) {
    return null;
  }
  const definition = findDefinition(text, target, position, syntax);
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
      value: hoverMarkdownForDefinition(target, text, definition),
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
  return generateSemanticTokens(doc.getText(), syntax);
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

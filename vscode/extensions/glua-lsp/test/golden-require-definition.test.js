"use strict";

const assert = require("assert");
const fs = require("fs");
const os = require("os");
const path = require("path");
const { spawn } = require("child_process");

const extensionRoot = path.resolve(__dirname, "..");
const repoRoot = path.resolve(extensionRoot, "..", "..", "..");
const serverPath = path.join(extensionRoot, "server", "index.js");
const goldenPath = path.join(repoRoot, "tests", "editor", "golden-require-definition.json");

function frame(payload) {
  const body = JSON.stringify(payload);
  return `Content-Length: ${Buffer.byteLength(body, "utf8")}\r\n\r\n${body}`;
}

function makeReader(onMessage) {
  let buffer = Buffer.alloc(0);
  return (chunk) => {
    buffer = Buffer.concat([buffer, chunk]);
    while (true) {
      const headerEnd = buffer.indexOf("\r\n\r\n");
      if (headerEnd < 0) {
        return;
      }
      const header = buffer.slice(0, headerEnd).toString("utf8");
      const match = header.match(/Content-Length:\s*(\d+)/i);
      assert(match, `missing Content-Length in ${header}`);
      const length = Number(match[1]);
      const bodyStart = headerEnd + 4;
      const bodyEnd = bodyStart + length;
      if (buffer.length < bodyEnd) {
        return;
      }
      const body = buffer.slice(bodyStart, bodyEnd).toString("utf8");
      buffer = buffer.slice(bodyEnd);
      onMessage(JSON.parse(body));
    }
  };
}

function waitFor(predicate, messages, timeoutMs, label) {
  const existing = messages.find(predicate);
  if (existing) {
    return Promise.resolve(existing);
  }
  return new Promise((resolve, reject) => {
    const deadline = setTimeout(() => {
      reject(new Error(`timeout waiting for ${label}`));
    }, timeoutMs);
    const waiter = (message) => {
      if (!predicate(message)) {
        return false;
      }
      clearTimeout(deadline);
      resolve(message);
      return true;
    };
    waiter.reject = (error) => {
      clearTimeout(deadline);
      reject(error);
    };
    messages.waiters.push(waiter);
  });
}

function positionOf(source, marker) {
  const markerIndex = source.indexOf(marker);
  assert(markerIndex >= 0, `marker not found: ${marker}`);
  const before = source.slice(0, markerIndex);
  const lines = before.split("\n");
  return {
    line: lines.length - 1,
    character: lines[lines.length - 1].length,
  };
}

function writeFixtureFiles(root, files) {
  for (const [relativePath, content] of Object.entries(files)) {
    const target = path.join(root, relativePath);
    fs.mkdirSync(path.dirname(target), { recursive: true });
    fs.writeFileSync(target, content, "utf8");
  }
}

async function main() {
  const golden = JSON.parse(fs.readFileSync(goldenPath, "utf8"));
  const fixtureRoot = fs.mkdtempSync(path.join(os.tmpdir(), "glua-require-definition-"));
  writeFixtureFiles(fixtureRoot, golden.files || {});

  const server = spawn(process.execPath, [serverPath, "--stdio"], {
    cwd: extensionRoot,
    stdio: ["pipe", "pipe", "pipe"],
  });
  const messages = [];
  messages.waiters = [];
  const stderr = [];

  server.stdout.on("data", makeReader((message) => {
    messages.push(message);
    messages.waiters = messages.waiters.filter((waiter) => !waiter(message));
  }));
  server.stderr.on("data", (chunk) => stderr.push(chunk.toString("utf8")));
  server.on("exit", (code) => {
    if (code === 0 || messages.waiters.length === 0) {
      return;
    }
    const error = new Error(`server exited with ${code}; stderr=${stderr.join("")}`);
    for (const waiter of messages.waiters) {
      if (typeof waiter.reject === "function") {
        waiter.reject(error);
      }
    }
    messages.waiters = [];
  });

  const send = (message) => server.stdin.write(frame(message));
  send({
    jsonrpc: "2.0",
    id: 1,
    method: "initialize",
    params: {
      processId: process.pid,
      rootUri: `file://${fixtureRoot}`,
      capabilities: {},
      initializationOptions: {
        syntax: "extended",
        locale: "en",
        builtinExtensions: [],
      },
    },
  });
  await waitFor((message) => message.id === 1, messages, 5000, "initialize response");
  send({ jsonrpc: "2.0", method: "initialized", params: {} });

  let requestId = 2;
  for (const testCase of golden.cases) {
    const documentPath = path.join(fixtureRoot, testCase.document);
    fs.mkdirSync(path.dirname(documentPath), { recursive: true });
    fs.writeFileSync(documentPath, testCase.source, "utf8");
    const uri = `file://${documentPath}`;
    send({
      jsonrpc: "2.0",
      method: "textDocument/didOpen",
      params: {
        textDocument: {
          uri,
          languageId: "lua",
          version: 1,
          text: testCase.source,
        },
      },
    });

    const id = requestId++;
    send({
      jsonrpc: "2.0",
      id,
      method: "textDocument/definition",
      params: {
        textDocument: { uri },
        position: positionOf(testCase.source, testCase.marker),
      },
    });
    const response = await waitFor((message) => message.id === id, messages, 5000, `definition for ${testCase.name}`);
    const result = Array.isArray(response.result) ? response.result : [];
    if (Array.isArray(testCase.targets)) {
      assert.strictEqual(result.length, testCase.targets.length, `${testCase.name} target count`);
      for (let index = 0; index < testCase.targets.length; index++) {
        const expectedTarget = testCase.targets[index];
        const expectedPath = path.join(fixtureRoot, expectedTarget.path);
        assert.strictEqual(filePathFromUri(result[index].uri), expectedPath, `${testCase.name} target ${index} path`);
        const targetSource = fs.readFileSync(expectedPath, "utf8");
        assert.deepStrictEqual(result[index].range.start, positionOf(targetSource, expectedTarget.marker), `${testCase.name} target ${index} range`);
      }
    } else {
      const actual = result.length > 0 ? filePathFromUri(result[0].uri) : null;
      const expected = testCase.target === null ? null : path.join(fixtureRoot, testCase.target);
      assert.strictEqual(actual, expected, `${testCase.name} target`);
      if (testCase.targetMarker) {
        assert(result.length > 0, `${testCase.name} result`);
        const targetSource = fs.readFileSync(expected, "utf8");
        assert.deepStrictEqual(result[0].range.start, positionOf(targetSource, testCase.targetMarker), `${testCase.name} target range`);
      }
    }
    if (testCase.nativeHint) {
      const hoverId = requestId++;
      send({
        jsonrpc: "2.0",
        id: hoverId,
        method: "textDocument/hover",
        params: {
          textDocument: { uri },
          position: positionOf(testCase.source, testCase.marker),
        },
      });
      const hoverResponse = await waitFor((message) => message.id === hoverId, messages, 5000, `hover for ${testCase.name}`);
      const hoverValue = hoverResponse.result && hoverResponse.result.contents
        ? hoverResponse.result.contents.value || hoverResponse.result.contents
        : "";
      assert(String(hoverValue).includes(testCase.nativeHint), `${testCase.name} native hint`);
      assert(String(hoverValue).includes(testCase.marker.slice(1, -1)), `${testCase.name} native module name`);
    }
  }

  const symbolDocumentPath = path.join(fixtureRoot, "app", "symbols.lua");
  const symbolSource = "value = 1\nlocal function maker(arg)\n  return value + arg\nend\nval";
  fs.writeFileSync(symbolDocumentPath, symbolSource, "utf8");
  const symbolUri = `file://${symbolDocumentPath}`;
  send({
    jsonrpc: "2.0",
    method: "textDocument/didOpen",
    params: {
      textDocument: {
        uri: symbolUri,
        languageId: "lua",
        version: 1,
        text: symbolSource,
      },
    },
  });
  const symbolDefinitionId = requestId++;
  send({
    jsonrpc: "2.0",
    id: symbolDefinitionId,
    method: "textDocument/definition",
    params: {
      textDocument: { uri: symbolUri },
      position: positionOf(symbolSource, "value +"),
    },
  });
  const symbolDefinition = await waitFor((message) => message.id === symbolDefinitionId, messages, 5000, "definition for shared symbol snapshot");
  const symbolDefinitionResult = Array.isArray(symbolDefinition.result) ? symbolDefinition.result : [];
  assert.strictEqual(symbolDefinitionResult.length, 1, "shared symbol definition result count");
  assert.strictEqual(filePathFromUri(symbolDefinitionResult[0].uri), symbolDocumentPath, "shared symbol definition uri");
  assert.deepStrictEqual(symbolDefinitionResult[0].range.start, { line: 0, character: 0 }, "shared symbol definition range");

  const completionId = requestId++;
  send({
    jsonrpc: "2.0",
    id: completionId,
    method: "textDocument/completion",
    params: {
      textDocument: { uri: symbolUri },
      position: { line: 4, character: 3 },
    },
  });
  const completion = await waitFor((message) => message.id === completionId, messages, 5000, "completion for shared symbol snapshot");
  const completionLabels = (completion.result || []).map((item) => item.label);
  assert(completionLabels.includes("value"), "shared symbol completion contains assignment target");
  assert(completionLabels.includes("maker"), "shared symbol completion contains local function");

  const formatDocumentPath = path.join(fixtureRoot, "app", "format.lua");
  const formatSource = [
    "extensions = {}",
    "extensions.timesPrint = function(name,times)",
    "for i = 1,times do",
    "print('hello,'..name)",
    "end",
    "end",
  ].join("\n");
  const expectedFormat = [
    "extensions = {}",
    "extensions.timesPrint = function(name, times)",
    "  for i = 1, times do",
    "    print('hello,' .. name)",
    "  end",
    "end",
  ].join("\n");
  fs.writeFileSync(formatDocumentPath, formatSource, "utf8");
  const formatUri = `file://${formatDocumentPath}`;
  send({
    jsonrpc: "2.0",
    method: "textDocument/didOpen",
    params: {
      textDocument: {
        uri: formatUri,
        languageId: "lua",
        version: 1,
        text: formatSource,
      },
    },
  });
  const formatId = requestId++;
  send({
    jsonrpc: "2.0",
    id: formatId,
    method: "textDocument/formatting",
    params: {
      textDocument: { uri: formatUri },
      options: { tabSize: 2, insertSpaces: true },
    },
  });
  const formatResponse = await waitFor((message) => message.id === formatId, messages, 5000, "format function assignment");
  assert.strictEqual(formatResponse.result.length, 1, "format edit count");
  assert.strictEqual(formatResponse.result[0].newText, expectedFormat, "function assignment formatting");

  const inlineFormatDocumentPath = path.join(fixtureRoot, "app", "inline-format.lua");
  const inlineFormatSource = "aaa = {['ccc'] = function () xxx end}\nnextValue = 1";
  const expectedInlineFormat = "aaa = {['ccc'] = function() xxx end}\nnextValue = 1";
  fs.writeFileSync(inlineFormatDocumentPath, inlineFormatSource, "utf8");
  const inlineFormatUri = `file://${inlineFormatDocumentPath}`;
  send({
    jsonrpc: "2.0",
    method: "textDocument/didOpen",
    params: {
      textDocument: {
        uri: inlineFormatUri,
        languageId: "lua",
        version: 1,
        text: inlineFormatSource,
      },
    },
  });
  const inlineFormatId = requestId++;
  send({
    jsonrpc: "2.0",
    id: inlineFormatId,
    method: "textDocument/formatting",
    params: {
      textDocument: { uri: inlineFormatUri },
      options: { tabSize: 2, insertSpaces: true },
    },
  });
  const inlineFormatResponse = await waitFor((message) => message.id === inlineFormatId, messages, 5000, "format inline table function");
  assert.strictEqual(inlineFormatResponse.result.length, 1, "inline format edit count");
  assert.strictEqual(inlineFormatResponse.result[0].newText, expectedInlineFormat, "inline table function formatting");

  send({ jsonrpc: "2.0", id: requestId++, method: "shutdown", params: null });
  await waitFor((message) => message.id === requestId - 1, messages, 5000, "shutdown response");
  send({ jsonrpc: "2.0", method: "exit", params: null });

  await new Promise((resolve, reject) => {
    const timer = setTimeout(() => {
      server.kill();
      reject(new Error(`server did not exit cleanly; stderr=${stderr.join("")}`));
    }, 5000);
    server.on("exit", (code) => {
      clearTimeout(timer);
      fs.rmSync(fixtureRoot, { recursive: true, force: true });
      if (code === 0 || code === null) {
        resolve();
      } else {
        reject(new Error(`server exited with ${code}; stderr=${stderr.join("")}`));
      }
    });
  });
}

function filePathFromUri(uri) {
  assert(uri && uri.startsWith("file://"), `expected file uri, got ${uri}`);
  return decodeURIComponent(new URL(uri).pathname);
}

main().catch((error) => {
  console.error(error && error.stack ? error.stack : error);
  process.exit(1);
});

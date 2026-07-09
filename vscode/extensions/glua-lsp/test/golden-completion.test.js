"use strict";

const assert = require("assert");
const fs = require("fs");
const os = require("os");
const path = require("path");
const { spawn } = require("child_process");

const extensionRoot = path.resolve(__dirname, "..");
const repoRoot = path.resolve(extensionRoot, "..", "..", "..");
const serverPath = path.join(extensionRoot, "server", "index.js");
const goldenPath = path.join(repoRoot, "tests", "editor", "golden-completion.json");

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
    const deadline = setTimeout(() => reject(new Error(`timeout waiting for ${label}`)), timeoutMs);
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

function positionOf(source, marker, cursor) {
  const markerIndex = source.indexOf(marker);
  assert(markerIndex >= 0, `marker not found: ${marker}`);
  const cursorIndex = cursor === "after-marker" ? markerIndex + marker.length : markerIndex;
  const before = source.slice(0, cursorIndex);
  const lines = before.split("\n");
  return {
    line: lines.length - 1,
    character: lines[lines.length - 1].length,
  };
}

function writeFixtureFiles(root, files) {
  for (const [relativePath, content] of Object.entries(files || {})) {
    const target = path.join(root, relativePath);
    fs.mkdirSync(path.dirname(target), { recursive: true });
    fs.writeFileSync(target, content, "utf8");
  }
}

async function main() {
  const golden = JSON.parse(fs.readFileSync(goldenPath, "utf8"));
  const fixtureRoot = fs.mkdtempSync(path.join(os.tmpdir(), "glua-completion-"));
  writeFixtureFiles(fixtureRoot, golden.files);
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
    const documentPath = path.join(fixtureRoot, testCase.document || `${testCase.name.replace(/[^A-Za-z0-9_-]/g, "_")}.lua`);
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
    const completionId = requestId++;
    send({
      jsonrpc: "2.0",
      id: completionId,
      method: "textDocument/completion",
      params: {
        textDocument: { uri },
        position: positionOf(testCase.source, testCase.marker, testCase.cursor),
      },
    });
    const completion = await waitFor((message) => message.id === completionId, messages, 5000, `completion for ${testCase.name}`);
    const items = completion.result || [];
    const labels = items.map((item) => item.label);
    for (const expected of testCase.expected) {
      assert(labels.includes(expected), `${testCase.name} should include ${expected}`);
    }
    for (const unexpected of testCase.notExpected || []) {
      assert(!labels.includes(unexpected), `${testCase.name} should not include ${unexpected}`);
    }
    for (const [label, expectedDetail] of Object.entries(testCase.detailContains || {})) {
      const item = items.find((candidate) => candidate.label === label);
      assert(item, `${testCase.name} detail item ${label}`);
      assert(String(item.detail || "").includes(expectedDetail), `${testCase.name} ${label} detail should include ${expectedDetail}`);
    }
    for (const [label, expectedDocumentation] of Object.entries(testCase.documentationContains || {})) {
      const item = items.find((candidate) => candidate.label === label);
      assert(item, `${testCase.name} documentation item ${label}`);
      const documentation = item.documentation && item.documentation.value ? item.documentation.value : item.documentation;
      assert(String(documentation || "").includes(expectedDocumentation), `${testCase.name} ${label} documentation should include ${expectedDocumentation}`);
    }
    for (const [label, expectedInsertText] of Object.entries(testCase.insertTextContains || {})) {
      const item = items.find((candidate) => candidate.label === label);
      assert(item, `${testCase.name} insert text item ${label}`);
      const newText = item.textEdit ? item.textEdit.newText : item.insertText;
      assert(String(newText || "").includes(expectedInsertText), `${testCase.name} ${label} insert text should include ${expectedInsertText}`);
    }
  }

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

main().catch((error) => {
  console.error(error && error.stack ? error.stack : error);
  process.exit(1);
});

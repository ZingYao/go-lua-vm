"use strict";

const assert = require("assert");
const fs = require("fs");
const path = require("path");
const { spawn } = require("child_process");

const extensionRoot = path.resolve(__dirname, "..");
const repoRoot = path.resolve(extensionRoot, "..", "..", "..");
const serverPath = path.join(extensionRoot, "server", "index.js");
const goldenPath = path.join(repoRoot, "tests", "editor", "golden-diagnostics.json");

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

async function main() {
  const golden = JSON.parse(fs.readFileSync(goldenPath, "utf8"));
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
      rootUri: `file://${repoRoot}`,
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

  for (const testCase of golden.cases) {
    const uri = `file://${path.join(repoRoot, "tests", "editor", `${testCase.name.replace(/[^A-Za-z0-9_-]/g, "_")}.lua`)}`;
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
    const diagnosticMessage = await waitFor(
      (message) => message.method === "textDocument/publishDiagnostics" && message.params && message.params.uri === uri,
      messages,
      5000,
      `diagnostics for ${testCase.name}`
    );
    const actual = diagnosticMessage.params.diagnostics.map((diagnostic) => diagnostic.message);
    assert.deepStrictEqual(actual, testCase.diagnostics, `${testCase.name} diagnostics`);
  }

  send({ jsonrpc: "2.0", id: 2, method: "shutdown", params: null });
  await waitFor((message) => message.id === 2, messages, 5000, "shutdown response");
  send({ jsonrpc: "2.0", method: "exit", params: null });

  await new Promise((resolve, reject) => {
    const timer = setTimeout(() => {
      server.kill();
      reject(new Error(`server did not exit cleanly; stderr=${stderr.join("")}`));
    }, 5000);
    server.on("exit", (code) => {
      clearTimeout(timer);
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

"use strict";

const assert = require("assert");
const fs = require("fs");
const path = require("path");
const vm = require("vm");

function createIndexedDB() {
  const records = new Map();
  let created = false;
  const database = {
    objectStoreNames: { contains: () => created },
    createObjectStore: () => { created = true; },
    close: () => {},
    transaction: () => {
      const transaction = {};
      transaction.objectStore = () => ({
        get: (key) => {
          const request = {};
          queueMicrotask(() => {
            request.result = records.get(key);
            if (request.onsuccess) request.onsuccess();
            if (transaction.oncomplete) transaction.oncomplete();
          });
          return request;
        },
        put: (value, key) => {
          records.set(key, value);
          queueMicrotask(() => {
            if (transaction.oncomplete) transaction.oncomplete();
          });
        },
      });
      return transaction;
    },
  };
  return {
    records,
    open: () => {
      const request = {};
      queueMicrotask(() => {
        request.result = database;
        if (!created && request.onupgradeneeded) request.onupgradeneeded();
        if (request.onsuccess) request.onsuccess();
      });
      return request;
    },
  };
}

function createDirectory(name) {
  const files = new Map();
  return {
    name,
    permission: "granted",
    isSameEntry(other) { return Promise.resolve(other === this); },
    queryPermission() { return Promise.resolve(this.permission); },
    requestPermission() { this.permission = "granted"; return Promise.resolve("granted"); },
    async *values() {
      for (const fileName of files.keys()) yield { kind: "file", name: fileName };
    },
    async getDirectoryHandle() { throw new Error("nested directories are not used by this test"); },
    async getFileHandle(fileName, options) {
      if (!files.has(fileName) && !(options && options.create)) throw new Error("file not found: " + fileName);
      if (!files.has(fileName)) files.set(fileName, "");
      return {
        async getFile() {
          const source = files.get(fileName);
          return { size: Buffer.byteLength(source), text: async () => source };
        },
        async createWritable() {
          return {
            async write(source) { files.set(fileName, String(source)); },
            async close() {},
          };
        },
      };
    },
  };
}

async function main() {
  const indexedDB = createIndexedDB();
  const browserDirectory = createDirectory("glua-playground");
  const storageRoot = { getDirectoryHandle: async () => browserDirectory };
  const localDirectory = createDirectory("project");
  const window = {
    indexedDB,
    showDirectoryPicker: async () => localDirectory,
    crypto: { randomUUID: () => "workspace-id" },
  };
  const context = {
    window,
    navigator: { storage: { getDirectory: async () => storageRoot } },
    Blob,
    Map,
    Promise,
    Math,
    Date,
    String,
    Error,
    console,
    queueMicrotask,
  };
  vm.runInNewContext(
    fs.readFileSync(path.resolve(__dirname, "..", "docs", "assets", "playground-fs.js"), "utf8"),
    context
  );

  const first = new window.GLuaWorkspaceFileSystem();
  await first.initialize("print('browser')\n");
  assert.strictEqual(first.snapshot().mode, "opfs");
  await first.openLocalDirectory();
  assert.strictEqual(first.snapshot().workspaceID, "local:workspace-id");
  const firstSession = { activeDocumentPath: "main.glua", documents: [{ path: "main.glua" }] };
  const latestSession = { activeDocumentPath: "second.glua", documents: [{ path: "main.glua" }, { path: "second.glua" }] };
  await Promise.all([first.saveSession(firstSession), first.saveSession(latestSession)]);
  indexedDB.records.delete("session:local:workspace-id");

  const restored = new window.GLuaWorkspaceFileSystem();
  await restored.initialize();
  assert.strictEqual(restored.snapshot().mode, "local");
  assert.strictEqual(restored.snapshot().label, "本地工作区 · project");
  assert.deepStrictEqual(await restored.loadSession(), {
    activeDocumentPath: "second.glua",
    documents: [{ path: "main.glua" }, { path: "second.glua" }],
  });

  localDirectory.permission = "denied";
  await assert.rejects(restored.listFiles(), /重新授权/);
  assert.strictEqual(restored.snapshot().mode, "local-pending");
  await restored.requestLocalPermission();
  assert.strictEqual(restored.snapshot().needsPermission, false);

  localDirectory.permission = "denied";
  const revoked = new window.GLuaWorkspaceFileSystem();
  await revoked.initialize();
  assert.strictEqual(revoked.snapshot().mode, "local-pending");
  assert.strictEqual(revoked.snapshot().needsPermission, true);
  await revoked.requestLocalPermission();
  assert.strictEqual(revoked.snapshot().mode, "local");
  assert.strictEqual(revoked.snapshot().needsPermission, false);

  console.log("playground workspace persistence tests passed");
}

main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});

(function (global) {
  "use strict";

  var workspaceDirectoryName = "glua-playground";
  var maxWorkspaceEntries = 2000;
  var maxTextFileBytes = 2 * 1024 * 1024;
  var databaseName = "glua-playground-workspaces";
  var databaseStoreName = "state";
  var databaseVersion = 1;
  var lastWorkspaceKey = "last-workspace";
  var lastLocalSessionKey = "session:last-local-workspace";

  function openDatabase() {
    if (!global.indexedDB) return Promise.resolve(null);
    return new Promise(function (resolve, reject) {
      var request = global.indexedDB.open(databaseName, databaseVersion);
      request.onupgradeneeded = function () {
        if (!request.result.objectStoreNames.contains(databaseStoreName)) request.result.createObjectStore(databaseStoreName);
      };
      request.onsuccess = function () { resolve(request.result); };
      request.onerror = function () { reject(request.error || new Error("无法打开工作区数据库。")); };
    });
  }

  async function readDatabaseValue(key) {
    var database = await openDatabase();
    if (!database) return null;
    return new Promise(function (resolve, reject) {
      var transaction = database.transaction(databaseStoreName, "readonly");
      var request = transaction.objectStore(databaseStoreName).get(key);
      request.onsuccess = function () { resolve(typeof request.result === "undefined" ? null : request.result); };
      request.onerror = function () { reject(request.error || new Error("读取工作区状态失败。")); };
      transaction.oncomplete = function () { database.close(); };
      transaction.onabort = function () { database.close(); };
    });
  }

  async function writeDatabaseValue(key, value) {
    var database = await openDatabase();
    if (!database) return false;
    return new Promise(function (resolve, reject) {
      var transaction = database.transaction(databaseStoreName, "readwrite");
      transaction.objectStore(databaseStoreName).put(value, key);
      transaction.oncomplete = function () { database.close(); resolve(true); };
      transaction.onerror = function () { database.close(); reject(transaction.error || new Error("保存工作区状态失败。")); };
      transaction.onabort = function () { database.close(); reject(transaction.error || new Error("保存工作区状态已取消。")); };
    });
  }

  function createWorkspaceID() {
    if (global.crypto && typeof global.crypto.randomUUID === "function") return global.crypto.randomUUID();
    return Date.now().toString(36) + "-" + Math.random().toString(36).slice(2);
  }

  function normalizePath(path) {
    var normalized = String(path || "").replace(/\\/g, "/").replace(/^\/+|\/+$/g, "");
    if (!normalized || normalized.split("/").some(function (part) { return !part || part === "." || part === ".."; })) {
      throw new Error("非法工作区路径：" + path);
    }
    return normalized;
  }

  function textFileName(path) {
    return /\.(?:lua|glua|txt|json|md|toml|yaml|yml)$/i.test(path);
  }

  function memoryDirectory() {
    return new Map();
  }

  function WorkspaceFileSystem() {
    this.mode = "memory";
    this.label = "临时工作区";
    this.root = null;
    this.memory = memoryDirectory();
    this.workspaceID = "memory";
    this.needsPermission = false;
    this.restoreError = "";
    this.sessionWriteQueue = Promise.resolve(true);
  }

  WorkspaceFileSystem.prototype.initialize = async function (defaultSource) {
    var savedWorkspace = null;
    try {
      savedWorkspace = await readDatabaseValue(lastWorkspaceKey);
    } catch (error) {
      this.restoreError = "读取上次工作区失败：" + String(error);
    }
    if (savedWorkspace && savedWorkspace.mode === "local" && savedWorkspace.handle) {
      await this.restoreLocalDirectory(savedWorkspace);
    } else if (navigator.storage && typeof navigator.storage.getDirectory === "function") {
      await this.useBrowserWorkspace();
    }
    if (!this.needsPermission && (this.mode === "opfs" || this.mode === "memory")) {
      var files = await this.listFiles();
      if (files.length === 0) await this.writeFile("main.glua", defaultSource || "print(\"Hello, GLua!\")\n");
    }
    return this.snapshot();
  };

  WorkspaceFileSystem.prototype.snapshot = function () {
    return {
      mode: this.mode,
      label: this.label,
      writable: !this.needsPermission,
      workspaceID: this.workspaceID,
      needsPermission: this.needsPermission,
      restoreError: this.restoreError,
    };
  };

  WorkspaceFileSystem.prototype.useBrowserWorkspace = async function (persist) {
    if (!navigator.storage || typeof navigator.storage.getDirectory !== "function") {
      this.mode = "memory";
      this.label = "临时工作区";
      this.root = null;
      this.workspaceID = "memory";
      this.needsPermission = false;
      this.restoreError = "";
      return this.snapshot();
    }
    var storageRoot = await navigator.storage.getDirectory();
    this.root = await storageRoot.getDirectoryHandle(workspaceDirectoryName, { create: true });
    this.mode = "opfs";
    this.label = "浏览器工作区 · " + workspaceDirectoryName;
    this.workspaceID = "opfs:" + workspaceDirectoryName;
    this.needsPermission = false;
    this.restoreError = "";
    if (persist !== false) await this.persistWorkspace({ mode: "opfs" });
    return this.snapshot();
  };

  WorkspaceFileSystem.prototype.openLocalDirectory = async function () {
    if (typeof global.showDirectoryPicker !== "function") {
      throw new Error("当前浏览器不支持打开本地目录，请使用浏览器工作区或 Chromium 内核浏览器。");
    }
    var directory = await global.showDirectoryPicker({ mode: "readwrite", id: "glua-workspace" });
    if (directory.requestPermission && await directory.requestPermission({ mode: "readwrite" }) !== "granted") {
      throw new Error("未获得本地目录读写权限。");
    }
    this.root = directory;
    this.mode = "local";
    this.workspaceID = "local:" + createWorkspaceID();
    this.label = "本地工作区 · " + (directory.name || "本地目录");
    this.needsPermission = false;
    this.restoreError = "";
    await this.persistWorkspace({
      mode: "local",
      handle: directory,
      name: directory.name || "本地目录",
      workspaceID: this.workspaceID,
    });
    return this.snapshot();
  };

  WorkspaceFileSystem.prototype.restoreLocalDirectory = async function (savedWorkspace) {
    this.root = savedWorkspace.handle;
    this.mode = "local";
    this.workspaceID = savedWorkspace.workspaceID || "local:" + createWorkspaceID();
    this.label = "本地工作区 · " + (savedWorkspace.name || this.root.name || "本地目录");
    var permission = "prompt";
    try {
      if (this.root.queryPermission) permission = await this.root.queryPermission({ mode: "readwrite" });
    } catch (error) {
      permission = "denied";
    }
    if (permission !== "granted") {
      this.mode = "local-pending";
      this.needsPermission = true;
      this.restoreError = "本地工作区权限已失效，请点击“重新授权”。";
      return this.snapshot();
    }
    this.needsPermission = false;
    this.restoreError = "";
    await this.persistWorkspace({
      mode: "local",
      handle: this.root,
      name: savedWorkspace.name || this.root.name || "本地目录",
      workspaceID: this.workspaceID,
    });
    return this.snapshot();
  };

  WorkspaceFileSystem.prototype.requestLocalPermission = async function () {
    if (!this.root || this.mode !== "local-pending") throw new Error("没有等待重新授权的本地工作区。请重新打开目录。");
    var permission = this.root.requestPermission ? await this.root.requestPermission({ mode: "readwrite" }) : "denied";
    if (permission !== "granted") {
      this.needsPermission = true;
      this.restoreError = "本地工作区授权失败，请允许读写权限后重试。";
      throw new Error(this.restoreError);
    }
    this.mode = "local";
    this.needsPermission = false;
    this.restoreError = "";
    this.label = "本地工作区 · " + (this.root.name || "本地目录");
    await this.persistWorkspace({
      mode: "local",
      handle: this.root,
      name: this.root.name || "本地目录",
      workspaceID: this.workspaceID,
    });
    return this.snapshot();
  };

  WorkspaceFileSystem.prototype.persistWorkspace = async function (workspace) {
    try {
      await writeDatabaseValue(lastWorkspaceKey, workspace);
    } catch (error) {
      this.restoreError = "保存工作区记录失败：" + String(error);
    }
  };

  WorkspaceFileSystem.prototype.loadSession = async function () {
    try {
      var session = await readDatabaseValue("session:" + this.workspaceID);
      if (session) return session;
      if ((this.mode !== "local" && this.mode !== "local-pending") || !this.root) return null;
      var recentLocalSession = await readDatabaseValue(lastLocalSessionKey);
      if (!recentLocalSession || !recentLocalSession.handle || !recentLocalSession.session) return null;
      if (!this.root.isSameEntry || !await this.root.isSameEntry(recentLocalSession.handle)) return null;
      return recentLocalSession.session;
    } catch (error) {
      this.restoreError = "读取编辑会话失败：" + String(error);
      return null;
    }
  };

  WorkspaceFileSystem.prototype.saveSession = async function (session) {
    var sessionKey = "session:" + this.workspaceID;
    var localWorkspace = this.mode === "local" || this.mode === "local-pending";
    var localHandle = this.root;
    var workspaceID = this.workspaceID;
    this.sessionWriteQueue = this.sessionWriteQueue.then(async function () {
      try {
        var saved = await writeDatabaseValue(sessionKey, session);
        if (!saved) throw new Error("当前浏览器不支持 IndexedDB 持久化。");
        if (localWorkspace && localHandle) {
          await writeDatabaseValue(lastLocalSessionKey, {
            handle: localHandle,
            workspaceID: workspaceID,
            session: session,
          });
        }
        return true;
      } catch (error) {
        this.restoreError = "保存编辑会话失败：" + String(error);
        return false;
      }
    }.bind(this));
    return this.sessionWriteQueue;
  };

  WorkspaceFileSystem.prototype.ensurePermission = async function () {
    if (this.mode === "local-pending" || this.needsPermission) throw new Error("本地工作区权限已失效，请点击“重新授权”。");
    if (this.mode !== "local" || !this.root || !this.root.queryPermission) return;
    var permission = await this.root.queryPermission({ mode: "readwrite" });
    if (permission === "granted") return;
    this.mode = "local-pending";
    this.needsPermission = true;
    this.restoreError = "本地工作区权限已失效，请点击“重新授权”。";
    throw new Error(this.restoreError);
  };

  WorkspaceFileSystem.prototype.listFiles = async function () {
    await this.ensurePermission();
    if (this.mode === "memory") return Array.from(this.memory.keys()).sort();
    var files = [];
    await walkDirectory(this.root, "", files);
    return files.sort();
  };

  async function walkDirectory(directory, prefix, files) {
    for await (var entry of directory.values()) {
      if (files.length >= maxWorkspaceEntries) throw new Error("工作区文件数量超过 " + maxWorkspaceEntries + " 个限制。");
      var path = prefix ? prefix + "/" + entry.name : entry.name;
      if (entry.kind === "directory") await walkDirectory(entry, path, files);
      else if (textFileName(path)) files.push(path);
    }
  }

  WorkspaceFileSystem.prototype.readFile = async function (path) {
    path = normalizePath(path);
    await this.ensurePermission();
    if (this.mode === "memory") {
      if (!this.memory.has(path)) throw new Error("文件不存在：" + path);
      return this.memory.get(path);
    }
    var fileHandle = await resolveFile(this.root, path, false);
    var file = await fileHandle.getFile();
    if (file.size > maxTextFileBytes) throw new Error("文件超过 2 MiB 编辑限制：" + path);
    var text = await file.text();
    if (text.indexOf("\u0000") !== -1) throw new Error("拒绝打开二进制文件：" + path);
    return text;
  };

  WorkspaceFileSystem.prototype.writeFile = async function (path, source) {
    path = normalizePath(path);
    await this.ensurePermission();
    if (new Blob([source]).size > maxTextFileBytes) throw new Error("文件超过 2 MiB 编辑限制：" + path);
    if (this.mode === "memory") {
      this.memory.set(path, source);
      return;
    }
    var fileHandle = await resolveFile(this.root, path, true);
    var writable = await fileHandle.createWritable();
    await writable.write(source);
    await writable.close();
  };

  WorkspaceFileSystem.prototype.deleteFile = async function (path) {
    path = normalizePath(path);
    await this.ensurePermission();
    if (this.mode === "memory") {
      this.memory.delete(path);
      return;
    }
    var parts = path.split("/");
    var fileName = parts.pop();
    var parent = await resolveDirectory(this.root, parts, false);
    await parent.removeEntry(fileName);
  };

  WorkspaceFileSystem.prototype.renameFile = async function (oldPath, newPath) {
    oldPath = normalizePath(oldPath);
    newPath = normalizePath(newPath);
    var source = await this.readFile(oldPath);
    await this.writeFile(newPath, source);
    await this.deleteFile(oldPath);
  };

  async function resolveFile(root, path, create) {
    var parts = path.split("/");
    var fileName = parts.pop();
    var directory = await resolveDirectory(root, parts, create);
    return directory.getFileHandle(fileName, { create: create });
  }

  async function resolveDirectory(root, parts, create) {
    var directory = root;
    for (var index = 0; index < parts.length; index += 1) {
      directory = await directory.getDirectoryHandle(parts[index], { create: create });
    }
    return directory;
  }

  global.GLuaWorkspaceFileSystem = WorkspaceFileSystem;
})(window);

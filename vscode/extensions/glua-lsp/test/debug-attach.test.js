"use strict";

const assert = require("assert");
const Module = require("module");

const originalLoad = Module._load;
const messages = [];
const debugConsoleText = [];
const executedCommands = [];
let startDebuggingImpl = async () => true;
const configValues = {
  executable: "",
  dapHost: "127.0.0.1",
  dapPort: 5678,
  useRemoteDap: false,
};

const vscodeMock = {
  env: {
    language: "en",
  },
  debug: {
    activeDebugConsole: {
      append(text) {
        debugConsoleText.push(text);
      },
    },
    startDebugging(folder, config) {
      return startDebuggingImpl(folder, config);
    },
    onDidTerminateDebugSession() {
      return { dispose() {} };
    },
  },
  window: {
    activeTextEditor: null,
    showErrorMessage(message) {
      messages.push(message);
      return Promise.resolve(undefined);
    },
  },
  workspace: {
    workspaceFolders: [],
    getConfiguration(section) {
      assert.strictEqual(section, "glua");
      return {
        get(key, fallback) {
          return Object.prototype.hasOwnProperty.call(configValues, key) ? configValues[key] : fallback;
        },
      };
    },
    getWorkspaceFolder() {
      return null;
    },
  },
  commands: {
    executeCommand(command) {
      executedCommands.push(command);
      return Promise.resolve(undefined);
    },
  },
  Selection: class Selection {
    constructor(anchor, active) {
      this.anchor = anchor;
      this.active = active;
    }
  },
};

Module._load = function loadWithVscodeMock(request, parent, isMain) {
  if (request === "vscode") {
    return vscodeMock;
  }
  return originalLoad.call(this, request, parent, isMain);
};

const extension = require("../extension");
Module._load = originalLoad;

async function main() {
  const outputLines = [];
  const outputChannel = {
    append(text) {
      outputLines.push(text);
    },
    appendLine(line) {
      outputLines.push(line);
    },
    show() {
      outputLines.push("[show]");
    },
  };
  const attachConfig = {
    host: "127.0.0.1",
    port: 5678,
  };

  startDebuggingImpl = async () => true;
  executedCommands.length = 0;
  assert.strictEqual(await extension._test.startAttachDebugSession(attachConfig, outputChannel), true);
  assert.deepStrictEqual(messages, []);
  assert(executedCommands.includes("workbench.panel.repl.view.focus"), "successful debug start should focus Debug Console");

  startDebuggingImpl = async () => false;
  executedCommands.length = 0;
  assert.strictEqual(await extension._test.startAttachDebugSession(attachConfig, outputChannel), false);
  assert(messages.pop().includes("127.0.0.1:5678"), "false return should show host/port failure");
  assert(!executedCommands.includes("workbench.panel.repl.view.focus"), "failed debug start should not switch to Debug Console");

  startDebuggingImpl = async () => {
    throw new Error("ECONNREFUSED");
  };
  assert.strictEqual(await extension._test.startAttachDebugSession(attachConfig, outputChannel), false);
  const errorMessage = messages.pop();
  assert(errorMessage.includes("ECONNREFUSED"), "thrown error should be included in user message");
  assert(errorMessage.includes("DAP server"), "failure message should include recovery hint");

  assert.deepStrictEqual(extension._test.blockEnterExpansion("for i = 1, 3 do", 17), {
    text: "\n  \nend",
    caretDelta: 3,
  });
  assert.deepStrictEqual(extension._test.blockEnterExpansion("  value = function ()", 21), {
    text: "\n    \n  end",
    caretDelta: 5,
  });
  assert.deepStrictEqual(extension._test.blockEnterExpansion("switch value do", 15), {
    text: "\n  case \n    \nend",
    caretDelta: 8,
  });
  assert.deepStrictEqual(extension._test.blockEnterExpansion("  case 1, 2", 11), {
    text: "\n    ",
    caretDelta: 5,
  });
  assert.deepStrictEqual(extension._test.blockEnterExpansion("  default", 9), {
    text: "\n    ",
    caretDelta: 5,
  });
  assert.deepStrictEqual(extension._test.blockEnterExpansion("repeat", 6), {
    text: "\n  \nuntil ",
    caretDelta: 3,
  });
  assert.strictEqual(extension._test.blockEnterExpansion("local value = 1", 15), null);

  assert.deepStrictEqual(extension._test.parseGluaDapReadyLine("GLua DAP server listening on 127.0.0.1:65019\n"), {
    host: "127.0.0.1",
    port: 65019,
  });
  assert.strictEqual(extension._test.parseGluaDapReadyLine("missing ready"), null);
  assert.strictEqual(extension._test.isManagedProcessControlLine("GLua DAP client configured; starting script.\n"), true);
  assert.strictEqual(extension._test.isManagedProcessControlLine("hello,圈圈\n"), false);

  debugConsoleText.length = 0;
  outputLines.length = 0;
  const managedOutputRouter = extension._test.createManagedProcessOutputRouter(outputChannel);
  managedOutputRouter.route("GLua DAP server listening on 127.0.0.1:65019\n", "stderr", false);
  managedOutputRouter.route("hello,圈圈\n", "stdout", true);
  managedOutputRouter.route("GLua DAP client configured; starting script.\n", "stderr", true);
  managedOutputRouter.flush(true);
  assert(outputLines.includes("GLua DAP server listening on 127.0.0.1:65019\n"), "ready line should stay in output channel");
  assert(outputLines.includes("GLua DAP client configured; starting script.\n"), "DAP control line should stay in output channel");
  assert.deepStrictEqual(debugConsoleText, ["hello,圈圈\n"], "script stdout should go to debug console");

  assert.strictEqual(extension._test.isGluaDebugSession({ type: "glua" }), true);
  assert.strictEqual(extension._test.isGluaDebugSession({ type: "node" }), false);
  executedCommands.length = 0;
  extension._test.focusDebugConsoleForSession({ type: "node" });
  assert.deepStrictEqual(executedCommands, [], "non-GLua debug sessions should not focus Debug Console");
  extension._test.focusDebugConsoleForSession({ type: "glua" });
  assert(executedCommands.includes("workbench.panel.repl.view.focus"), "GLua debug sessions should focus Debug Console");

  const managedFailure = extension._test.managedDebugFailureMessage(
    { gluaExecutable: "/bin/glua", cwd: "/tmp/project" },
    {
      command: "/bin/glua --glua-dap-listen=127.0.0.1:0 main.glua",
      cwd: "/tmp/project",
      listen: "127.0.0.1:0",
      stderr: "boom",
      exitCode: 2,
    }
  );
  assert(managedFailure.includes("command=/bin/glua"), "managed failure should include command");
  assert(managedFailure.includes("cwd=/tmp/project"), "managed failure should include cwd");
  assert(managedFailure.includes("listen=127.0.0.1:0"), "managed failure should include listen address");
  assert(managedFailure.includes("stderr=boom"), "managed failure should include stderr tail");

  const debuggedConfigs = [];
  startDebuggingImpl = async (folder, config) => {
    debuggedConfigs.push(config);
    return true;
  };
  vscodeMock.window.activeTextEditor = {
    document: {
      uri: { scheme: "file", fsPath: "/tmp/project/main.glua" },
      languageId: "glua",
      isDirty: false,
      save() {
        return Promise.resolve(true);
      },
    },
  };
  configValues.executable = "/bin/glua";
  configValues.useRemoteDap = false;
  assert.strictEqual(await extension._test.debugCurrentFile(outputChannel), true);
  assert.strictEqual(debuggedConfigs.pop().request, "launch", "configured glua executable should launch by default");

  const workspaceFolder = { uri: { fsPath: "/tmp/project" } };
  const emptyLocalConfig = extension._test.normalizeDebugConfiguration(workspaceFolder, {}, outputChannel);
  assert.strictEqual(emptyLocalConfig.request, "launch", "empty debug config should launch locally when glua.useRemoteDap is false");
  assert.strictEqual(emptyLocalConfig.gluaExecutable, "/bin/glua");
  assert.strictEqual(emptyLocalConfig.cwd, "/tmp/project");
  assert.strictEqual(emptyLocalConfig.port, 0);

  configValues.executable = "";
  const missingExecutableConfig = extension._test.normalizeDebugConfiguration(workspaceFolder, { request: "launch" }, outputChannel);
  assert.strictEqual(missingExecutableConfig, undefined, "local launch without executable should not fall back to attach");
  assert(messages.pop().includes("glua.executable"), "missing executable should explain the required setting");

  configValues.executable = "/bin/glua";
  configValues.useRemoteDap = true;
  configValues.dapHost = "10.0.0.9";
  configValues.dapPort = 4567;
  assert.strictEqual(await extension._test.debugCurrentFile(outputChannel), true);
  const remoteConfig = debuggedConfigs.pop();
  assert.strictEqual(remoteConfig.request, "attach", "glua.useRemoteDap should force attach mode");
  assert.strictEqual(remoteConfig.host, "10.0.0.9");
  assert.strictEqual(remoteConfig.port, 4567);
  assert.strictEqual(remoteConfig.gluaExecutable, "");

  const emptyRemoteConfig = extension._test.normalizeDebugConfiguration(workspaceFolder, {}, outputChannel);
  assert.strictEqual(emptyRemoteConfig.request, "attach", "empty debug config should attach only when glua.useRemoteDap is true");
  assert.strictEqual(emptyRemoteConfig.host, "10.0.0.9");
  assert.strictEqual(emptyRemoteConfig.port, 4567);
}

main().catch((error) => {
  console.error(error && error.stack ? error.stack : error);
  process.exit(1);
});

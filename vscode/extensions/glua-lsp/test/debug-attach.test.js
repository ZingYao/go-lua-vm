"use strict";

const assert = require("assert");
const Module = require("module");

const originalLoad = Module._load;
const messages = [];
let startDebuggingImpl = async () => true;

const vscodeMock = {
  env: {
    language: "en",
  },
  debug: {
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
  commands: {
    executeCommand() {
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
  assert.strictEqual(await extension._test.startAttachDebugSession(attachConfig, outputChannel), true);
  assert.deepStrictEqual(messages, []);

  startDebuggingImpl = async () => false;
  assert.strictEqual(await extension._test.startAttachDebugSession(attachConfig, outputChannel), false);
  assert(messages.pop().includes("127.0.0.1:5678"), "false return should show host/port failure");

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
}

main().catch((error) => {
  console.error(error && error.stack ? error.stack : error);
  process.exit(1);
});

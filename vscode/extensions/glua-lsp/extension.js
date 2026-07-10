const path = require("path");
const fs = require("fs");
const { spawn } = require("child_process");
const vscode = require("vscode");
const { resolveGlualsExecutable } = require("./gluals-resolver");
const {
  getBuiltinFunction,
  makeBuiltinStubContent,
  setBuiltinLocale,
  getBuiltinLocale,
  applyBuiltinExtensionCatalog,
  resetBuiltinExtensions,
} = require("./server/builtin-functions");

const builtinDocScheme = "glua-builtin";
const DEFAULT_DOC_LANGUAGE = "auto";
const COMMAND_OPEN_BUILTIN_SIGNATURE_JSON = "glua.openBuiltinSignatureJson";
const COMMAND_SHOW_BUILTIN_DOC_STATUS = "glua.showBuiltinDocStatus";
const COMMAND_SHOW_OUTPUT = "glua.showOutput";
const COMMAND_CREATE_ATTACH_CONFIG = "glua.createAttachConfig";
const COMMAND_START_ATTACH_DEBUG = "glua.startAttachDebug";
const COMMAND_RUN_CURRENT_FILE = "glua.runCurrentFile";
const COMMAND_DEBUG_CURRENT_FILE = "glua.debugCurrentFile";
const COMMAND_SELECT_GLUA_EXECUTABLE = "glua.selectGluaExecutable";
const COMMAND_SELECT_GLUAC_EXECUTABLE = "glua.selectGluacExecutable";
const COMMAND_SELECT_BUILTIN_DOCS = "glua.selectBuiltinDocs";
const BUILTIN_SIG_FILE_NAME = "glua-builtin-docs.json";
const DEBUG_TYPE = "glua";
const DEFAULT_DEBUG_HOST = "127.0.0.1";
const DEFAULT_DEBUG_PORT = 5678;
const DEFAULT_DAP_READY_TIMEOUT_MS = 5000;
const GLUA_DAP_READY_PREFIX = "GLua DAP server listening on ";
const managedDebugProcesses = new Map();
let managedDebugProcessSeq = 0;

function isChineseEnvironment() {
  return String(vscode.env.language || "").toLowerCase().startsWith("zh");
}

function localizeText(texts) {
  const key = isChineseEnvironment() ? "zh" : "en";
  return texts[key] || texts.en || "";
}

function buildBuiltinSignatureTemplate() {
  const template = {
    _demo: {
      description: {
        "en": "This file overrides builtin function docs/signatures for glua language server. Keep `functions` for your method map.",
        "zh-CN": "该文件用于覆盖/扩展 glua language server 的内置方法签名与说明；请在 `functions` 下配置目标方法。",
      },
      steps: {
        "en": [
          "1) Edit [\"string.some\"] entry.",
          "2) Add to settings: glua.builtinDocs = [\".vscode/glua-builtin-docs.json\"] (workspace path relative).",
          "3) Reload window.",
        ],
        "zh-CN": [
          "1) 修改 `functions` 中对应方法配置。",
          "2) 在设置中配置：glua.builtinDocs = [\".vscode/glua-builtin-docs.json\"]。",
          "3) 重载窗口。",
        ],
      },
      supportedLocales: ["en", "zh-CN"],
    },
    "functions": {
      "string.match": {
        "signature": {
          "en": "string.match(s, pattern [, init])",
          "zh-CN": "string.match(s, pattern [, init])",
        },
        "returns": {
          "en": "returns matching substring(s) or nil.",
          "zh-CN": "返回匹配子串，未匹配返回 nil。",
        },
        "params": {
          "en": [
            "s: string",
            "pattern: pattern string",
            "init?: number",
          ],
          "zh-CN": [
            "s: 字符串",
            "pattern: 模式",
            "init?: 起始下标",
          ],
        },
        "description": {
          "en": "Finds the first match of pattern in string.",
          "zh-CN": "查找字符串中第一个匹配片段。",
        },
        "example": {
          "en": "local first, second = string.match('Hello, world!', '(.-),(.-)!')",
          "zh-CN": "local first, second = string.match('Hello, world!', '(.-),(.-)!')",
        },
      },
    },
  };
  return `${JSON.stringify(template, null, 2)}\n`;
}

function getWorkspaceBuiltinSignaturePath(context) {
  const workspaceFolders = vscode.workspace.workspaceFolders || [];
  if (workspaceFolders.length === 0) {
    return "";
  }
  return path.join(workspaceFolders[0].uri.fsPath, ".vscode", BUILTIN_SIG_FILE_NAME);
}

function getGlobalBuiltinSignaturePath(context) {
  const globalRoot = context.globalStorageUri ? context.globalStorageUri.fsPath : "";
  if (!globalRoot) {
    return "";
  }
  return path.join(globalRoot, BUILTIN_SIG_FILE_NAME);
}

async function openOrCreateBuiltinSignatureFile(context, scope) {
  const workspacePath = getWorkspaceBuiltinSignaturePath(context);
  const globalPath = getGlobalBuiltinSignaturePath(context);
  let targetPath = "";
  let locationLabel = "";

  if (scope === "workspace") {
    targetPath = workspacePath;
    locationLabel = localizeText({ en: "project", zh: "项目级" });
  } else if (scope === "global") {
    targetPath = globalPath;
    locationLabel = localizeText({ en: "global", zh: "全局级" });
  } else if (workspacePath) {
    targetPath = workspacePath;
    locationLabel = localizeText({ en: "project", zh: "项目级" });
  } else if (globalPath) {
    targetPath = globalPath;
    locationLabel = localizeText({ en: "global", zh: "全局级" });
  }

  if (!targetPath) {
    vscode.window.showErrorMessage(
      localizeText({ en: "glua-lsp: Cannot resolve builtin signature file location.", zh: "glua-lsp: 无法定位内置方法签名文件目录。" })
    );
    return;
  }

  if (scope === "workspace" && !workspacePath) {
    const fallback = await vscode.window.showWarningMessage(
      localizeText({ en: "No workspace opened. Switch to global signature file instead.", zh: "当前未打开工作区，已自动切换到全局签名文件" }),
      localizeText({ en: "Continue", zh: "继续" })
    );
    if (!fallback) {
      return;
    }
  }

  const dir = path.dirname(targetPath);
  if (!fs.existsSync(dir)) {
    fs.mkdirSync(dir, { recursive: true });
  }

  if (!fs.existsSync(targetPath)) {
    fs.writeFileSync(targetPath, buildBuiltinSignatureTemplate(), "utf8");
    vscode.window.showInformationMessage(
      localizeText({ en: `glua-lsp: Created ${locationLabel} signature JSON: ${targetPath}`, zh: `glua-lsp: 已创建${locationLabel}签名 JSON：${targetPath}` })
    );
  }

  if (scope === "workspace") {
    const config = vscode.workspace.getConfiguration("glua");
    const currentDocs = config.get("builtinDocs", []);
    const docPath = path.join(".vscode", BUILTIN_SIG_FILE_NAME);
    if (Array.isArray(currentDocs) && !currentDocs.includes(docPath)) {
      await config.update("builtinDocs", [...currentDocs, docPath], vscode.ConfigurationTarget.Workspace);
    }
  }

  const document = await vscode.workspace.openTextDocument(vscode.Uri.file(targetPath));
  await vscode.window.showTextDocument(document, { preview: false });
}

function looksLikeLocale(rawLocale) {
  const raw = String(rawLocale || "").toLowerCase();
  return /^[a-z]{2,3}([_-][a-z0-9]{2,8}){0,3}$/.test(raw);
}

function resolveDocLanguage(value, uiLanguage = "en") {
  const raw = String(value || DEFAULT_DOC_LANGUAGE).toLowerCase();
  if (raw === "auto") {
    return uiLanguage;
  }
  return raw;
}

function parseBuiltinExtCatalogFromFile(filePath) {
  if (!filePath || !fs.existsSync(filePath)) {
    return null;
  }
  const inferLocaleFromFilePath = (resolvedPath) => {
    if (!resolvedPath) {
      return "";
    }
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
    const text = fs.readFileSync(filePath, "utf8");
    const parsed = JSON.parse(text);
    if (!parsed || typeof parsed !== "object") {
      return null;
    }

    const isLocaleMap = (candidate) => {
      if (!candidate || typeof candidate !== "object" || Array.isArray(candidate)) {
        return false;
      }
      const keys = Object.keys(candidate);
      if (keys.length === 0) {
        return false;
      }
      return keys.every((key) => looksLikeLocale(key));
    };

    const source = parsed.functions || parsed.builtins || parsed;
    if (!source || typeof source !== "object") {
      return null;
    }
    const candidateBuckets = isLocaleMap(source)
      ? Object.entries(source).map(([locale, definitions]) => ({ locale, definitions }))
      : [];

    const catalogLocale = parsed.locale || parsed.language || inferLocaleFromFilePath(filePath);
    if (candidateBuckets.length > 0) {
      return {
        catalog: candidateBuckets,
        locale: catalogLocale,
      };
    }

    return {
      catalog: source,
      locale: catalogLocale,
    };
  } catch (error) {
    vscode.window.showWarningMessage(`glua-lsp: failed to parse builtin docs ${filePath}, ${error && error.message ? error.message : error}`);
    return null;
  }
}

function resolveBuiltinDocPaths(rawPaths) {
  const workspaceFolders = vscode.workspace.workspaceFolders || [];
  const workspaceRoot = workspaceFolders.length > 0 ? workspaceFolders[0].uri.fsPath : process.cwd();
  const items = Array.isArray(rawPaths) ? rawPaths : [];
  const abs = [];
  for (const raw of items) {
    const candidate = String(raw || "").trim();
    if (!candidate) {
      continue;
    }
    const resolved = path.isAbsolute(candidate) ? candidate : path.join(workspaceRoot, candidate);
    if (fs.existsSync(resolved)) {
      abs.push(resolved);
    }
  }
  return abs;
}

async function promptAndOpenBuiltinSignatureFile(context) {
  const workspacePath = getWorkspaceBuiltinSignaturePath(context);
  const globalPath = getGlobalBuiltinSignaturePath(context);

  const options = [];
  if (workspacePath) {
    options.push({
      label: localizeText({ en: "Create or open project signature JSON", zh: "打开/创建项目级签名文件" }),
      description: workspacePath,
      value: "workspace",
    });
  }
  if (globalPath) {
    options.push({
      label: localizeText({ en: "Create or open global signature JSON", zh: "打开/创建全局级签名文件" }),
      description: globalPath,
      value: "global",
    });
  }

  if (options.length === 0) {
    vscode.window.showErrorMessage(
      localizeText({ en: "glua-lsp: Cannot resolve available signature file path.", zh: "glua-lsp: 无法解析可用签名文件路径" })
    );
    return;
  }

  const scope = options.length === 1 ? options[0].value : (await vscode.window.showQuickPick(options, {
    placeHolder: localizeText({ en: "Choose scope to open or create", zh: "选择要打开/创建的签名 JSON 文件范围" }),
  }))?.value;

  if (!scope) {
    return;
  }

  await openOrCreateBuiltinSignatureFile(context, scope);
}

function applyBuiltinDocsFromConfig(config) {
  const configuredLanguage = config.get("docLanguage", DEFAULT_DOC_LANGUAGE);
  const language = resolveDocLanguage(configuredLanguage, vscode.env.language);
  setBuiltinLocale(language);
  resetBuiltinExtensions();
  const resolvedLanguage = getBuiltinLocale();
  const docs = resolveBuiltinDocPaths(config.get("builtinDocs", []));
  for (const file of docs) {
    const payload = parseBuiltinExtCatalogFromFile(file);
    if (!payload) {
      continue;
    }
    if (Array.isArray(payload.catalog)) {
      for (const bucket of payload.catalog) {
        if (!bucket || typeof bucket !== "object") {
          continue;
        }
        applyBuiltinExtensionCatalog(bucket.definitions, bucket.locale || payload.locale);
      }
      continue;
    }
    if (payload.catalog) {
      applyBuiltinExtensionCatalog(payload.catalog, payload.locale);
    }
  }
  return { language, resolvedLanguage, configuredLanguage, uiLanguage: vscode.env.language, docs };
}

function logResolvedDocLanguage(outputChannel, stage, docConfig) {
  if (!outputChannel || !docConfig) {
    return;
  }
  outputChannel.appendLine(
    `[glua-lsp] ${stage}: vscode.env.language=${docConfig.uiLanguage}; glua.docLanguage=${docConfig.configuredLanguage}; requested doc language=${docConfig.language}; resolved doc language=${docConfig.resolvedLanguage}; builtin docs=${docConfig.docs.length}`
  );
}

function resolveDebugHost(value) {
  const host = String(value || "").trim();
  return host || DEFAULT_DEBUG_HOST;
}

function resolveDebugPort(value) {
  const port = Number(value);
  if (Number.isInteger(port) && port >= 1 && port <= 65535) {
    return port;
  }
  return DEFAULT_DEBUG_PORT;
}

function isValidDebugPort(value) {
  const port = Number(value);
  return Number.isInteger(port) && port >= 1 && port <= 65535;
}

function defaultDebugAttachConfig() {
  return {
    type: DEBUG_TYPE,
    request: "attach",
    name: "Attach to GLua DAP",
    host: dapHostConfig(),
    port: dapPortConfig(),
  };
}

function localLaunchExecutableMissingMessage() {
  return localizeText({
    en: "glua.executable is empty. Configure a glua executable for local launch, or enable glua.useRemoteDap for attach debugging.",
    zh: "glua.executable 为空。请配置 glua 可执行文件以本地启动，或启用 glua.useRemoteDap 使用远程 attach 调试。",
  });
}

function defaultDebugLaunchConfig(program = "${file}", cwd = undefined) {
  return {
    type: DEBUG_TYPE,
    request: "launch",
    name: localizeText({ en: "Debug current GLua file", zh: "调试当前 GLua 文件" }),
    program,
    gluaExecutable: gluaExecutableConfig(),
    useRemoteDap: false,
    args: [],
    cwd,
    host: DEFAULT_DEBUG_HOST,
    port: 0,
  };
}

function publicDebugConfig(config) {
  return config;
}

function gluaExecutableConfig() {
  const config = vscode.workspace.getConfiguration("glua");
  return String(config.get("executable", "") || "").trim();
}

function dapHostConfig() {
  const config = vscode.workspace.getConfiguration("glua");
  return resolveDebugHost(config.get("dapHost", DEFAULT_DEBUG_HOST));
}

function dapPortConfig() {
  const config = vscode.workspace.getConfiguration("glua");
  return resolveDebugPort(config.get("dapPort", DEFAULT_DEBUG_PORT));
}

function useRemoteDapConfig() {
  const config = vscode.workspace.getConfiguration("glua");
  return config.get("useRemoteDap", false) === true;
}

async function selectExecutableSetting(settingKey, title, outputChannel) {
  const selected = await vscode.window.showOpenDialog({
    title,
    canSelectFiles: true,
    canSelectFolders: false,
    canSelectMany: false,
    openLabel: localizeText({ en: "Use executable", zh: "使用该可执行文件" }),
  });
  const file = selected && selected[0] ? selected[0].fsPath : "";
  if (!file) {
    return;
  }
  await vscode.workspace.getConfiguration("glua").update(settingKey, file, vscode.ConfigurationTarget.Workspace);
  if (outputChannel) {
    outputChannel.appendLine(`[glua-lsp] set glua.${settingKey}=${file}`);
  }
}

async function selectBuiltinDocsSetting(outputChannel) {
  const selected = await vscode.window.showOpenDialog({
    title: localizeText({ en: "Select builtin docs JSON", zh: "选择内置文档 JSON 文件" }),
    canSelectFiles: true,
    canSelectFolders: false,
    canSelectMany: true,
    filters: {
      JSON: ["json"],
    },
    openLabel: localizeText({ en: "Use JSON file", zh: "使用 JSON 文件" }),
  });
  if (!selected || selected.length === 0) {
    return;
  }
  const config = vscode.workspace.getConfiguration("glua");
  const currentDocs = config.get("builtinDocs", []);
  const nextDocs = Array.isArray(currentDocs) ? [...currentDocs] : [];
  for (const uri of selected) {
    const file = uri && uri.fsPath ? uri.fsPath : "";
    if (file && !nextDocs.includes(file)) {
      nextDocs.push(file);
    }
  }
  await config.update("builtinDocs", nextDocs, vscode.ConfigurationTarget.Workspace);
  if (outputChannel) {
    outputChannel.appendLine(`[glua-lsp] set glua.builtinDocs=${JSON.stringify(nextDocs)}`);
  }
}

async function createAttachDebugConfiguration(outputChannel) {
  const workspaceFolder = (vscode.workspace.workspaceFolders || [])[0];
  const target = workspaceFolder
    ? vscode.Uri.joinPath(workspaceFolder.uri, ".vscode", "launch.json")
    : null;
  const attachConfig = publicDebugConfig(defaultDebugAttachConfig());
  const snippet = JSON.stringify(attachConfig, null, 2);
  if (!target) {
    await vscode.env.clipboard.writeText(snippet);
    vscode.window.showInformationMessage(
      localizeText({
        en: "GLua DAP attach configuration copied to clipboard.",
        zh: "GLua DAP attach 配置已复制到剪贴板。",
      })
    );
    return;
  }

  const launchTemplate = `${JSON.stringify({
    version: "0.2.0",
    configurations: [attachConfig],
  }, null, 2)}\n`;
  await vscode.workspace.fs.createDirectory(vscode.Uri.joinPath(workspaceFolder.uri, ".vscode"));
  try {
    await vscode.workspace.fs.stat(target);
    const document = await vscode.workspace.openTextDocument(target);
    await vscode.window.showTextDocument(document, { preview: false });
    await vscode.env.clipboard.writeText(snippet);
    vscode.window.showInformationMessage(
      localizeText({
        en: "launch.json already exists. GLua DAP attach configuration copied to clipboard.",
        zh: "launch.json 已存在，GLua DAP attach 配置已复制到剪贴板。",
      })
    );
  } catch (error) {
    await vscode.workspace.fs.writeFile(target, Buffer.from(launchTemplate, "utf8"));
    const document = await vscode.workspace.openTextDocument(target);
    await vscode.window.showTextDocument(document, { preview: false });
    if (outputChannel) {
      outputChannel.appendLine(`[glua-lsp] created debug attach configuration at ${target.fsPath}`);
    }
  }
}

async function promptAndStartAttachDebug(outputChannel) {
  const defaults = defaultDebugAttachConfig();
  if (outputChannel) {
    outputChannel.appendLine(`[glua-dap] start attach host=${defaults.host}; port=${defaults.port}`);
  }
  await startAttachDebugSession(defaults, outputChannel);
}

function debugAttachFailureMessage(attachConfig, error) {
  const reason = error && error.message ? ` ${error.message}` : "";
  return localizeText({
    en: `GLua DAP attach failed for ${attachConfig.host}:${attachConfig.port}.${reason} Check that glua was started with its DAP server enabled and that host/port are reachable.`,
    zh: `GLua DAP attach 连接 ${attachConfig.host}:${attachConfig.port} 失败。${reason}请确认 glua 已启用 DAP server，且 host/port 可访问。`,
  });
}

function tailText(text, maxLength = 4000) {
  const value = String(text || "");
  if (value.length <= maxLength) {
    return value;
  }
  return value.slice(value.length - maxLength);
}

function parseGluaDapReadyLine(text) {
  const lines = String(text || "").split(/\r?\n/);
  for (const line of lines) {
    const index = line.indexOf(GLUA_DAP_READY_PREFIX);
    if (index < 0) {
      continue;
    }
    const address = line.slice(index + GLUA_DAP_READY_PREFIX.length).trim();
    const match = address.match(/^(.+):(\d+)$/);
    if (!match) {
      continue;
    }
    const port = Number(match[2]);
    if (!Number.isInteger(port) || port < 1 || port > 65535) {
      continue;
    }
    return {
      host: match[1],
      port,
    };
  }
  return null;
}

function isManagedProcessControlLine(line) {
  const trimmed = String(line || "").trim();
  return trimmed.startsWith("GLua DAP ");
}

function appendDebugConsole(text) {
  const debugConsole = vscode.debug && vscode.debug.activeDebugConsole;
  if (!debugConsole || !text) {
    return false;
  }
  if (typeof debugConsole.append === "function") {
    debugConsole.append(text);
    return true;
  }
  if (typeof debugConsole.appendLine === "function") {
    debugConsole.appendLine(String(text).replace(/\r?\n$/, ""));
    return true;
  }
  return false;
}

async function showDebugConsole() {
  const commands = [
    "workbench.panel.repl.view.focus",
    "workbench.debug.action.focusRepl",
    "workbench.debug.action.toggleRepl",
  ];
  for (const command of commands) {
    try {
      await vscode.commands.executeCommand(command);
      return true;
    } catch (error) {
      // Older or embedded VS Code builds may not expose every command; try the next known command id.
    }
  }
  return false;
}

function scheduleDebugConsoleFocus() {
  void showDebugConsole();
  const shortTimer = setTimeout(() => {
    void showDebugConsole();
  }, 250);
  const longTimer = setTimeout(() => {
    void showDebugConsole();
  }, 800);
  for (const timer of [shortTimer, longTimer]) {
    if (timer && typeof timer.unref === "function") {
      timer.unref();
    }
  }
}

function isGluaDebugSession(session) {
  return !!(session && session.type === DEBUG_TYPE);
}

function focusDebugConsoleForSession(session) {
  if (!isGluaDebugSession(session)) {
    return;
  }
  scheduleDebugConsoleFocus();
}

function createManagedProcessOutputRouter(outputChannel) {
  const pending = {
    stdout: "",
    stderr: "",
  };
  const appendOutput = (text) => {
    if (outputChannel && text) {
      outputChannel.append(text);
    }
  };
  const routeCompleteLine = (lineText, debugStarted) => {
    if (!debugStarted || isManagedProcessControlLine(lineText)) {
      appendOutput(lineText);
      return;
    }
    if (!appendDebugConsole(lineText)) {
      appendOutput(lineText);
    }
  };
  const route = (text, streamName, debugStarted) => {
    const key = streamName === "stderr" ? "stderr" : "stdout";
    pending[key] += String(text || "");
    for (;;) {
      const match = pending[key].match(/\r?\n/);
      if (!match || match.index === undefined) {
        return;
      }
      const endIndex = match.index + match[0].length;
      const lineText = pending[key].slice(0, endIndex);
      pending[key] = pending[key].slice(endIndex);
      routeCompleteLine(lineText, debugStarted);
    }
  };
  const flush = (debugStarted) => {
    for (const key of Object.keys(pending)) {
      if (!pending[key]) {
        continue;
      }
      routeCompleteLine(pending[key], debugStarted);
      pending[key] = "";
    }
  };
  return { route, flush };
}

function managedDebugFailureMessage(launchConfig, details) {
  const stderrTail = tailText(details.stderr || "").trim();
  const stdoutTail = tailText(details.stdout || "").trim();
  const parts = [
    localizeText({ en: "Failed to start GLua debug session.", zh: "启动 GLua Debug 会话失败。" }),
    `command=${details.command || launchConfig.gluaExecutable || configuredGluaExecutable()}`,
    `cwd=${details.cwd || launchConfig.cwd || ""}`,
    `listen=${details.listen || `${DEFAULT_DEBUG_HOST}:0`}`,
  ];
  if (details.exitCode !== undefined || details.signal) {
    parts.push(`exit=${details.exitCode === null || details.exitCode === undefined ? "null" : details.exitCode}${details.signal ? ` signal=${details.signal}` : ""}`);
  }
  if (details.error) {
    parts.push(`error=${details.error.message || details.error}`);
  }
  if (stderrTail) {
    parts.push(`stderr=${stderrTail}`);
  }
  if (stdoutTail) {
    parts.push(`stdout=${stdoutTail}`);
  }
  return parts.join(" | ");
}

function normalizeLaunchArgs(args) {
  return Array.isArray(args) ? args.map((arg) => String(arg)) : [];
}

function startManagedGluaDapProcess(launchConfig, outputChannel) {
  const executable = String(launchConfig.gluaExecutable || gluaExecutableConfig() || "").trim();
  if (!executable) {
    throw new Error(localLaunchExecutableMissingMessage());
  }
  const program = String(launchConfig.program || "").trim();
  const cwd = String(launchConfig.cwd || (program ? path.dirname(program) : "") || process.cwd());
  const listen = `${DEFAULT_DEBUG_HOST}:0`;
  const args = [
    `--glua-dap-listen=${listen}`,
    program,
    ...normalizeLaunchArgs(launchConfig.args),
  ].filter(Boolean);
  const commandText = `${executable} ${args.map((arg) => JSON.stringify(arg)).join(" ")}`;
  const processId = `glua-${Date.now()}-${++managedDebugProcessSeq}`;
  let stdout = "";
  let stderr = "";
  let settled = false;
  const outputRouter = createManagedProcessOutputRouter(outputChannel);
  if (outputChannel) {
    outputChannel.appendLine(`[glua-dap] launch command=${commandText}; cwd=${cwd}; listen=${listen}`);
  }
  return new Promise((resolve, reject) => {
    const child = spawn(executable, args, { cwd, shell: false });
    const fail = (details) => {
      if (settled) {
        return;
      }
      settled = true;
      clearTimeout(timer);
      try {
        child.kill();
      } catch (error) {
        // Ignore kill errors; the process may already have exited.
      }
      outputRouter.flush(false);
      const message = managedDebugFailureMessage(launchConfig, {
        command: commandText,
        cwd,
        listen,
        stdout,
        stderr,
        ...details,
      });
      if (outputChannel) {
        outputChannel.appendLine(`[glua-dap] ${message}`);
        outputChannel.show(true);
      }
      reject(new Error(message));
    };
    const timer = setTimeout(() => {
      fail({ error: new Error(`Timed out waiting for '${GLUA_DAP_READY_PREFIX}'`) });
    }, DEFAULT_DAP_READY_TIMEOUT_MS);
    const handleOutput = (chunk, streamName) => {
      const text = chunk.toString("utf8");
      if (streamName === "stderr") {
        stderr = tailText(stderr + text);
      } else {
        stdout = tailText(stdout + text);
      }
      outputRouter.route(text, streamName, settled);
      const ready = parseGluaDapReadyLine(`${stdout}\n${stderr}`);
      if (!ready || settled) {
        return;
      }
      settled = true;
      clearTimeout(timer);
      managedDebugProcesses.set(processId, child);
      if (outputChannel) {
        outputChannel.appendLine(`[glua-dap] ready host=${ready.host}; port=${ready.port}; process=${processId}`);
      }
      resolve({
        host: ready.host,
        port: ready.port,
        processId,
        child,
      });
    };
    child.stdout.on("data", (chunk) => handleOutput(chunk, "stdout"));
    child.stderr.on("data", (chunk) => handleOutput(chunk, "stderr"));
    child.on("error", (error) => fail({ error }));
    child.on("exit", (exitCode, signal) => {
      managedDebugProcesses.delete(processId);
      outputRouter.flush(settled);
      if (settled) {
        if (outputChannel) {
          outputChannel.appendLine(`[glua-dap] process exited code=${exitCode === null ? "null" : exitCode}${signal ? ` signal=${signal}` : ""}`);
        }
        return;
      }
      fail({ exitCode, signal });
    });
  });
}

function stopManagedDebugProcess(processId) {
  if (!processId) {
    return;
  }
  const child = managedDebugProcesses.get(processId);
  managedDebugProcesses.delete(processId);
  if (!child || child.killed) {
    return;
  }
  try {
    child.kill();
  } catch (error) {
    // Ignore cleanup errors; VS Code is already ending the debug session.
  }
}

function currentGluaDocument() {
  const editor = vscode.window.activeTextEditor;
  if (!editor || !editor.document || editor.document.uri.scheme !== "file") {
    return null;
  }
  const languageId = editor.document.languageId;
  const filePath = editor.document.uri.fsPath;
  if (languageId !== "glua" && languageId !== "lua" && !filePath.endsWith(".glua") && !filePath.endsWith(".lua")) {
    return null;
  }
  return editor.document;
}

function configuredGluaExecutable() {
  return gluaExecutableConfig() || "glua";
}

async function runCurrentFile(outputChannel) {
  const document = currentGluaDocument();
  if (!document) {
    vscode.window.showWarningMessage(localizeText({ en: "Open a .glua or .lua file first.", zh: "请先打开 .glua 或 .lua 文件。" }));
    return false;
  }
  if (document.isDirty) {
    await document.save();
  }
  const executable = configuredGluaExecutable();
  const cwd = vscode.workspace.getWorkspaceFolder(document.uri)?.uri.fsPath || path.dirname(document.uri.fsPath);
  if (outputChannel) {
    outputChannel.show(true);
    outputChannel.appendLine(`[glua-run] ${executable} ${document.uri.fsPath}`);
  }
  const child = spawn(executable, [document.uri.fsPath], { cwd, shell: false });
  child.stdout.on("data", (chunk) => outputChannel && outputChannel.append(chunk.toString("utf8")));
  child.stderr.on("data", (chunk) => outputChannel && outputChannel.append(chunk.toString("utf8")));
  child.on("error", (error) => {
    const message = localizeText({
      en: `Failed to run glua: ${error.message}. Configure glua.executable first.`,
      zh: `运行 glua 失败：${error.message}。请先配置 glua.executable。`,
    });
    if (outputChannel) {
      outputChannel.appendLine(`[glua-run] ${message}`);
    }
    vscode.window.showErrorMessage(message);
  });
  child.on("exit", (code, signal) => {
    if (outputChannel) {
      outputChannel.appendLine(`[glua-run] exited code=${code === null ? "null" : code}${signal ? ` signal=${signal}` : ""}`);
    }
  });
  return true;
}

async function debugCurrentFile(outputChannel) {
  const document = currentGluaDocument();
  if (!document) {
    vscode.window.showWarningMessage(localizeText({ en: "Open a .glua or .lua file first.", zh: "请先打开 .glua 或 .lua 文件。" }));
    return false;
  }
  if (document.isDirty) {
    await document.save();
  }
  const workspaceFolder = vscode.workspace.getWorkspaceFolder(document.uri);
  const executable = gluaExecutableConfig();
  const useRemoteDap = useRemoteDapConfig();
  const config = useRemoteDap
    ? {
      ...defaultDebugAttachConfig(),
      name: localizeText({ en: "Attach to GLua DAP", zh: "附加到 GLua DAP" }),
      program: document.uri.fsPath,
      gluaExecutable: "",
      useRemoteDap: true,
      args: [],
      cwd: workspaceFolder ? workspaceFolder.uri.fsPath : path.dirname(document.uri.fsPath),
    }
    : {
      ...defaultDebugLaunchConfig(document.uri.fsPath, workspaceFolder ? workspaceFolder.uri.fsPath : path.dirname(document.uri.fsPath)),
      gluaExecutable: executable,
    };
  if (outputChannel) {
    outputChannel.appendLine(`[glua-dap] debug current file=${document.uri.fsPath}; request=${config.request}; glua=${config.gluaExecutable || "(remote)"}`);
  }
  return startAttachDebugSession(config, outputChannel);
}

function blockEnterExpansion(source, offset) {
  const safeOffset = Math.max(0, Math.min(Number(offset) || 0, source.length));
  const lineStart = source.lastIndexOf("\n", Math.max(0, safeOffset - 1)) + 1;
  const lineBeforeCaret = source.slice(lineStart, safeOffset);
  const trimmed = lineBeforeCaret.trim();
  if (!trimmed) {
    return null;
  }
  const indent = (lineBeforeCaret.match(/^\s*/) || [""])[0];
  const expansionText = (closeText) => {
    const innerIndent = `${indent}  `;
    return {
      text: `\n${innerIndent}\n${indent}${closeText}`,
      caretDelta: 1 + innerIndent.length,
    };
  };
  if (/^switch\b.*\bdo\s*$/.test(trimmed)) {
    const caseIndent = `${indent}  `;
    const bodyIndent = `${indent}    `;
    return {
      text: `\n${caseIndent}case \n${bodyIndent}\n${indent}end`,
      caretDelta: 1 + caseIndent.length + "case ".length,
    };
  }
  if (/^(case\b.+|default)\s*$/.test(trimmed)) {
    const bodyIndent = `${indent}  `;
    return {
      text: `\n${bodyIndent}`,
      caretDelta: 1 + bodyIndent.length,
    };
  }
  if (trimmed === "repeat") {
    return expansionText("until ");
  }
  if (
    (trimmed.endsWith(" do") && !/^switch\b/.test(trimmed))
    || trimmed.endsWith(" then")
    || /^(local\s+)?function\b.*\)\s*$/.test(trimmed)
    || /.*=\s*function\s*\([^)]*\)\s*$/.test(trimmed)
    || /.*\bfunction\s*\([^)]*\)\s*$/.test(trimmed)
  ) {
    return expansionText("end");
  }
  return null;
}

async function handleTypeCommand(args) {
  const text = args && typeof args.text === "string" ? args.text : "";
  const editor = vscode.window.activeTextEditor;
  if (text !== "\n" || !editor || !editor.selection || !editor.selection.isEmpty) {
    return vscode.commands.executeCommand("default:type", args);
  }
  const languageId = editor.document.languageId;
  if (languageId !== "glua" && languageId !== "lua") {
    return vscode.commands.executeCommand("default:type", args);
  }
  const offset = editor.document.offsetAt(editor.selection.active);
  const expansion = blockEnterExpansion(editor.document.getText(), offset);
  if (!expansion) {
    return vscode.commands.executeCommand("default:type", args);
  }
  const insertPosition = editor.selection.active;
  await editor.edit((editBuilder) => {
    editBuilder.insert(insertPosition, expansion.text);
  });
  const caretOffset = offset + expansion.caretDelta;
  const caretPosition = editor.document.positionAt(caretOffset);
  editor.selection = new vscode.Selection(caretPosition, caretPosition);
  return undefined;
}

async function startAttachDebugSession(attachConfig, outputChannel) {
  try {
    const started = await vscode.debug.startDebugging(undefined, attachConfig);
    if (started) {
      scheduleDebugConsoleFocus();
      return true;
    }
    const message = debugAttachFailureMessage(attachConfig);
    if (outputChannel) {
      outputChannel.appendLine(`[glua-dap] ${message}`);
      outputChannel.show(true);
    }
    vscode.window.showErrorMessage(message);
    return false;
  } catch (error) {
    const message = debugAttachFailureMessage(attachConfig, error);
    if (outputChannel) {
      outputChannel.appendLine(`[glua-dap] ${message}`);
      outputChannel.show(true);
    }
    vscode.window.showErrorMessage(message);
    return false;
  }
}

function normalizeDebugConfiguration(folder, config, outputChannel) {
  const hasExplicitConfig = config && Object.keys(config).length > 0;
  if (!hasExplicitConfig) {
    if (useRemoteDapConfig()) {
      return defaultDebugAttachConfig();
    }
    return defaultDebugLaunchConfig("${file}", folder && folder.uri ? folder.uri.fsPath : undefined);
  }

  const defaults = config.request === "launch"
    ? defaultDebugLaunchConfig("${file}", folder && folder.uri ? folder.uri.fsPath : undefined)
    : defaultDebugAttachConfig();
  const next = {
    ...defaults,
    ...(config || {}),
  };
  next.type = DEBUG_TYPE;
  next.request = next.request === "launch" ? "launch" : "attach";
  if (next.request === "launch") {
    if (next.useRemoteDap === true) {
      next.request = "attach";
      next.host = resolveDebugHost(next.host || dapHostConfig());
      next.port = resolveDebugPort(next.port || dapPortConfig());
      return next;
    }
    next.gluaExecutable = String(next.gluaExecutable || gluaExecutableConfig() || "").trim();
    if (!next.gluaExecutable) {
      const message = localLaunchExecutableMissingMessage();
      if (outputChannel) {
        outputChannel.appendLine(`[glua-dap] ${message}`);
        outputChannel.show(true);
      }
      vscode.window.showErrorMessage(message);
      return undefined;
    }
    next.program = next.program || "${file}";
    next.args = Array.isArray(next.args) ? next.args : [];
    next.cwd = next.cwd || (folder && folder.uri ? folder.uri.fsPath : undefined);
    next.host = DEFAULT_DEBUG_HOST;
    next.port = 0;
    return next;
  }
  next.host = resolveDebugHost(next.host || dapHostConfig());
  next.port = resolveDebugPort(next.port || dapPortConfig());
  return next;
}

function registerDebugSupport(context, outputChannel) {
  const configurationProvider = {
    resolveDebugConfiguration(folder, config) {
      return normalizeDebugConfiguration(folder, config, outputChannel);
    },
  };

  const descriptorFactory = {
    async createDebugAdapterDescriptor(session) {
      let host = resolveDebugHost(session.configuration.host);
      let port = resolveDebugPort(session.configuration.port);
      if (session.configuration.request === "launch") {
        const launchResult = await startManagedGluaDapProcess(session.configuration, outputChannel);
        host = launchResult.host || DEFAULT_DEBUG_HOST;
        port = launchResult.port;
        session.configuration.__gluaManagedProcessId = launchResult.processId;
      }
      if (outputChannel) {
        outputChannel.appendLine(`[glua-dap] ${session.configuration.request || "attach"} host=${host}; port=${port}; glua=${session.configuration.gluaExecutable || gluaExecutableConfig() || "(not set)"}`);
      }
      return new vscode.DebugAdapterServer(port, host);
    },
  };

  const debugSubscriptions = [
    vscode.debug.registerDebugConfigurationProvider(DEBUG_TYPE, configurationProvider),
    vscode.debug.registerDebugAdapterDescriptorFactory(DEBUG_TYPE, descriptorFactory),
    vscode.debug.onDidStartDebugSession((session) => {
      focusDebugConsoleForSession(session);
    }),
    vscode.debug.onDidTerminateDebugSession((session) => {
      stopManagedDebugProcess(session.configuration && session.configuration.__gluaManagedProcessId);
    }),
  ];
  if (typeof vscode.debug.onDidChangeActiveDebugSession === "function") {
    debugSubscriptions.push(vscode.debug.onDidChangeActiveDebugSession((session) => {
      focusDebugConsoleForSession(session);
    }));
  }
  context.subscriptions.push(...debugSubscriptions);
}

function registerBuiltinDocumentProvider(context) {
  const provider = {
    provideTextDocumentContent(uri) {
      const name = path.basename(uri.path, ".lua");
      const info = getBuiltinFunction(name);
      if (!info) {
        return "-- builtin doc unavailable\n";
      }
      return makeBuiltinStubContent(name, info);
    },
  };

  const openHook = vscode.workspace.onDidOpenTextDocument(async (document) => {
    if (document.uri.scheme !== builtinDocScheme) {
      return;
    }
    await vscode.languages.setTextDocumentLanguage(document, "glua");
  });

  context.subscriptions.push(vscode.workspace.registerTextDocumentContentProvider(builtinDocScheme, provider));
  context.subscriptions.push(openHook);
}

let client;
let lastDocConfig = null;
let outputChannelRef = null;

const GLUA_TEXTMATE_COLOR_RULES = [
  {
    name: "GLua keyword",
    scope: [
      "keyword.control.glua",
      "keyword.operator.glua",
      "storage.type.function.glua",
      "storage.modifier.local.glua",
    ],
    settings: {
      foreground: "#CC7832",
    },
  },
  {
    name: "GLua function",
    scope: [
      "entity.name.function.glua",
      "entity.name.function.call.glua",
      "entity.name.function.member.glua",
    ],
    settings: {
      foreground: "#56A8F5",
    },
  },
  {
    name: "GLua library",
    scope: [
      "entity.name.type.library.glua",
      "support.type.library.glua",
      "support.class.glua",
    ],
    settings: {
      foreground: "#4EC9B0",
    },
  },
  {
    name: "GLua string",
    scope: [
      "string.quoted.single.glua",
      "string.quoted.double.glua",
      "string.quoted.long-bracket.glua",
    ],
    settings: {
      foreground: "#6A8759",
    },
  },
  {
    name: "GLua number",
    scope: [
      "constant.numeric.glua",
    ],
    settings: {
      foreground: "#6897BB",
    },
  },
];

const GLUA_SEMANTIC_COLOR_RULES = {
  "keyword:glua": "#CC7832",
  "function:glua": "#56A8F5",
  "method:glua": "#56A8F5",
  "namespace:glua": "#4EC9B0",
};

async function applyGluaEditorColors(outputChannel) {
  const config = vscode.workspace.getConfiguration();
  const tokenColors = config.get("editor.tokenColorCustomizations") || {};
  const currentRules = Array.isArray(tokenColors.textMateRules) ? tokenColors.textMateRules : [];
  const preservedRules = currentRules.filter((rule) => !rule || typeof rule.name !== "string" || !rule.name.startsWith("GLua "));
  const nextTokenColors = {
    ...tokenColors,
    textMateRules: [
      ...preservedRules,
      ...GLUA_TEXTMATE_COLOR_RULES,
    ],
  };

  const semanticColors = config.get("editor.semanticTokenColorCustomizations") || {};
  const nextSemanticColors = {
    ...semanticColors,
    enabled: true,
    rules: {
      ...(semanticColors.rules || {}),
      ...GLUA_SEMANTIC_COLOR_RULES,
    },
  };

  await config.update("editor.tokenColorCustomizations", nextTokenColors, vscode.ConfigurationTarget.Workspace);
  await config.update("editor.semanticTokenColorCustomizations", nextSemanticColors, vscode.ConfigurationTarget.Workspace);
  await config.update("editor.semanticHighlighting.enabled", true, vscode.ConfigurationTarget.Workspace);
  if (outputChannel) {
    outputChannel.appendLine("[glua-lsp] applied workspace GLua editor color rules");
  }
}

function activate(context) {
  const outputChannel = vscode.window.createOutputChannel("glua Language Server");
  outputChannelRef = outputChannel;
  context.subscriptions.push(outputChannel);
  outputChannel.appendLine(`[glua-lsp] activate extensionPath=${context.extensionPath}`);
  applyGluaEditorColors(outputChannel).catch((error) => {
    outputChannel.appendLine(`[glua-lsp] failed to apply editor color rules: ${error && error.message ? error.message : error}`);
  });

  context.subscriptions.push(
    vscode.commands.registerCommand(COMMAND_OPEN_BUILTIN_SIGNATURE_JSON, () =>
      promptAndOpenBuiltinSignatureFile(context)
    )
  );
  context.subscriptions.push(
    vscode.commands.registerCommand(COMMAND_SHOW_BUILTIN_DOC_STATUS, () => {
      if (outputChannelRef) {
        outputChannelRef.show(true);
      }
      const config = lastDocConfig || applyBuiltinDocsFromConfig(vscode.workspace.getConfiguration("glua"));
      const docs = config.docs.length === 0 ? "(none)" : config.docs.join(", ");
      const message = `glua-lsp docs: vscode.env.language=${config.uiLanguage}; glua.docLanguage=${config.configuredLanguage}; resolved=${config.resolvedLanguage}; builtinDocs=${docs}`;
      if (outputChannelRef) {
        outputChannelRef.appendLine(`[glua-lsp] status: ${message}`);
      }
      vscode.window.showInformationMessage(message);
    })
  );
  context.subscriptions.push(
    vscode.commands.registerCommand(COMMAND_SHOW_OUTPUT, () => {
      if (outputChannelRef) {
        outputChannelRef.show(true);
        outputChannelRef.appendLine("[glua-lsp] output requested");
      } else {
        vscode.window.showWarningMessage("glua-lsp output channel is not initialized");
      }
    })
  );
  context.subscriptions.push(
    vscode.commands.registerCommand(COMMAND_CREATE_ATTACH_CONFIG, () =>
      createAttachDebugConfiguration(outputChannel)
    )
  );
  context.subscriptions.push(
    vscode.commands.registerCommand(COMMAND_START_ATTACH_DEBUG, () =>
      promptAndStartAttachDebug(outputChannel)
    )
  );
  context.subscriptions.push(
    vscode.commands.registerCommand(COMMAND_RUN_CURRENT_FILE, () =>
      runCurrentFile(outputChannel)
    )
  );
  context.subscriptions.push(
    vscode.commands.registerCommand(COMMAND_DEBUG_CURRENT_FILE, () =>
      debugCurrentFile(outputChannel)
    )
  );
  context.subscriptions.push(
    vscode.commands.registerCommand(COMMAND_SELECT_GLUA_EXECUTABLE, () =>
      selectExecutableSetting("executable", "Select glua executable", outputChannel)
    )
  );
  context.subscriptions.push(
    vscode.commands.registerCommand(COMMAND_SELECT_GLUAC_EXECUTABLE, () =>
      selectExecutableSetting("gluacExecutable", "Select gluac executable", outputChannel)
    )
  );
  context.subscriptions.push(
    vscode.commands.registerCommand(COMMAND_SELECT_BUILTIN_DOCS, () =>
      selectBuiltinDocsSetting(outputChannel)
    )
  );
  context.subscriptions.push(vscode.commands.registerCommand("type", handleTypeCommand));

  registerBuiltinDocumentProvider(context);
  registerDebugSupport(context, outputChannel);

  let languageClientApi;
  try {
    languageClientApi = require("vscode-languageclient/node");
  } catch (error) {
    const message = `glua-lsp: failed to load vscode-languageclient. Run npm install in ${context.extensionPath}. ${error && error.message ? error.message : error}`;
    outputChannel.appendLine(`[glua-lsp] ${message}`);
    outputChannel.show(true);
    vscode.window.showErrorMessage(message);
    return;
  }
  const { LanguageClient } = languageClientApi;

  const config = vscode.workspace.getConfiguration("glua");
  const docConfig = applyBuiltinDocsFromConfig(config);
  lastDocConfig = docConfig;
  logResolvedDocLanguage(outputChannel, "activate", docConfig);
  const syntax = config.get("syntax", "extended");
  const events = config.get("events", true);
  let gluals;
  try {
    gluals = resolveGlualsExecutable(context.extensionPath, config.get("languageServerExecutable", ""));
  } catch (error) {
    const message = `glua-lsp: ${error && error.message ? error.message : error}`;
    outputChannel.appendLine(`[glua-lsp] ${message}`);
    outputChannel.show(true);
    vscode.window.showErrorMessage(message);
    return;
  }

  const serverOptions = {
    command: gluals.path,
    args: [
      "--gluals-syntax", syntax,
      "--gluals-builtin-docs", path.join(context.extensionPath, "server", "builtin-functions.json"),
      ...docConfig.docs.flatMap((file) => ["--gluals-builtin-docs", file]),
    ],
    options: { cwd: context.extensionPath },
  };

  const clientOptions = {
    documentSelector: [
      { scheme: "file", language: "glua" },
      { scheme: "file", language: "lua" },
      { scheme: "untitled", language: "glua" },
      { scheme: "untitled", language: "lua" },
    ],
    initializationOptions: {
      syntax,
      events,
      locale: docConfig.language,
      resolvedLocale: docConfig.resolvedLanguage,
      builtinExtensions: docConfig.docs,
    },
    outputChannel,
    traceOutputChannel: outputChannel,
  };

  client = new LanguageClient(
    "glua-lsp",
    "glua Language Server",
    serverOptions,
    clientOptions
  );

  context.subscriptions.push(client);
  outputChannel.appendLine(`[glua-lsp] starting gluals=${gluals.path}; bundled=${gluals.bundled}; syntax=${syntax}`);
  client.start().then(
    () => outputChannel.appendLine("[glua-lsp] language client started"),
    (error) => {
      const message = `glua-lsp: failed to start language client: ${error && error.message ? error.message : error}`;
      outputChannel.appendLine(`[glua-lsp] ${message}`);
      outputChannel.show(true);
      vscode.window.showErrorMessage(message);
    }
  );

  vscode.workspace.onDidChangeConfiguration((event) => {
    if (!event.affectsConfiguration("glua")) {
      return;
    }
    const current = vscode.workspace.getConfiguration("glua");
    const updated = applyBuiltinDocsFromConfig(current);
    lastDocConfig = updated;
    logResolvedDocLanguage(outputChannel, "configuration changed", updated);
    if (client) {
      client.sendNotification("workspace/didChangeConfiguration", {
        settings: {
          glua: {
            docLanguage: updated.language,
            resolvedDocLanguage: updated.resolvedLanguage,
            builtinDocs: updated.docs,
          },
        },
      });
    }
  });
}

function deactivate() {
  if (!client) {
    return undefined;
  }
  return client.stop();
}

module.exports = {
  activate,
  deactivate,
  _test: {
    debugAttachFailureMessage,
    startAttachDebugSession,
    runCurrentFile,
    debugCurrentFile,
    parseGluaDapReadyLine,
    managedDebugFailureMessage,
    isManagedProcessControlLine,
    createManagedProcessOutputRouter,
    showDebugConsole,
    scheduleDebugConsoleFocus,
    isGluaDebugSession,
    focusDebugConsoleForSession,
    normalizeDebugConfiguration,
    blockEnterExpansion,
    useRemoteDapConfig,
  },
};

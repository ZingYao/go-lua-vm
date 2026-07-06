const path = require("path");
const fs = require("fs");
const vscode = require("vscode");
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
const BUILTIN_SIG_FILE_NAME = "glua-builtin-docs.json";
const DEBUG_TYPE = "glua";
const DEFAULT_DEBUG_HOST = "127.0.0.1";
const DEFAULT_DEBUG_PORT = 5678;

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
  const config = vscode.workspace.getConfiguration("glua");
  return {
    type: DEBUG_TYPE,
    request: "attach",
    name: "Attach to GLua DAP",
    host: resolveDebugHost(config.get("debug.host", DEFAULT_DEBUG_HOST)),
    port: resolveDebugPort(config.get("debug.port", DEFAULT_DEBUG_PORT)),
  };
}

async function createAttachDebugConfiguration(outputChannel) {
  const workspaceFolder = (vscode.workspace.workspaceFolders || [])[0];
  const target = workspaceFolder
    ? vscode.Uri.joinPath(workspaceFolder.uri, ".vscode", "launch.json")
    : null;
  const attachConfig = defaultDebugAttachConfig();
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
  const host = await vscode.window.showInputBox({
    title: "GLua DAP Attach Host",
    prompt: localizeText({
      en: "Enter the IP address or host name of the running GLua DAP server.",
      zh: "输入正在运行的 GLua DAP server IP 或主机名。",
    }),
    value: defaults.host,
    validateInput(value) {
      return String(value || "").trim()
        ? null
        : localizeText({ en: "Host is required.", zh: "必须填写 Host。" });
    },
  });
  if (!host) {
    return;
  }

  const portText = await vscode.window.showInputBox({
    title: "GLua DAP Attach Port",
    prompt: localizeText({
      en: "Enter the TCP port of the running GLua DAP server.",
      zh: "输入正在运行的 GLua DAP server TCP 端口。",
    }),
    value: String(defaults.port),
    validateInput(value) {
      return isValidDebugPort(Number(value))
        ? null
        : localizeText({ en: "Port must be a number from 1 to 65535.", zh: "端口必须是 1 到 65535 的数字。" });
    },
  });
  if (!portText) {
    return;
  }

  const attachConfig = {
    ...defaults,
    host: resolveDebugHost(host),
    port: resolveDebugPort(Number(portText)),
  };
  if (outputChannel) {
    outputChannel.appendLine(`[glua-dap] start attach host=${attachConfig.host}; port=${attachConfig.port}`);
  }
  await vscode.debug.startDebugging(undefined, attachConfig);
}

function registerDebugSupport(context, outputChannel) {
  const configurationProvider = {
    resolveDebugConfiguration(folder, config) {
      const defaults = defaultDebugAttachConfig();
      const next = {
        ...defaults,
        ...(config || {}),
      };
      next.type = DEBUG_TYPE;
      next.request = "attach";
      next.host = resolveDebugHost(next.host);
      next.port = resolveDebugPort(next.port);
      return next;
    },
  };

  const descriptorFactory = {
    createDebugAdapterDescriptor(session) {
      const host = resolveDebugHost(session.configuration.host);
      const port = resolveDebugPort(session.configuration.port);
      if (outputChannel) {
        outputChannel.appendLine(`[glua-dap] attach host=${host}; port=${port}`);
      }
      return new vscode.DebugAdapterServer(port, host);
    },
  };

  context.subscriptions.push(
    vscode.debug.registerDebugConfigurationProvider(DEBUG_TYPE, configurationProvider),
    vscode.debug.registerDebugAdapterDescriptorFactory(DEBUG_TYPE, descriptorFactory)
  );
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
  const { LanguageClient, TransportKind } = languageClientApi;

  const config = vscode.workspace.getConfiguration("glua");
  const docConfig = applyBuiltinDocsFromConfig(config);
  lastDocConfig = docConfig;
  logResolvedDocLanguage(outputChannel, "activate", docConfig);
  const syntax = config.get("syntax", "extended");
  const extensionServerPath = path.join(__dirname, "server", "index.js");

  const serverOptions = {
    run: {
      module: extensionServerPath,
      transport: TransportKind.ipc,
      options: { cwd: context.extensionPath },
    },
    debug: {
      module: extensionServerPath,
      transport: TransportKind.ipc,
      options: {
        cwd: context.extensionPath,
        execArgv: ["--inspect=6009"],
      },
    },
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
  outputChannel.appendLine(`[glua-lsp] starting server=${extensionServerPath}; syntax=${syntax}`);
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
};

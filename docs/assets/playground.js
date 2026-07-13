(function () {
  "use strict";

  var scriptURL = document.currentScript && document.currentScript.src;
  var assetBase = scriptURL ? new URL(".", scriptURL) : new URL("assets/", window.location.href);
  var maxDropBytes = 1024 * 1024;
  var worker = null;
  var languageWorker = null;
  var languageWorkerReady = false;
  var languageQueue = [];
  var languageRequests = new Map();
  var languageRequestID = 0;
  var diagnosticsTimer = null;
  var pendingRequest = null;
  var modal = null;
  var dialog = null;
  var dialogHeader = null;
  var dialogOffsetX = 0;
  var dialogOffsetY = 0;
  var dialogDragState = null;
  var editor = null;
  var editorShell = null;
  var output = null;
  var input = null;
  var fileLabel = null;
  var localsPanel = null;
  var statusLabel = null;
  var breakpoints = new Set();
  var currentLine = 0;
  var sessionState = "idle";
  var pageActions = null;
  var pageTopButton = null;
  var currentLineClass = 0;
  var editorDecorationIDs = [];
  var workspaceFileSystem = null;
  var workspaceFileList = null;
  var workspaceLabel = null;
  var workspaceSearch = null;
  var workspaceFiles = [];
  var expandedDirectories = new Set([""]);
  var editorTabs = null;
  var openDocuments = new Map();
  var workspaceSourceFiles = {};
  var activeDocumentPath = "";
  var suppressEditorChange = false;
  var workspaceInitialization = null;
  var workspaceSessionRestored = false;
  var sessionSaveScheduled = false;
  var sessionPersistenceError = "";
  var playgroundThemeStorageKey = "glua-playground-theme";
  var currentPlaygroundTheme = resolveInitialPlaygroundTheme();
  var themeSelector = null;
  var completionCatalog = null;
  var completionCatalogPromise = null;
  var completionTriggerTimer = null;
  var luaKeywords = [
    "and", "break", "case", "const", "continue", "default", "do", "else", "elseif", "end",
    "false", "for", "function", "goto", "if", "in", "local", "nil", "not", "or", "repeat",
    "return", "switch", "then", "true", "until", "while",
  ];
  var semanticTokenTypes = ["namespace", "type", "class", "enum", "interface", "struct", "typeParameter", "parameter", "variable", "property", "enumMember", "event", "function", "method", "macro", "keyword", "modifier", "comment", "string", "number", "regexp", "operator"];

  function element(tag, className, text) {
    var node = document.createElement(tag);
    if (className) node.className = className;
    if (typeof text === "string") node.textContent = text;
    return node;
  }

  function button(label, action, description, shortcut) {
    var node = element("button", "glua-playground-button", label);
    node.type = "button";
    node.dataset.action = action;
    node.title = description + (shortcut ? "（" + shortcut + "）" : "");
    node.dataset.tooltip = node.title;
    return node;
  }

  function resolveInitialPlaygroundTheme() {
    try {
      var storedTheme = window.localStorage.getItem(playgroundThemeStorageKey);
      if (storedTheme === "light" || storedTheme === "dark") return storedTheme;
    } catch (error) {
      // 浏览器禁用站点存储时继续使用系统主题，不影响编辑器启动。
    }
    return window.matchMedia && window.matchMedia("(prefers-color-scheme: light)").matches ? "light" : "dark";
  }

  function monacoThemeName(theme) {
    return theme === "light" ? "glua-light" : "glua-dark";
  }

  function applyPlaygroundTheme(theme, persist) {
    currentPlaygroundTheme = theme === "light" ? "light" : "dark";
    if (dialog) dialog.dataset.theme = currentPlaygroundTheme;
    if (themeSelector) themeSelector.value = currentPlaygroundTheme;
    if (window.monaco && window.monaco.editor) window.monaco.editor.setTheme(monacoThemeName(currentPlaygroundTheme));
    if (!persist) return;
    try {
      window.localStorage.setItem(playgroundThemeStorageKey, currentPlaygroundTheme);
    } catch (error) {
      appendOutput("stderr", "保存主题选择失败：" + String(error) + "\n");
    }
  }

  function ensureModal() {
    if (modal) return modal;

    modal = element("div", "glua-playground-overlay");
    modal.hidden = true;
    modal.setAttribute("role", "dialog");
    modal.setAttribute("aria-modal", "true");
    modal.setAttribute("aria-label", "GLua Playground");

    dialog = element("section", "glua-playground-dialog");
    dialog.dataset.theme = currentPlaygroundTheme;
    dialogHeader = element("header", "glua-playground-header");
    var heading = element("div", "glua-playground-heading");
    heading.appendChild(element("strong", "", "GLua Playground"));
    fileLabel = element("span", "glua-playground-file", "文档示例");
    heading.appendChild(fileLabel);
    statusLabel = element("span", "glua-playground-status", "就绪");
    dialogHeader.appendChild(heading);
    dialogHeader.appendChild(statusLabel);
    var themeControl = element("label", "glua-playground-theme-control");
    themeControl.appendChild(element("span", "", "主题"));
    themeSelector = element("select", "glua-playground-theme-select");
    themeSelector.setAttribute("aria-label", "编辑器主题");
    [["dark", "暗色"], ["light", "亮色"]].forEach(function (definition) {
      var option = element("option", "", definition[1]);
      option.value = definition[0];
      themeSelector.appendChild(option);
    });
    themeSelector.value = currentPlaygroundTheme;
    themeSelector.addEventListener("change", function () { applyPlaygroundTheme(themeSelector.value, true); });
    themeControl.appendChild(themeSelector);
    dialogHeader.appendChild(themeControl);
    dialogHeader.appendChild(button("全屏", "fullscreen", "切换全屏", "Alt+Enter"));
    dialogHeader.appendChild(button("关闭", "close", "关闭窗口", "Esc"));

    var toolbar = element("div", "glua-playground-toolbar");
    [
      ["执行", "run", "执行代码", "Ctrl/Cmd+Enter"],
      ["调试", "debug", "开始调试", "F5"],
      ["保存", "save", "保存当前文件", "Ctrl/Cmd+S"],
      ["新建", "newFile", "新建文件", ""],
      ["打开目录", "openLocal", "打开本地目录", ""],
      ["重新授权", "reauthorize", "重新申请本地工作区读写权限", ""],
      ["浏览器工作区", "useOPFS", "切换到浏览器工作区", ""],
      ["停止", "stop", "停止执行或调试", "Shift+F5"],
      ["暂停", "pause", "暂停调试", ""],
      ["继续", "continue", "继续调试", "F5"],
      ["进入", "stepInto", "单步进入", "F11"],
      ["跳过", "stepOver", "单步跳过", "F10"],
      ["跳出", "stepOut", "单步跳出", "Shift+F11"],
      ["格式化", "format", "格式化代码", "Alt+Shift+F"],
      ["清空输出", "clear", "清空 stdout 和 stderr", ""],
    ].forEach(function (definition) {
      toolbar.appendChild(button(definition[0], definition[1], definition[2], definition[3]));
    });

    var workspace = element("div", "glua-playground-workspace");
    var main = element("div", "glua-playground-main");
    var ide = element("div", "glua-playground-ide");
    var explorer = element("aside", "glua-playground-explorer");
    var explorerHeader = element("div", "glua-playground-explorer-header");
    explorerHeader.appendChild(element("strong", "", "资源管理器"));
    workspaceLabel = element("span", "glua-playground-workspace-label", "正在加载...");
    explorerHeader.appendChild(workspaceLabel);
    workspaceSearch = element("input", "glua-playground-file-search");
    workspaceSearch.type = "search";
    workspaceSearch.placeholder = "搜索文件";
    workspaceSearch.setAttribute("aria-label", "搜索工作区文件");
    explorerHeader.appendChild(workspaceSearch);
    explorer.appendChild(explorerHeader);
    workspaceFileList = element("div", "glua-playground-files");
    explorer.appendChild(workspaceFileList);
    ide.appendChild(explorer);

    var editorPane = element("div", "glua-playground-editor-pane");
    editorTabs = element("div", "glua-playground-tabs");
    editorPane.appendChild(editorTabs);
    editorShell = element("div", "glua-playground-editor-shell");
    editorShell.tabIndex = 0;
    editorShell.setAttribute("aria-label", "GLua 代码编辑器，可拖放代码文件");
    var editorHost = element("div", "glua-playground-editor");
    editorHost.setAttribute("aria-label", "GLua 代码");
    editorShell.appendChild(editorHost);
    var dropHint = element("div", "glua-playground-drop-hint", "拖放 .lua / .glua / 文本文件到这里");
    editorShell.appendChild(dropHint);
    editorPane.appendChild(editorShell);
    ide.appendChild(editorPane);
    main.appendChild(ide);

    var outputHeader = element("div", "glua-playground-output-header");
    outputHeader.appendChild(element("strong", "", "输出（stdout / stderr）"));
    output = element("pre", "glua-playground-output");
    output.setAttribute("aria-live", "polite");
    main.appendChild(outputHeader);
    main.appendChild(output);

    var inputRow = element("div", "glua-playground-input-row");
    input = element("input", "glua-playground-input");
    input.type = "text";
    input.placeholder = "模拟 stdin 输入，Enter 发送";
    input.setAttribute("aria-label", "标准输入");
    inputRow.appendChild(input);
    inputRow.appendChild(button("发送输入", "sendInput", "发送标准输入", "Enter"));
    inputRow.appendChild(button("发送 EOF", "sendEOF", "发送输入结束标记", ""));
    main.appendChild(inputRow);

    var inspector = element("aside", "glua-playground-inspector");
    inspector.appendChild(element("strong", "", "调试变量"));
    inspector.appendChild(element("p", "glua-playground-inspector-help", "调试暂停后显示当前帧局部变量；点击可展开 table。"));
    localsPanel = element("div", "glua-playground-locals");
    inspector.appendChild(localsPanel);
    workspace.appendChild(main);
    workspace.appendChild(inspector);

    dialog.appendChild(dialogHeader);
    dialog.appendChild(toolbar);
    dialog.appendChild(workspace);
    modal.appendChild(dialog);
    document.body.appendChild(modal);

    dialogHeader.addEventListener("pointerdown", startDialogDrag);
    dialogHeader.addEventListener("pointermove", moveDialogDrag);
    dialogHeader.addEventListener("pointerup", finishDialogDrag);
    dialogHeader.addEventListener("pointercancel", finishDialogDrag);
    window.addEventListener("resize", constrainDialogPosition, { passive: true });

    registerGLuaLanguage();
    editor = window.monaco.editor.create(editorHost, {
      value: "",
      language: "glua",
      theme: monacoThemeName(currentPlaygroundTheme),
      automaticLayout: true,
      glyphMargin: true,
      lineNumbers: "on",
      minimap: { enabled: true },
      fontFamily: "ui-monospace, SFMono-Regular, Menlo, Consolas, monospace",
      fontSize: 13,
      lineHeight: 22,
      tabSize: 2,
      insertSpaces: true,
      wordWrap: "off",
      scrollBeyondLastLine: false,
      suggestOnTriggerCharacters: true,
      quickSuggestions: { other: true, comments: false, strings: false },
      snippetSuggestions: "inline",
      suggestSelection: "first",
      bracketPairColorization: { enabled: true },
      guides: { bracketPairs: true, indentation: true },
    });
    editor.addAction({
      id: "glua.formatDocument",
      label: "格式化 GLua 代码",
      keybindings: [window.monaco.KeyMod.Alt | window.monaco.KeyMod.Shift | window.monaco.KeyCode.KeyF],
      run: function () { formatSource(); },
    });
    editor.addAction({
      id: "glua.runDocument",
      label: "执行 GLua 代码",
      keybindings: [window.monaco.KeyMod.CtrlCmd | window.monaco.KeyCode.Enter],
      run: function () { start(false); },
    });

    modal.addEventListener("click", handleAction);
    editor.onDidChangeModelContent(function () {
      if (!suppressEditorChange && activeDocumentPath && openDocuments.has(activeDocumentPath)) {
        var documentState = openDocuments.get(activeDocumentPath);
        documentState.source = getEditorValue();
        documentState.dirty = documentState.source !== documentState.savedSource;
        renderEditorTabs();
        scheduleWorkspaceSessionSave();
      }
      scheduleDiagnostics();
      renderGutter();
    });
    editor.onMouseDown(toggleBreakpoint);
    editor.onKeyDown(handleEditorKeyDown);
    editor.onDidChangeCursorPosition(scheduleWorkspaceSessionSave);
    editor.onDidChangeCursorSelection(scheduleWorkspaceSessionSave);
    editor.onDidScrollChange(scheduleWorkspaceSessionSave);
    editor.onDidType(function (text) {
      scheduleMemberCompletion(text);
    });
    input.addEventListener("keydown", function (event) {
      if (event.key === "Enter") {
        event.preventDefault();
        sendInput();
      }
    });
    workspaceSearch.addEventListener("input", function () {
      renderWorkspaceTree();
    });
    workspaceSearch.addEventListener("keydown", function (event) {
      if (event.key === "Escape" && workspaceSearch.value) {
        event.preventDefault();
        workspaceSearch.value = "";
        renderWorkspaceTree();
      }
    });
    installDropHandlers(editorShell);
    loadCompletionCatalog();
    ensureLanguageWorker();
    scheduleDiagnostics();
    workspaceInitialization = initializeWorkspace();
    document.addEventListener("keydown", handleShortcut);
    document.addEventListener("visibilitychange", function () {
      if (document.visibilityState === "hidden") persistWorkspaceSession();
    });
    window.addEventListener("pagehide", persistWorkspaceSession);
    updateControls();
    return modal;
  }

  function registerGLuaLanguage() {
    if (window.monaco.languages.getLanguages().some(function (language) { return language.id === "glua"; })) return;
    window.monaco.languages.register({ id: "glua", extensions: [".lua", ".glua"] });
    window.monaco.languages.setMonarchTokensProvider("glua", {
      defaultToken: "",
      tokenPostfix: ".glua",
      keywords: luaKeywords,
      builtins: ["assert", "error", "ipairs", "next", "pairs", "pcall", "print", "rawequal", "rawget", "rawlen", "rawset", "require", "select", "tonumber", "tostring", "type", "xpcall"],
      tokenizer: {
        root: [
          [/--\[(=*)\[/, "comment", "@longcomment"],
          [/--.*$/, "comment"],
          [/\[(=*)\[/, "string", "@longstring"],
          [/\b(?:glua|string|math|table|io|os|coroutine|debug|utf8)\b(?=\s*[.:])/, "namespace"],
          [/([.:])\s*(progress_(?:line|start|end|error|exit|function_call|function_return|function_error|function_exit))\b/, ["delimiter", "event"]],
          [/([.:])\s*([a-zA-Z_][\w]*)(?=\s*\()/, ["delimiter", "method"]],
          [/([.:])\s*([a-zA-Z_][\w]*)/, ["delimiter", "variable.property"]],
          [/[a-zA-Z_][\w]*(?=\s*\()/, "function"],
          [/\b(?:false|nil|true|_ENV|_G|_VERSION|_glua_const)\b/, "constant"],
          [/[a-zA-Z_][\w]*/, { cases: { "@keywords": "keyword", "@builtins": "predefined", "@default": "identifier" } }],
          [/0[xX][0-9a-fA-F]+(?:\.[0-9a-fA-F]*)?(?:[pP][+-]?\d+)?|\d+(?:\.\d*)?(?:[eE][+-]?\d+)?|\.\d+(?:[eE][+-]?\d+)?/, "number"],
          [/[{}()\[\]]/, "@brackets"],
          [/[<>]=?|==|~=|[-+*\/%^#&|~]/, "operator"],
          [/"/, "string", "@stringDouble"],
          [/'/, "string", "@stringSingle"],
        ],
        longcomment: [[/\]=*\]/, "comment", "@pop"], [/./, "comment"]],
        longstring: [[/\]=*\]/, "string", "@pop"], [/./, "string"]],
        stringDouble: [[/[^\\"]+/, "string"], [/\\./, "string.escape"], [/"/, "string", "@pop"]],
        stringSingle: [[/[^\\']+/, "string"], [/\\./, "string.escape"], [/'/, "string", "@pop"]],
      },
    });
    window.monaco.languages.setLanguageConfiguration("glua", {
      comments: { lineComment: "--", blockComment: ["--[[", "]]" ] },
      brackets: [["{", "}"], ["[", "]"], ["(", ")"]],
      autoClosingPairs: [{ open: "{", close: "}" }, { open: "[", close: "]" }, { open: "(", close: ")" }, { open: "\"", close: "\"" }, { open: "'", close: "'" }],
      indentationRules: {
        increaseIndentPattern: /^.*\b(?:then|do|function|repeat|switch|case|default)\b.*$/,
        decreaseIndentPattern: /^\s*(?:end|until|else|elseif|case|default)\b/,
      },
    });
    window.monaco.editor.defineTheme("glua-dark", {
      base: "vs-dark",
      inherit: true,
      rules: [
        { token: "keyword.glua", foreground: "F472B6" },
        { token: "predefined.glua", foreground: "67E8F9" },
        { token: "namespace.glua", foreground: "67E8F9" },
        { token: "function.glua", foreground: "60A5FA" },
        { token: "method.glua", foreground: "60A5FA" },
        { token: "variable.property.glua", foreground: "C4B5FD" },
        { token: "event.glua", foreground: "FBBF24" },
        { token: "constant.glua", foreground: "FBBF24" },
        { token: "string.glua", foreground: "86EFAC" },
        { token: "number.glua", foreground: "FBBF24" },
        { token: "comment.glua", foreground: "7F9CA3", fontStyle: "italic" },
      ],
      colors: { "editor.background": "#122126", "editorGutter.background": "#0d191d", "editorLineNumber.foreground": "#6f898f" },
    });
    window.monaco.editor.defineTheme("glua-light", {
      base: "vs",
      inherit: true,
      rules: [
        { token: "keyword.glua", foreground: "BE185D" },
        { token: "predefined.glua", foreground: "0369A1" },
        { token: "namespace.glua", foreground: "0369A1" },
        { token: "function.glua", foreground: "1D4ED8" },
        { token: "method.glua", foreground: "1D4ED8" },
        { token: "variable.property.glua", foreground: "6D28D9" },
        { token: "event.glua", foreground: "A16207" },
        { token: "constant.glua", foreground: "A16207" },
        { token: "string.glua", foreground: "15803D" },
        { token: "number.glua", foreground: "A16207" },
        { token: "comment.glua", foreground: "64748B", fontStyle: "italic" },
      ],
      colors: {
        "editor.background": "#FFFFFF",
        "editor.foreground": "#24343A",
        "editorGutter.background": "#F3F7F7",
        "editorLineNumber.foreground": "#6B7F84",
        "editor.selectionBackground": "#CCFBF1",
        "editor.lineHighlightBackground": "#F8FBFB",
      },
    });
    window.monaco.languages.registerCompletionItemProvider("glua", {
      triggerCharacters: [".", ":"],
      provideCompletionItems: provideLanguageCompletions,
    });
    window.monaco.languages.registerHoverProvider("glua", { provideHover: provideLanguageHover });
    window.monaco.languages.registerDefinitionProvider("glua", { provideDefinition: provideLanguageDefinition });
    window.monaco.languages.registerDocumentSemanticTokensProvider("glua", {
      getLegend: function () { return { tokenTypes: semanticTokenTypes, tokenModifiers: [] }; },
      provideDocumentSemanticTokens: provideLanguageSemanticTokens,
      releaseDocumentSemanticTokens: function () {},
    });
  }

  function installDropHandlers(target) {
    ["dragenter", "dragover"].forEach(function (name) {
      target.addEventListener(name, function (event) {
        event.preventDefault();
        target.classList.add("is-dragging");
      });
    });
    ["dragleave", "drop"].forEach(function (name) {
      target.addEventListener(name, function (event) {
        event.preventDefault();
        target.classList.remove("is-dragging");
      });
    });
    target.addEventListener("drop", function (event) {
      var files = event.dataTransfer && event.dataTransfer.files;
      if (!files || files.length !== 1) {
        appendOutput("stderr", "请一次拖放一个代码文件。\n");
        return;
      }
      loadDroppedFile(files[0]);
    });
  }

  function loadDroppedFile(file) {
    var name = file.name || "代码文件";
    var extensionOK = /\.(?:lua|glua|txt)$/i.test(name);
    var textType = !file.type || /^text\//.test(file.type) || /lua/.test(file.type);
    if (!extensionOK && !textType) {
      appendOutput("stderr", "不支持的文件类型：" + name + "\n");
      return;
    }
    if (file.size > maxDropBytes) {
      appendOutput("stderr", "文件超过 1 MiB 限制：" + name + "\n");
      return;
    }
    file.text().then(function (source) {
      if (source.indexOf("\u0000") !== -1) {
        appendOutput("stderr", "拒绝包含 NUL 字节的二进制文件：" + name + "\n");
        return;
      }
      setEditorValue(source);
      fileLabel.textContent = name;
      breakpoints.clear();
      currentLine = 0;
      renderGutter();
      editor.focus();
    }).catch(function (error) {
      appendOutput("stderr", "读取文件失败：" + String(error) + "\n");
    });
  }

  function handleAction(event) {
    var target = event.target.closest("[data-action]");
    if (!target || !modal.contains(target)) return;
    var action = target.dataset.action;
    if (action === "run") start(false);
    else if (action === "debug") start(true);
    else if (action === "save") saveActiveDocument();
    else if (action === "newFile") createWorkspaceFile();
    else if (action === "openLocal") switchToLocalDirectory();
    else if (action === "reauthorize") requestWorkspacePermission();
    else if (action === "useOPFS") switchToBrowserWorkspace();
    else if (action === "toggleDirectory") toggleWorkspaceDirectory(target.dataset.path);
    else if (action === "openFile") openWorkspaceFile(target.dataset.path);
    else if (action === "renameFile") renameWorkspaceFile(target.dataset.path);
    else if (action === "deleteFile") deleteWorkspaceFile(target.dataset.path);
    else if (action === "activateTab") activateDocument(target.dataset.path);
    else if (action === "closeTab") closeDocument(target.dataset.path);
    else if (action === "stop") stopSession(true);
    else if (action === "pause") sendDebug("pause");
    else if (action === "continue") sendDebug("continue");
    else if (action === "stepInto") sendDebug("stepInto");
    else if (action === "stepOver") sendDebug("stepOver");
    else if (action === "stepOut") sendDebug("stepOut");
    else if (action === "format") formatSource();
    else if (action === "clear") output.textContent = "";
    else if (action === "sendInput") sendInput();
    else if (action === "sendEOF") post({ action: "closeInput" });
    else if (action === "fullscreen") toggleFullscreen();
    else if (action === "close") closeModal();
  }

  function start(debug) {
    stopSession(false);
    output.textContent = "";
    localsPanel.textContent = "";
    currentLine = 0;
    renderGutter();
    sessionState = "loading";
    pendingRequest = {
      action: "run",
      source: getEditorValue(),
      path: activeDocumentPath.replace(/^@\//, ""),
      workspace: JSON.stringify(executionWorkspaceSnapshot()),
      debug: debug,
      breakpoints: Array.from(breakpoints).sort(function (left, right) { return left - right; }),
    };
    worker = new Worker(new URL("playground-worker.js", assetBase));
    worker.onmessage = handleWorkerMessage;
    worker.onerror = function (event) {
      appendOutput("stderr", "Worker 错误：" + event.message + "\n");
      sessionState = "finished";
      updateControls();
    };
    updateControls();
  }

  function handleWorkerMessage(event) {
    var message = event.data || {};
    if (message.type === "ready") {
      if (pendingRequest) {
        worker.postMessage(pendingRequest);
        pendingRequest = null;
      }
      return;
    }
    if (message.type === "running") {
      sessionState = "running";
    } else if (message.type === "output") {
      appendOutput(message.stream || "stdout", message.text || "");
    } else if (message.type === "paused") {
      sessionState = "paused";
      currentLine = message.line || 0;
      renderGutter();
      renderLocals(message.locals || [], message);
    } else if (message.type === "finished") {
      sessionState = "finished";
      currentLine = 0;
      renderGutter();
    } else if (message.type === "formatResult") {
      if (message.error) appendOutput("stderr", "格式化失败：" + message.error + "\n");
      else setEditorValue(message.source || "");
      terminateWorker();
      sessionState = "idle";
    } else if (message.type === "error" || message.type === "bootstrapError") {
      appendOutput(message.stream || "stderr", message.text || "未知错误\n");
      sessionState = "finished";
    }
    updateControls();
  }

  function getEditorValue() {
    return editor ? editor.getValue() : "";
  }

  function setEditorValue(source) {
    if (!editor) return;
    suppressEditorChange = true;
    editor.setValue(source);
    editor.setPosition({ lineNumber: 1, column: 1 });
    suppressEditorChange = false;
    renderGutter();
  }

  async function initializeWorkspace() {
    workspaceFileSystem = new window.GLuaWorkspaceFileSystem();
    try {
      var workspace = await workspaceFileSystem.initialize('-- GLua 浏览器工作区入口\nprint("Hello, GLua!")\n');
      updateWorkspacePresentation();
      if (workspace.needsPermission) {
        workspaceFiles = [];
        workspaceSourceFiles = {};
        renderWorkspaceTree();
        appendOutput("stderr", workspace.restoreError + "\n");
      } else {
        await refreshWorkspaceFiles();
      }
      workspaceSessionRestored = await restoreWorkspaceSession();
      if (workspace.restoreError && !workspace.needsPermission) appendOutput("stderr", workspace.restoreError + "\n");
      return workspaceSessionRestored;
    } catch (error) {
      workspaceLabel.textContent = "工作区不可用";
      appendOutput("stderr", "初始化工作区失败：" + String(error) + "\n");
      return false;
    }
  }

  function updateWorkspacePresentation() {
    if (!workspaceFileSystem) return;
    var workspace = workspaceFileSystem.snapshot();
    if (workspaceLabel) workspaceLabel.textContent = workspace.label;
    updateControls();
  }

  function reportWorkspaceError(prefix, error) {
    updateWorkspacePresentation();
    var workspace = workspaceFileSystem ? workspaceFileSystem.snapshot() : null;
    if (workspace && workspace.needsPermission) {
      appendOutput("stderr", prefix + "：本地工作区权限已失效，请点击“重新授权”。\n");
      return;
    }
    appendOutput("stderr", prefix + "：" + String(error) + "\n");
  }

  async function refreshWorkspaceFiles() {
    var files = await workspaceFileSystem.listFiles();
    await refreshWorkspaceSources(files);
    workspaceFiles = files;
    updateWorkspacePresentation();
    renderWorkspaceTree();
  }

  function captureActiveDocumentState() {
    if (!editor || !activeDocumentPath || !openDocuments.has(activeDocumentPath)) return;
    var documentState = openDocuments.get(activeDocumentPath);
    documentState.source = documentState.model.getValue();
    documentState.viewState = editor.saveViewState();
  }

  function serializableViewState(viewState) {
    if (!viewState) return null;
    try {
      return JSON.parse(JSON.stringify(viewState));
    } catch (error) {
      return null;
    }
  }

  function workspaceSessionSnapshot() {
    captureActiveDocumentState();
    var documents = [];
    openDocuments.forEach(function (documentState, path) {
      documents.push({
        path: path,
        workspace: documentState.workspace,
        source: documentState.model.getValue(),
        savedSource: documentState.savedSource,
        dirty: documentState.model.getValue() !== documentState.savedSource,
        viewState: serializableViewState(documentState.viewState),
      });
    });
    return {
      version: 1,
      activeDocumentPath: activeDocumentPath,
      documents: documents,
      updatedAt: Date.now(),
    };
  }

  function scheduleWorkspaceSessionSave() {
    if (!workspaceFileSystem || sessionSaveScheduled) return;
    sessionSaveScheduled = true;
    Promise.resolve().then(function () {
      sessionSaveScheduled = false;
      persistWorkspaceSession();
    });
  }

  async function persistWorkspaceSession() {
    if (!workspaceFileSystem) return false;
    var saved = await workspaceFileSystem.saveSession(workspaceSessionSnapshot());
    if (!saved) {
      var persistenceError = workspaceFileSystem.snapshot().restoreError || "保存编辑会话失败。";
      if (persistenceError !== sessionPersistenceError) appendOutput("stderr", persistenceError + "\n");
      sessionPersistenceError = persistenceError;
      return false;
    }
    sessionPersistenceError = "";
    return true;
  }

  async function restoreWorkspaceSession() {
    if (!workspaceFileSystem) return false;
    var session = await workspaceFileSystem.loadSession();
    if (!session || !Array.isArray(session.documents) || !session.documents.length) return false;
    var workspace = workspaceFileSystem.snapshot();
    disposeOpenDocuments();
    activeDocumentPath = "";
    for (var index = 0; index < session.documents.length; index += 1) {
      var savedDocument = session.documents[index];
      if (!savedDocument || !savedDocument.path || typeof savedDocument.source !== "string") continue;
      var source = savedDocument.source;
      var savedSource = typeof savedDocument.savedSource === "string" ? savedDocument.savedSource : source;
      var dirty = Boolean(savedDocument.dirty) || source !== savedSource;
      var conflict = false;
      if (savedDocument.workspace && !workspace.needsPermission) {
        try {
          var diskSource = await workspaceFileSystem.readFile(savedDocument.path);
          if (dirty) {
            conflict = diskSource !== savedSource;
          } else {
            source = diskSource;
            savedSource = diskSource;
          }
        } catch (error) {
          if (!dirty) {
            appendOutput("stderr", "恢复时跳过不存在或无法读取的文件 " + savedDocument.path + "：" + String(error) + "\n");
            continue;
          }
          conflict = true;
        }
      }
      var documentState = createDocumentState(savedDocument.path, source, Boolean(savedDocument.workspace), {
        savedSource: savedSource,
        dirty: dirty,
        viewState: savedDocument.viewState || null,
        conflict: conflict,
      });
      openDocuments.set(savedDocument.path, documentState);
      if (conflict) appendOutput("stderr", "检测到文件与未保存草稿冲突，已保留草稿：" + savedDocument.path + "\n");
    }
    var restorePath = session.activeDocumentPath && openDocuments.has(session.activeDocumentPath)
      ? session.activeDocumentPath
      : openDocuments.keys().next().value;
    if (!restorePath) return false;
    activateDocument(restorePath);
    renderEditorTabs();
    return true;
  }

  function renderWorkspaceTree() {
    if (!workspaceFileList) return;
    workspaceFileList.textContent = "";
    var query = workspaceSearch ? workspaceSearch.value.trim().toLowerCase() : "";
    var visibleFiles = query ? workspaceFiles.filter(function (path) { return path.toLowerCase().indexOf(query) !== -1; }) : workspaceFiles.slice();
    if (!visibleFiles.length) {
      workspaceFileList.appendChild(element("p", "glua-playground-empty", query ? "没有匹配的文件。" : "工作区没有可编辑文件。"));
      return;
    }
    var root = { directories: new Map(), files: [] };
    visibleFiles.forEach(function (filePath) {
      var parts = filePath.split("/");
      var fileName = parts.pop();
      var node = root;
      var directoryPath = "";
      parts.forEach(function (part) {
        directoryPath = directoryPath ? directoryPath + "/" + part : part;
        if (!node.directories.has(part)) node.directories.set(part, { name: part, path: directoryPath, directories: new Map(), files: [] });
        node = node.directories.get(part);
      });
      node.files.push({ name: fileName, path: filePath });
    });
    renderWorkspaceTreeNode(root, workspaceFileList, 0, Boolean(query));
  }

  function renderWorkspaceTreeNode(node, container, depth, forceExpanded) {
    Array.from(node.directories.values()).sort(function (left, right) { return left.name.localeCompare(right.name); }).forEach(function (directory) {
      var expanded = forceExpanded || expandedDirectories.has(directory.path);
      var directoryRow = element("div", "glua-playground-directory-row");
      directoryRow.style.setProperty("--tree-indent", (depth * 14) + "px");
      var toggle = button((expanded ? "▾ " : "▸ ") + directory.name, "toggleDirectory", (expanded ? "折叠 " : "展开 ") + directory.path);
      toggle.dataset.path = directory.path;
      toggle.classList.add("glua-playground-directory-toggle");
      directoryRow.appendChild(toggle);
      container.appendChild(directoryRow);
      if (expanded) {
        var children = element("div", "glua-playground-directory-children");
        renderWorkspaceTreeNode(directory, children, depth + 1, forceExpanded);
        container.appendChild(children);
      }
    });
    node.files.sort(function (left, right) { return left.name.localeCompare(right.name); }).forEach(function (file) {
      var row = element("div", "glua-playground-file-row");
      row.style.setProperty("--tree-indent", (depth * 14) + "px");
      var open = button(file.name, "openFile", "打开 " + file.path);
      open.classList.add("glua-playground-file-open");
      open.dataset.path = file.path;
      row.appendChild(open);
      var rename = button("改", "renameFile", "重命名 " + file.path);
      rename.dataset.path = file.path;
      row.appendChild(rename);
      var remove = button("删", "deleteFile", "删除 " + file.path);
      remove.dataset.path = file.path;
      row.appendChild(remove);
      container.appendChild(row);
    });
  }

  function toggleWorkspaceDirectory(path) {
    if (!path) return;
    if (expandedDirectories.has(path)) expandedDirectories.delete(path);
    else expandedDirectories.add(path);
    renderWorkspaceTree();
  }

  async function refreshWorkspaceSources(files) {
    var nextFiles = {};
    for (var index = 0; index < files.length; index += 1) {
      var path = files[index];
      try {
        nextFiles[path] = await workspaceFileSystem.readFile(path);
      } catch (error) {
        appendOutput("stderr", "工作区跳过无法读取的文件 " + path + "：" + String(error) + "\n");
      }
    }
    workspaceSourceFiles = nextFiles;
  }

  function languageWorkspaceSnapshot() {
    var snapshot = {};
    Object.keys(workspaceSourceFiles).forEach(function (path) {
      if (/\.(?:lua|glua)$/i.test(path)) snapshot[path] = workspaceSourceFiles[path];
    });
    openDocuments.forEach(function (documentState, path) {
      if (documentState.workspace && /\.(?:lua|glua)$/i.test(path)) snapshot[path] = documentState.model.getValue();
    });
    return snapshot;
  }

  function executionWorkspaceSnapshot() {
    var snapshot = Object.assign({}, workspaceSourceFiles);
    openDocuments.forEach(function (documentState, path) {
      if (documentState.workspace) snapshot[path] = documentState.model.getValue();
    });
    return snapshot;
  }

  async function openWorkspaceFile(path) {
    if (!path || !workspaceFileSystem) return;
    try {
      if (!openDocuments.has(path)) {
        var source = await workspaceFileSystem.readFile(path);
        openDocuments.set(path, createDocumentState(path, source, true));
      }
      activateDocument(path);
    } catch (error) {
      reportWorkspaceError("打开文件失败", error);
    }
  }

  function activateDocument(path) {
    if (!path || !openDocuments.has(path)) return;
    captureActiveDocumentState();
    activeDocumentPath = path;
    var documentState = openDocuments.get(path);
    suppressEditorChange = true;
    editor.setModel(documentState.model);
    suppressEditorChange = false;
    fileLabel.textContent = path;
    breakpoints.clear();
    currentLine = 0;
    renderEditorTabs();
    scheduleDiagnostics();
    if (documentState.viewState) editor.restoreViewState(documentState.viewState);
    editor.focus();
    scheduleWorkspaceSessionSave();
  }

  function openScratchDocument(source, label) {
    var baseName = label === "当前页面示例" ? "当前页面示例.glua" : "文档示例.glua";
    var path = "@/" + baseName;
    if (openDocuments.has(path)) openDocuments.get(path).model.dispose();
    openDocuments.set(path, createDocumentState(path, source || "", false));
    activateDocument(path);
  }

  function createDocumentState(path, source, workspace, restoredState) {
    restoredState = restoredState || {};
    var scheme = workspace ? "file" : "inmemory";
    var uriPath = "/" + path.replace(/^@\//, "examples/");
    var uri = window.monaco.Uri.from({ scheme: scheme, path: uriPath });
    var existingModel = window.monaco.editor.getModel(uri);
    if (existingModel) existingModel.dispose();
    return {
      source: source,
      savedSource: typeof restoredState.savedSource === "string" ? restoredState.savedSource : source,
      dirty: Boolean(restoredState.dirty),
      workspace: workspace,
      viewState: restoredState.viewState || null,
      conflict: Boolean(restoredState.conflict),
      model: window.monaco.editor.createModel(source, "glua", uri),
    };
  }

  function renderEditorTabs() {
    if (!editorTabs) return;
    editorTabs.textContent = "";
    openDocuments.forEach(function (documentState, path) {
      var tab = element("div", "glua-playground-tab" + (path === activeDocumentPath ? " is-active" : ""));
      var activate = button(path.replace(/^@\//, ""), "activateTab", "切换到 " + path);
      activate.dataset.path = path;
      activate.classList.add("glua-playground-tab-name");
      if (documentState.dirty) activate.textContent += " ●";
      if (documentState.conflict) activate.textContent += " ⚠";
      tab.appendChild(activate);
      var close = button("×", "closeTab", "关闭 " + path);
      close.dataset.path = path;
      close.classList.add("glua-playground-tab-close");
      tab.appendChild(close);
      editorTabs.appendChild(tab);
    });
  }

  function closeDocument(path) {
    var documentState = openDocuments.get(path);
    if (!documentState) return;
    if (documentState.dirty && !window.confirm("文件尚未保存，仍要关闭吗？\n" + path)) return;
    openDocuments.delete(path);
    documentState.model.dispose();
    if (activeDocumentPath === path) {
      activeDocumentPath = "";
      var nextPath = openDocuments.keys().next().value;
      if (nextPath) activateDocument(nextPath);
      else resetEditorDocument();
    }
    renderEditorTabs();
    scheduleWorkspaceSessionSave();
  }

  async function saveActiveDocument() {
    if (!activeDocumentPath || !openDocuments.has(activeDocumentPath) || !workspaceFileSystem) return;
    var documentState = openDocuments.get(activeDocumentPath);
    var targetPath = activeDocumentPath;
    if (!documentState.workspace) {
      targetPath = window.prompt("保存到当前工作区的文件路径", activeDocumentPath.replace(/^@\//, ""));
      if (!targetPath) return;
    }
    try {
      documentState.source = getEditorValue();
      await workspaceFileSystem.writeFile(targetPath, documentState.source);
      documentState.savedSource = documentState.source;
      documentState.dirty = false;
      documentState.conflict = false;
      documentState.workspace = true;
      if (targetPath !== activeDocumentPath) {
        openDocuments.delete(activeDocumentPath);
        activeDocumentPath = targetPath;
        openDocuments.set(targetPath, documentState);
      }
      fileLabel.textContent = targetPath;
      workspaceSourceFiles[targetPath] = documentState.source;
      await refreshWorkspaceFiles();
      renderEditorTabs();
      scheduleWorkspaceSessionSave();
    } catch (error) {
      reportWorkspaceError("保存文件失败", error);
    }
  }

  async function createWorkspaceFile() {
    if (!workspaceFileSystem) return;
    var path = window.prompt("新建文件路径", "script.glua");
    if (!path) return;
    try {
      await workspaceFileSystem.writeFile(path, "");
      await refreshWorkspaceFiles();
      await openWorkspaceFile(path);
    } catch (error) {
      reportWorkspaceError("新建文件失败", error);
    }
  }

  async function renameWorkspaceFile(path) {
    var nextPath = window.prompt("重命名文件", path);
    if (!nextPath || nextPath === path) return;
    try {
      await workspaceFileSystem.renameFile(path, nextPath);
      if (openDocuments.has(path)) {
        var documentState = openDocuments.get(path);
        var renamedState = createDocumentState(nextPath, documentState.model.getValue(), true, {
          savedSource: documentState.savedSource,
          dirty: documentState.dirty,
          viewState: documentState.viewState,
          conflict: documentState.conflict,
        });
        documentState.model.dispose();
        openDocuments.delete(path);
        openDocuments.set(nextPath, renamedState);
        if (activeDocumentPath === path) {
          activeDocumentPath = nextPath;
          editor.setModel(renamedState.model);
          fileLabel.textContent = nextPath;
        }
      }
      await refreshWorkspaceFiles();
      renderEditorTabs();
      scheduleWorkspaceSessionSave();
    } catch (error) {
      reportWorkspaceError("重命名失败", error);
    }
  }

  async function deleteWorkspaceFile(path) {
    if (!window.confirm("确定删除文件吗？\n" + path)) return;
    try {
      await workspaceFileSystem.deleteFile(path);
      if (openDocuments.has(path)) closeDocument(path);
      await refreshWorkspaceFiles();
      scheduleWorkspaceSessionSave();
    } catch (error) {
      reportWorkspaceError("删除文件失败", error);
    }
  }

  async function switchToLocalDirectory() {
    try {
      var previousSessionSave = persistWorkspaceSession();
      await workspaceFileSystem.openLocalDirectory();
      await previousSessionSave;
      disposeOpenDocuments();
      activeDocumentPath = "";
      resetEditorDocument();
      await refreshWorkspaceFiles();
      workspaceSessionRestored = await restoreWorkspaceSession();
      renderEditorTabs();
      updateWorkspacePresentation();
    } catch (error) {
      if (error && error.name === "AbortError") return;
      reportWorkspaceError("打开本地目录失败", error);
    }
  }

  async function switchToBrowserWorkspace() {
    try {
      await persistWorkspaceSession();
      await workspaceFileSystem.useBrowserWorkspace();
      disposeOpenDocuments();
      activeDocumentPath = "";
      resetEditorDocument();
      await refreshWorkspaceFiles();
      workspaceSessionRestored = await restoreWorkspaceSession();
      renderEditorTabs();
      updateWorkspacePresentation();
    } catch (error) {
      reportWorkspaceError("切换浏览器工作区失败", error);
    }
  }

  async function requestWorkspacePermission() {
    if (!workspaceFileSystem) return;
    try {
      await workspaceFileSystem.requestLocalPermission();
      await persistWorkspaceSession();
      await refreshWorkspaceFiles();
      workspaceSessionRestored = await restoreWorkspaceSession();
      updateWorkspacePresentation();
      appendOutput("stdout", "本地工作区已重新授权。\n");
    } catch (error) {
      reportWorkspaceError("重新授权失败", error);
    }
  }

  function disposeOpenDocuments() {
    openDocuments.forEach(function (documentState) {
      if (documentState.model) documentState.model.dispose();
    });
    openDocuments.clear();
  }

  function resetEditorDocument() {
    var uri = window.monaco.Uri.parse("inmemory://glua/empty.glua");
    var existingModel = window.monaco.editor.getModel(uri);
    if (existingModel) existingModel.dispose();
    editor.setModel(window.monaco.editor.createModel("", "glua", uri));
  }

  function ensureLanguageWorker() {
    if (languageWorker) return;
    languageWorker = new Worker(new URL("playground-worker.js", assetBase));
    languageWorker.onmessage = function (event) {
      var message = event.data || {};
      if (message.type === "ready") {
        languageWorkerReady = true;
        languageQueue.splice(0).forEach(function (request) { languageWorker.postMessage(request); });
        return;
      }
      if (message.type !== "languageResult") return;
      var pending = languageRequests.get(message.requestId);
      if (!pending) return;
      languageRequests.delete(message.requestId);
      if (message.error) pending.reject(new Error(message.error));
      else pending.resolve(message.result);
    };
    languageWorker.onerror = function (event) {
      var error = new Error("GLua 浏览器语言服务错误：" + event.message);
      languageRequests.forEach(function (pending) { pending.reject(error); });
      languageRequests.clear();
      languageQueue = [];
      languageWorkerReady = false;
      languageWorker.terminate();
      languageWorker = null;
      if (output) appendOutput("stderr", error.message + "\n");
    };
  }

  function requestLanguage(operation, source, line, character) {
    ensureLanguageWorker();
    languageRequestID += 1;
    var request = {
      action: "language",
      operation: operation,
      requestId: languageRequestID,
      source: source,
      line: line || 0,
      character: character || 0,
      path: activeDocumentPath.replace(/^@\//, ""),
      workspace: JSON.stringify(languageWorkspaceSnapshot()),
    };
    return new Promise(function (resolve, reject) {
      languageRequests.set(request.requestId, { resolve: resolve, reject: reject });
      if (languageWorkerReady) languageWorker.postMessage(request);
      else languageQueue.push(request);
    });
  }

  function provideLanguageCompletions(model, position) {
    var word = model.getWordUntilPosition(position);
    var fallbackRange = new window.monaco.Range(position.lineNumber, word.startColumn, position.lineNumber, position.column);
    var languageCompletions = requestLanguage("completion", model.getValue(), position.lineNumber - 1, position.column - 1)
      .catch(function () { return []; });
    return Promise.all([languageCompletions, loadCompletionCatalog()]).then(function (results) {
      var wasmSuggestions = (results[0] || []).map(function (item) {
        var isSnippet = item.insertTextFormat === 2;
        return {
          label: item.label,
          kind: monacoCompletionKind(item.kind),
          insertText: item.insertText || item.label,
          insertTextRules: isSnippet ? window.monaco.languages.CompletionItemInsertTextRule.InsertAsSnippet : undefined,
          range: item.range ? monacoRange(item.range) : fallbackRange,
          sortText: item.sortText,
          detail: item.detail || "GLua symbol",
          documentation: item.documentation ? { value: item.documentation } : undefined,
        };
      });
      var catalogSuggestions = provideMonacoCompletions(model, position).suggestions;
      return { suggestions: mergeCompletionSuggestions(wasmSuggestions, catalogSuggestions) };
    });
  }

  function isMemberCompletionAtCursor() {
    var model = editor && editor.getModel();
    var position = editor && editor.getPosition();
    if (!model || !position || !window.GLuaPlaygroundEditorCore) return false;
    var linePrefix = model.getLineContent(position.lineNumber).slice(0, position.column - 1);
    return Boolean(window.GLuaPlaygroundEditorCore.completionContext(linePrefix).namespace);
  }

  function scheduleMemberCompletion(text) {
    if (completionTriggerTimer) window.clearTimeout(completionTriggerTimer);
    completionTriggerTimer = null;
    if (text !== "." && text !== ":" && (!/[A-Za-z0-9_]/.test(text) || !isMemberCompletionAtCursor())) return;
    completionTriggerTimer = window.setTimeout(function () {
      completionTriggerTimer = null;
      if (editor && isMemberCompletionAtCursor()) editor.trigger("glua", "editor.action.triggerSuggest", {});
    }, text === "." || text === ":" ? 40 : 120);
  }

  function mergeCompletionSuggestions(primary, fallback) {
    var merged = [];
    var seen = new Set();
    primary.concat(fallback).forEach(function (suggestion) {
      var label = typeof suggestion.label === "string" ? suggestion.label : suggestion.label.label;
      if (!label || seen.has(label)) return;
      seen.add(label);
      merged.push(suggestion);
    });
    return merged;
  }

  function monacoCompletionKind(kind) {
    if (kind === 6) return window.monaco.languages.CompletionItemKind.Variable;
    if (kind === 21) return window.monaco.languages.CompletionItemKind.Constant;
    if (kind === 14) return window.monaco.languages.CompletionItemKind.Keyword;
    if (kind === 15) return window.monaco.languages.CompletionItemKind.Snippet;
    return window.monaco.languages.CompletionItemKind.Function;
  }

  function handleEditorKeyDown(event) {
    if (!editor || event.keyCode !== window.monaco.KeyCode.Enter || event.ctrlKey || event.metaKey || event.altKey || event.shiftKey) return;
    var model = editor.getModel();
    var position = editor.getPosition();
    if (!model || !position || !window.GLuaPlaygroundEditorCore) return;
    var line = model.getLineContent(position.lineNumber);
    var beforeCaret = line.slice(0, position.column - 1);
    var afterCaret = line.slice(position.column - 1);
    if (afterCaret.trim()) return;
    var nextLine = nextSignificantLine(model, position.lineNumber + 1);
    var expansion = window.GLuaPlaygroundEditorCore.blockExpansion(beforeCaret, nextLine);
    if (!expansion) return;
    event.preventDefault();
    event.stopPropagation();
    editor.executeEdits("glua.blockEnter", [{
      range: new window.monaco.Range(position.lineNumber, position.column, position.lineNumber, position.column),
      text: expansion.text,
      forceMoveMarkers: true,
    }]);
    editor.setPosition({
      lineNumber: position.lineNumber + expansion.caretLineDelta,
      column: expansion.caretColumn,
    });
    editor.revealPositionInCenterIfOutsideViewport(editor.getPosition());
  }

  function nextSignificantLine(model, startLine) {
    for (var lineNumber = startLine; lineNumber <= model.getLineCount(); lineNumber += 1) {
      var content = model.getLineContent(lineNumber);
      if (content.trim()) return content;
    }
    return "";
  }

  function provideLanguageHover(model, position) {
    return requestLanguage("hover", model.getValue(), position.lineNumber - 1, position.column - 1).then(function (hover) {
      if (!hover) return null;
      return { contents: [{ value: hover.markdown }] };
    });
  }

  function provideLanguageDefinition(model, position) {
    return requestLanguage("definition", model.getValue(), position.lineNumber - 1, position.column - 1).then(function (definition) {
      if (!definition) return null;
      if (!definition.path) return { uri: model.uri, range: monacoRange(definition.range) };
      return ensureDefinitionModel(definition.path).then(function (definitionModel) {
        return { uri: definitionModel.uri, range: monacoRange(definition.range) };
      });
    });
  }

  function ensureDefinitionModel(path) {
    if (openDocuments.has(path)) return Promise.resolve(openDocuments.get(path).model);
    return workspaceFileSystem.readFile(path).then(function (source) {
      var documentState = createDocumentState(path, source, true);
      openDocuments.set(path, documentState);
      renderEditorTabs();
      return documentState.model;
    });
  }

  function provideLanguageSemanticTokens(model) {
    return requestLanguage("semanticTokens", model.getValue(), 0, 0).then(function (tokens) {
      return { data: new Uint32Array(tokens || []) };
    });
  }

  function scheduleDiagnostics() {
    if (!editor || suppressEditorChange) return;
    if (diagnosticsTimer) window.clearTimeout(diagnosticsTimer);
    diagnosticsTimer = window.setTimeout(function () {
      var model = editor.getModel();
      if (!model) return;
      var version = model.getVersionId();
      requestLanguage("diagnostics", model.getValue(), 0, 0).then(function (diagnostics) {
        if (editor.getModel() !== model || model.getVersionId() !== version) return;
        var markers = (diagnostics || []).map(function (diagnostic) {
          var range = diagnostic.range;
          return {
            startLineNumber: range.start.line + 1,
            startColumn: range.start.character + 1,
            endLineNumber: range.end.line + 1,
            endColumn: range.end.character + 1,
            severity: window.monaco.MarkerSeverity.Error,
            source: diagnostic.source || "glua",
            message: diagnostic.message,
          };
        });
        window.monaco.editor.setModelMarkers(model, "glua", markers);
      }).catch(function (error) {
        if (output) appendOutput("stderr", "语言诊断失败：" + String(error) + "\n");
      });
    }, 180);
  }

  function monacoRange(range) {
    return new window.monaco.Range(range.start.line + 1, range.start.character + 1, range.end.line + 1, range.end.character + 1);
  }

  function loadCompletionCatalog() {
    if (completionCatalogPromise) return completionCatalogPromise;
    completionCatalogPromise = fetch(new URL("builtin-functions.json", assetBase))
      .then(function (response) {
        if (!response.ok) throw new Error("HTTP " + response.status);
        return response.json();
      })
      .then(function (catalog) {
        completionCatalog = catalog && catalog.functions ? catalog.functions : {};
        return completionCatalog;
      })
      .catch(function (error) {
        completionCatalog = {};
        if (output) appendOutput("stderr", "补齐数据加载失败：" + String(error) + "\n");
        return completionCatalog;
      });
    return completionCatalogPromise;
  }

  function provideMonacoCompletions(model, position) {
    var linePrefix = model.getLineContent(position.lineNumber).slice(0, position.column - 1);
    var context = window.GLuaPlaygroundEditorCore.completionContext(linePrefix);
    var namespace = context.namespace;
    var catalogNamespace = context.catalogNamespace;
    var memberPrefix = context.memberPrefix;
    var candidates = new Map();
    var replacementRange = new window.monaco.Range(position.lineNumber, position.column - memberPrefix.length, position.lineNumber, position.column);

    function addCandidate(label, detail, description, kind, insertText) {
      if (!label || (memberPrefix && label.indexOf(memberPrefix) !== 0) || candidates.has(label)) return;
      var completionKind = kind === "keyword" ? window.monaco.languages.CompletionItemKind.Keyword :
        kind === "local" ? window.monaco.languages.CompletionItemKind.Variable :
          kind === "namespace" ? window.monaco.languages.CompletionItemKind.Module :
            kind === "constant" ? window.monaco.languages.CompletionItemKind.Constant :
              window.monaco.languages.CompletionItemKind.Function;
      var isSnippet = kind === "builtin" && insertText && insertText !== label;
      candidates.set(label, {
        label: label,
        kind: completionKind,
        insertText: insertText || label,
        insertTextRules: isSnippet ? window.monaco.languages.CompletionItemInsertTextRule.InsertAsSnippet : undefined,
        range: replacementRange,
        detail: detail || kind || "GLua symbol",
        documentation: description ? { value: description } : undefined,
        sortText: (kind === "local" ? "0" : kind === "builtin" ? "1" : kind === "namespace" ? "2" : "3") + label,
      });
    }

    Object.keys(completionCatalog || {}).forEach(function (fullName) {
      var entry = completionCatalog[fullName] || {};
      if (catalogNamespace && fullName.indexOf(catalogNamespace) !== 0) return;
      var remainder = catalogNamespace ? fullName.slice(catalogNamespace.length) : fullName;
      var parts = remainder.split(".");
      var label = parts[0];
      var isNamespace = parts.length > 1;
      var signature = localizedCatalogValue(entry.signature) || fullName;
      var description = localizedCatalogValue(entry.description);
      if (isNamespace) {
        addCandidate(label, label + " namespace", description, "namespace", label);
        return;
      }
      var isFunction = signature.indexOf("(") >= 0;
      var insertText = isFunction ? window.GLuaPlaygroundEditorCore.completionSnippet(label, signature) : label;
      addCandidate(label, signature, description, isFunction ? "builtin" : "constant", insertText);
    });

    if (!namespace) {
      luaKeywords.forEach(function (keyword) {
        addCandidate(keyword, "GLua 关键字", "", "keyword");
      });
      sourceSymbols(model.getValue(), { line: position.lineNumber - 1, ch: position.column - 1 }).forEach(function (symbol) {
        addCandidate(symbol.name, symbol.detail, "当前代码中已声明的符号", "local");
      });
    }

    return { suggestions: Array.from(candidates.values()) };
  }

  function localizedCatalogValue(value) {
    if (!value) return "";
    if (typeof value === "string") return value;
    return value["zh-CN"] || value.en || "";
  }

  function sourceSymbols(source, cursor) {
    var sourceLines = source.split("\n").slice(0, cursor.line + 1);
    sourceLines[sourceLines.length - 1] = (sourceLines[sourceLines.length - 1] || "").slice(0, cursor.ch);
    var beforeCursor = sourceLines.join("\n");
    var symbols = new Map();
    var declarationPattern = /\b(local|const|function)\s+([A-Za-z_][A-Za-z0-9_]*)/g;
    var declaration = null;
    while ((declaration = declarationPattern.exec(beforeCursor)) !== null) {
      symbols.set(declaration[2], {
        name: declaration[2],
        detail: declaration[1] === "function" ? "当前文件函数" : "当前文件 " + declaration[1],
      });
    }
    return Array.from(symbols.values());
  }

  function formatSource() {
    if (["loading", "running", "paused", "formatting"].indexOf(sessionState) !== -1) return;
    stopSession(false);
    sessionState = "formatting";
    pendingRequest = { action: "format", source: getEditorValue() };
    worker = new Worker(new URL("playground-worker.js", assetBase));
    worker.onmessage = handleWorkerMessage;
    worker.onerror = function (event) {
      appendOutput("stderr", "格式化 Worker 错误：" + event.message + "\n");
      terminateWorker();
      sessionState = "idle";
      updateControls();
    };
    updateControls();
  }

  function appendOutput(stream, text) {
    if (!output) return;
    var span = element("span", "glua-output-" + (stream === "stderr" ? "stderr" : "stdout"), text);
    output.appendChild(span);
    output.scrollTop = output.scrollHeight;
  }

  function sendInput() {
    var text = input.value;
    if (!worker || !text) return;
    post({ action: "input", text: text });
    appendOutput("stdout", "> " + text + "\n");
    input.value = "";
    input.focus();
  }

  function sendDebug(command) {
    if (!worker) return;
    post({ action: "debugCommand", command: command });
    if (command !== "pause") {
      sessionState = "running";
      currentLine = 0;
      renderGutter();
      updateControls();
    }
  }

  function post(message) {
    if (worker) worker.postMessage(message);
  }

  function stopSession(showMessage) {
    if (worker) {
      terminateWorker();
      if (showMessage) appendOutput("stderr", "[执行已停止]\n");
    }
    pendingRequest = null;
    sessionState = "idle";
    currentLine = 0;
    if (localsPanel) localsPanel.textContent = "";
    renderGutter();
    updateControls();
  }

  function terminateWorker() {
    if (!worker) return;
    worker.terminate();
    worker = null;
  }

  function renderGutter() {
    if (!editor) return;
    var count = Math.max(1, editor.getModel().getLineCount());
    Array.from(breakpoints).forEach(function (line) {
      if (line > count) breakpoints.delete(line);
    });
    currentLineClass = currentLine;
    var decorations = [];
    breakpoints.forEach(function (line) {
      if (line < 1 || line > count) return;
      decorations.push({
        range: new window.monaco.Range(line, 1, line, 1),
        options: { isWholeLine: false, glyphMarginClassName: "glua-monaco-breakpoint", glyphMarginHoverMessage: { value: "第 " + line + " 行断点" } },
      });
    });
    if (currentLine > 0 && currentLine <= count) {
      decorations.push({
        range: new window.monaco.Range(currentLine, 1, currentLine, 1),
        options: { isWholeLine: true, className: "glua-current-line", glyphMarginClassName: "glua-monaco-current" },
      });
      editor.revealLineInCenterIfOutsideViewport(currentLine);
    }
    editorDecorationIDs = editor.deltaDecorations(editorDecorationIDs, decorations);
  }

  function toggleBreakpoint(mouseEvent) {
    var targetType = mouseEvent.target.type;
    if (targetType !== window.monaco.editor.MouseTargetType.GUTTER_GLYPH_MARGIN && targetType !== window.monaco.editor.MouseTargetType.GUTTER_LINE_NUMBERS) return;
    if (!mouseEvent.target.position) return;
    var line = mouseEvent.target.position.lineNumber;
    if (breakpoints.has(line)) breakpoints.delete(line);
    else breakpoints.add(line);
    renderGutter();
    post({ action: "breakpoints", lines: Array.from(breakpoints) });
  }

  function renderLocals(locals, paused) {
    localsPanel.textContent = "";
    localsPanel.appendChild(element("div", "glua-paused-location", (paused.source || "playground.lua") + ":" + paused.line + " · " + paused.reason));
    if (!locals.length) {
      localsPanel.appendChild(element("p", "", "当前行没有可见局部变量。"));
      return;
    }
    locals.forEach(function (variable) {
      localsPanel.appendChild(renderVariable(variable));
    });
  }

  function renderVariable(variable) {
    var hasChildren = variable.children && variable.children.length;
    var node = element(hasChildren ? "details" : "div", "glua-variable");
    var row = element(hasChildren ? "summary" : "div", "glua-variable-row");
    row.appendChild(element("span", "glua-variable-name", variable.name + (variable.const ? " (const)" : "")));
    row.appendChild(element("span", "glua-variable-value", variable.value));
    row.appendChild(element("span", "glua-variable-type", variable.type));
    node.appendChild(row);
    if (hasChildren) {
      var children = element("div", "glua-variable-children");
      variable.children.forEach(function (child) { children.appendChild(renderVariable(child)); });
      node.appendChild(children);
    }
    return node;
  }

  function updateControls() {
    if (!modal) return;
    statusLabel.textContent = {
      idle: "就绪",
      loading: "正在加载 WASM",
      running: "运行中",
      paused: "已暂停",
      finished: "已结束",
      formatting: "正在格式化",
    }[sessionState] || sessionState;
    modal.querySelectorAll("[data-action]").forEach(function (control) {
      var action = control.dataset.action;
      if (action === "reauthorize") {
        control.hidden = !(workspaceFileSystem && workspaceFileSystem.snapshot().needsPermission);
        control.disabled = false;
      }
      if (action === "stop") control.disabled = ["loading", "running", "paused"].indexOf(sessionState) === -1;
      if (action === "pause") control.disabled = sessionState !== "running";
      if (["continue", "stepInto", "stepOver", "stepOut"].indexOf(action) !== -1) control.disabled = sessionState !== "paused";
      if (action === "format") control.disabled = ["loading", "running", "paused", "formatting"].indexOf(sessionState) !== -1;
      if (action === "sendInput" || action === "sendEOF") control.disabled = !worker;
    });
  }

  function openModal(source, label) {
    if (!window.monaco) {
      window.GLuaMonacoReady.then(function () { openModal(source, label); }).catch(function (error) {
        window.alert("Monaco 编辑器加载失败：" + String(error));
      });
      return;
    }
    ensureModal();
    output.textContent = "";
    localsPanel.textContent = "";
    breakpoints.clear();
    currentLine = 0;
    resetDialogPosition();
    renderGutter();
    modal.hidden = false;
    document.body.classList.add("glua-playground-open");
    editor.layout();
    Promise.resolve(workspaceInitialization).then(function (restored) {
      var explicitExample = label === "文档示例";
      if (explicitExample || !restored || !openDocuments.size) openScratchDocument(source || "", label || "文档示例");
      else if (activeDocumentPath && openDocuments.has(activeDocumentPath)) activateDocument(activeDocumentPath);
      editor.layout();
      editor.focus();
    });
  }

  function closeModal() {
    stopSession(false);
    if (!modal) return;
    persistWorkspaceSession();
    finishDialogDrag();
    modal.hidden = true;
    modal.classList.remove("is-fullscreen");
    resetDialogPosition();
    document.body.classList.remove("glua-playground-open");
  }

  function toggleFullscreen() {
    if (!modal) return;
    finishDialogDrag();
    var enteringFullscreen = !modal.classList.contains("is-fullscreen");
    modal.classList.toggle("is-fullscreen", enteringFullscreen);
    if (!enteringFullscreen) window.requestAnimationFrame(constrainDialogPosition);
  }

  function startDialogDrag(event) {
    if (!dialog || !dialogHeader || !modal || modal.hidden || modal.classList.contains("is-fullscreen")) return;
    if (event.isPrimary === false || event.button !== 0) return;
    if (event.target && event.target.closest && event.target.closest("button, input, textarea, select, a")) return;
    var rect = dialog.getBoundingClientRect();
    if (rect.width >= window.innerWidth - 1 || rect.height >= window.innerHeight - 1) return;
    dialogDragState = {
      pointerId: event.pointerId,
      startX: event.clientX,
      startY: event.clientY,
      offsetX: dialogOffsetX,
      offsetY: dialogOffsetY,
      rect: { left: rect.left, right: rect.right, top: rect.top, bottom: rect.bottom },
    };
    dialog.classList.add("is-dragging");
    if (dialogHeader.setPointerCapture) dialogHeader.setPointerCapture(event.pointerId);
    event.preventDefault();
  }

  function moveDialogDrag(event) {
    if (!dialogDragState || event.pointerId !== dialogDragState.pointerId || !window.GLuaPlaygroundEditorCore) return;
    var delta = window.GLuaPlaygroundEditorCore.clampDragDelta(
      dialogDragState.rect,
      { width: window.innerWidth, height: window.innerHeight },
      { x: event.clientX - dialogDragState.startX, y: event.clientY - dialogDragState.startY },
      8
    );
    setDialogPosition(dialogDragState.offsetX + delta.x, dialogDragState.offsetY + delta.y);
    event.preventDefault();
  }

  function finishDialogDrag(event) {
    if (!dialogDragState) return;
    var pointerId = dialogDragState.pointerId;
    dialogDragState = null;
    if (dialog) dialog.classList.remove("is-dragging");
    if (dialogHeader && dialogHeader.hasPointerCapture && dialogHeader.hasPointerCapture(pointerId)) {
      dialogHeader.releasePointerCapture(pointerId);
    }
    if (event) event.preventDefault();
  }

  function setDialogPosition(offsetX, offsetY) {
    dialogOffsetX = Number(offsetX) || 0;
    dialogOffsetY = Number(offsetY) || 0;
    if (!dialog) return;
    dialog.style.setProperty("--glua-dialog-x", dialogOffsetX + "px");
    dialog.style.setProperty("--glua-dialog-y", dialogOffsetY + "px");
  }

  function resetDialogPosition() {
    setDialogPosition(0, 0);
  }

  function constrainDialogPosition() {
    if (!dialog || !modal || modal.hidden || modal.classList.contains("is-fullscreen") || !window.GLuaPlaygroundEditorCore) return;
    var rect = dialog.getBoundingClientRect();
    if (rect.width >= window.innerWidth - 1 || rect.height >= window.innerHeight - 1) {
      resetDialogPosition();
      return;
    }
    var correction = window.GLuaPlaygroundEditorCore.clampDragDelta(
      { left: rect.left, right: rect.right, top: rect.top, bottom: rect.bottom },
      { width: window.innerWidth, height: window.innerHeight },
      { x: 0, y: 0 },
      8
    );
    setDialogPosition(dialogOffsetX + correction.x, dialogOffsetY + correction.y);
  }

  function handleShortcut(event) {
    if (!modal || modal.hidden) return;
    if (event.altKey && event.key === "Enter") {
      event.preventDefault();
      toggleFullscreen();
    } else if (event.key === "Escape" && modal.classList.contains("is-fullscreen")) {
      event.preventDefault();
      toggleFullscreen();
    } else if ((event.ctrlKey || event.metaKey) && event.key === "Enter") {
      event.preventDefault();
      start(false);
    } else if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "s") {
      event.preventDefault();
      saveActiveDocument();
    } else if ((event.ctrlKey || event.metaKey) && event.code === "Space") {
      event.preventDefault();
      editor.trigger("glua", "editor.action.triggerSuggest", {});
    } else if (event.altKey && event.shiftKey && event.key.toLowerCase() === "f") {
      if (event.defaultPrevented) return;
      event.preventDefault();
      formatSource();
    } else if (event.key === "F5" && event.shiftKey) {
      event.preventDefault();
      stopSession(true);
    } else if (event.key === "F5") {
      event.preventDefault();
      if (sessionState === "paused") sendDebug("continue");
      else start(true);
    } else if (event.key === "F10") {
      event.preventDefault();
      sendDebug("stepOver");
    } else if (event.key === "F11" && event.shiftKey) {
      event.preventDefault();
      sendDebug("stepOut");
    } else if (event.key === "F11") {
      event.preventDefault();
      sendDebug("stepInto");
    }
  }

  function decorateExamples() {
    document.querySelectorAll("pre[data-lang='lua'], pre[data-lang='glua'], pre code.lang-lua, pre code.lang-glua, pre code.language-lua, pre code.language-glua").forEach(function (matched) {
      var pre = matched.tagName === "PRE" ? matched : matched.closest("pre");
      if (!pre || pre.dataset.gluaRunnable === "true") return;
      var code = pre.querySelector("code") || matched;
      pre.dataset.gluaRunnable = "true";
      pre.classList.add("glua-runnable-example");
      var run = element("button", "glua-run-example", "运行");
      run.type = "button";
      run.title = "在 GLua Playground 中运行此示例";
      run.addEventListener("click", function () {
        openModal(code.textContent, "文档示例");
      });
      pre.appendChild(run);
    });
  }

  function ensurePageActions() {
    if (pageActions) return;
    pageActions = element("nav", "glua-page-actions");
    pageActions.setAttribute("aria-label", "文档快捷操作");
    pageTopButton = element("button", "glua-page-action", "Top");
    pageTopButton.type = "button";
    pageTopButton.title = "回到页面顶部";
    pageTopButton.hidden = true;
    pageTopButton.addEventListener("click", function () {
      window.scrollTo({ top: 0, behavior: "smooth" });
    });
    var debugButton = element("button", "glua-page-action glua-page-action-debug", "Code");
    debugButton.type = "button";
    debugButton.title = "打开 GLua Code 窗口";
    debugButton.addEventListener("click", function () {
      var code = document.querySelector("pre[data-glua-runnable='true'] code");
      var source = code ? code.textContent : '-- 在这里编写 GLua 代码\nprint("Hello, GLua!")\n';
      openModal(source, code ? "当前页面示例" : "快速调试");
    });
    pageActions.appendChild(pageTopButton);
    pageActions.appendChild(debugButton);
    document.body.appendChild(pageActions);
    window.addEventListener("scroll", updateTopButton, { passive: true });
    updateTopButton();
  }

  function updateTopButton() {
    if (!pageTopButton) return;
    var scrollingElement = document.scrollingElement || document.documentElement;
    pageTopButton.hidden = (window.scrollY || scrollingElement.scrollTop || 0) <= 160;
  }

  window.$docsify = window.$docsify || {};
  window.$docsify.plugins = (window.$docsify.plugins || []).concat(function (hook) {
    hook.doneEach(function () {
      decorateExamples();
      ensurePageActions();
    });
  });
  window.GLuaPlayground = { open: openModal };
})();

package com.glua.jetbrains;

import com.intellij.openapi.editor.Document;

import java.util.ArrayDeque;
import java.util.ArrayList;
import java.util.Deque;
import java.util.HashSet;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Set;

final class GluaAnalysis {
    private static final Map<String, String> VALUE_RETURN_TYPES = Map.ofEntries(
        Map.entry("io.open", "file"),
        Map.entry("io.popen", "file"),
        Map.entry("io.tmpfile", "file"),
        Map.entry("io.input", "file"),
        Map.entry("io.output", "file"),
        Map.entry("io.stdin", "file"),
        Map.entry("io.stdout", "file"),
        Map.entry("io.stderr", "file"),
        Map.entry("file.write", "file")
    );
    private static final Map<String, Set<String>> TYPE_METHODS = Map.of(
        "file", Set.of("close", "flush", "lines", "read", "seek", "setvbuf", "write"),
        "table", Set.of("concat", "insert", "move", "pack", "remove", "sort", "unpack"),
        "string", Set.of("byte", "char", "dump", "find", "format", "gmatch", "gsub", "len", "lower", "match", "pack", "packsize", "rep", "reverse", "sub", "unpack", "upper"),
        "math", Set.of("abs", "acos", "asin", "atan", "ceil", "cos", "deg", "exp", "floor", "fmod", "log", "max", "min", "modf", "rad", "random", "randomseed", "sin", "sqrt", "tan", "tointeger", "type", "ult"),
        "io", Set.of("close", "flush", "input", "lines", "open", "output", "popen", "read", "tmpfile", "type", "write"),
        "os", Set.of("clock", "date", "difftime", "execute", "exit", "getenv", "remove", "rename", "setlocale", "time", "tmpname"),
        "coroutine", Set.of("create", "resume", "running", "status", "wrap", "yield"),
        "debug", Set.of("debug", "gethook", "getinfo", "getlocal", "getmetatable", "getregistry", "getupvalue", "getuservalue", "sethook", "setlocal", "setmetatable", "setupvalue", "setuservalue", "traceback", "upvalueid", "upvaluejoin"),
        "utf8", Set.of("char", "codes", "codepoint", "len", "offset"),
        "package", Set.of("loadlib", "searchpath")
    );
    private static final Set<String> STANDARD_DECLARED = Set.of(
        "_G", "_VERSION", "_ENV", "false", "nil", "true",
        "string", "math", "table", "io", "os", "coroutine", "debug", "utf8", "package",
        "assert", "collectgarbage", "dofile", "error", "getmetatable", "ipairs", "load",
        "loadfile", "next", "pairs", "pcall", "print", "rawequal", "rawget", "rawlen",
        "rawset", "require", "select", "setmetatable", "tonumber", "tostring", "type",
        "xpcall"
    );

    private GluaAnalysis() {
    }

    static String builtinTargetAt(Document document, int offset) {
        return builtinTargetAt(document.getCharsSequence(), offset);
    }

    static String builtinTargetAt(CharSequence source, int offset) {
        List<GluaToken> tokens = GluaLexerUtil.scan(source);
        int index = nameTokenIndexAt(tokens, Math.max(0, offset));
        if (index < 0) {
            return null;
        }
        GluaToken token = tokens.get(index);
        if (!token.isName()) {
            return null;
        }
        GluaBuiltinCatalog catalog = GluaBuiltinCatalog.getInstance();
        if (index > 1 && isSeparator(tokens.get(index - 1)) && tokens.get(index - 2).isName()) {
            GluaToken separator = tokens.get(index - 1);
            String receiver = tokens.get(index - 2).text;
            String module = separator.text.equals(":")
                ? inferredReceiverType(tokens, index - 2, token.start)
                : "";
            if (module == null || module.isBlank()) {
                module = receiver;
            }
            String qualified = module + "." + token.text;
            if (catalog.get(qualified) != null) {
                return qualified;
            }
            String methodTarget = catalog.targetForMethod(token.text, module);
            if (methodTarget != null) {
                return methodTarget;
            }
        }
        if (index + 2 < tokens.size() && isSeparator(tokens.get(index + 1)) && tokens.get(index + 2).isName()) {
            String qualified = token.text + "." + tokens.get(index + 2).text;
            if (catalog.get(qualified) != null) {
                return qualified;
            }
            String methodTarget = catalog.targetForMethod(tokens.get(index + 2).text, token.text);
            if (methodTarget != null) {
                return methodTarget;
            }
        }
        if (catalog.get(token.text) != null) {
            return token.text;
        }
        return null;
    }

    private static int nameTokenIndexAt(List<GluaToken> tokens, int offset) {
        int index = GluaLexerUtil.tokenIndexAt(tokens, offset);
        if (index >= 0 && tokens.get(index).isName()) {
            return index;
        }
        if (index >= 0) {
            int previous = previousVisibleIndex(tokens, index);
            if (previous >= 0 && tokens.get(previous).isName()) {
                return previous;
            }
            int next = nextVisibleIndex(tokens, index);
            if (next >= 0 && tokens.get(next).isName()) {
                return next;
            }
        }
        if (offset > 0) {
            int before = GluaLexerUtil.tokenIndexAt(tokens, offset - 1);
            if (before >= 0 && tokens.get(before).isName()) {
                return before;
            }
        }
        return -1;
    }

    static CompletionContext completionContext(Document document, int offset) {
        List<GluaToken> tokens = GluaLexerUtil.scan(document.getCharsSequence());
        int at = GluaLexerUtil.tokenIndexAt(tokens, offset);
        if (at >= 0) {
            GluaToken current = tokens.get(at);
            if (current.isName()) {
                if (at > 1 && isSeparator(tokens.get(at - 1)) && tokens.get(at - 2).isName()) {
                    String module = completionModule(tokens, at - 1, at - 2, current.start);
                    return new CompletionContext(true, false, module, tokens.get(at - 2).text, tokens.get(at - 1).text, current.text);
                }
                return new CompletionContext(false, keywordCompletionMode(tokens, at), "", "", "", current.text);
            }
        }
        int before = GluaLexerUtil.tokenIndexBefore(tokens, offset);
        if (before >= 0 && tokens.get(before).isName() && before > 1 && isSeparator(tokens.get(before - 1)) && tokens.get(before - 2).isName()) {
            String module = completionModule(tokens, before - 1, before - 2, offset);
            return new CompletionContext(true, false, module, tokens.get(before - 2).text, tokens.get(before - 1).text, tokens.get(before).text);
        }
        if (before >= 0 && isSeparator(tokens.get(before)) && before > 0 && tokens.get(before - 1).isName()) {
            String module = completionModule(tokens, before, before - 1, offset);
            return new CompletionContext(true, false, module, tokens.get(before - 1).text, tokens.get(before).text, "");
        }
        return new CompletionContext(false, false, "", "", "", "");
    }

    private static boolean keywordCompletionMode(List<GluaToken> tokens, int tokenIndex) {
        if (tokenIndex < 0) {
            return false;
        }
        for (int cursor = tokenIndex - 1; cursor >= 0; cursor--) {
            GluaToken token = tokens.get(cursor);
            if (token.type.equals("space") || token.type.equals("comment")) {
                continue;
            }
            if (token.text.equals("for") || token.text.equals("while") || token.text.equals("if")) {
                return true;
            }
            if (token.text.equals("do") || token.text.equals("then") || token.text.equals("end") || token.text.equals(";")) {
                return false;
            }
        }
        return false;
    }

    static List<String> symbolCompletionNames(Document document, String prefix) {
        return symbolSnapshot(document.getCharsSequence()).completionNames(prefix);
    }

    static List<SymbolCompletion> symbolCompletions(Document document, String prefix) {
        return symbolSnapshot(document.getCharsSequence()).completions(prefix);
    }

    private static String completionModule(List<GluaToken> tokens, int separatorIndex, int receiverIndex, int offset) {
        String inferred = tokens.get(separatorIndex).text.equals(":")
            ? inferredReceiverType(tokens, receiverIndex, offset)
            : "";
        if (inferred != null && !inferred.isBlank()) {
            return inferred;
        }
        return tokens.get(receiverIndex).text;
    }

    private static String inferredReceiverType(List<GluaToken> tokens, int receiverIndex, int offset) {
        String receiver = tokens.get(receiverIndex).text;
        String inferred = "";
        for (int i = 0; i < receiverIndex; i++) {
            GluaToken token = tokens.get(i);
            if (!token.text.equals(receiver) || token.start >= offset) {
                continue;
            }
            String candidate = inferredTypeFromAssignment(tokens, i, offset);
            if (candidate != null && !candidate.isBlank()) {
                inferred = candidate;
            }
        }
        return inferred;
    }

    private static String inferredTypeFromAssignment(List<GluaToken> tokens, int variableIndex, int offset) {
        int next = nextVisibleIndex(tokens, variableIndex);
        if (next < 0 || !tokens.get(next).text.equals("=")) {
            return "";
        }
        int moduleIndex = nextVisibleIndex(tokens, next);
        int separatorIndex = nextVisibleIndex(tokens, moduleIndex);
        int memberIndex = nextVisibleIndex(tokens, separatorIndex);
        if (moduleIndex < 0 || separatorIndex < 0 || memberIndex < 0 || tokens.get(memberIndex).start >= offset) {
            return "";
        }
        GluaToken module = tokens.get(moduleIndex);
        if (module.text.equals("{")) {
            return "table";
        }
        if (module.type.equals("string")) {
            return "string";
        }
        GluaToken separator = tokens.get(separatorIndex);
        GluaToken member = tokens.get(memberIndex);
        if (!module.isName() || !member.isName() || !separator.text.equals(".")) {
            return "";
        }
        return VALUE_RETURN_TYPES.getOrDefault(module.text + "." + member.text, "");
    }

    static TextDefinition localDefinition(Document document, String name, int offset) {
        return symbolSnapshot(document.getCharsSequence()).definition(name, offset);
    }

    static void collectDiagnostics(CharSequence source, DiagnosticSink sink) {
        List<GluaToken> tokens = GluaLexerUtil.scan(source);
        Deque<String> blocks = new ArrayDeque<>();
        Deque<SwitchCaseScope> switchScopes = new ArrayDeque<>();
        for (int i = 0; i < tokens.size(); i++) {
            GluaToken token = tokens.get(i);
            if (!"keyword".equals(token.type)) {
                continue;
            }
            switch (token.text) {
                case "switch" -> {
                    blocks.push(token.text);
                    switchScopes.push(new SwitchCaseScope());
                }
                case "repeat", "if", "while", "for", "function" -> blocks.push(token.text);
                case "do" -> {
                    GluaToken previous = previousVisible(tokens, i);
                    if (previous == null || previous.text.equals("then") || previous.text.equals("end")) {
                        blocks.push("do");
                    }
                }
                case "case", "default" -> {
                    if (!blocks.contains("switch") || !lineStart(source, token.start)) {
                        sink.error(token.start, token.end, "syntax error near '" + token.text + "'");
                    } else if (token.text.equals("case") && !switchScopes.isEmpty()) {
                        collectDuplicateSwitchCaseValues(source, tokens, i, switchScopes.peek(), sink);
                    }
                }
                case "end" -> {
                    if (blocks.isEmpty()) {
                        sink.error(token.start, token.end, "syntax error near 'end'");
                    } else {
                        String block = blocks.pop();
                        if (block.equals("switch") && !switchScopes.isEmpty()) {
                            switchScopes.pop();
                        }
                    }
                }
                case "until" -> {
                    if (!blocks.isEmpty() && blocks.peek().equals("repeat")) {
                        blocks.pop();
                    } else {
                        sink.error(token.start, token.end, "syntax error near 'until'");
                    }
                }
                default -> {
                }
            }
        }
        if (!blocks.isEmpty()) {
            int end = source.length();
            sink.error(Math.max(0, end - 1), end, "syntax error near <eof>");
        }
        SymbolSnapshot symbols = symbolSnapshot(source, tokens);
        collectTypedMethodDiagnostics(tokens, sink);
        collectUndeclaredIdentifierDiagnostics(tokens, symbols, sink);
    }

    private static void collectDuplicateSwitchCaseValues(CharSequence source,
                                                         List<GluaToken> tokens,
                                                         int caseIndex,
                                                         SwitchCaseScope scope,
                                                         DiagnosticSink sink) {
        for (int i = nextVisibleIndex(tokens, caseIndex); i >= 0 && i < tokens.size(); i = nextVisibleIndex(tokens, i)) {
            GluaToken token = tokens.get(i);
            if (hasLineBreakBetween(source, tokens.get(caseIndex).start, token.start)) {
                return;
            }
            if (token.text.equals(",")) {
                continue;
            }
            String key = staticCaseValueKey(token);
            if (key == null) {
                continue;
            }
            Integer firstStart = scope.values.putIfAbsent(key, token.start);
            if (firstStart != null) {
                sink.error(token.start, token.end, "duplicate switch case value");
            }
        }
    }

    private static boolean hasLineBreakBetween(CharSequence source, int start, int end) {
        for (int i = Math.max(0, start); i < Math.min(source.length(), end); i++) {
            char ch = source.charAt(i);
            if (ch == '\n' || ch == '\r') {
                return true;
            }
        }
        return false;
    }

    private static String staticCaseValueKey(GluaToken token) {
        if (token.type.equals("number")) {
            try {
                double value = Double.parseDouble(token.text);
                if (Double.isFinite(value) && Math.rint(value) == value) {
                    return "number:int:" + Long.toString((long) value);
                }
                if (Double.isFinite(value)) {
                    return "number:float:" + Double.toString(value);
                }
            } catch (NumberFormatException ignored) {
                return "number:text:" + token.text;
            }
            return "number:text:" + token.text;
        }
        if (token.type.equals("string")) {
            return "string:" + token.text;
        }
        if (token.type.equals("keyword") && (token.text.equals("nil") || token.text.equals("true") || token.text.equals("false"))) {
            return "keyword:" + token.text;
        }
        return null;
    }

    private static final class SwitchCaseScope {
        private final Map<String, Integer> values = new LinkedHashMap<>();
    }

    private static void collectTypedMethodDiagnostics(List<GluaToken> tokens, DiagnosticSink sink) {
        for (int i = 2; i + 1 < tokens.size(); i++) {
            GluaToken receiver = tokens.get(i - 2);
            GluaToken separator = tokens.get(i - 1);
            GluaToken method = tokens.get(i);
            GluaToken call = nextVisible(tokens, i);
            if (!separator.text.equals(":") || !receiver.isName() || !method.isName() || call == null || !call.text.equals("(")) {
                continue;
            }
            GluaToken beforeReceiver = previousVisible(tokens, i - 2);
            if (beforeReceiver != null && beforeReceiver.text.equals("function")) {
                continue;
            }
            String receiverType = inferredReceiverType(tokens, i - 2, method.start);
            if (receiverType == null || receiverType.isBlank()) {
                continue;
            }
            Set<String> methods = TYPE_METHODS.get(receiverType);
            if (methods == null || methods.contains(method.text)) {
                continue;
            }
            sink.error(method.start, method.end, "type '" + receiverType + "' has no method '" + method.text + "'");
        }
    }

    private static void collectUndeclaredIdentifierDiagnostics(List<GluaToken> tokens, SymbolSnapshot symbols, DiagnosticSink sink) {
        Set<String> declared = symbols.declared();
        Set<String> reported = new HashSet<>();
        for (int i = 0; i < tokens.size(); i++) {
            GluaToken token = tokens.get(i);
            if (!token.type.equals("identifier") || declared.contains(token.text)) {
                continue;
            }
            GluaToken previous = previousVisible(tokens, i);
            GluaToken next = nextVisible(tokens, i);
            if (previous != null && (previous.text.equals(".") || previous.text.equals(":") || previous.text.equals("function"))) {
                continue;
            }
            if (next != null && (next.text.equals("=") || next.text.equals(".") || next.text.equals(":"))) {
                continue;
            }
            String key = token.text + ":" + token.start;
            if (!reported.add(key)) {
                continue;
            }
            sink.error(token.start, token.end, "undefined identifier '" + token.text + "'");
        }
    }

    private static SymbolSnapshot symbolSnapshot(CharSequence source) {
        return symbolSnapshot(source, GluaLexerUtil.scan(source));
    }

    private static SymbolSnapshot symbolSnapshot(CharSequence source, List<GluaToken> tokens) {
        SymbolSnapshot symbols = new SymbolSnapshot(new HashSet<>(STANDARD_DECLARED), new LinkedHashMap<>(), new LinkedHashMap<>(), new LinkedHashMap<>());
        for (int i = 0; i < tokens.size(); i++) {
            GluaToken token = tokens.get(i);
            if (!token.type.equals("keyword")) {
                continue;
            }
            if (token.text.equals("local")) {
                int next = nextVisibleIndex(tokens, i);
                if (next >= 0 && tokens.get(next).text.equals("function")) {
                    int name = nextVisibleIndex(tokens, next);
                    if (name >= 0 && tokens.get(name).isName()) {
                        addFunctionSymbol(symbols, tokens.get(name), functionSignature(tokens, name));
                        collectFunctionParameters(tokens, name, symbols);
                    }
                    continue;
                }
                for (int cursor = next; cursor >= 0 && cursor < tokens.size(); cursor = nextVisibleIndex(tokens, cursor)) {
                    GluaToken current = tokens.get(cursor);
                    if (current.text.equals("=") || current.text.equals("do")) {
                        break;
                    }
                    addSymbol(symbols, current);
                }
                continue;
            }
            if (token.text.equals("function")) {
                int name = nextVisibleIndex(tokens, i);
                if (name >= 0 && tokens.get(name).isName()) {
                    addFunctionSymbol(symbols, tokens.get(name), functionSignature(tokens, name));
                    collectFunctionParameters(tokens, name, symbols);
                    continue;
                }
                collectFunctionExpressionParameters(tokens, i, symbols);
                continue;
            }
            if (token.text.equals("for")) {
                for (int cursor = nextVisibleIndex(tokens, i); cursor >= 0 && cursor < tokens.size(); cursor = nextVisibleIndex(tokens, cursor)) {
                    GluaToken current = tokens.get(cursor);
                    if (current.text.equals("in") || current.text.equals("=") || current.text.equals("do")) {
                        break;
                    }
                    addSymbol(symbols, current);
                }
            }
        }
        collectAssignmentTargets(source, tokens, symbols);
        return symbols;
    }

    private static void addSymbol(SymbolSnapshot symbols, GluaToken token) {
        if (token == null || !token.isName() || token.type.equals("keyword")) {
            return;
        }
        symbols.declared().add(token.text);
        TextDefinition definition = new TextDefinition(token.start, token.end);
        symbols.definitions().computeIfAbsent(token.text, ignored -> new ArrayList<>()).add(definition);
        symbols.userSymbols().putIfAbsent(token.text, definition);
    }

    private static void addFunctionSymbol(SymbolSnapshot symbols, GluaToken token, String signature) {
        addSymbol(symbols, token);
        if (token != null && token.isName()) {
            symbols.functionSignatures().put(token.text, signature == null || signature.isBlank() ? token.text + "()" : signature);
        }
    }

    private static void collectAssignmentTargets(CharSequence source, List<GluaToken> tokens, SymbolSnapshot symbols) {
        for (int i = 0; i < tokens.size(); i++) {
            GluaToken token = tokens.get(i);
            if (!token.text.equals("=")) {
                continue;
            }
            if (delimiterDepthBefore(tokens, i) != 0) {
                continue;
            }
            int statementStart = assignmentStatementStart(source, tokens, i);
            collectSimpleAssignmentTargets(tokens, statementStart, i, symbols);
        }
    }

    private static void collectSimpleAssignmentTargets(List<GluaToken> tokens, int statementStart, int equalsIndex, SymbolSnapshot symbols) {
        int segmentStart = statementStart;
        int depth = 0;
        for (int cursor = statementStart; cursor <= equalsIndex; cursor++) {
            GluaToken current = tokens.get(cursor);
            if (cursor == equalsIndex || (current.text.equals(",") && depth == 0)) {
                addSimpleAssignmentTarget(tokens, segmentStart, cursor, symbols);
                segmentStart = cursor + 1;
                continue;
            }
            if (isOpenDelimiter(current.text)) {
                depth++;
                continue;
            }
            if (isCloseDelimiter(current.text) && depth > 0) {
                depth--;
            }
        }
    }

    private static void addSimpleAssignmentTarget(List<GluaToken> tokens, int start, int end, SymbolSnapshot symbols) {
        GluaToken onlyName = null;
        for (int cursor = start; cursor < end; cursor++) {
            GluaToken current = tokens.get(cursor);
            if (current.type.equals("space") || current.type.equals("comment")) {
                continue;
            }
            if (current.type.equals("keyword") && current.text.equals("local")) {
                continue;
            }
            if (onlyName != null || !current.isName() || current.type.equals("keyword")) {
                return;
            }
            onlyName = current;
        }
        if (onlyName != null) {
            addSymbol(symbols, onlyName);
        }
    }

    private static int assignmentStatementStart(CharSequence source, List<GluaToken> tokens, int equalsIndex) {
        GluaToken equalsToken = tokens.get(equalsIndex);
        for (int cursor = previousVisibleIndex(tokens, equalsIndex); cursor >= 0; cursor = previousVisibleIndex(tokens, cursor)) {
            GluaToken current = tokens.get(cursor);
            if (current.text.equals(";") || !sameLine(source, current.end, equalsToken.start)) {
                return nextVisibleIndex(tokens, cursor);
            }
        }
        return nextVisibleIndex(tokens, -1);
    }

    private static int delimiterDepthBefore(List<GluaToken> tokens, int tokenIndex) {
        int depth = 0;
        for (int cursor = 0; cursor < tokenIndex; cursor++) {
            GluaToken current = tokens.get(cursor);
            if (isOpenDelimiter(current.text)) {
                depth++;
                continue;
            }
            if (isCloseDelimiter(current.text) && depth > 0) {
                depth--;
            }
        }
        return depth;
    }

    private static boolean isOpenDelimiter(String text) {
        return text.equals("(") || text.equals("{") || text.equals("[");
    }

    private static boolean isCloseDelimiter(String text) {
        return text.equals(")") || text.equals("}") || text.equals("]");
    }

    private static boolean sameLine(CharSequence source, int start, int end) {
        int lower = Math.max(0, Math.min(start, end));
        int upper = Math.min(source.length(), Math.max(start, end));
        for (int i = lower; i < upper; i++) {
            char ch = source.charAt(i);
            if (ch == '\n' || ch == '\r') {
                return false;
            }
        }
        return true;
    }

    private static void collectFunctionParameters(List<GluaToken> tokens, int functionNameIndex, SymbolSnapshot symbols) {
        int openIndex = -1;
        for (int cursor = nextVisibleIndex(tokens, functionNameIndex); cursor >= 0 && cursor < tokens.size(); cursor = nextVisibleIndex(tokens, cursor)) {
            if (tokens.get(cursor).text.equals("(")) {
                openIndex = cursor;
                break;
            }
        }
        collectParametersAfterOpen(tokens, openIndex, symbols);
    }

    private static String functionSignature(List<GluaToken> tokens, int functionNameIndex) {
        GluaToken token = tokens.get(functionNameIndex);
        return token.text + "(" + String.join(", ", functionParameterNames(tokens, functionNameIndex)) + ")";
    }

    private static List<String> functionParameterNames(List<GluaToken> tokens, int functionNameIndex) {
        int openIndex = -1;
        for (int cursor = nextVisibleIndex(tokens, functionNameIndex); cursor >= 0 && cursor < tokens.size(); cursor = nextVisibleIndex(tokens, cursor)) {
            if (tokens.get(cursor).text.equals("(")) {
                openIndex = cursor;
                break;
            }
        }
        if (openIndex < 0) {
            return List.of();
        }
        List<String> params = new ArrayList<>();
        for (int cursor = nextVisibleIndex(tokens, openIndex); cursor >= 0 && cursor < tokens.size(); cursor = nextVisibleIndex(tokens, cursor)) {
            GluaToken current = tokens.get(cursor);
            if (current.text.equals(")")) {
                return params;
            }
            if (current.isName() && !current.type.equals("keyword")) {
                params.add(current.text);
            }
        }
        return params;
    }

    private static void collectFunctionExpressionParameters(List<GluaToken> tokens, int functionIndex, SymbolSnapshot symbols) {
        int openIndex = nextVisibleIndex(tokens, functionIndex);
        if (openIndex < 0 || !tokens.get(openIndex).text.equals("(")) {
            return;
        }
        collectParametersAfterOpen(tokens, openIndex, symbols);
    }

    private static void collectParametersAfterOpen(List<GluaToken> tokens, int openIndex, SymbolSnapshot symbols) {
        if (openIndex < 0) {
            return;
        }
        for (int cursor = nextVisibleIndex(tokens, openIndex); cursor >= 0 && cursor < tokens.size(); cursor = nextVisibleIndex(tokens, cursor)) {
            GluaToken current = tokens.get(cursor);
            if (current.text.equals(")")) {
                return;
            }
            addSymbol(symbols, current);
        }
    }

    private static boolean isSeparator(GluaToken token) {
        return token != null && (token.text.equals(".") || token.text.equals(":"));
    }

    private static GluaToken previousVisible(List<GluaToken> tokens, int index) {
        for (int i = index - 1; i >= 0; i--) {
            GluaToken token = tokens.get(i);
            if (!"space".equals(token.type) && !"comment".equals(token.type)) {
                return token;
            }
        }
        return null;
    }

    private static GluaToken nextVisible(List<GluaToken> tokens, int index) {
        int next = nextVisibleIndex(tokens, index);
        return next < 0 ? null : tokens.get(next);
    }

    private static int previousVisibleIndex(List<GluaToken> tokens, int index) {
        for (int i = index - 1; i >= 0; i--) {
            GluaToken token = tokens.get(i);
            if (!"space".equals(token.type) && !"comment".equals(token.type)) {
                return i;
            }
        }
        return -1;
    }

    private static int nextVisibleIndex(List<GluaToken> tokens, int index) {
        for (int i = index + 1; i < tokens.size(); i++) {
            GluaToken token = tokens.get(i);
            if (!"space".equals(token.type) && !"comment".equals(token.type)) {
                return i;
            }
        }
        return -1;
    }

    private static boolean lineStart(CharSequence source, int offset) {
        for (int i = offset - 1; i >= 0; i--) {
            char ch = source.charAt(i);
            if (ch == '\n' || ch == '\r') {
                return true;
            }
            if (!Character.isWhitespace(ch)) {
                return false;
            }
        }
        return true;
    }

    record CompletionContext(boolean method, boolean keywordOnly, String module, String receiver, String separator, String prefix) {
    }

    record TextDefinition(int start, int end) {
    }

    record SymbolCompletion(String name, String signature) {
    }

    record SymbolSnapshot(Set<String> declared, Map<String, List<TextDefinition>> definitions, Map<String, TextDefinition> userSymbols, Map<String, String> functionSignatures) {
        TextDefinition definition(String name, int offset) {
            List<TextDefinition> matches = definitions.getOrDefault(name, List.of());
            TextDefinition best = null;
            for (TextDefinition current : matches) {
                if (current.start() <= offset) {
                    best = current;
                } else if (best == null) {
                    best = current;
                }
            }
            return best;
        }

        List<String> completionNames(String prefix) {
            String effectivePrefix = prefix == null ? "" : prefix;
            List<String> names = new ArrayList<>();
            for (String name : userSymbols.keySet()) {
                if (name.startsWith(effectivePrefix)) {
                    names.add(name);
                }
            }
            return names;
        }

        List<SymbolCompletion> completions(String prefix) {
            String effectivePrefix = prefix == null ? "" : prefix;
            List<SymbolCompletion> completions = new ArrayList<>();
            for (String name : userSymbols.keySet()) {
                if (name.startsWith(effectivePrefix)) {
                    completions.add(new SymbolCompletion(name, functionSignatures.get(name)));
                }
            }
            return completions;
        }
    }

    interface DiagnosticSink {
        void error(int start, int end, String message);
    }
}

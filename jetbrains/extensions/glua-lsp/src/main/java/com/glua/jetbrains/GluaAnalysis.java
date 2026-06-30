package com.glua.jetbrains;

import com.intellij.openapi.editor.Document;

import java.util.ArrayDeque;
import java.util.Deque;
import java.util.HashSet;
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
                    return new CompletionContext(true, module, current.text);
                }
                return new CompletionContext(false, "", current.text);
            }
        }
        int before = GluaLexerUtil.tokenIndexBefore(tokens, offset);
        if (before >= 0 && isSeparator(tokens.get(before)) && before > 0 && tokens.get(before - 1).isName()) {
            String module = completionModule(tokens, before, before - 1, offset);
            return new CompletionContext(true, module, "");
        }
        return new CompletionContext(false, "", "");
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
        List<GluaToken> tokens = GluaLexerUtil.scan(document.getCharsSequence());
        TextDefinition best = null;
        for (int i = 0; i < tokens.size(); i++) {
            GluaToken token = tokens.get(i);
            if (!token.text.equals(name)) {
                continue;
            }
            if (!isDefinitionAt(tokens, i)) {
                continue;
            }
            TextDefinition current = new TextDefinition(token.start, token.end);
            if (token.start <= offset) {
                best = current;
            } else if (best == null) {
                best = current;
            }
        }
        return best;
    }

    static void collectDiagnostics(CharSequence source, DiagnosticSink sink) {
        List<GluaToken> tokens = GluaLexerUtil.scan(source);
        Deque<String> blocks = new ArrayDeque<>();
        for (int i = 0; i < tokens.size(); i++) {
            GluaToken token = tokens.get(i);
            if (!"keyword".equals(token.type)) {
                continue;
            }
            switch (token.text) {
                case "switch", "repeat", "if", "while", "for", "function" -> blocks.push(token.text);
                case "do" -> {
                    GluaToken previous = previousVisible(tokens, i);
                    if (previous == null || previous.text.equals("then") || previous.text.equals("end")) {
                        blocks.push("do");
                    }
                }
                case "case", "default" -> {
                    if (!blocks.contains("switch") || !lineStart(source, token.start)) {
                        sink.error(token.start, token.end, "syntax error near '" + token.text + "'");
                    }
                }
                case "end" -> {
                    if (blocks.isEmpty()) {
                        sink.error(token.start, token.end, "syntax error near 'end'");
                    } else {
                        blocks.pop();
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
        collectTypedMethodDiagnostics(tokens, sink);
        collectUndeclaredIdentifierDiagnostics(tokens, sink);
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

    private static void collectUndeclaredIdentifierDiagnostics(List<GluaToken> tokens, DiagnosticSink sink) {
        Set<String> declared = declaredIdentifiers(tokens);
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

    private static Set<String> declaredIdentifiers(List<GluaToken> tokens) {
        Set<String> declared = new HashSet<>(STANDARD_DECLARED);
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
                        declared.add(tokens.get(name).text);
                        collectFunctionParameters(tokens, name, declared);
                    }
                    continue;
                }
                for (int cursor = next; cursor >= 0 && cursor < tokens.size(); cursor = nextVisibleIndex(tokens, cursor)) {
                    GluaToken current = tokens.get(cursor);
                    if (current.text.equals("=") || current.text.equals("do")) {
                        break;
                    }
                    if (current.isName()) {
                        declared.add(current.text);
                    }
                }
                continue;
            }
            if (token.text.equals("function")) {
                int name = nextVisibleIndex(tokens, i);
                if (name >= 0 && tokens.get(name).isName()) {
                    declared.add(tokens.get(name).text);
                    collectFunctionParameters(tokens, name, declared);
                    continue;
                }
                collectFunctionExpressionParameters(tokens, i, declared);
                continue;
            }
            if (token.text.equals("for")) {
                for (int cursor = nextVisibleIndex(tokens, i); cursor >= 0 && cursor < tokens.size(); cursor = nextVisibleIndex(tokens, cursor)) {
                    GluaToken current = tokens.get(cursor);
                    if (current.text.equals("in") || current.text.equals("=") || current.text.equals("do")) {
                        break;
                    }
                    if (current.isName()) {
                        declared.add(current.text);
                    }
                }
            }
        }
        return declared;
    }

    private static void collectFunctionParameters(List<GluaToken> tokens, int functionNameIndex, Set<String> declared) {
        int openIndex = -1;
        for (int cursor = nextVisibleIndex(tokens, functionNameIndex); cursor >= 0 && cursor < tokens.size(); cursor = nextVisibleIndex(tokens, cursor)) {
            if (tokens.get(cursor).text.equals("(")) {
                openIndex = cursor;
                break;
            }
        }
        collectParametersAfterOpen(tokens, openIndex, declared);
    }

    private static void collectFunctionExpressionParameters(List<GluaToken> tokens, int functionIndex, Set<String> declared) {
        int openIndex = nextVisibleIndex(tokens, functionIndex);
        if (openIndex < 0 || !tokens.get(openIndex).text.equals("(")) {
            return;
        }
        collectParametersAfterOpen(tokens, openIndex, declared);
    }

    private static void collectParametersAfterOpen(List<GluaToken> tokens, int openIndex, Set<String> declared) {
        if (openIndex < 0) {
            return;
        }
        for (int cursor = nextVisibleIndex(tokens, openIndex); cursor >= 0 && cursor < tokens.size(); cursor = nextVisibleIndex(tokens, cursor)) {
            GluaToken current = tokens.get(cursor);
            if (current.text.equals(")")) {
                return;
            }
            if (current.isName()) {
                declared.add(current.text);
            }
        }
    }

    private static boolean isSeparator(GluaToken token) {
        return token != null && (token.text.equals(".") || token.text.equals(":"));
    }

    private static boolean isDefinitionAt(List<GluaToken> tokens, int index) {
        GluaToken prev = previousVisible(tokens, index);
        GluaToken prev2 = previousVisible(tokens, tokens.indexOf(prev));
        return prev != null && (prev.text.equals("local") || prev.text.equals("function") || prev.text.equals(":"))
            || prev != null && prev2 != null && prev2.text.equals("local") && prev.text.equals("function");
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

    record CompletionContext(boolean method, String module, String prefix) {
    }

    record TextDefinition(int start, int end) {
    }

    interface DiagnosticSink {
        void error(int start, int end, String message);
    }
}

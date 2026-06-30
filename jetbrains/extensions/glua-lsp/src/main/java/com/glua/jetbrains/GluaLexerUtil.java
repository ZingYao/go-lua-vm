package com.glua.jetbrains;

import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import java.util.Set;

final class GluaLexerUtil {
    static final Set<String> KEYWORDS = Set.of(
        "and", "break", "do", "else", "elseif", "end", "false", "for", "function", "if",
        "in", "local", "nil", "not", "or", "repeat", "return", "then", "true", "until",
        "while", "goto", "continue", "switch", "case", "default"
    );
    private static final Set<String> STANDARD_LIBRARIES = Set.of(
        "string", "math", "table", "io", "os", "coroutine", "debug", "utf8", "package"
    );
    private static final Set<String> BASE_BUILTIN_FUNCTIONS = Set.of(
        "assert", "collectgarbage", "dofile", "error", "getmetatable", "ipairs", "load",
        "loadfile", "next", "pairs", "pcall", "print", "rawequal", "rawget", "rawlen",
        "rawset", "require", "select", "setmetatable", "tonumber", "tostring", "type",
        "xpcall"
    );
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
        "io", Set.of("close", "flush", "input", "lines", "open", "output", "popen", "read", "tmpfile", "type", "write"),
        "os", Set.of("clock", "date", "difftime", "execute", "exit", "getenv", "remove", "rename", "setlocale", "time", "tmpname"),
        "coroutine", Set.of("create", "resume", "running", "status", "wrap", "yield"),
        "debug", Set.of("debug", "gethook", "getinfo", "getlocal", "getmetatable", "getregistry", "getupvalue", "getuservalue", "sethook", "setlocal", "setmetatable", "setupvalue", "setuservalue", "traceback", "upvalueid", "upvaluejoin"),
        "utf8", Set.of("char", "codes", "codepoint", "len", "offset"),
        "package", Set.of("loadlib", "searchpath")
    );

    private GluaLexerUtil() {
    }

    static List<GluaToken> scan(CharSequence source) {
        List<GluaToken> tokens = new ArrayList<>();
        int length = source.length();
        int index = 0;
        while (index < length) {
            char ch = source.charAt(index);
            if (Character.isWhitespace(ch)) {
                int start = index++;
                while (index < length && Character.isWhitespace(source.charAt(index))) {
                    index++;
                }
                tokens.add(new GluaToken("space", source.subSequence(start, index).toString(), start, index));
                continue;
            }
            if (ch == '-' && index + 1 < length && source.charAt(index + 1) == '-') {
                int start = index;
                index += 2;
                while (index < length && source.charAt(index) != '\n') {
                    index++;
                }
                tokens.add(new GluaToken("comment", source.subSequence(start, index).toString(), start, index));
                continue;
            }
            if (ch == '\'' || ch == '"') {
                int start = index;
                char quote = ch;
                index++;
                boolean escaped = false;
                while (index < length) {
                    char current = source.charAt(index++);
                    if (escaped) {
                        escaped = false;
                        continue;
                    }
                    if (current == '\\') {
                        escaped = true;
                        continue;
                    }
                    if (current == quote) {
                        break;
                    }
                }
                tokens.add(new GluaToken("string", source.subSequence(start, index).toString(), start, index));
                continue;
            }
            if (Character.isDigit(ch)) {
                int start = index++;
                while (index < length) {
                    char current = source.charAt(index);
                    if (!Character.isLetterOrDigit(current) && current != '.' && current != '_' && current != '+' && current != '-') {
                        break;
                    }
                    index++;
                }
                tokens.add(new GluaToken("number", source.subSequence(start, index).toString(), start, index));
                continue;
            }
            if (Character.isLetter(ch) || ch == '_') {
                int start = index++;
                while (index < length) {
                    char current = source.charAt(index);
                    if (!Character.isLetterOrDigit(current) && current != '_') {
                        break;
                    }
                    index++;
                }
                String text = source.subSequence(start, index).toString();
                String type = KEYWORDS.contains(text) ? "keyword" : "identifier";
                tokens.add(new GluaToken(type, text, start, index));
                continue;
            }
            int start = index;
            if (index + 1 < length) {
                String two = source.subSequence(index, index + 2).toString();
                if (two.equals("::") || two.equals("..") || two.equals("==") || two.equals("~=") || two.equals("<=") || two.equals(">=")) {
                    index += 2;
                    tokens.add(new GluaToken("operator", two, start, index));
                    continue;
                }
            }
            index++;
            String text = source.subSequence(start, index).toString();
            String type = "(){}[]<>=+-*/%^#.,;:".contains(text) ? "operator" : "bad";
            tokens.add(new GluaToken(type, text, start, index));
        }
        return enrichTokenTypes(tokens);
    }

    private static List<GluaToken> enrichTokenTypes(List<GluaToken> tokens) {
        List<GluaToken> enriched = new ArrayList<>(tokens.size());
        for (int i = 0; i < tokens.size(); i++) {
            GluaToken token = tokens.get(i);
            String type = token.type;
            if ("identifier".equals(type)) {
                GluaToken previous = visibleToken(tokens, i, -1);
                GluaToken next = visibleToken(tokens, i, 1);
                if (STANDARD_LIBRARIES.contains(token.text) && isSeparator(next)) {
                    type = "library";
                } else if (isKnownMemberFunction(tokens, i, previous, next)) {
                    type = "memberFunction";
                } else if (previous != null && previous.text.equals("function")) {
                    type = "functionDeclaration";
                } else if (BASE_BUILTIN_FUNCTIONS.contains(token.text) && next != null && next.text.equals("(")) {
                    type = "builtinFunction";
                } else if (!isSeparator(previous) && next != null && next.text.equals("(")) {
                    type = "functionCall";
                }
            }
            enriched.add(new GluaToken(type, token.text, token.start, token.end));
        }
        return enriched;
    }

    private static GluaToken visibleToken(List<GluaToken> tokens, int index, int step) {
        for (int i = index + step; i >= 0 && i < tokens.size(); i += step) {
            GluaToken token = tokens.get(i);
            if (!"space".equals(token.type) && !"comment".equals(token.type)) {
                return token;
            }
        }
        return null;
    }

    private static boolean isSeparator(GluaToken token) {
        return token != null && "operator".equals(token.type) && (token.text.equals(".") || token.text.equals(":"));
    }

    private static boolean isKnownMemberFunction(List<GluaToken> tokens, int index, GluaToken previous, GluaToken next) {
        if (!isSeparator(previous) || next == null || !next.text.equals("(")) {
            return false;
        }
        int separatorIndex = previousVisibleIndex(tokens, index);
        int receiverIndex = previousVisibleIndex(tokens, separatorIndex);
        if (receiverIndex < 0) {
            return false;
        }
        String module = tokens.get(separatorIndex).text.equals(":")
            ? inferredReceiverType(tokens, receiverIndex, tokens.get(index).start)
            : tokens.get(receiverIndex).text;
        if (module == null || module.isBlank()) {
            module = tokens.get(receiverIndex).text;
        }
        Set<String> methods = TYPE_METHODS.get(module);
        return methods != null && methods.contains(tokens.get(index).text);
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
        int equalsIndex = nextVisibleIndex(tokens, variableIndex);
        if (equalsIndex < 0 || !tokens.get(equalsIndex).text.equals("=")) {
            return "";
        }
        int moduleIndex = nextVisibleIndex(tokens, equalsIndex);
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

    static int tokenIndexAt(List<GluaToken> tokens, int offset) {
        for (int i = 0; i < tokens.size(); i++) {
            GluaToken token = tokens.get(i);
            if (token.start <= offset && offset < token.end && !"space".equals(token.type) && !"comment".equals(token.type)) {
                return i;
            }
        }
        return -1;
    }

    static int tokenIndexBefore(List<GluaToken> tokens, int offset) {
        int index = -1;
        for (int i = 0; i < tokens.size(); i++) {
            GluaToken token = tokens.get(i);
            if ("space".equals(token.type) || "comment".equals(token.type)) {
                continue;
            }
            if (token.end <= offset) {
                index = i;
                continue;
            }
            break;
        }
        return index;
    }
}

package com.glua.jetbrains;

import java.util.ArrayList;
import java.util.List;
import java.util.Set;

final class GluaLexerUtil {
    static final Set<String> KEYWORDS = Set.of(
        "and", "break", "do", "else", "elseif", "end", "false", "for", "function", "if",
        "in", "local", "nil", "not", "or", "repeat", "return", "then", "true", "until",
        "while", "goto", "continue", "switch", "case", "default"
    );
    private static final Set<String> STANDARD_LIBRARIES = Set.of(
        "string", "math", "table", "io", "os", "coroutine", "debug", "utf8"
    );
    private static final Set<String> BASE_BUILTIN_FUNCTIONS = Set.of(
        "assert", "collectgarbage", "dofile", "error", "getmetatable", "ipairs", "load",
        "loadfile", "next", "pairs", "pcall", "print", "rawequal", "rawget", "rawlen",
        "rawset", "require", "select", "setmetatable", "tonumber", "tostring", "type",
        "xpcall"
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
                } else if (isSeparator(previous) && next != null && next.text.equals("(")) {
                    type = "memberFunction";
                } else if (previous != null && previous.text.equals("function")) {
                    type = "functionDeclaration";
                } else if (BASE_BUILTIN_FUNCTIONS.contains(token.text) && next != null && next.text.equals("(")) {
                    type = "builtinFunction";
                } else if (next != null && next.text.equals("(")) {
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

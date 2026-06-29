package com.glua.jetbrains;

import com.intellij.openapi.editor.Document;

import java.util.ArrayDeque;
import java.util.Deque;
import java.util.List;

final class GluaAnalysis {
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
            String qualified = tokens.get(index - 2).text + "." + token.text;
            if (catalog.get(qualified) != null) {
                return qualified;
            }
            String methodTarget = catalog.targetForMethod(token.text, tokens.get(index - 2).text);
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
                    return new CompletionContext(true, tokens.get(at - 2).text, current.text);
                }
                return new CompletionContext(false, "", current.text);
            }
        }
        int before = GluaLexerUtil.tokenIndexBefore(tokens, offset);
        if (before >= 0 && isSeparator(tokens.get(before)) && before > 0 && tokens.get(before - 1).isName()) {
            return new CompletionContext(true, tokens.get(before - 1).text, "");
        }
        return new CompletionContext(false, "", "");
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

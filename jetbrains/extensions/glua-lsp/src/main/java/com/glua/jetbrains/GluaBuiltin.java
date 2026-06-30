package com.glua.jetbrains;

import java.util.List;
import java.util.Set;

final class GluaBuiltin {
    private static final Set<String> KEYWORDS = Set.of(
        "and", "break", "do", "else", "elseif", "end", "false", "for", "function", "if",
        "in", "local", "nil", "not", "or", "repeat", "return", "then", "true", "until",
        "while", "goto", "continue", "switch", "case", "default"
    );

    final String signature;
    final String description;
    final List<String> params;
    final String returns;
    final String example;

    GluaBuiltin(String signature, String description, List<String> params, String returns, String example) {
        this.signature = signature == null ? "" : signature;
        this.description = description == null ? "" : description;
        this.params = params == null ? List.of() : params;
        this.returns = returns == null ? "" : returns;
        this.example = example == null ? "" : example;
    }

    String markdown(String name) {
        Labels labels = Labels.of(GluaBuiltinCatalog.getInstance().locale());
        StringBuilder builder = new StringBuilder();
        builder.append(codeBlock(signature));
        builder.append("<p><b>").append(escape(labels.description)).append("</b><br/>").append(escape(description)).append("</p>");
        builder.append("<p><b>").append(escape(labels.parameters)).append("</b></p><ul>");
        for (String param : params) {
            builder.append("<li><code>").append(escape(param)).append("</code></li>");
        }
        builder.append("</ul>");
        builder.append("<p><b>").append(escape(labels.returns)).append("</b><br/>").append(escape(returns)).append("</p>");
        if (!example.isBlank()) {
            builder.append("<p><b>").append(escape(labels.example)).append("</b></p>").append(codeBlock(example));
        }
        builder.append("<p><code>").append(escape(name)).append("</code></p>");
        return builder.toString();
    }

    String quickInfo() {
        Labels labels = Labels.of(GluaBuiltinCatalog.getInstance().locale());
        StringBuilder builder = new StringBuilder("<html><body>");
        builder.append(codeBlock(signature));
        if (!description.isBlank()) {
            builder.append("<p><b>").append(escape(labels.description)).append("</b><br/>").append(escape(description)).append("</p>");
        }
        if (!params.isEmpty()) {
            builder.append("<p><b>").append(escape(labels.parameters)).append("</b></p><ul>");
            for (String param : params) {
                builder.append("<li><code>").append(escape(param)).append("</code></li>");
            }
            builder.append("</ul>");
        }
        if (!returns.isBlank()) {
            builder.append("<p><b>").append(escape(labels.returns)).append("</b><br/>").append(escape(returns)).append("</p>");
        }
        if (!example.isBlank()) {
            builder.append("<p><b>").append(escape(labels.example)).append("</b></p>").append(codeBlock(example));
        }
        builder.append("</body></html>");
        return builder.toString();
    }

    private static String escape(String value) {
        return value.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;");
    }

    private static String codeBlock(String code) {
        return "<pre style=\"padding:8px; white-space:pre-wrap;\">" + highlightCode(code) + "</pre>";
    }

    private static String highlightCode(String code) {
        StringBuilder builder = new StringBuilder();
        int index = 0;
        while (index < code.length()) {
            char ch = code.charAt(index);
            if (ch == '-' && index + 1 < code.length() && code.charAt(index + 1) == '-') {
                int start = index;
                index += 2;
                while (index < code.length() && code.charAt(index) != '\n') {
                    index++;
                }
                appendSpan(builder, "#808080", code.substring(start, index));
                continue;
            }
            if (ch == '\'' || ch == '"') {
                int start = index;
                char quote = ch;
                index++;
                boolean escaped = false;
                while (index < code.length()) {
                    char current = code.charAt(index++);
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
                appendSpan(builder, "#6A8759", code.substring(start, index));
                continue;
            }
            if (Character.isDigit(ch)) {
                int start = index++;
                while (index < code.length()) {
                    char current = code.charAt(index);
                    if (!Character.isLetterOrDigit(current) && current != '.' && current != '_' && current != '+' && current != '-') {
                        break;
                    }
                    index++;
                }
                appendSpan(builder, "#6897BB", code.substring(start, index));
                continue;
            }
            if (Character.isLetter(ch) || ch == '_') {
                int start = index++;
                while (index < code.length()) {
                    char current = code.charAt(index);
                    if (!Character.isLetterOrDigit(current) && current != '_') {
                        break;
                    }
                    index++;
                }
                String text = code.substring(start, index);
                if (KEYWORDS.contains(text)) {
                    appendSpan(builder, "#CC7832", text);
                } else if (nextNonSpace(code, index) == '(' || previousNonSpace(code, start) == '.' || previousNonSpace(code, start) == ':') {
                    appendSpan(builder, "#56A8F5", text);
                } else {
                    builder.append(escape(text));
                }
                continue;
            }
            builder.append(escape(String.valueOf(ch)));
            index++;
        }
        return builder.toString();
    }

    private static char previousNonSpace(String code, int index) {
        for (int i = index - 1; i >= 0; i--) {
            char ch = code.charAt(i);
            if (!Character.isWhitespace(ch)) {
                return ch;
            }
        }
        return '\0';
    }

    private static char nextNonSpace(String code, int index) {
        for (int i = index; i < code.length(); i++) {
            char ch = code.charAt(i);
            if (!Character.isWhitespace(ch)) {
                return ch;
            }
        }
        return '\0';
    }

    private static void appendSpan(StringBuilder builder, String color, String text) {
        builder.append("<span style=\"color:")
            .append(color)
            .append("\">")
            .append(escape(text))
            .append("</span>");
    }

    private record Labels(String description, String parameters, String returns, String example) {
        static Labels of(String locale) {
            String normalized = locale == null ? "" : locale.toLowerCase();
            if (normalized.startsWith("zh")) {
                return new Labels("说明", "参数", "返回值", "示例");
            }
            return new Labels("Description", "Parameters", "Returns", "Example");
        }
    }
}

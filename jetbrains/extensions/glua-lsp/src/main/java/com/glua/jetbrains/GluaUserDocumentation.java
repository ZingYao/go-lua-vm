package com.glua.jetbrains;

import com.intellij.openapi.util.TextRange;

import java.util.ArrayList;
import java.util.List;

final class GluaUserDocumentation {
    private GluaUserDocumentation() {
    }

    static Entry documentationAt(CharSequence source, int nameStart, int nameEnd, String name) {
        String comment = commentBlockBeforeOffset(source, nameStart);
        String locale = GluaBuiltinCatalog.getInstance().locale();
        Labels labels = Labels.of(locale);
        int line = lineNumber(source, nameStart);
        int column = columnNumber(source, nameStart);
        String html = html(name, comment, labels, line, column);
        String quick = quick(name, comment, labels, line, column);
        return new Entry(name, html, quick, TextRange.create(nameStart, nameEnd));
    }

    static String standardSnippet() {
        return String.join("\n",
            "-- description: function description",
            "-- param: name string parameter description",
            "-- return: nil",
            "-- example:",
            "--   module.function(name)",
            "-- output:",
            "--   expected output"
        );
    }

    private static String html(String name, String comment, Labels labels, int line, int column) {
        StringBuilder builder = new StringBuilder("<html><body>");
        builder.append("<p><code>").append(escape(name)).append("</code></p>");
        appendComment(builder, comment, labels);
        builder.append("<p>").append(escape(labels.definedAt(line, column))).append("</p>");
        builder.append("</body></html>");
        return builder.toString();
    }

    private static String quick(String name, String comment, Labels labels, int line, int column) {
        StringBuilder builder = new StringBuilder("<html><body>");
        builder.append("<p><code>").append(escape(name)).append("</code></p>");
        appendComment(builder, comment, labels);
        builder.append("<p>").append(escape(labels.definedAt(line, column))).append("</p>");
        builder.append("</body></html>");
        return builder.toString();
    }

    private static void appendComment(StringBuilder builder, String comment, Labels labels) {
        Annotation annotation = parse(comment);
        if (!annotation.description.isEmpty()) {
            builder.append("<p>").append(escape(String.join(" ", annotation.description))).append("</p>");
        }
        if (!annotation.params.isEmpty()) {
            builder.append("<p><b>").append(escape(labels.parameters)).append("</b></p><ul>");
            for (Param param : annotation.params) {
                builder.append("<li><code>").append(escape(param.name)).append("</code>");
                if (!param.type.isBlank()) {
                    builder.append(" <code>").append(escape(param.type)).append("</code>");
                }
                if (!param.description.isBlank()) {
                    builder.append(" - ").append(escape(param.description));
                }
                builder.append("</li>");
            }
            builder.append("</ul>");
        }
        if (!annotation.returns.isEmpty()) {
            builder.append("<p><b>").append(escape(labels.returns)).append("</b><br/>")
                .append(escape(String.join(" ", annotation.returns))).append("</p>");
        }
        if (!annotation.example.isEmpty()) {
            builder.append("<p><b>").append(escape(labels.example)).append("</b></p>")
                .append(codeBlock(String.join("\n", annotation.example)));
        }
        if (!annotation.output.isEmpty()) {
            builder.append("<p><b>").append(escape(labels.output)).append("</b></p>")
                .append(codeBlock(String.join("\n", annotation.output)));
        }
        if (!annotation.other.isEmpty()) {
            builder.append("<p>").append(escape(String.join("\n", annotation.other)).replace("\n", "<br/>")).append("</p>");
        }
    }

    private static Annotation parse(String comment) {
        Annotation annotation = new Annotation();
        if (comment == null || comment.isBlank()) {
            return annotation;
        }
        String section = "";
        for (String rawLine : comment.split("\\R")) {
            String line = rawLine.trim();
            if (line.isBlank()) {
                continue;
            }
            String lower = line.toLowerCase();
            if (lower.startsWith("description:") || lower.startsWith("desc:")) {
                section = "description";
                addRemainder(annotation.description, line);
                continue;
            }
            if (lower.startsWith("param:") || lower.startsWith("parameter:")) {
                section = "";
                annotation.params.add(parseParam(line));
                continue;
            }
            if (lower.startsWith("return:") || lower.startsWith("returns:")) {
                section = "returns";
                addRemainder(annotation.returns, line);
                continue;
            }
            if (lower.startsWith("example:")) {
                section = "example";
                addRemainder(annotation.example, line);
                continue;
            }
            if (lower.startsWith("output:")) {
                section = "output";
                addRemainder(annotation.output, line);
                continue;
            }
            switch (section) {
                case "description" -> annotation.description.add(line);
                case "returns" -> annotation.returns.add(line);
                case "example" -> annotation.example.add(line);
                case "output" -> annotation.output.add(line);
                default -> annotation.other.add(line);
            }
        }
        return annotation;
    }

    private static Param parseParam(String line) {
        String value = line.substring(line.indexOf(':') + 1).trim();
        String[] parts = value.split("\\s+", 3);
        String name = parts.length > 0 ? parts[0] : "";
        String type = parts.length > 1 ? parts[1] : "";
        String description = parts.length > 2 ? parts[2] : "";
        return new Param(name, type, description);
    }

    private static void addRemainder(List<String> target, String line) {
        int colon = line.indexOf(':');
        if (colon < 0) {
            return;
        }
        String value = line.substring(colon + 1).trim();
        if (!value.isBlank()) {
            target.add(value);
        }
    }

    private static String commentBlockBeforeOffset(CharSequence source, int offset) {
        String text = source.toString();
        int lineStart = text.lastIndexOf('\n', Math.max(0, offset - 1));
        int cursor = lineStart < 0 ? 0 : lineStart;
        List<String> lines = new ArrayList<>();
        while (cursor > 0) {
            int previousEnd = cursor;
            int previousStart = text.lastIndexOf('\n', previousEnd - 1) + 1;
            String line = text.substring(previousStart, previousEnd).trim();
            if (line.isBlank()) {
                if (lines.isEmpty()) {
                    cursor = Math.max(0, previousStart - 1);
                    continue;
                }
                break;
            }
            if (!line.startsWith("--")) {
                break;
            }
            lines.add(0, line.replaceFirst("^--\\s?", ""));
            cursor = Math.max(0, previousStart - 1);
        }
        return String.join("\n", lines).trim();
    }

    private static int lineNumber(CharSequence source, int offset) {
        int line = 1;
        for (int i = 0; i < Math.min(offset, source.length()); i++) {
            if (source.charAt(i) == '\n') {
                line++;
            }
        }
        return line;
    }

    private static int columnNumber(CharSequence source, int offset) {
        int start = source.toString().lastIndexOf('\n', Math.max(0, offset - 1));
        return offset - start;
    }

    private static String codeBlock(String code) {
        return "<pre style=\"padding:8px; white-space:pre-wrap;\">" + escape(code) + "</pre>";
    }

    private static String escape(String value) {
        return value == null ? "" : value.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;");
    }

    record Entry(String name, String html, String quickInfo, TextRange range) {
    }

    private record Labels(String parameters, String returns, String example, String output, String definedPattern) {
        static Labels of(String locale) {
            String normalized = locale == null ? "" : locale.toLowerCase();
            if (normalized.startsWith("zh")) {
                return new Labels("参数", "返回值", "示例", "输出", "定义于第 %d 行，第 %d 列。");
            }
            return new Labels("Parameters", "Returns", "Example", "Output", "Defined at line %d, column %d.");
        }

        String definedAt(int line, int column) {
            return String.format(definedPattern, line, column);
        }
    }

    private static final class Annotation {
        private final List<String> description = new ArrayList<>();
        private final List<Param> params = new ArrayList<>();
        private final List<String> returns = new ArrayList<>();
        private final List<String> example = new ArrayList<>();
        private final List<String> output = new ArrayList<>();
        private final List<String> other = new ArrayList<>();
    }

    private record Param(String name, String type, String description) {
    }
}

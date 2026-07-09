package com.glua.jetbrains;

import java.util.ArrayList;
import java.util.List;

final class GluaFormatter {
    private GluaFormatter() {
    }

    static String format(String source) {
        String[] lines = source.replace("\r\n", "\n").replace('\r', '\n').split("\n", -1);
        StringBuilder builder = new StringBuilder();
        List<String> frames = new ArrayList<>();
        CommentState commentState = new CommentState();
        for (int i = 0; i < lines.length; i++) {
            String rawTrimmed = lines[i].trim();
            if (rawTrimmed.isEmpty()) {
                builder.append('\n');
                continue;
            }
            SplitLine split = splitLineComment(rawTrimmed, commentState);
            String code = normalizeSpaces(split.code().trim());
            String comment = split.comment().trim();
            String first = firstWord(code);
            adjustBeforeLine(frames, first);
            builder.append("  ".repeat(Math.max(0, frames.size()))).append(joinCodeAndComment(code, comment));
            if (i < lines.length - 1) {
                builder.append('\n');
            }
            adjustAfterLine(frames, first, code);
        }
        return builder.toString();
    }

    private static SplitLine splitLineComment(String line, CommentState state) {
        if (!state.longCommentClose.isBlank()) {
            if (line.contains(state.longCommentClose)) {
                state.longCommentClose = "";
            }
            return new SplitLine("", line);
        }
        char quote = 0;
        boolean escaped = false;
        for (int i = 0; i < line.length(); i++) {
            char ch = line.charAt(i);
            if (quote != 0) {
                if (escaped) {
                    escaped = false;
                    continue;
                }
                if (ch == '\\') {
                    escaped = true;
                    continue;
                }
                if (ch == quote) {
                    quote = 0;
                }
                continue;
            }
            if (ch == '"' || ch == '\'') {
                quote = ch;
                continue;
            }
            if (ch == '-' && i + 1 < line.length() && line.charAt(i + 1) == '-') {
                String closeText = longBracketCloseText(line, i + 2);
                if (!closeText.isBlank() && !line.substring(i + 2).contains(closeText)) {
                    state.longCommentClose = closeText;
                }
                return new SplitLine(line.substring(0, i), line.substring(i));
            }
        }
        return new SplitLine(line, "");
    }

    private static String longBracketCloseText(String line, int openIndex) {
        if (openIndex >= line.length() || line.charAt(openIndex) != '[') {
            return "";
        }
        int index = openIndex + 1;
        while (index < line.length() && line.charAt(index) == '=') {
            index++;
        }
        if (index >= line.length() || line.charAt(index) != '[') {
            return "";
        }
        return "]" + "=".repeat(index - openIndex - 1) + "]";
    }

    private static String joinCodeAndComment(String code, String comment) {
        if (code.isBlank()) {
            return comment;
        }
        if (comment.isBlank()) {
            return code;
        }
        return code + " " + comment;
    }

    private static void adjustBeforeLine(List<String> frames, String first) {
        if (first.equals("end")) {
            popKind(frames, "case");
            popOne(frames);
            return;
        }
        if (first.equals("until")) {
            popUntil(frames, "repeat");
            return;
        }
        if (first.equals("else") || first.equals("elseif")) {
            popKind(frames, "normal");
            return;
        }
        if (first.equals("case") || first.equals("default")) {
            popKind(frames, "case");
        }
    }

    private static void adjustAfterLine(List<String> frames, String first, String line) {
        if (first.equals("switch")) {
            frames.add("switch");
            return;
        }
        if (first.equals("case") || first.equals("default")) {
            frames.add("case");
            return;
        }
        if (first.equals("repeat")) {
            frames.add("repeat");
            return;
        }
        if (first.equals("else") || first.equals("elseif")) {
            frames.add("normal");
            return;
        }
        if (opensBlock(first, line)) {
            frames.add("normal");
        }
    }

    private static void popOne(List<String> frames) {
        if (!frames.isEmpty()) {
            frames.remove(frames.size() - 1);
        }
    }

    private static void popKind(List<String> frames, String kind) {
        if (!frames.isEmpty() && frames.get(frames.size() - 1).equals(kind)) {
            frames.remove(frames.size() - 1);
        }
    }

    private static void popUntil(List<String> frames, String kind) {
        for (int i = frames.size() - 1; i >= 0; i--) {
            if (frames.get(i).equals(kind)) {
                while (frames.size() > i) {
                    frames.remove(frames.size() - 1);
                }
                return;
            }
        }
        frames.clear();
    }

    private static boolean opensBlock(String first, String line) {
        if (List.of("function", "repeat").contains(first)) {
            return blockOpenCount(line) > blockCloseCount(line);
        }
        if (line.endsWith(" then") || line.endsWith(" do") || line.startsWith("function ") || hasFunctionExpression(line)) {
            return blockOpenCount(line) > blockCloseCount(line);
        }
        return false;
    }

    private static boolean hasFunctionExpression(String line) {
        return line.matches(".*(^|[^A-Za-z0-9_])function\\s*\\(.*");
    }

    private static int blockOpenCount(String line) {
        return wordCount(line, "function") + wordCount(line, "then") + wordCount(line, "do");
    }

    private static int blockCloseCount(String line) {
        return wordCount(line, "end") + wordCount(line, "until");
    }

    private static int wordCount(String line, String word) {
        int count = 0;
        java.util.regex.Matcher matcher = java.util.regex.Pattern.compile("(^|[^A-Za-z0-9_])" + word + "([^A-Za-z0-9_]|$)").matcher(line);
        while (matcher.find()) {
            count++;
        }
        return count;
    }

    private static String firstWord(String line) {
        int space = line.indexOf(' ');
        return space < 0 ? line : line.substring(0, space);
    }

    private static String normalizeSpaces(String line) {
        StringBuilder builder = new StringBuilder();
        boolean inString = false;
        char quote = 0;
        boolean previousSpace = false;
        for (int i = 0; i < line.length(); i++) {
            char ch = line.charAt(i);
            if ((ch == '"' || ch == '\'') && (i == 0 || line.charAt(i - 1) != '\\') && (!inString || ch == quote)) {
                if (inString) {
                    inString = false;
                    quote = 0;
                } else {
                    inString = true;
                    quote = ch;
                }
                builder.append(ch);
                previousSpace = false;
                continue;
            }
            if (inString && ch != quote) {
                builder.append(ch);
                continue;
            }
            if (Character.isWhitespace(ch)) {
                if (!previousSpace) {
                    builder.append(' ');
                    previousSpace = true;
                }
                continue;
            }
            builder.append(ch);
            previousSpace = false;
        }
        return builder.toString();
    }

    private static final class CommentState {
        private String longCommentClose = "";
    }

    private record SplitLine(String code, String comment) {
    }
}

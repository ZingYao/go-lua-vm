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
        for (int i = 0; i < lines.length; i++) {
            String trimmed = normalizeSpaces(lines[i].trim());
            if (trimmed.isEmpty()) {
                builder.append('\n');
                continue;
            }
            String first = firstWord(trimmed);
            adjustBeforeLine(frames, first);
            builder.append("  ".repeat(Math.max(0, frames.size()))).append(trimmed);
            if (i < lines.length - 1) {
                builder.append('\n');
            }
            adjustAfterLine(frames, first, trimmed);
        }
        return builder.toString();
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
            return true;
        }
        return line.endsWith(" then") || line.endsWith(" do") || line.startsWith("function ");
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
}

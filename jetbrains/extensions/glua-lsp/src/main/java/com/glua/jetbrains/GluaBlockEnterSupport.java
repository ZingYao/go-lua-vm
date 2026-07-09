package com.glua.jetbrains;

final class GluaBlockEnterSupport {
    private GluaBlockEnterSupport() {
    }

    static Expansion expansion(CharSequence source, int offset) {
        int safeOffset = Math.max(0, Math.min(offset, source.length()));
        int lineStart = lineStart(source, safeOffset);
        String lineBeforeCaret = source.subSequence(lineStart, safeOffset).toString();
        String trimmed = lineBeforeCaret.trim();
        if (trimmed.isBlank()) {
            return null;
        }
        String indent = leadingWhitespace(lineBeforeCaret);
        if (trimmed.matches("^switch\\b.*\\bdo\\s*$")) {
            String caseIndent = indent + "  ";
            String bodyIndent = indent + "    ";
            String text = "\n" + caseIndent + "case \n" + bodyIndent + "\n" + indent + "end";
            return new Expansion(text, 1 + caseIndent.length() + "case ".length());
        }
        if (trimmed.matches("^(case\\b.+|default)\\s*$")) {
            String bodyIndent = indent + "  ";
            return new Expansion("\n" + bodyIndent, 1 + bodyIndent.length());
        }
        if (trimmed.equals("repeat")) {
            return expansionText(indent, "until ");
        }
        if (opensEndBlock(trimmed)) {
            return expansionText(indent, "end");
        }
        return null;
    }

    private static Expansion expansionText(String indent, String closeText) {
        String innerIndent = indent + "  ";
        String text = "\n" + innerIndent + "\n" + indent + closeText;
        return new Expansion(text, 1 + innerIndent.length());
    }

    private static boolean opensEndBlock(String trimmed) {
        return trimmed.endsWith(" do") && !trimmed.matches("^switch\\b.*")
            || trimmed.endsWith(" then")
            || trimmed.matches("^(local\\s+)?function\\b.*\\)\\s*$")
            || trimmed.matches(".*=\\s*function\\s*\\([^)]*\\)\\s*$")
            || trimmed.matches(".*\\bfunction\\s*\\([^)]*\\)\\s*$");
    }

    private static int lineStart(CharSequence source, int offset) {
        for (int index = offset - 1; index >= 0; index--) {
            char ch = source.charAt(index);
            if (ch == '\n' || ch == '\r') {
                return index + 1;
            }
        }
        return 0;
    }

    private static String leadingWhitespace(String line) {
        int end = 0;
        while (end < line.length() && Character.isWhitespace(line.charAt(end))) {
            end++;
        }
        return line.substring(0, end);
    }

    record Expansion(String text, int caretDelta) {
    }
}

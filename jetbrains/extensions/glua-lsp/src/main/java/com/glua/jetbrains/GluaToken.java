package com.glua.jetbrains;

import com.intellij.openapi.util.TextRange;

final class GluaToken {
    final String type;
    final String text;
    final int start;
    final int end;

    GluaToken(String type, String text, int start, int end) {
        this.type = type;
        this.text = text;
        this.start = start;
        this.end = end;
    }

    TextRange range() {
        return TextRange.create(start, end);
    }

    boolean isName() {
        return "identifier".equals(type)
            || "functionDeclaration".equals(type)
            || "functionCall".equals(type)
            || "builtinFunction".equals(type)
            || "memberFunction".equals(type)
            || "library".equals(type)
            || "keyword".equals(type);
    }
}

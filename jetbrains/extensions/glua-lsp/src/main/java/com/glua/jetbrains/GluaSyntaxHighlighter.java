package com.glua.jetbrains;

import com.intellij.lexer.Lexer;
import com.intellij.openapi.editor.DefaultLanguageHighlighterColors;
import com.intellij.openapi.editor.HighlighterColors;
import com.intellij.openapi.editor.colors.TextAttributesKey;
import com.intellij.openapi.editor.markup.TextAttributes;
import com.intellij.openapi.fileTypes.SyntaxHighlighterBase;
import com.intellij.psi.tree.IElementType;
import org.jetbrains.annotations.NotNull;

import java.awt.Color;

public final class GluaSyntaxHighlighter extends SyntaxHighlighterBase {
    private static final TextAttributesKey GLUA_KEYWORD = key("GLUA_KEYWORD", 0xCC7832, DefaultLanguageHighlighterColors.KEYWORD);
    private static final TextAttributesKey GLUA_STRING = key("GLUA_STRING", 0x6A8759, DefaultLanguageHighlighterColors.STRING);
    private static final TextAttributesKey GLUA_NUMBER = key("GLUA_NUMBER", 0x6897BB, DefaultLanguageHighlighterColors.NUMBER);
    private static final TextAttributesKey GLUA_COMMENT = key("GLUA_COMMENT", 0x808080, DefaultLanguageHighlighterColors.LINE_COMMENT);
    private static final TextAttributesKey GLUA_OPERATOR = key("GLUA_OPERATOR", 0xA9B7C6, DefaultLanguageHighlighterColors.OPERATION_SIGN);
    private static final TextAttributesKey GLUA_FUNCTION_DECLARATION = key("GLUA_FUNCTION_DECLARATION", 0x56A8F5, DefaultLanguageHighlighterColors.FUNCTION_DECLARATION);
    private static final TextAttributesKey GLUA_FUNCTION_CALL = key("GLUA_FUNCTION_CALL", 0x56A8F5, DefaultLanguageHighlighterColors.FUNCTION_CALL);
    private static final TextAttributesKey GLUA_BUILTIN_FUNCTION = key("GLUA_BUILTIN_FUNCTION", 0x56A8F5, DefaultLanguageHighlighterColors.FUNCTION_CALL);
    private static final TextAttributesKey GLUA_MEMBER_FUNCTION = key("GLUA_MEMBER_FUNCTION", 0x56A8F5, DefaultLanguageHighlighterColors.FUNCTION_CALL);
    private static final TextAttributesKey GLUA_LIBRARY = key("GLUA_LIBRARY", 0x4EC9B0, DefaultLanguageHighlighterColors.CLASS_REFERENCE);
    private static final TextAttributesKey[] KEYWORD = pack(GLUA_KEYWORD);
    private static final TextAttributesKey[] STRING = pack(GLUA_STRING);
    private static final TextAttributesKey[] NUMBER = pack(GLUA_NUMBER);
    private static final TextAttributesKey[] COMMENT = pack(GLUA_COMMENT);
    private static final TextAttributesKey[] OPERATOR = pack(GLUA_OPERATOR);
    private static final TextAttributesKey[] FUNCTION_DECLARATION = pack(GLUA_FUNCTION_DECLARATION);
    private static final TextAttributesKey[] FUNCTION_CALL = pack(GLUA_FUNCTION_CALL);
    private static final TextAttributesKey[] BUILTIN_FUNCTION = pack(GLUA_BUILTIN_FUNCTION);
    private static final TextAttributesKey[] MEMBER_FUNCTION = pack(GLUA_MEMBER_FUNCTION);
    private static final TextAttributesKey[] LIBRARY = pack(GLUA_LIBRARY);
    private static final TextAttributesKey[] BAD = pack(HighlighterColors.BAD_CHARACTER);
    private static final TextAttributesKey[] EMPTY = TextAttributesKey.EMPTY_ARRAY;

    @Override
    public @NotNull Lexer getHighlightingLexer() {
        return new GluaLexer();
    }

    @Override
    public TextAttributesKey @NotNull [] getTokenHighlights(IElementType tokenType) {
        if (tokenType == GluaTokenType.KEYWORD) {
            return KEYWORD;
        }
        if (tokenType == GluaTokenType.STRING) {
            return STRING;
        }
        if (tokenType == GluaTokenType.NUMBER) {
            return NUMBER;
        }
        if (tokenType == GluaTokenType.COMMENT) {
            return COMMENT;
        }
        if (tokenType == GluaTokenType.OPERATOR) {
            return OPERATOR;
        }
        if (tokenType == GluaTokenType.FUNCTION_DECLARATION) {
            return FUNCTION_DECLARATION;
        }
        if (tokenType == GluaTokenType.FUNCTION_CALL) {
            return FUNCTION_CALL;
        }
        if (tokenType == GluaTokenType.BUILTIN_FUNCTION) {
            return BUILTIN_FUNCTION;
        }
        if (tokenType == GluaTokenType.MEMBER_FUNCTION) {
            return MEMBER_FUNCTION;
        }
        if (tokenType == GluaTokenType.LIBRARY) {
            return LIBRARY;
        }
        if (tokenType == GluaTokenType.BAD_CHARACTER) {
            return BAD;
        }
        return EMPTY;
    }

    private static TextAttributesKey key(String externalName, int rgb, TextAttributesKey fallback) {
        return TextAttributesKey.createTextAttributesKey(externalName, attributes(rgb, fallback));
    }

    private static TextAttributes attributes(int rgb, TextAttributesKey fallback) {
        TextAttributes fallbackAttributes = fallback.getDefaultAttributes();
        TextAttributes attributes = fallbackAttributes == null ? new TextAttributes() : fallbackAttributes.clone();
        attributes.setForegroundColor(new Color(rgb));
        return attributes;
    }
}

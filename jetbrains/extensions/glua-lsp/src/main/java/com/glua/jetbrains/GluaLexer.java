package com.glua.jetbrains;

import com.intellij.lexer.LexerBase;
import com.intellij.psi.TokenType;
import com.intellij.psi.tree.IElementType;
import org.jetbrains.annotations.NotNull;
import org.jetbrains.annotations.Nullable;

import java.util.List;

public final class GluaLexer extends LexerBase {
    private CharSequence buffer = "";
    private int startOffset;
    private int endOffset;
    private int tokenIndex;
    private List<GluaToken> tokens = List.of();

    @Override
    public void start(@NotNull CharSequence buffer, int startOffset, int endOffset, int initialState) {
        this.buffer = buffer;
        this.startOffset = startOffset;
        this.endOffset = endOffset;
        this.tokens = GluaLexerUtil.scan(buffer.subSequence(startOffset, endOffset));
        this.tokenIndex = 0;
    }

    @Override
    public int getState() {
        return 0;
    }

    @Override
    public @Nullable IElementType getTokenType() {
        if (tokenIndex >= tokens.size()) {
            return null;
        }
        GluaToken token = tokens.get(tokenIndex);
        return switch (token.type) {
            case "space" -> TokenType.WHITE_SPACE;
            case "keyword" -> GluaTokenType.KEYWORD;
            case "functionDeclaration" -> GluaTokenType.FUNCTION_DECLARATION;
            case "functionCall" -> GluaTokenType.FUNCTION_CALL;
            case "builtinFunction" -> GluaTokenType.BUILTIN_FUNCTION;
            case "memberFunction" -> GluaTokenType.MEMBER_FUNCTION;
            case "library" -> GluaTokenType.LIBRARY;
            case "identifier" -> GluaTokenType.IDENTIFIER;
            case "string" -> GluaTokenType.STRING;
            case "number" -> GluaTokenType.NUMBER;
            case "comment" -> GluaTokenType.COMMENT;
            case "operator" -> GluaTokenType.OPERATOR;
            default -> GluaTokenType.BAD_CHARACTER;
        };
    }

    @Override
    public int getTokenStart() {
        return tokenIndex >= tokens.size() ? endOffset : startOffset + tokens.get(tokenIndex).start;
    }

    @Override
    public int getTokenEnd() {
        return tokenIndex >= tokens.size() ? endOffset : startOffset + tokens.get(tokenIndex).end;
    }

    @Override
    public void advance() {
        tokenIndex++;
    }

    @Override
    public @NotNull CharSequence getBufferSequence() {
        return buffer;
    }

    @Override
    public int getBufferEnd() {
        return endOffset;
    }
}

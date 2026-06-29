package com.glua.jetbrains;

import com.intellij.psi.tree.IElementType;
import org.jetbrains.annotations.NonNls;
import org.jetbrains.annotations.NotNull;

public final class GluaTokenType extends IElementType {
    public static final GluaTokenType IDENTIFIER = new GluaTokenType("IDENTIFIER");
    public static final GluaTokenType FUNCTION_DECLARATION = new GluaTokenType("FUNCTION_DECLARATION");
    public static final GluaTokenType FUNCTION_CALL = new GluaTokenType("FUNCTION_CALL");
    public static final GluaTokenType BUILTIN_FUNCTION = new GluaTokenType("BUILTIN_FUNCTION");
    public static final GluaTokenType MEMBER_FUNCTION = new GluaTokenType("MEMBER_FUNCTION");
    public static final GluaTokenType LIBRARY = new GluaTokenType("LIBRARY");
    public static final GluaTokenType KEYWORD = new GluaTokenType("KEYWORD");
    public static final GluaTokenType STRING = new GluaTokenType("STRING");
    public static final GluaTokenType NUMBER = new GluaTokenType("NUMBER");
    public static final GluaTokenType COMMENT = new GluaTokenType("COMMENT");
    public static final GluaTokenType OPERATOR = new GluaTokenType("OPERATOR");
    public static final GluaTokenType BAD_CHARACTER = new GluaTokenType("BAD_CHARACTER");
    public static final GluaTokenType WHITE_SPACE = new GluaTokenType("WHITE_SPACE");

    private GluaTokenType(@NonNls @NotNull String debugName) {
        super(debugName, GluaLanguage.INSTANCE);
    }
}

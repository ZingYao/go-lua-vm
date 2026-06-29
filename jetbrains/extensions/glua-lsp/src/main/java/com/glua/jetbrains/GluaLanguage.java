package com.glua.jetbrains;

import com.intellij.lang.Language;

public final class GluaLanguage extends Language {
    public static final GluaLanguage INSTANCE = new GluaLanguage();

    private GluaLanguage() {
        super("GLua");
    }
}

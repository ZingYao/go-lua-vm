package com.glua.jetbrains;

import java.util.Locale;

final class GluaUiText {
    private GluaUiText() {
    }

    static String text(String en, String zh) {
        return Locale.getDefault().getLanguage().equalsIgnoreCase("zh") ? zh : en;
    }
}

package com.glua.jetbrains;

import org.jetbrains.annotations.NotNull;

public record GluaDapSetVariableResult(boolean success, @NotNull String error) {
    public static @NotNull GluaDapSetVariableResult ok() {
        return new GluaDapSetVariableResult(true, "");
    }

    public static @NotNull GluaDapSetVariableResult failure(@NotNull String error) {
        return new GluaDapSetVariableResult(false, error);
    }
}

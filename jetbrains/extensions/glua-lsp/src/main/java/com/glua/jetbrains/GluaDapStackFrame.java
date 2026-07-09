package com.glua.jetbrains;

import org.jetbrains.annotations.NotNull;

public record GluaDapStackFrame(@NotNull String source, int line) {
}

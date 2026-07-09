package com.glua.jetbrains;

import org.jetbrains.annotations.NotNull;

public record GluaDapVariable(@NotNull String name, @NotNull String value, @NotNull String type) {
}

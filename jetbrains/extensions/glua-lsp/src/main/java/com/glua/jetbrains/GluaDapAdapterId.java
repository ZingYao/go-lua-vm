package com.glua.jetbrains;

import com.intellij.platform.dap.DebugAdapterId;

public final class GluaDapAdapterId extends DebugAdapterId {
    public static final GluaDapAdapterId INSTANCE = new GluaDapAdapterId();

    private GluaDapAdapterId() {
        super("glua", "GLua");
    }
}

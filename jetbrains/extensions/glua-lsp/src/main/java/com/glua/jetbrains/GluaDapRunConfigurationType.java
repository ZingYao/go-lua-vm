package com.glua.jetbrains;

import com.intellij.icons.AllIcons;
import com.intellij.execution.configurations.ConfigurationTypeBase;

public final class GluaDapRunConfigurationType extends ConfigurationTypeBase {
    public GluaDapRunConfigurationType() {
        super("GLua", "GLua", GluaUiText.text("Run or debug Lua/glua files with GLua", "使用 GLua 运行或调试 Lua/glua 文件"), AllIcons.Empty);
        addFactory(new GluaDapRunConfigurationFactory(this));
    }
}

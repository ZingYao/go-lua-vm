package com.glua.jetbrains;

import com.intellij.icons.AllIcons;
import com.intellij.execution.configurations.ConfigurationTypeBase;

public final class GluaDapRunConfigurationType extends ConfigurationTypeBase {
    public GluaDapRunConfigurationType() {
        super("GLuaDapAttach", "GLua DAP Attach", "Attach to a running GLua Debug Adapter Protocol server", AllIcons.Empty);
        addFactory(new GluaDapRunConfigurationFactory(this));
    }
}

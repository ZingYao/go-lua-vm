package com.glua.jetbrains;

import com.intellij.execution.configurations.ConfigurationFactory;
import com.intellij.execution.configurations.ConfigurationType;
import com.intellij.execution.configurations.RunConfiguration;
import com.intellij.openapi.application.ApplicationManager;
import com.intellij.openapi.project.Project;
import org.jetbrains.annotations.NotNull;

public final class GluaDapRunConfigurationFactory extends ConfigurationFactory {
    public GluaDapRunConfigurationFactory(@NotNull ConfigurationType type) {
        super(type);
    }

    @Override
    public @NotNull String getId() {
        return "GLuaDapAttachFactory";
    }

    @Override
    public @NotNull RunConfiguration createTemplateConfiguration(@NotNull Project project) {
        GluaSettings settings = ApplicationManager.getApplication().getService(GluaSettings.class);
        GluaDapRunConfiguration configuration = new GluaDapRunConfiguration(project, this, "Attach to GLua DAP");
        configuration.setHost(settings.dapHost());
        configuration.setPort(settings.dapPort());
        configuration.setAllowRunningInParallel(true);
        return configuration;
    }
}

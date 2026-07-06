package com.glua.jetbrains;

import com.intellij.execution.ExecutionException;
import com.intellij.execution.Executor;
import com.intellij.execution.configurations.ConfigurationFactory;
import com.intellij.execution.configurations.LocatableConfigurationBase;
import com.intellij.execution.configurations.RuntimeConfigurationError;
import com.intellij.execution.configurations.RunProfileState;
import com.intellij.execution.runners.ExecutionEnvironment;
import com.intellij.openapi.options.SettingsEditor;
import com.intellij.openapi.project.Project;
import org.jdom.Element;
import org.jetbrains.annotations.NotNull;
import org.jetbrains.annotations.Nullable;

public final class GluaDapRunConfiguration extends LocatableConfigurationBase<Object> {
    private static final String HOST_ATTR = "host";
    private static final String PORT_ATTR = "port";

    private String host = "127.0.0.1";
    private int port = 5678;

    public GluaDapRunConfiguration(@NotNull Project project,
                                   @NotNull ConfigurationFactory factory,
                                   @Nullable String name) {
        super(project, factory, name);
    }

    @Override
    public @NotNull SettingsEditor<? extends GluaDapRunConfiguration> getConfigurationEditor() {
        return new GluaDapRunConfigurationEditor();
    }

    @Override
    public @Nullable RunProfileState getState(@NotNull Executor executor,
                                              @NotNull ExecutionEnvironment environment) throws ExecutionException {
        return new GluaDapRunProfileState(environment, host(), port());
    }

    @Override
    public void checkConfiguration() throws RuntimeConfigurationError {
        if (host().isBlank()) {
            throw new RuntimeConfigurationError("DAP attach host is required.");
        }
        if (port() < 1 || port() > 65535) {
            throw new RuntimeConfigurationError("DAP attach port must be between 1 and 65535.");
        }
    }

    @Override
    public void readExternal(@NotNull Element element) {
        super.readExternal(element);
        host = normalizeHost(element.getAttributeValue(HOST_ATTR));
        port = normalizePort(element.getAttributeValue(PORT_ATTR));
    }

    @Override
    public void writeExternal(@NotNull Element element) {
        super.writeExternal(element);
        element.setAttribute(HOST_ATTR, host());
        element.setAttribute(PORT_ATTR, String.valueOf(port()));
    }

    public String host() {
        return normalizeHost(host);
    }

    public void setHost(String host) {
        this.host = normalizeHost(host);
    }

    public int port() {
        return normalizePort(String.valueOf(port));
    }

    public void setPort(int port) {
        this.port = normalizePort(String.valueOf(port));
    }

    private static String normalizeHost(String value) {
        return value == null || value.isBlank() ? "127.0.0.1" : value.trim();
    }

    private static int normalizePort(String value) {
        try {
            int parsed = Integer.parseInt(value == null ? "" : value.trim());
            return parsed >= 1 && parsed <= 65535 ? parsed : 5678;
        } catch (NumberFormatException ignored) {
            return 5678;
        }
    }
}

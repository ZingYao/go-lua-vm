package com.glua.jetbrains;

import com.intellij.execution.ExecutionException;
import com.intellij.execution.Executor;
import com.intellij.execution.configurations.ConfigurationFactory;
import com.intellij.execution.configurations.LocatableConfigurationBase;
import com.intellij.execution.configurations.RuntimeConfigurationError;
import com.intellij.execution.configurations.RunProfileState;
import com.intellij.execution.executors.DefaultRunExecutor;
import com.intellij.execution.runners.ExecutionEnvironment;
import com.intellij.openapi.options.SettingsEditor;
import com.intellij.openapi.project.Project;
import org.jdom.Element;
import org.jetbrains.annotations.NotNull;
import org.jetbrains.annotations.Nullable;

public final class GluaDapRunConfiguration extends LocatableConfigurationBase<Object> {
    private static final String GLUA_EXECUTABLE_ATTR = "gluaExecutable";
    private static final String PROGRAM_ATTR = "program";
    private static final String DAP_HOST_ATTR = "dapHost";
    private static final String DAP_PORT_ATTR = "dapPort";
    private static final String USE_REMOTE_DAP_ATTR = "useRemoteDap";
    static final String INTERNAL_DAP_HOST = "127.0.0.1";
    static final int INTERNAL_DAP_PORT = 5678;

    private String gluaExecutable = "";
    private String program = "";
    private String dapHost = INTERNAL_DAP_HOST;
    private int dapPort = INTERNAL_DAP_PORT;
    private boolean useRemoteDap = false;

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
        if (DefaultRunExecutor.EXECUTOR_ID.equals(executor.getId())) {
            return new GluaRunProfileState(environment, gluaExecutable(), program());
        }
        return new GluaDapRunProfileState(environment, gluaExecutable(), program(), host(), port(), useRemoteDap());
    }

    @Override
    public void checkConfiguration() throws RuntimeConfigurationError {
        if (program().isBlank()) {
            throw new RuntimeConfigurationError(GluaUiText.text("GLua program file is required.", "必须选择 GLua 程序文件。"));
        }
    }

    @Override
    public void readExternal(@NotNull Element element) {
        super.readExternal(element);
        gluaExecutable = normalizePath(element.getAttributeValue(GLUA_EXECUTABLE_ATTR));
        program = normalizePath(element.getAttributeValue(PROGRAM_ATTR));
        dapHost = normalizeHost(element.getAttributeValue(DAP_HOST_ATTR));
        dapPort = normalizePort(element.getAttributeValue(DAP_PORT_ATTR));
        useRemoteDap = Boolean.parseBoolean(element.getAttributeValue(USE_REMOTE_DAP_ATTR));
    }

    @Override
    public void writeExternal(@NotNull Element element) {
        super.writeExternal(element);
        element.setAttribute(GLUA_EXECUTABLE_ATTR, gluaExecutable());
        element.setAttribute(PROGRAM_ATTR, program());
        element.setAttribute(DAP_HOST_ATTR, host());
        element.setAttribute(DAP_PORT_ATTR, String.valueOf(port()));
        element.setAttribute(USE_REMOTE_DAP_ATTR, String.valueOf(useRemoteDap()));
    }

    public String host() {
        return normalizeHost(dapHost);
    }

    public int port() {
        return normalizePort(dapPort);
    }

    public boolean useRemoteDap() {
        return useRemoteDap;
    }

    public void setUseRemoteDap(boolean useRemoteDap) {
        this.useRemoteDap = useRemoteDap;
    }

    public void setDapHost(String dapHost) {
        this.dapHost = normalizeHost(dapHost);
    }

    public void setDapPort(int dapPort) {
        this.dapPort = normalizePort(dapPort);
    }

    public String gluaExecutable() {
        return normalizePath(gluaExecutable);
    }

    public void setGluaExecutable(String gluaExecutable) {
        this.gluaExecutable = normalizePath(gluaExecutable);
    }

    public String program() {
        return normalizePath(program);
    }

    public void setProgram(String program) {
        this.program = normalizePath(program);
    }

    private static String normalizePath(String value) {
        return value == null ? "" : value.trim();
    }

    private static String normalizeHost(String value) {
        return value == null || value.isBlank() ? INTERNAL_DAP_HOST : value.trim();
    }

    private static int normalizePort(String value) {
        if (value == null || value.isBlank()) {
            return INTERNAL_DAP_PORT;
        }
        try {
            return normalizePort(Integer.parseInt(value.trim()));
        } catch (NumberFormatException ignored) {
            return INTERNAL_DAP_PORT;
        }
    }

    private static int normalizePort(int value) {
        return value >= 1 && value <= 65535 ? value : INTERNAL_DAP_PORT;
    }
}

package com.glua.jetbrains;

import com.intellij.execution.ExecutionException;
import com.intellij.execution.configurations.CommandLineState;
import com.intellij.execution.process.ProcessHandler;
import com.intellij.execution.runners.ExecutionEnvironment;
import org.jetbrains.annotations.NotNull;

public final class GluaDapRunProfileState extends CommandLineState {
    private final String host;
    private final int port;

    public GluaDapRunProfileState(@NotNull ExecutionEnvironment environment, @NotNull String host, int port) {
        super(environment);
        this.host = host;
        this.port = port;
    }

    @Override
    protected @NotNull ProcessHandler startProcess() throws ExecutionException {
        return new GluaDapAttachProcessHandler(host, port);
    }
}

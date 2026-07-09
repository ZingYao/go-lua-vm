package com.glua.jetbrains;

import com.intellij.execution.ExecutionException;
import com.intellij.execution.configurations.CommandLineState;
import com.intellij.execution.filters.TextConsoleBuilderFactory;
import com.intellij.execution.process.ProcessHandler;
import com.intellij.execution.runners.ExecutionEnvironment;
import org.jetbrains.annotations.NotNull;

public final class GluaDapRunProfileState extends CommandLineState {
    private final String gluaExecutable;
    private final String program;
    private final String host;
    private final int port;
    private final boolean useRemoteDap;

    public GluaDapRunProfileState(@NotNull ExecutionEnvironment environment,
                                  @NotNull String gluaExecutable,
                                  @NotNull String program,
                                  @NotNull String host,
                                  int port,
                                  boolean useRemoteDap) {
        super(environment);
        this.gluaExecutable = gluaExecutable;
        this.program = program;
        this.host = host;
        this.port = port;
        this.useRemoteDap = useRemoteDap;
        setConsoleBuilder(TextConsoleBuilderFactory.getInstance().createBuilder(environment.getProject()));
    }

    @Override
    protected @NotNull ProcessHandler startProcess() throws ExecutionException {
        if (useRemoteDap) {
            return new GluaDapRemoteProcessHandler(getEnvironment().getProject(), host, port, program);
        }
        return GluaDapLaunchProcessHandler.create(getEnvironment().getProject(), gluaExecutable, program);
    }
}

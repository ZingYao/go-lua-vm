package com.glua.jetbrains;

import com.intellij.execution.ExecutionException;
import com.intellij.execution.configurations.CommandLineState;
import com.intellij.execution.process.ProcessHandler;
import com.intellij.execution.runners.ExecutionEnvironment;
import org.jetbrains.annotations.NotNull;

public final class GluaDapRunProfileState extends CommandLineState {
    private final String gluaExecutable;
    private final String program;

    public GluaDapRunProfileState(@NotNull ExecutionEnvironment environment,
                                  @NotNull String gluaExecutable,
                                  @NotNull String program) {
        super(environment);
        this.gluaExecutable = gluaExecutable;
        this.program = program;
    }

    @Override
    protected @NotNull ProcessHandler startProcess() throws ExecutionException {
        return GluaDapLaunchProcessHandler.create(gluaExecutable, program);
    }
}

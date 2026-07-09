package com.glua.jetbrains;

import com.intellij.execution.ExecutionException;
import com.intellij.execution.configurations.CommandLineState;
import com.intellij.execution.configurations.GeneralCommandLine;
import com.intellij.execution.filters.TextConsoleBuilderFactory;
import com.intellij.execution.process.OSProcessHandler;
import com.intellij.execution.process.ProcessHandler;
import com.intellij.execution.runners.ExecutionEnvironment;
import com.intellij.openapi.vfs.LocalFileSystem;
import com.intellij.openapi.vfs.VirtualFile;
import org.jetbrains.annotations.NotNull;

import java.nio.file.Path;

public final class GluaRunProfileState extends CommandLineState {
    private final String gluaExecutable;
    private final String program;

    public GluaRunProfileState(@NotNull ExecutionEnvironment environment,
                               @NotNull String gluaExecutable,
                               @NotNull String program) {
        super(environment);
        this.gluaExecutable = gluaExecutable.isBlank() ? "glua" : gluaExecutable;
        this.program = program;
        setConsoleBuilder(TextConsoleBuilderFactory.getInstance().createBuilder(environment.getProject()));
    }

    @Override
    protected @NotNull ProcessHandler startProcess() throws ExecutionException {
        GeneralCommandLine commandLine = new GeneralCommandLine(gluaExecutable, program);
        VirtualFile file = LocalFileSystem.getInstance().findFileByNioFile(Path.of(program));
        if (file != null && file.getParent() != null) {
            commandLine.withWorkDirectory(file.getParent().getPath());
        }
        return new OSProcessHandler(commandLine);
    }
}

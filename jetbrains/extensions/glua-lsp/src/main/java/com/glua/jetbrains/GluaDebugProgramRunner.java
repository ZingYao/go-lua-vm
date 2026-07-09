package com.glua.jetbrains;

import com.intellij.execution.ExecutionException;
import com.intellij.execution.configurations.RunProfile;
import com.intellij.execution.configurations.RunProfileState;
import com.intellij.execution.configurations.RunnerSettings;
import com.intellij.execution.executors.DefaultDebugExecutor;
import com.intellij.execution.runners.ExecutionEnvironment;
import com.intellij.execution.runners.GenericProgramRunner;
import com.intellij.execution.ui.RunContentDescriptor;
import com.intellij.xdebugger.XDebugProcessStarter;
import com.intellij.xdebugger.XDebugSession;
import com.intellij.xdebugger.XDebuggerManager;
import org.jetbrains.annotations.NotNull;
import org.jetbrains.annotations.Nullable;

public final class GluaDebugProgramRunner extends GenericProgramRunner<RunnerSettings> {
    public GluaDebugProgramRunner() {
        super();
    }

    @Override
    public @NotNull String getRunnerId() {
        return "GLuaDebugProgramRunner";
    }

    @Override
    public boolean canRun(@NotNull String executorId, @NotNull RunProfile profile) {
        return DefaultDebugExecutor.EXECUTOR_ID.equals(executorId) && profile instanceof GluaDapRunConfiguration;
    }

    @Override
    protected @Nullable RunContentDescriptor doExecute(@NotNull RunProfileState state,
                                                       @NotNull ExecutionEnvironment environment) throws ExecutionException {
        RunProfile profile = environment.getRunProfile();
        if (!(profile instanceof GluaDapRunConfiguration configuration)) {
            throw new ExecutionException(GluaUiText.text("GLua DAP configuration is required.", "需要 GLua DAP 调试配置。"));
        }
        GluaDapLaunchProcessHandler handler = GluaDapLaunchProcessHandler.create(
            environment.getProject(),
            configuration.gluaExecutable(),
            configuration.program()
        );
        XDebuggerManager.getInstance(environment.getProject()).startSessionAndShowTab(profile.getName(), new XDebugProcessStarter() {
            @Override
            public @NotNull GluaDebugProcess start(@NotNull XDebugSession session) {
                return new GluaDebugProcess(session, handler);
            }
        }, environment);
        return null;
    }
}

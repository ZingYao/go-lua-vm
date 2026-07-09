package com.glua.jetbrains;

import com.intellij.execution.ExecutionException;
import com.intellij.execution.configurations.RunProfile;
import com.intellij.execution.configurations.RunProfileState;
import com.intellij.execution.configurations.RunnerSettings;
import com.intellij.execution.executors.DefaultDebugExecutor;
import com.intellij.execution.runners.ExecutionEnvironment;
import com.intellij.execution.runners.GenericProgramRunner;
import com.intellij.execution.ui.RunContentDescriptor;
import com.intellij.xdebugger.XDebugProcess;
import com.intellij.xdebugger.XDebugProcessStarter;
import com.intellij.xdebugger.XDebugSession;
import com.intellij.xdebugger.XDebuggerManager;
import com.intellij.xdebugger.XSessionStartedResult;
import org.jetbrains.annotations.NotNull;

public final class GluaDebugProgramRunner extends GenericProgramRunner<RunnerSettings> {
    @Override
    public @NotNull String getRunnerId() {
        return "GLuaDebugProgramRunner";
    }

    @Override
    public boolean canRun(@NotNull String executorId, @NotNull RunProfile profile) {
        return DefaultDebugExecutor.EXECUTOR_ID.equals(executorId) && profile instanceof GluaDapRunConfiguration;
    }

    @Override
    protected RunContentDescriptor doExecute(@NotNull RunProfileState state,
                                             @NotNull ExecutionEnvironment environment) throws ExecutionException {
        XSessionStartedResult result = XDebuggerManager.getInstance(environment.getProject())
            .newSessionBuilder(new XDebugProcessStarter() {
                @Override
                public @NotNull XDebugProcess start(@NotNull XDebugSession session) {
                    GluaDapRunConfiguration configuration = (GluaDapRunConfiguration) environment.getRunProfile();
                    return new GluaDebugProcess(session, configuration.host(), configuration.port());
                }
            })
            .environment(environment)
            .startSession();
        return result.getRunContentDescriptor();
    }
}

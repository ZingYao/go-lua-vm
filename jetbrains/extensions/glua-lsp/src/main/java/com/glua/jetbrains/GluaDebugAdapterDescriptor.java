package com.glua.jetbrains;

import com.intellij.execution.ExecutionException;
import com.intellij.execution.ExecutionResult;
import com.intellij.execution.configurations.RunProfile;
import com.intellij.execution.process.ProcessHandler;
import com.intellij.execution.runners.ExecutionEnvironment;
import com.intellij.platform.dap.DapBreakpointsDescription;
import com.intellij.platform.dap.DebugAdapterDescriptor;
import com.intellij.platform.dap.connection.DebugAdapterHandle;
import com.intellij.platform.dap.connection.SocketConnectionAdapterHandleImpl;
import kotlin.Unit;
import kotlin.coroutines.Continuation;
import org.jetbrains.annotations.NotNull;
import org.jetbrains.annotations.Nullable;

import java.time.Duration;

public final class GluaDebugAdapterDescriptor extends DebugAdapterDescriptor<GluaDapAdapterId> {
    @Override
    public @NotNull GluaDapAdapterId getId() {
        return GluaDapAdapterId.INSTANCE;
    }

    @Override
    public @NotNull Object launchDebugAdapter(@NotNull ExecutionEnvironment environment,
                                              @NotNull ExecutionResult executionResult,
                                              @NotNull String sessionId,
                                              @NotNull Continuation<? super DebugAdapterHandle> continuation) throws ExecutionException {
        RunProfile profile = environment.getRunProfile();
        if (!(profile instanceof GluaDapRunConfiguration configuration)) {
            throw new ExecutionException("GLua DAP configuration is required.");
        }
        ProcessHandler processHandler = executionResult.getProcessHandler();
        if (!(processHandler instanceof GluaDapLaunchProcessHandler gluaHandler)) {
            throw new ExecutionException("GLua DAP launch process is required.");
        }
        GluaDapLaunchProcessHandler.ReadyTarget target = gluaHandler.awaitReady(Duration.ofSeconds(5));
        return new SocketConnectionAdapterHandleImpl(target.host(), target.port(), ignored -> Unit.INSTANCE);
    }

    @Override
    public @NotNull DapBreakpointsDescription getBreakpointsDescription() {
        return new DapBreakpointsDescription(GluaLineBreakpointType.class, null);
    }
}

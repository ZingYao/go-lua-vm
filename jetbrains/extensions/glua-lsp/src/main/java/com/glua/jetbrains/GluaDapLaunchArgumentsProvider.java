package com.glua.jetbrains;

import com.intellij.execution.configurations.RunProfile;
import com.intellij.execution.executors.DefaultDebugExecutor;
import com.intellij.openapi.project.Project;
import com.intellij.platform.dap.DapLaunchArgumentsProvider;
import com.intellij.platform.dap.DapStartRequest;
import com.intellij.platform.dap.LaunchRequestArguments;
import org.jetbrains.annotations.NotNull;

import java.util.LinkedHashMap;
import java.util.Map;

public final class GluaDapLaunchArgumentsProvider implements DapLaunchArgumentsProvider {
    @Override
    public boolean isApplicable(@NotNull String executorId, @NotNull RunProfile profile) {
        if (!(profile instanceof GluaDapRunConfiguration)) {
            return false;
        }
        return DefaultDebugExecutor.EXECUTOR_ID.equals(executorId);
    }

    @Override
    public @NotNull LaunchRequestArguments getLaunchArguments(@NotNull Project project, @NotNull RunProfile profile) {
        GluaDapRunConfiguration configuration = (GluaDapRunConfiguration) profile;
        Map<String, Object> arguments = new LinkedHashMap<>();
        arguments.put("type", GluaDapAdapterId.INSTANCE.getType());
        arguments.put("request", DapStartRequest.Attach.getValue());
        arguments.put("name", configuration.getName());
        arguments.put("host", configuration.host());
        arguments.put("port", configuration.port());
        arguments.put("program", configuration.program());
        arguments.put("gluaExecutable", configuration.gluaExecutable());
        return new LaunchRequestArguments(GluaDapAdapterId.INSTANCE, DapStartRequest.Attach, arguments);
    }
}

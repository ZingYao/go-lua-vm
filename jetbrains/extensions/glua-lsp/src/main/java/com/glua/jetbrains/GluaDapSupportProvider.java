package com.glua.jetbrains;

import com.intellij.openapi.project.Project;
import com.intellij.platform.dap.DebugAdapterDescriptor;
import com.intellij.platform.dap.DebugAdapterSupportProvider;
import org.jetbrains.annotations.NotNull;

public final class GluaDapSupportProvider implements DebugAdapterSupportProvider<GluaDapAdapterId> {
    @Override
    public @NotNull GluaDapAdapterId getAdapterId() {
        return GluaDapAdapterId.INSTANCE;
    }

    @Override
    public @NotNull DebugAdapterDescriptor<GluaDapAdapterId> createDebugAdapterDescriptor(@NotNull Project project) {
        return new GluaDebugAdapterDescriptor();
    }
}

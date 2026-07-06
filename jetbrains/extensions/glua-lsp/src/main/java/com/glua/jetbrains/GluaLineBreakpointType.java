package com.glua.jetbrains;

import com.intellij.openapi.project.Project;
import com.intellij.openapi.vfs.VirtualFile;
import com.intellij.xdebugger.breakpoints.XLineBreakpointType;
import org.jetbrains.annotations.NotNull;
import org.jetbrains.annotations.Nullable;

public final class GluaLineBreakpointType extends XLineBreakpointType<GluaBreakpointProperties> {
    public GluaLineBreakpointType() {
        super("glua-line-breakpoint", "GLua Line Breakpoint");
    }

    @Override
    public @Nullable GluaBreakpointProperties createBreakpointProperties(@NotNull VirtualFile file, int line) {
        return new GluaBreakpointProperties();
    }

    @Override
    public boolean canPutAt(@NotNull VirtualFile file, int line, @NotNull Project project) {
        String extension = file.getExtension();
        return "glua".equalsIgnoreCase(extension) || "lua".equalsIgnoreCase(extension);
    }
}

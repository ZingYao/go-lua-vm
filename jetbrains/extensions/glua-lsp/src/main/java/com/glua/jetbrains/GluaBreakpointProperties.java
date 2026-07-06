package com.glua.jetbrains;

import com.intellij.xdebugger.breakpoints.XBreakpointProperties;
import org.jetbrains.annotations.Nullable;

public final class GluaBreakpointProperties extends XBreakpointProperties<GluaBreakpointProperties.State> {
    public static final class State {
    }

    @Override
    public @Nullable State getState() {
        return new State();
    }

    @Override
    public void loadState(@Nullable State state) {
    }
}

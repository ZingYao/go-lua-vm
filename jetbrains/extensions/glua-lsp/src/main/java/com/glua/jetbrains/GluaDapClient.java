package com.glua.jetbrains;

import org.jetbrains.annotations.NotNull;

import java.util.List;
import java.util.function.Consumer;

interface GluaDapClient {
    void setDebugProcess(@NotNull GluaDebugProcess debugProcess);

    void sendControlCommand(@NotNull String command);

    void setBreakpointsMuted(boolean muted);

    void syncBreakpointsAsync();

    @NotNull List<GluaDapVariable> currentVariables();

    void requestVariables(int variablesReference, @NotNull Consumer<List<GluaDapVariable>> callback);

    void setVariable(int variablesReference,
                     @NotNull String name,
                     @NotNull String value,
                     @NotNull Consumer<GluaDapSetVariableResult> callback);
}

package com.glua.jetbrains;

import org.jetbrains.annotations.NotNull;

import java.util.List;

interface GluaDapClient {
    void setDebugProcess(@NotNull GluaDebugProcess debugProcess);

    void sendControlCommand(@NotNull String command);

    void syncBreakpointsAsync();

    @NotNull List<GluaDapVariable> currentVariables();
}

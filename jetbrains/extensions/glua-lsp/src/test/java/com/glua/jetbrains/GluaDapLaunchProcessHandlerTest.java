package com.glua.jetbrains;

import org.junit.jupiter.api.Test;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertNull;

final class GluaDapLaunchProcessHandlerTest {
    @Test
    void parseReadyTargetReadsHostAndPort() {
        GluaDapLaunchProcessHandler.ReadyTarget target = GluaDapLaunchProcessHandler.parseReadyTarget(
            "GLua DAP server listening on 127.0.0.1:65019\n"
        );
        assertNotNull(target);
        assertEquals("127.0.0.1", target.host());
        assertEquals(65019, target.port());
    }

    @Test
    void parseReadyTargetRejectsMissingMarker() {
        assertNull(GluaDapLaunchProcessHandler.parseReadyTarget("plain stderr"));
    }
}

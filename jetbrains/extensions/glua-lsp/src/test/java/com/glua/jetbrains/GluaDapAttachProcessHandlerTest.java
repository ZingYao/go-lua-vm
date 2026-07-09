package com.glua.jetbrains;

import org.junit.jupiter.api.Test;

import java.net.ConnectException;

import static org.junit.jupiter.api.Assertions.assertTrue;

final class GluaDapAttachProcessHandlerTest {
    @Test
    void failureMessageIncludesTargetErrorAndRecoveryHint() {
        String message = GluaDapAttachProcessHandler.failureMessage(
            "127.0.0.1",
            5678,
            new ConnectException("Connection refused")
        );
        assertTrue(message.contains("127.0.0.1:5678"), "message should include attach target");
        assertTrue(message.contains("Connection refused"), "message should include connection error");
        assertTrue(message.contains("No GLua DAP server is listening"), "message should explain missing DAP server");
        assertTrue(message.contains("glua CLI"), "message should point users at the runtime capability gap");
        assertTrue(message.contains("hit breakpoints"), "message should explain breakpoint impact");
    }
}

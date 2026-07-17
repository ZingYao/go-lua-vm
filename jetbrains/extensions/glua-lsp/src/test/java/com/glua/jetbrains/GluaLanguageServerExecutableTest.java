package com.glua.jetbrains;

import org.junit.jupiter.api.Test;

import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;

final class GluaLanguageServerExecutableTest {
    @Test
    void bundlesOnlyRequestedTargets() {
        assertTrue(GluaLanguageServerExecutable.isBundled("darwin", "amd64"));
        assertTrue(GluaLanguageServerExecutable.isBundled("darwin", "arm64"));
        assertTrue(GluaLanguageServerExecutable.isBundled("linux", "amd64"));
        assertTrue(GluaLanguageServerExecutable.isBundled("linux", "arm64"));
        assertTrue(GluaLanguageServerExecutable.isBundled("windows", "amd64"));
        assertTrue(GluaLanguageServerExecutable.isBundled("windows", "arm64"));
        assertFalse(GluaLanguageServerExecutable.isBundled("linux", "386"));
        assertFalse(GluaLanguageServerExecutable.isBundled("freebsd", "amd64"));
    }

    @Test
    void normalizesJvmPlatformNames() {
        assertTrue(GluaLanguageServerExecutable.normalizedOs("Mac OS X").equals("darwin"));
        assertTrue(GluaLanguageServerExecutable.normalizedArch("aarch64").equals("arm64"));
        assertTrue(GluaLanguageServerExecutable.normalizedArch("x86_64").equals("amd64"));
    }
}

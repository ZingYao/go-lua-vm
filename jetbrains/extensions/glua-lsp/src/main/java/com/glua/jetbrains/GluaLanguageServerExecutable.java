package com.glua.jetbrains;

import com.intellij.openapi.application.PathManager;

import java.io.IOException;
import java.io.InputStream;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.StandardCopyOption;
import java.util.Locale;

public final class GluaLanguageServerExecutable {
    private GluaLanguageServerExecutable() {
    }

    public static Path resolve(String configuredPath) throws IOException {
        if (configuredPath != null && !configuredPath.isBlank()) {
            Path configured = Path.of(configuredPath.trim()).toAbsolutePath().normalize();
            if (!Files.isRegularFile(configured)) {
                throw new IOException("configured gluals executable does not exist: " + configured);
            }
            return configured;
        }

        String os = normalizedOs(System.getProperty("os.name", ""));
        String arch = normalizedArch(System.getProperty("os.arch", ""));
        String executableName = os.equals("windows") ? "gluals.exe" : "gluals";
        String resource = "/gluals/" + os + "-" + arch + "/" + executableName;
        if (!isBundled(os, arch)) {
            throw new IOException("gluals is not bundled for " + os + "/" + arch + "; configure the gluals executable path in GLua settings");
        }

        Path target = Path.of(PathManager.getPluginTempPath()).resolve("glua-lsp").resolve(os + "-" + arch).resolve(executableName);
        Files.createDirectories(target.getParent());
        try (InputStream input = GluaLanguageServerExecutable.class.getResourceAsStream(resource)) {
            if (input == null) {
                throw new IOException("bundled gluals executable is missing: " + resource);
            }
            Files.copy(input, target, StandardCopyOption.REPLACE_EXISTING);
        }
        if (!os.equals("windows") && !target.toFile().setExecutable(true, true)) {
            throw new IOException("cannot mark bundled gluals executable as executable: " + target);
        }
        return target;
    }

    public static Path resolveBuiltinCatalog() throws IOException {
        Path target = Path.of(PathManager.getPluginTempPath()).resolve("glua-lsp").resolve("builtin-functions.json");
        Files.createDirectories(target.getParent());
        try (InputStream input = GluaLanguageServerExecutable.class.getResourceAsStream("/builtin-functions.json")) {
            if (input == null) {
                throw new IOException("bundled gluals builtin catalog is missing");
            }
            Files.copy(input, target, StandardCopyOption.REPLACE_EXISTING);
        }
        return target;
    }

    static boolean isBundled(String os, String arch) {
        return (os.equals("darwin") && (arch.equals("amd64") || arch.equals("arm64")))
            || (os.equals("windows") && arch.equals("amd64"));
    }

    static String normalizedOs(String value) {
        String normalized = value.toLowerCase(Locale.ROOT);
        if (normalized.contains("mac") || normalized.contains("darwin")) {
            return "darwin";
        }
        if (normalized.contains("win")) {
            return "windows";
        }
        return "linux";
    }

    static String normalizedArch(String value) {
        String normalized = value.toLowerCase(Locale.ROOT);
        if (normalized.equals("x86_64") || normalized.equals("x64")) {
            return "amd64";
        }
        if (normalized.equals("aarch64")) {
            return "arm64";
        }
        return normalized;
    }
}

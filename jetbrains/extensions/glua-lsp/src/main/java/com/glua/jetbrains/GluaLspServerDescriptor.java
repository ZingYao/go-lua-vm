package com.glua.jetbrains;

import com.intellij.execution.ExecutionException;
import com.intellij.execution.configurations.GeneralCommandLine;
import com.intellij.openapi.application.ApplicationManager;
import com.intellij.openapi.project.Project;
import com.intellij.openapi.vfs.VirtualFile;
import com.intellij.platform.lsp.api.ProjectWideLspServerDescriptor;
import org.jetbrains.annotations.NotNull;

import java.io.IOException;
import java.nio.file.Path;
import java.util.Map;

public final class GluaLspServerDescriptor extends ProjectWideLspServerDescriptor {
    public GluaLspServerDescriptor(@NotNull Project project) {
        super(project, "GLua Language Server");
    }

    @Override
    public boolean isSupportedFile(@NotNull VirtualFile file) {
        String extension = file.getExtension();
        return extension != null && (extension.equalsIgnoreCase("lua") || extension.equalsIgnoreCase("glua"));
    }

    @Override
    public @NotNull GeneralCommandLine createCommandLine() throws ExecutionException {
        GluaSettings settings = ApplicationManager.getApplication().getService(GluaSettings.class);
        try {
            Path executable = GluaLanguageServerExecutable.resolve(settings.languageServerExecutable());
            Path catalog = GluaLanguageServerExecutable.resolveBuiltinCatalog();
            return new GeneralCommandLine(executable.toString(), "--gluals-syntax", settings.syntax(), "--gluals-builtin-docs", catalog.toString());
        } catch (IOException error) {
            throw new ExecutionException(error.getMessage(), error);
        }
    }

    @Override
    public @NotNull String getLanguageId(@NotNull VirtualFile file) {
        return "glua";
    }

    @Override
    public Object createInitializationOptions() {
        // 同时传递用户配置语言和按 IDE 环境解析后的实际语言，供 gluals 正确本地化文档。
        GluaSettings settings = ApplicationManager.getApplication().getService(GluaSettings.class);
        String resolvedLocale = GluaBuiltinCatalog.getInstance().locale();
        return Map.of(
            "syntax", settings.syntax(),
            "events", settings.events(),
            "locale", settings.docLanguage(),
            "resolvedLocale", resolvedLocale
        );
    }
}

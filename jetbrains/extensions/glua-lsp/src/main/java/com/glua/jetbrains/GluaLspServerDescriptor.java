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
import java.util.ArrayList;
import java.util.List;
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
        // 解析 gluals 与全部 builtin catalog 路径后构造语言服务器启动命令。
        GluaSettings settings = ApplicationManager.getApplication().getService(GluaSettings.class);
        try {
            Path executable = GluaLanguageServerExecutable.resolve(settings.languageServerExecutable());
            Path catalog = GluaLanguageServerExecutable.resolveBuiltinCatalog();
            return new GeneralCommandLine(executable.toString()).withParameters(commandArguments(settings, catalog));
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
            "resolvedLocale", resolvedLocale,
            "builtinExtensions", settings.builtinDocs()
        );
    }

    // commandArguments 生成语法模式、内置目录和用户扩展目录对应的 gluals 启动参数。
    static List<String> commandArguments(GluaSettings settings, Path catalog) {
        // 先加入插件随包目录，再按设置顺序追加有效的外部 JSON 绝对路径。
        List<String> arguments = new ArrayList<>();
        arguments.add("--gluals-syntax");
        arguments.add(settings.syntax());
        arguments.add("--gluals-builtin-docs");
        arguments.add(catalog.toString());
        for (String builtinDoc : settings.builtinDocs()) {
            if (builtinDoc == null || builtinDoc.isBlank()) {
                continue;
            }
            arguments.add("--gluals-builtin-docs");
            arguments.add(Path.of(builtinDoc.trim()).toAbsolutePath().normalize().toString());
        }
        return arguments;
    }
}

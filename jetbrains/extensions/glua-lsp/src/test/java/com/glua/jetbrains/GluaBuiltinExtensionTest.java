package com.glua.jetbrains;

import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.List;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNotNull;

// GluaBuiltinExtensionTest 验证外部 builtin JSON 能传入 gluals 并参与限定名解析。
final class GluaBuiltinExtensionTest {
    // tempDir 保存每个测试独立的临时 catalog 目录。
    @TempDir
    Path tempDir;

    // commandArgumentsIncludesAllConfiguredCatalogs 验证多个有效路径均按顺序转换为重复启动参数。
    @Test
    void commandArgumentsIncludesAllConfiguredCatalogs() {
        // 构造包含首尾空白和空项的设置，验证规范化路径与空项过滤语义。
        GluaSettings settings = new GluaSettings();
        Path bundledCatalog = tempDir.resolve("builtin-functions.json");
        Path androidCatalog = tempDir.resolve("android.json");
        Path commonCatalog = tempDir.resolve("common.json");
        settings.setBuiltinDocs(List.of("  " + androidCatalog + "  ", "", commonCatalog.toString()));

        assertEquals(List.of(
            "--gluals-syntax", "extended",
            "--gluals-builtin-docs", bundledCatalog.toString(),
            "--gluals-builtin-docs", androidCatalog.toAbsolutePath().normalize().toString(),
            "--gluals-builtin-docs", commonCatalog.toAbsolutePath().normalize().toString()
        ), GluaLspServerDescriptor.commandArguments(settings, bundledCatalog));
    }

    // externalCatalogMethodResolvesAsBuiltinTarget 验证 app.isInstalled 能从外部 JSON 解析为文档与跳转目标。
    @Test
    void externalCatalogMethodResolvesAsBuiltinTarget() throws Exception {
        // 写入最小双语 catalog，并在测试结束后恢复全局 IDE 设置与目录缓存。
        Path androidCatalog = tempDir.resolve("android.json");
        Files.writeString(androidCatalog, """
            {
              "functions": {
                "app.isInstalled": {
                  "signature": {"en": "app.isInstalled(packageName)", "zh-CN": "app.isInstalled(packageName)"},
                  "description": {"en": "Checks installation.", "zh-CN": "判断应用是否已经安装。"},
                  "params": {"en": ["packageName: string."], "zh-CN": ["packageName：string。"]},
                  "returns": {"en": "returns: boolean.", "zh-CN": "返回：boolean。"},
                  "example": {"en": "app.isInstalled('demo')", "zh-CN": "app.isInstalled('demo')"}
                }
              }
            }
            """, StandardCharsets.UTF_8);

        try {
            GluaBuiltinCatalog.getInstance().reload(List.of(androidCatalog.toString()), "zh-CN");

            GluaBuiltin builtin = GluaBuiltinCatalog.getInstance().get("app.isInstalled");
            assertNotNull(builtin);
            assertEquals("判断应用是否已经安装。", builtin.description);
            String source = "if not app.isInstalled(packageName) then\nend\n";
            assertEquals("app.isInstalled", GluaAnalysis.builtinTargetAt(source, source.indexOf("isInstalled")));
        } finally {
            GluaBuiltinCatalog.getInstance().reload(List.of(), "en");
        }
    }
}

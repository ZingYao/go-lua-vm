package com.glua.jetbrains;

import org.junit.jupiter.api.Test;
import org.w3c.dom.Document;
import org.w3c.dom.Element;
import org.w3c.dom.NodeList;

import javax.xml.parsers.DocumentBuilderFactory;
import java.nio.file.Path;

import static org.junit.jupiter.api.Assertions.assertEquals;

// GluaPluginRegistrationTest 验证导航相关实现已注册到 JetBrains 扩展点。
final class GluaPluginRegistrationTest {
    // registersBuiltinNavigationExtensions 验证内置声明跳转处理器与引用贡献器各注册一次。
    @Test
    void registersBuiltinNavigationExtensions() throws Exception {
        // 解析插件描述文件并按扩展点与实现类精确计数。
        Document document = DocumentBuilderFactory.newInstance()
            .newDocumentBuilder()
            .parse(Path.of("src", "main", "resources", "META-INF", "plugin.xml").toFile());

        assertEquals(1, extensionCount(document, "gotoDeclarationHandler", "com.glua.jetbrains.GluaGotoDeclarationHandler"));
        assertEquals(1, extensionCount(document, "psi.referenceContributor", "com.glua.jetbrains.GluaBuiltinReferenceContributor"));
    }

    // extensionCount 统计指定扩展点下匹配实现类的注册数量。
    private static int extensionCount(Document document, String extensionPoint, String implementation) {
        // 遍历同名扩展节点并仅累计实现类完全匹配的注册。
        NodeList extensions = document.getElementsByTagName(extensionPoint);
        int matches = 0;
        for (int index = 0; index < extensions.getLength(); index++) {
            Element extension = (Element) extensions.item(index);
            if (implementation.equals(extension.getAttribute("implementation"))) {
                // 当前注册命中目标实现类，累计一次以检测缺失或重复配置。
                matches++;
            }
        }
        return matches;
    }
}

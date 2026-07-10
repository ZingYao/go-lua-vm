package com.glua.jetbrains;

import com.google.gson.Gson;
import com.google.gson.JsonObject;
import com.intellij.openapi.editor.Document;
import com.intellij.openapi.editor.impl.DocumentImpl;
import org.junit.jupiter.api.Test;

import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

final class GluaAnalysisGoldenTest {
    private static final Pattern STATIC_REQUIRE = Pattern.compile("require\\([\"']([^\"']+)[\"']\\)");

    @Test
    void goldenDiagnostics() throws IOException {
        Path goldenPath = Path.of("..", "..", "..", "tests", "editor", "golden-diagnostics.json").normalize();
        GoldenFile golden = new Gson().fromJson(Files.readString(goldenPath, StandardCharsets.UTF_8), GoldenFile.class);
        for (GoldenCase testCase : golden.cases) {
            List<String> diagnostics = new ArrayList<>();
            if (testCase.source.contains("require(")) {
                Path projectDir = goldenPath.getParent();
                Path currentPath = projectDir.resolve(testCase.name.replaceAll("[^A-Za-z0-9_-]", "_") + ".lua").normalize();
                GluaAnalysis.collectDiagnostics(testCase.source, currentPath, projectDir, Map.of(), "extended", true, (start, end, message) -> diagnostics.add(message));
            } else {
                GluaAnalysis.collectDiagnostics(testCase.source, (start, end, message) -> diagnostics.add(message));
            }
            assertEquals(testCase.diagnostics, diagnostics, testCase.name);
        }
    }

    @Test
    void goldenNativeRequireHints() throws IOException {
        Path goldenPath = Path.of("..", "..", "..", "tests", "editor", "golden-require-definition.json").normalize();
        RequireGoldenFile golden = new Gson().fromJson(Files.readString(goldenPath, StandardCharsets.UTF_8), RequireGoldenFile.class);
        for (RequireGoldenCase testCase : golden.cases) {
            if (testCase.nativeHint == null || testCase.nativeHint.isBlank()) {
                continue;
            }
            Matcher matcher = STATIC_REQUIRE.matcher(testCase.source);
            assertTrue(matcher.find(), testCase.name + " static require");
            assertTrue(GluaRequireSupport.isNativeModuleName(matcher.group(1)), testCase.name + " native module");
        }
    }

    @Test
    void goldenRequireMemberDefinitions() throws IOException {
        Path goldenPath = Path.of("..", "..", "..", "tests", "editor", "golden-require-definition.json").normalize();
        RequireGoldenFile golden = new Gson().fromJson(Files.readString(goldenPath, StandardCharsets.UTF_8), RequireGoldenFile.class);
        for (RequireGoldenCase testCase : golden.cases) {
            if (testCase.targetMarker == null || testCase.targetMarker.isBlank() || testCase.target == null || testCase.target.isBlank()) {
                continue;
            }
            String moduleText = golden.files.get(testCase.target);
            assertTrue(moduleText != null, testCase.name + " target fixture");
            List<GluaRequireSupport.ExportedMember> members = GluaRequireSupport.moduleExportMembers(moduleText, "", Path.of(testCase.target));
            GluaRequireSupport.ExportedMember matched = members.stream()
                .filter(member -> member.name().equals(testCase.targetMarker))
                .findFirst()
                .orElse(null);
            assertTrue(matched != null, testCase.name + " exported member");
            assertEquals(moduleText.indexOf(testCase.targetMarker), matched.start(), testCase.name + " target range");
            assertTrue(!matched.signature().isBlank(), testCase.name + " member signature");
        }
    }

    @Test
    void goldenRequireMemberCallers() throws IOException {
        Path goldenPath = Path.of("..", "..", "..", "tests", "editor", "golden-require-definition.json").normalize();
        RequireGoldenFile golden = new Gson().fromJson(Files.readString(goldenPath, StandardCharsets.UTF_8), RequireGoldenFile.class);
        for (RequireGoldenCase testCase : golden.cases) {
            if (testCase.targets == null) {
                continue;
            }
            int markerOffset = testCase.source.indexOf(testCase.marker);
            assertTrue(markerOffset >= 0, testCase.name + " marker");
            GluaRequireSupport.ExportedDefinition definition = GluaRequireSupport.exportedMemberDefinitionAt(testCase.source, markerOffset);
            assertTrue(definition != null, testCase.name + " exported definition");
            Map<Path, String> sources = new LinkedHashMap<>();
            for (Map.Entry<String, String> entry : golden.files.entrySet()) {
                sources.put(Path.of(entry.getKey()).normalize(), entry.getValue());
            }
            sources.put(Path.of(testCase.document).normalize(), testCase.source);
            List<GluaRequireSupport.CallerTarget> callers = GluaRequireSupport.memberCallersInFiles(
                Path.of(testCase.document).normalize(),
                definition.member(),
                sources,
                Path.of("")
            );
            assertEquals(testCase.targets.size(), callers.size(), testCase.name + " caller count");
            for (int i = 0; i < testCase.targets.size(); i++) {
                RequireGoldenTarget expected = testCase.targets.get(i);
                Path expectedPath = Path.of(expected.path).normalize();
                assertEquals(expectedPath, callers.get(i).path(), testCase.name + " caller path " + i);
                String targetSource = sources.get(expectedPath);
                assertTrue(targetSource != null, testCase.name + " caller source " + expected.path);
                assertEquals(targetSource.indexOf(expected.marker), callers.get(i).start(), testCase.name + " caller offset " + i);
            }
        }
    }

    @Test
    void goldenCompletionSymbols() throws IOException {
        Path goldenPath = Path.of("..", "..", "..", "tests", "editor", "golden-completion.json").normalize();
        CompletionGoldenFile golden = new Gson().fromJson(Files.readString(goldenPath, StandardCharsets.UTF_8), CompletionGoldenFile.class);
        for (CompletionGoldenCase testCase : golden.cases) {
            List<String> completions = completionsFor(golden, testCase);
            for (String expected : testCase.expected) {
                assertTrue(completions.contains(expected), testCase.name + " completion contains " + expected);
            }
            for (String unexpected : testCase.notExpected) {
                assertTrue(!completions.contains(unexpected), testCase.name + " completion excludes " + unexpected);
            }
            if ("module-member".equals(testCase.kind)) {
                List<GluaRequireSupport.ExportedMember> members = moduleMembersFor(golden, testCase);
                for (Map.Entry<String, String> entry : testCase.detailContains.entrySet()) {
                    GluaRequireSupport.ExportedMember member = findMember(members, entry.getKey());
                    assertTrue(member != null, testCase.name + " detail member " + entry.getKey());
                    assertTrue(member.detail().contains(entry.getValue()), testCase.name + " detail contains " + entry.getValue());
                }
                for (Map.Entry<String, String> entry : testCase.documentationContains.entrySet()) {
                    GluaRequireSupport.ExportedMember member = findMember(members, entry.getKey());
                    assertTrue(member != null, testCase.name + " documentation member " + entry.getKey());
                    assertTrue(member.documentation().contains(entry.getValue()), testCase.name + " documentation contains " + entry.getValue());
                }
                for (Map.Entry<String, String> entry : testCase.insertTextContains.entrySet()) {
                    GluaRequireSupport.ExportedMember member = findMember(members, entry.getKey());
                    assertTrue(member != null, testCase.name + " insert text member " + entry.getKey());
                    assertEquals(entry.getValue().replaceAll("\\$\\{\\d+:([^}]+)}", "$1"), GluaCompletionContributor.functionSnippetText(member.name(), member.signature()), testCase.name + " insert text");
                }
            }
        }
    }

    @Test
    void symbolSnapshotFeedsDiagnosticsDefinitionAndCompletion() {
        String completeSource = String.join("\n",
            "value = 1",
            "local function maker(arg)",
            "  return value + arg",
            "end"
        );
        List<String> diagnostics = new ArrayList<>();
        GluaAnalysis.collectDiagnostics(completeSource, (start, end, message) -> diagnostics.add(message));
        assertEquals(List.of(), diagnostics);

        Document document = new DocumentImpl(completeSource);
        GluaAnalysis.TextDefinition definition = GluaAnalysis.localDefinition(document, "value", completeSource.indexOf("value +"));
        assertEquals(0, definition.start());

        Document completionDocument = new DocumentImpl(completeSource + "\nval");
        assertTrue(GluaAnalysis.symbolCompletionNames(completionDocument, "val").contains("value"));
        assertTrue(GluaAnalysis.symbolCompletionNames(completionDocument, "mak").contains("maker"));
        GluaAnalysis.SymbolCompletion makerCompletion = GluaAnalysis.symbolCompletions(completionDocument, "mak").stream()
            .filter(item -> item.name().equals("maker"))
            .findFirst()
            .orElseThrow();
        assertEquals("maker(arg)", makerCompletion.signature());
    }

    @Test
    void eventNamespaceAliasesResolveBuiltinDefinition() {
        String source = String.join("\n",
            "local event = glua.event",
            "local events = event.events",
            "assert(event.events.progress_end == 'progress.end')",
            "assert(events.progress_end == 'progress.end')"
        );

        assertEquals(
            "glua.event.events.progress_end",
            GluaAnalysis.builtinTargetAt(source, source.indexOf("progress_end"))
        );
        assertEquals(
            "glua.event.events.progress_end",
            GluaAnalysis.builtinTargetAt(source, source.lastIndexOf("progress_end"))
        );
    }

    @Test
    void requiredGluaConstTableMemberAssignmentIsDiagnosed() {
        Path projectDir = Path.of("/fixture").normalize();
        Path currentPath = projectDir.resolve("main.glua").normalize();
        Path modulePath = projectDir.resolve("module.glua").normalize();
        Map<Path, String> files = Map.of(
            modulePath,
            "local tools = {}\ntools['_glua_const'] = {\n  a = 1,\n  b = 2,\n}\nreturn tools\n"
        );
        String source = "local tools = require('module')\ntools.a = 66\ntools[\"b\"] = 77\n";
        List<String> diagnostics = new ArrayList<>();

        GluaAnalysis.collectDiagnostics(source, currentPath, projectDir, files, "extended", true, (start, end, message) -> diagnostics.add(message));

        assertEquals(List.of("cannot assign to const table field", "cannot assign to const table field"), diagnostics);
    }

    @Test
    void functionTemplateEscapesParameterDefaults() {
        assertEquals("\"plain\"", GluaCompletionContributor.quoteTemplateExpression("plain"));
        assertEquals("\"quote\\\"name\"", GluaCompletionContributor.quoteTemplateExpression("quote\"name"));
        assertEquals("\"path\\\\name\"", GluaCompletionContributor.quoteTemplateExpression("path\\name"));
        assertEquals("match(s, pattern, init)", GluaCompletionContributor.functionSnippetText("match", "string.match(s, pattern [, init])"));
    }

    @Test
    void formatterIndentsFunctionAssignmentBodies() {
        String source = String.join("\n",
            "extensions = {}",
            "extensions.timesPrint = function(name,times)",
            "for i = 1,times do",
            "print('hello,'..name)",
            "end",
            "end"
        );
        String expected = String.join("\n",
            "extensions = {}",
            "extensions.timesPrint = function(name,times)",
            "  for i = 1,times do",
            "    print('hello,'..name)",
            "  end",
            "end"
        );
        assertEquals(expected, GluaFormatter.format(source));
    }

    @Test
    void formatterKeepsInlineTableFunctionClosed() {
        String source = String.join("\n",
            "aaa = {['ccc'] = function () xxx end}",
            "nextValue = 1"
        );
        assertEquals(source, GluaFormatter.format(source));
    }

    @Test
    void formatterIgnoresCommentBlockWords() {
        String source = String.join("\n",
            "extensions = {}",
            "-- description: function description",
            "-- param: name string parameter description",
            "-- return: nil",
            "extensions.timesPrint = function(name,times)",
            "for i = 1,times do",
            "print('hello,'..name)",
            "end",
            "end",
            "return extensions"
        );
        String expected = String.join("\n",
            "extensions = {}",
            "-- description: function description",
            "-- param: name string parameter description",
            "-- return: nil",
            "extensions.timesPrint = function(name,times)",
            "  for i = 1,times do",
            "    print('hello,'..name)",
            "  end",
            "end",
            "return extensions"
        );
        assertEquals(expected, GluaFormatter.format(source));
    }

    @Test
    void formatterIgnoresLongCommentBlockWords() {
        String source = String.join("\n",
            "--[[",
            "function fake()",
            "for i = 1, 3 do",
            "end",
            "]]",
            "value = 1"
        );
        assertEquals(source, GluaFormatter.format(source));
    }

    @Test
    void enterExpandsBlockOpeners() {
        assertEnterExpansion("for i = 1, 3 do", "\n  \nend", 3);
        assertEnterExpansion("  value = function ()", "\n    \n  end", 5);
        assertEnterExpansion("switch value do", "\n  case \n    \nend", 8);
        assertEnterExpansion("  case 1, 2", "\n    ", 5);
        assertEnterExpansion("  default", "\n    ", 5);
        assertEnterExpansion("if ok then", "\n  \nend", 3);
        assertEnterExpansion("repeat", "\n  \nuntil ", 3);
    }

    private static void assertEnterExpansion(String source, String expectedText, int expectedCaretDelta) {
        GluaBlockEnterSupport.Expansion expansion = GluaBlockEnterSupport.expansion(source, source.length());
        assertTrue(expansion != null, source + " expansion");
        assertEquals(expectedText, expansion.text(), source + " expansion text");
        assertEquals(expectedCaretDelta, expansion.caretDelta(), source + " caret");
    }

    private static final class GoldenFile {
        List<GoldenCase> cases = List.of();
    }

    private static final class GoldenCase {
        String name = "";
        String source = "";
        List<String> diagnostics = List.of();
    }

    private static final class RequireGoldenFile {
        Map<String, String> files = Map.of();
        List<RequireGoldenCase> cases = List.of();
    }

    private static final class RequireGoldenCase {
        String name = "";
        String document = "";
        String source = "";
        String marker = "";
        String nativeHint = "";
        String target = "";
        String targetMarker = "";
        List<RequireGoldenTarget> targets = null;
    }

    private static final class RequireGoldenTarget {
        String path = "";
        String marker = "";
    }

    private static String completionPrefix(String source, String marker) {
        int markerIndex = source.indexOf(marker);
        if (markerIndex < 0) {
            return "";
        }
        int start = markerIndex;
        while (start > 0 && Character.isJavaIdentifierPart(source.charAt(start - 1))) {
            start--;
        }
        return source.substring(start, markerIndex);
    }

    private static List<String> completionsFor(CompletionGoldenFile golden, CompletionGoldenCase testCase) {
        if ("keyword".equals(testCase.kind)) {
            GluaAnalysis.CompletionContext context = GluaAnalysis.completionContext(
                new DocumentImpl(testCase.source),
                testCase.source.indexOf(testCase.marker)
            );
            List<String> result = new ArrayList<>();
            if (context.keywordOnly() && "do".startsWith(context.prefix())) {
                result.add("do");
            }
            return result;
        }
        if ("module-member".equals(testCase.kind)) {
            String separator = testCase.marker.endsWith(":") ? ":" : ".";
            return moduleMembersFor(golden, testCase).stream()
                .filter(member -> !separator.equals(":") || member.callStyle().equals(":"))
                .map(GluaRequireSupport.ExportedMember::name)
                .toList();
        }
        if ("builtin-member".equals(testCase.kind)) {
            GluaAnalysis.CompletionContext context = GluaAnalysis.completionContext(
                new DocumentImpl(testCase.source),
                testCase.source.indexOf(testCase.marker) + testCase.marker.length()
            );
            assertTrue(context.method(), testCase.name + " builtin method context");
            List<String> result = new ArrayList<>();
            for (String name : builtinNamesFromResource()) {
                String prefix = context.module() + ".";
                if (name.startsWith(prefix)) {
                    String method = name.substring(prefix.length());
                    if (method.startsWith(context.prefix())) {
                        result.add(method);
                    }
                }
            }
            return result;
        }
        if ("builtin-global".equals(testCase.kind)) {
            String prefix = completionPrefix(testCase.source, testCase.marker);
            return builtinNamesFromResource().stream()
                .filter(name -> !name.contains("."))
                .filter(name -> name.startsWith(prefix))
                .toList();
        }
        Document document = new DocumentImpl(testCase.source);
        return GluaAnalysis.symbolCompletionNames(document, completionPrefix(testCase.source, testCase.marker));
    }

    private static List<String> builtinNamesFromResource() {
        try {
            Path builtinPath = Path.of("src", "main", "resources", "builtin-functions.json");
            JsonObject root = new Gson().fromJson(Files.readString(builtinPath, StandardCharsets.UTF_8), JsonObject.class);
            JsonObject functions = root.getAsJsonObject("functions");
            return functions.keySet().stream().sorted().toList();
        } catch (IOException error) {
            throw new AssertionError("failed to read builtin-functions.json", error);
        }
    }

    private static List<GluaRequireSupport.ExportedMember> moduleMembersFor(CompletionGoldenFile golden, CompletionGoldenCase testCase) {
        Matcher matcher = STATIC_REQUIRE.matcher(testCase.source);
        assertTrue(matcher.find(), testCase.name + " static require");
        String moduleName = matcher.group(1);
        String modulePath = "app/" + moduleName.replace('.', '/') + ".lua";
        String moduleText = golden.files.get(modulePath);
        assertTrue(moduleText != null, testCase.name + " fixture " + modulePath);
        return GluaRequireSupport.moduleExportSnapshot(moduleText, "", Path.of(modulePath)).members();
    }

    private static GluaRequireSupport.ExportedMember findMember(List<GluaRequireSupport.ExportedMember> members, String name) {
        return members.stream().filter(member -> member.name().equals(name)).findFirst().orElse(null);
    }

    private static final class CompletionGoldenFile {
        Map<String, String> files = Map.of();
        List<CompletionGoldenCase> cases = List.of();
    }

    private static final class CompletionGoldenCase {
        String name = "";
        String kind = "";
        String source = "";
        String marker = "";
        List<String> expected = List.of();
        List<String> notExpected = List.of();
        Map<String, String> detailContains = Map.of();
        Map<String, String> documentationContains = Map.of();
        Map<String, String> insertTextContains = Map.of();
    }
}

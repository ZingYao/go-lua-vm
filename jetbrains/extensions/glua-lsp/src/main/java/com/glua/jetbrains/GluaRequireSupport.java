package com.glua.jetbrains;

import com.intellij.openapi.project.Project;
import com.intellij.openapi.fileEditor.FileDocumentManager;
import com.intellij.openapi.vfs.LocalFileSystem;
import com.intellij.openapi.vfs.VirtualFile;
import com.intellij.psi.PsiElement;
import com.intellij.psi.PsiFile;
import com.intellij.psi.PsiManager;

import java.nio.file.Files;
import java.nio.file.Path;
import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.stream.Stream;

final class GluaRequireSupport {
    private static final Set<String> NATIVE_MODULES = Set.of("cjson", "cjson.safe", "lpeg", "socket.core", "mime.core");

    private GluaRequireSupport() {
    }

    static Target requiredModuleAt(PsiFile file, int offset) {
        List<GluaToken> tokens = GluaLexerUtil.scan(file.getText());
        int index = GluaLexerUtil.tokenIndexAt(tokens, offset);
        if (index < 0 || !tokens.get(index).type.equals("string")) {
            return null;
        }
        String moduleName = unquote(tokens.get(index).text);
        int openIndex = previousVisibleIndex(tokens, index);
        if (openIndex < 0) {
            return null;
        }
        int requireIndex = previousVisibleIndex(tokens, openIndex);
        if (openIndex < 0 || requireIndex < 0 || !tokens.get(openIndex).text.equals("(") || !tokens.get(requireIndex).text.equals("require")) {
            return null;
        }
        Path path = resolveModule(file, moduleName);
        if (path == null) {
            return null;
        }
        PsiFile targetFile = psiFile(file.getProject(), path);
        return targetFile == null ? null : new Target(targetFile, targetFile, 0, 1, path, moduleName);
    }

    static String nativeRequiredModuleAt(PsiFile file, int offset) {
        List<GluaToken> tokens = GluaLexerUtil.scan(file.getText());
        int index = GluaLexerUtil.tokenIndexAt(tokens, offset);
        if (index < 0 || !tokens.get(index).type.equals("string")) {
            return null;
        }
        String moduleName = unquote(tokens.get(index).text);
        int openIndex = previousVisibleIndex(tokens, index);
        int requireIndex = previousVisibleIndex(tokens, openIndex);
        if (openIndex < 0 || requireIndex < 0 || !tokens.get(openIndex).text.equals("(") || !tokens.get(requireIndex).text.equals("require")) {
            return null;
        }
        if (!isNativeModuleName(moduleName) || resolveModule(file, moduleName) != null) {
            return null;
        }
        return moduleName;
    }

    static boolean isNativeModuleName(String moduleName) {
        return NATIVE_MODULES.contains(moduleName);
    }

    static List<ExportedMember> requiredModuleCompletionMembers(PsiFile file, String receiver, String separator, String prefix) {
        if (receiver == null || receiver.isBlank()) {
            return List.of();
        }
        Path modulePath = requiredModuleForReceiver(file, GluaLexerUtil.scan(file.getText()), receiver);
        if (modulePath == null) {
            return List.of();
        }
        String text;
        try {
            text = Files.readString(modulePath, StandardCharsets.UTF_8);
        } catch (IOException ignored) {
            return List.of();
        }
        String effectivePrefix = prefix == null ? "" : prefix;
        List<ExportedMember> matches = new ArrayList<>();
        for (ExportedMember member : moduleExportSnapshot(text, receiver, modulePath).members()) {
            if (!member.name().startsWith(effectivePrefix)) {
                continue;
            }
            if (":".equals(separator) && !":".equals(member.callStyle())) {
                continue;
            }
            matches.add(member);
        }
        return matches;
    }

    static Target requiredMemberAt(PsiFile file, int offset) {
        List<GluaToken> tokens = GluaLexerUtil.scan(file.getText());
        int index = GluaLexerUtil.tokenIndexAt(tokens, offset);
        int separatorIndex = previousVisibleIndex(tokens, index);
        int receiverIndex = previousVisibleIndex(tokens, separatorIndex);
        if (index < 0 || receiverIndex < 0 || separatorIndex < 0 || !tokens.get(index).isName() || (!tokens.get(separatorIndex).text.equals(".") && !tokens.get(separatorIndex).text.equals(":")) || !tokens.get(receiverIndex).isName()) {
            return null;
        }
        String receiver = tokens.get(receiverIndex).text;
        String member = tokens.get(index).text;
        String separator = tokens.get(separatorIndex).text;
        Path modulePath = requiredModuleForReceiver(file, tokens, receiver);
        if (modulePath == null) {
            return null;
        }
        PsiFile targetFile = psiFile(file.getProject(), modulePath);
        if (targetFile == null) {
            return null;
        }
        for (ExportedMember definition : moduleExportSnapshot(targetFile.getText(), receiver, modulePath).members()) {
            if (!definition.name().equals(member)) {
                continue;
            }
            if (separator.equals(":") && !definition.callStyle().equals(":")) {
                continue;
            }
            PsiElement element = targetFile.findElementAt(definition.start());
            return new Target(targetFile, element == null ? targetFile : element, definition.start(), definition.end(), modulePath, definition.signature());
        }
        return null;
    }

    static List<Target> requiredMemberCallersAt(PsiFile file, int offset) {
        if (file.getVirtualFile() == null || file.getProject().getBasePath() == null) {
            return null;
        }
        ExportedDefinition definition = exportedMemberDefinitionAt(file.getText(), offset);
        if (definition == null) {
            return null;
        }
        Path modulePath = Path.of(file.getVirtualFile().getPath()).normalize();
        Path projectDir = Path.of(file.getProject().getBasePath()).normalize();
        List<CallerTarget> callers = memberCallersInFiles(modulePath, definition.member(), workspaceLuaSources(projectDir), projectDir);
        List<Target> targets = new ArrayList<>();
        for (CallerTarget caller : callers) {
            PsiFile callerFile = psiFile(file.getProject(), caller.path());
            if (callerFile == null) {
                continue;
            }
            PsiElement element = callerFile.findElementAt(caller.start());
            targets.add(new Target(callerFile, element == null ? callerFile : element, caller.start(), caller.end(), caller.path(), definition.member()));
        }
        return targets;
    }

    static List<CallerTarget> memberCallersInFiles(Path modulePath, String member, Map<Path, String> files, Path projectDir) {
        if (modulePath == null || member == null || member.isBlank()) {
            return List.of();
        }
        Path normalizedModulePath = modulePath.normalize();
        List<CallerTarget> callers = new ArrayList<>();
        for (Map.Entry<Path, String> entry : files.entrySet()) {
            Path filePath = entry.getKey().normalize();
            List<GluaToken> tokens = GluaLexerUtil.scan(entry.getValue());
            Set<String> receivers = new java.util.LinkedHashSet<>();
            for (Map.Entry<String, Path> binding : localRequireBindings(filePath, projectDir, tokens, files.keySet()).entrySet()) {
                if (binding.getValue().normalize().equals(normalizedModulePath)) {
                    receivers.add(binding.getKey());
                }
            }
            if (receivers.isEmpty()) {
                continue;
            }
            for (int i = 0; i < tokens.size(); i++) {
                int separatorIndex = previousVisibleIndex(tokens, i);
                int receiverIndex = previousVisibleIndex(tokens, separatorIndex);
                if (receiverIndex < 0 || separatorIndex < 0 || !tokens.get(i).isName() || (!tokens.get(separatorIndex).text.equals(".") && !tokens.get(separatorIndex).text.equals(":"))) {
                    continue;
                }
                if (receivers.contains(tokens.get(receiverIndex).text) && tokens.get(i).text.equals(member)) {
                    callers.add(new CallerTarget(filePath, tokens.get(i).start, tokens.get(i).end));
                }
            }
        }
        return callers;
    }

    static ExportedDefinition exportedMemberDefinitionAt(CharSequence source, int offset) {
        List<GluaToken> tokens = GluaLexerUtil.scan(source);
        int index = GluaLexerUtil.tokenIndexAt(tokens, offset);
        int separatorIndex = previousVisibleIndex(tokens, index);
        int receiverIndex = previousVisibleIndex(tokens, separatorIndex);
        if (index < 0 || receiverIndex < 0 || separatorIndex < 0 || !tokens.get(index).isName() || !tokens.get(receiverIndex).isName()) {
            return null;
        }
        String separator = tokens.get(separatorIndex).text;
        if (!separator.equals(".") && !separator.equals(":")) {
            return null;
        }
        Set<String> exportedTables = returnedTableNames(tokens);
        if (!exportedTables.contains(tokens.get(receiverIndex).text)) {
            return null;
        }
        boolean isDefinition = separator.equals(":") && isFunctionStatementMember(tokens, receiverIndex)
            || separator.equals(".") && isFunctionStatementMember(tokens, receiverIndex)
            || separator.equals(".") && memberDefinitionAt(source, tokens.get(index).start) != null;
        return isDefinition ? new ExportedDefinition(tokens.get(receiverIndex).text, tokens.get(index).text, tokens.get(index).start, tokens.get(index).end) : null;
    }

    static MemberDefinition memberDefinitionAt(CharSequence source, int offset) {
        List<GluaToken> tokens = GluaLexerUtil.scan(source);
        int index = GluaLexerUtil.tokenIndexAt(tokens, offset);
        int separatorIndex = previousVisibleIndex(tokens, index);
        int receiverIndex = previousVisibleIndex(tokens, separatorIndex);
        if (index < 0 || receiverIndex < 0 || separatorIndex < 0 || !tokens.get(index).isName() || !tokens.get(separatorIndex).text.equals(".") || !tokens.get(receiverIndex).isName()) {
            return null;
        }
        GluaToken member = tokens.get(index);
        int lineEnd = lineEnd(source, member.end);
        boolean hasEquals = false;
        boolean hasFunction = false;
        for (int cursor = nextVisibleIndex(tokens, index); cursor >= 0 && cursor < tokens.size() && tokens.get(cursor).start <= lineEnd; cursor = nextVisibleIndex(tokens, cursor)) {
            if (tokens.get(cursor).text.equals("=")) {
                hasEquals = true;
            }
            if (tokens.get(cursor).text.equals("function")) {
                hasFunction = true;
                break;
            }
        }
        if (!hasEquals || !hasFunction) {
            return null;
        }
        return new MemberDefinition(tokens.get(receiverIndex).text + "." + member.text, member.start, member.end);
    }

    static MemberDefinition functionDefinitionAt(CharSequence source, int offset) {
        List<GluaToken> tokens = GluaLexerUtil.scan(source);
        int index = GluaLexerUtil.tokenIndexAt(tokens, offset);
        if (index < 1 || !tokens.get(index).isName()) {
            return null;
        }
        int previousIndex = previousVisibleIndex(tokens, index);
        GluaToken previous = previousIndex < 0 ? null : tokens.get(previousIndex);
        int previousPreviousIndex = previousVisibleIndex(tokens, previousIndex);
        if (previous != null && previous.text.equals("function")) {
            return new MemberDefinition(tokens.get(index).text, tokens.get(index).start, tokens.get(index).end);
        }
        if (previous != null && previous.text.equals("function") && previousPreviousIndex >= 0 && tokens.get(previousPreviousIndex).text.equals("local")) {
            return new MemberDefinition(tokens.get(index).text, tokens.get(index).start, tokens.get(index).end);
        }
        return null;
    }

    static MemberDefinition localMemberReferenceDefinitionAt(CharSequence source, int offset) {
        List<GluaToken> tokens = GluaLexerUtil.scan(source);
        int index = GluaLexerUtil.tokenIndexAt(tokens, offset);
        int separatorIndex = previousVisibleIndex(tokens, index);
        int receiverIndex = previousVisibleIndex(tokens, separatorIndex);
        if (index < 0 || receiverIndex < 0 || separatorIndex < 0 || !tokens.get(index).isName() || !tokens.get(separatorIndex).text.equals(".") || !tokens.get(receiverIndex).isName()) {
            return null;
        }
        String receiver = tokens.get(receiverIndex).text;
        String member = tokens.get(index).text;
        MemberDefinition best = null;
        for (int i = 0; i < tokens.size(); i++) {
            int dot = nextVisibleIndex(tokens, i);
            int name = nextVisibleIndex(tokens, dot);
            if (name < 0 || !tokens.get(i).text.equals(receiver) || !tokens.get(dot).text.equals(".") || !tokens.get(name).text.equals(member)) {
                continue;
            }
            MemberDefinition definition = memberDefinitionAt(source, tokens.get(name).start);
            if (definition == null) {
                continue;
            }
            if (tokens.get(name).start <= offset) {
                best = definition;
            } else if (best == null) {
                best = definition;
            }
        }
        return best;
    }

    static Map<String, Path> localRequireBindings(Path filePath, Path projectDir, List<GluaToken> tokens, Set<Path> knownFiles) {
        Map<String, Path> bindings = new LinkedHashMap<>();
        for (int i = 0; i < tokens.size(); i++) {
            int receiverIndex = tokens.get(i).text.equals("local") ? nextVisibleIndex(tokens, i) : i;
            int equalsIndex = nextVisibleIndex(tokens, receiverIndex);
            int requireIndex = nextVisibleIndex(tokens, equalsIndex);
            int moduleIndex = moduleStringIndex(tokens, requireIndex);
            if (receiverIndex < 0 || equalsIndex < 0 || requireIndex < 0 || moduleIndex < 0) {
                continue;
            }
            if (!tokens.get(receiverIndex).isName() || !tokens.get(equalsIndex).text.equals("=") || !tokens.get(requireIndex).text.equals("require") || !tokens.get(moduleIndex).type.equals("string")) {
                continue;
            }
            Path resolved = resolveModule(filePath, projectDir, unquote(tokens.get(moduleIndex).text), knownFiles);
            if (resolved != null) {
                bindings.put(tokens.get(receiverIndex).text, resolved);
            }
        }
        return bindings;
    }

    private static int moduleStringIndex(List<GluaToken> tokens, int requireIndex) {
        int firstIndex = nextVisibleIndex(tokens, requireIndex);
        if (firstIndex < 0) {
            return -1;
        }
        if (tokens.get(firstIndex).type.equals("string")) {
            return firstIndex;
        }
        int secondIndex = nextVisibleIndex(tokens, firstIndex);
        if (tokens.get(firstIndex).text.equals("(") && secondIndex >= 0 && tokens.get(secondIndex).type.equals("string")) {
            return secondIndex;
        }
        return -1;
    }

    private static Path requiredModuleForReceiver(PsiFile file, List<GluaToken> tokens, String receiver) {
        for (int i = 0; i < tokens.size(); i++) {
            if (!tokens.get(i).text.equals("local")) {
                continue;
            }
            int receiverIndex = nextVisibleIndex(tokens, i);
            int equalsIndex = nextVisibleIndex(tokens, receiverIndex);
            int requireIndex = nextVisibleIndex(tokens, equalsIndex);
            int openIndex = nextVisibleIndex(tokens, requireIndex);
            int moduleIndex = nextVisibleIndex(tokens, openIndex);
            if (receiverIndex < 0 || equalsIndex < 0 || requireIndex < 0 || openIndex < 0 || moduleIndex < 0) {
                continue;
            }
            if (!tokens.get(receiverIndex).text.equals(receiver) || !tokens.get(equalsIndex).text.equals("=") || !tokens.get(requireIndex).text.equals("require") || !tokens.get(openIndex).text.equals("(") || !tokens.get(moduleIndex).type.equals("string")) {
                continue;
            }
            Path resolved = resolveModule(file, unquote(tokens.get(moduleIndex).text));
            if (resolved != null) {
                return resolved;
            }
        }
        return null;
    }

    private static MemberDefinition exportedMemberDefinition(String text, String receiver, String member) {
        List<GluaToken> tokens = GluaLexerUtil.scan(text);
        Set<String> exportedTables = returnedTableNames(tokens);
        exportedTables.add(receiver);
        for (int i = 0; i < tokens.size(); i++) {
            int separatorIndex = nextVisibleIndex(tokens, i);
            int memberIndex = nextVisibleIndex(tokens, separatorIndex);
            if (memberIndex < 0) {
                continue;
            }
            boolean receiverMatches = tokens.get(i).text.equals(receiver) || exportedTables.contains(tokens.get(i).text);
            if (receiverMatches && tokens.get(separatorIndex).text.equals(".") && tokens.get(memberIndex).text.equals(member)) {
                MemberDefinition definition = memberDefinitionAt(text, tokens.get(memberIndex).start);
                if (definition != null) {
                    return definition;
                }
            }
            MemberDefinition indexedDefinition = indexedMemberFunctionDefinition(tokens, i, exportedTables, member);
            if (indexedDefinition != null) {
                return indexedDefinition;
            }
        }
        MemberDefinition literalDefinition = tableLiteralMemberFunctionDefinition(tokens, exportedTables, member);
        if (literalDefinition != null) {
            return literalDefinition;
        }
        return null;
    }

    static List<ExportedMember> moduleExportMembers(String text, String receiver, Path sourcePath) {
        return moduleExportSnapshot(text, receiver, sourcePath).members();
    }

    static ModuleExportSnapshot moduleExportSnapshot(String text, String receiver, Path sourcePath) {
        List<GluaToken> tokens = GluaLexerUtil.scan(text);
        Set<String> exportedTables = returnedTableNames(tokens);
        if (receiver != null && !receiver.isBlank()) {
            exportedTables.add(receiver);
        }
        List<ExportedMember> members = new ArrayList<>();
        Set<String> seen = new java.util.LinkedHashSet<>();
        Set<String> constMembers = constExportMembers(tokens, exportedTables);
        for (String constMember : constMembers) {
            addExportedMember(members, seen, text, tokens, constMember, ".", "const", 0, Math.min(1, text.length()), sourcePath);
        }
        for (int i = 0; i < tokens.size(); i++) {
            int separatorIndex = nextVisibleIndex(tokens, i);
            if (separatorIndex < 0) {
                continue;
            }
            int memberIndex = nextVisibleIndex(tokens, separatorIndex);
            if (memberIndex >= 0 && exportedTables.contains(tokens.get(i).text) && tokens.get(memberIndex).isName()) {
                String separator = tokens.get(separatorIndex).text;
                if (separator.equals(":") && isFunctionStatementMember(tokens, i)) {
                    addExportedMember(members, seen, text, tokens, tokens.get(memberIndex).text, ":", "method", tokens.get(memberIndex).start, tokens.get(memberIndex).end, sourcePath);
                    continue;
                }
                if (separator.equals(".") && (isFunctionStatementMember(tokens, i) || memberDefinitionAt(text, tokens.get(memberIndex).start) != null)) {
                    addExportedMember(members, seen, text, tokens, tokens.get(memberIndex).text, ".", "function", tokens.get(memberIndex).start, tokens.get(memberIndex).end, sourcePath);
                    continue;
                }
                if (separator.equals(".") && hasAssignmentAfter(tokens, memberIndex) && !tokens.get(memberIndex).text.equals("_glua_const")) {
                    addExportedMember(members, seen, text, tokens, tokens.get(memberIndex).text, ".", "variable", tokens.get(memberIndex).start, tokens.get(memberIndex).end, sourcePath);
                    continue;
                }
            }
            MemberDefinition indexedDefinition = indexedMemberFunctionDefinition(tokens, i, exportedTables, "");
            if (indexedDefinition != null) {
                addExportedMember(members, seen, text, tokens, indexedDefinition.name(), ".", "function", indexedDefinition.start(), indexedDefinition.end(), sourcePath);
                continue;
            }
            MemberDefinition indexedValue = indexedMemberValueDefinition(tokens, i, exportedTables, "");
            if (indexedValue != null && !indexedValue.name().equals("_glua_const")) {
                addExportedMember(members, seen, text, tokens, indexedValue.name(), ".", "variable", indexedValue.start(), indexedValue.end(), sourcePath);
            }
        }
        for (String tableName : exportedTables) {
            for (TableRange range : tableConstructorRanges(tokens, tableName)) {
                for (MemberDefinition definition : tableFieldFunctionDefinitions(tokens, range.openIndex + 1, range.closeIndex, "")) {
                    addExportedMember(members, seen, text, tokens, definition.name(), ".", "function", definition.start(), definition.end(), sourcePath);
                }
                for (MemberDefinition definition : tableFieldValueDefinitions(tokens, range.openIndex + 1, range.closeIndex, "")) {
                    addExportedMember(members, seen, text, tokens, definition.name(), ".", "variable", definition.start(), definition.end(), sourcePath);
                }
            }
        }
        return new ModuleExportSnapshot(sourcePath, text, exportedTables, members, constMembers);
    }

    private static Set<String> constExportMembers(List<GluaToken> tokens, Set<String> exportedTables) {
        Set<String> members = new java.util.LinkedHashSet<>();
        for (int i = 0; i < tokens.size(); i++) {
            if (!exportedTables.contains(tokens.get(i).text)) {
                continue;
            }
            int separatorIndex = nextVisibleIndex(tokens, i);
            int memberIndex = nextVisibleIndex(tokens, separatorIndex);
            if (separatorIndex < 0 || memberIndex < 0) {
                continue;
            }
            int tableOpenIndex = -1;
            if (tokens.get(separatorIndex).text.equals(".") && tokens.get(memberIndex).text.equals("_glua_const")) {
                int equalsIndex = nextVisibleIndex(tokens, memberIndex);
                tableOpenIndex = nextVisibleIndex(tokens, equalsIndex);
            } else if (tokens.get(separatorIndex).text.equals("[")) {
                int closeIndex = nextVisibleIndex(tokens, memberIndex);
                String key = stringTokenValue(tokens.get(memberIndex));
                if (closeIndex >= 0 && tokens.get(closeIndex).text.equals("]") && key.equals("_glua_const")) {
                    int equalsIndex = nextVisibleIndex(tokens, closeIndex);
                    tableOpenIndex = nextVisibleIndex(tokens, equalsIndex);
                }
            }
            addConstMembersFromTable(tokens, tableOpenIndex, members);
        }
        for (String tableName : exportedTables) {
            for (TableRange range : tableConstructorRanges(tokens, tableName)) {
                int depth = 0;
                for (int i = range.openIndex + 1; i < range.closeIndex; i++) {
                    GluaToken token = tokens.get(i);
                    if (depth == 0) {
                        int tableOpenIndex = constTableFieldOpenIndex(tokens, i, range.closeIndex);
                        if (tableOpenIndex > 0) {
                            addConstMembersFromTable(tokens, tableOpenIndex, members);
                        }
                    }
                    if (isOpenDelimiter(token.text)) {
                        depth++;
                        continue;
                    }
                    if (isCloseDelimiter(token.text) && depth > 0) {
                        depth--;
                    }
                }
            }
        }
        return members;
    }

    private static int constTableFieldOpenIndex(List<GluaToken> tokens, int keyIndex, int endIndex) {
        GluaToken key = tokens.get(keyIndex);
        if (key.text.equals("[")) {
            int stringIndex = nextVisibleIndex(tokens, keyIndex);
            int closeIndex = nextVisibleIndex(tokens, stringIndex);
            int equalsIndex = nextVisibleIndex(tokens, closeIndex);
            int openIndex = nextVisibleIndex(tokens, equalsIndex);
            return stringIndex >= 0 && closeIndex >= 0 && equalsIndex >= 0 && openIndex >= 0 && closeIndex < endIndex && equalsIndex < endIndex && openIndex < endIndex && stringTokenValue(tokens.get(stringIndex)).equals("_glua_const") && tokens.get(closeIndex).text.equals("]") && tokens.get(equalsIndex).text.equals("=") && tokens.get(openIndex).text.equals("{")
                ? openIndex
                : -1;
        }
        int equalsIndex = nextVisibleIndex(tokens, keyIndex);
        int openIndex = nextVisibleIndex(tokens, equalsIndex);
        return equalsIndex >= 0 && openIndex >= 0 && key.text.equals("_glua_const") && equalsIndex < endIndex && openIndex < endIndex && tokens.get(equalsIndex).text.equals("=") && tokens.get(openIndex).text.equals("{")
            ? openIndex
            : -1;
    }

    private static void addConstMembersFromTable(List<GluaToken> tokens, int openIndex, Set<String> members) {
        if (openIndex < 0 || openIndex >= tokens.size() || !tokens.get(openIndex).text.equals("{")) {
            return;
        }
        int closeIndex = matchingDelimiterIndex(tokens, openIndex);
        if (closeIndex <= openIndex) {
            return;
        }
        int depth = 0;
        for (int i = openIndex + 1; i < closeIndex; i++) {
            GluaToken token = tokens.get(i);
            if (depth == 0 && token.isName()) {
                int equalsIndex = nextVisibleIndex(tokens, i);
                if (equalsIndex < closeIndex && tokens.get(equalsIndex).text.equals("=")) {
                    members.add(token.text);
                }
            }
            if (depth == 0 && token.text.equals("[")) {
                int keyIndex = nextVisibleIndex(tokens, i);
                int closeBracketIndex = nextVisibleIndex(tokens, keyIndex);
                int equalsIndex = nextVisibleIndex(tokens, closeBracketIndex);
                String key = stringTokenValue(tokens.get(keyIndex));
                if (!key.isBlank() && closeBracketIndex < closeIndex && equalsIndex < closeIndex && tokens.get(closeBracketIndex).text.equals("]") && tokens.get(equalsIndex).text.equals("=")) {
                    members.add(key);
                }
            }
            if (isOpenDelimiter(token.text)) {
                depth++;
                continue;
            }
            if (isCloseDelimiter(token.text) && depth > 0) {
                depth--;
            }
        }
    }

    private static void addExportedMember(List<ExportedMember> members, Set<String> seen, String text, List<GluaToken> tokens, String name, String callStyle, String kind, int start, int end, Path sourcePath) {
        if (name == null || name.isBlank() || seen.contains(name)) {
            return;
        }
        seen.add(name);
        String signature = memberSignature(tokens, start, name);
        String detail = switch (kind) {
            case "method" -> signature + " method";
            case "const" -> "GLua const field";
            case "variable" -> "GLua table field";
            default -> signature;
        };
        String documentation = GluaUserDocumentation.documentationAt(text, start, end, signature, "en").quickInfo();
        members.add(new ExportedMember(name, callStyle, kind, start, end, sourcePath, signature, detail, documentation));
    }

    private static boolean hasAssignmentAfter(List<GluaToken> tokens, int memberIndex) {
        int equalsIndex = nextVisibleIndex(tokens, memberIndex);
        return equalsIndex >= 0 && tokens.get(equalsIndex).text.equals("=");
    }

    private static String memberSignature(List<GluaToken> tokens, int memberStart, String name) {
        int memberIndex = tokenIndexAtStart(tokens, memberStart);
        int functionIndex = functionIndexForMember(tokens, memberIndex);
        if (functionIndex < 0) {
            return name + "()";
        }
        int openIndex = -1;
        for (int cursor = functionIndex + 1; cursor < tokens.size(); cursor++) {
            if (tokens.get(cursor).text.equals("(")) {
                openIndex = cursor;
                break;
            }
            if (tokens.get(cursor).start > tokens.get(functionIndex).end && openIndex < 0 && !sameLine(tokens.get(functionIndex), tokens.get(cursor))) {
                break;
            }
        }
        if (openIndex < 0) {
            return name + "()";
        }
        int closeIndex = matchingDelimiterIndex(tokens, openIndex);
        if (closeIndex < 0) {
            return name + "()";
        }
        List<String> params = new ArrayList<>();
        for (int cursor = openIndex + 1; cursor < closeIndex; cursor++) {
            GluaToken token = tokens.get(cursor);
            if (token.isName() || token.text.equals("...")) {
                params.add(token.text);
            }
        }
        return name + "(" + String.join(", ", params) + ")";
    }

    private static int functionIndexForMember(List<GluaToken> tokens, int memberIndex) {
        if (memberIndex < 0) {
            return -1;
        }
        int separatorIndex = previousVisibleIndex(tokens, memberIndex);
        int receiverIndex = previousVisibleIndex(tokens, separatorIndex);
        int functionIndex = previousVisibleIndex(tokens, receiverIndex);
        if (functionIndex >= 0 && tokens.get(functionIndex).text.equals("function")) {
            return functionIndex;
        }
        int closeBracketIndex = nextVisibleIndex(tokens, memberIndex);
        if (closeBracketIndex >= 0 && tokens.get(closeBracketIndex).text.equals("]")) {
            return indexedFunctionIndex(tokens, closeBracketIndex);
        }
        int equalsIndex = nextVisibleIndex(tokens, memberIndex);
        int nextFunctionIndex = nextVisibleIndex(tokens, equalsIndex);
        if (equalsIndex >= 0 && nextFunctionIndex >= 0 && tokens.get(equalsIndex).text.equals("=") && tokens.get(nextFunctionIndex).text.equals("function")) {
            return nextFunctionIndex;
        }
        return -1;
    }

    private static int tokenIndexAtStart(List<GluaToken> tokens, int start) {
        for (int i = 0; i < tokens.size(); i++) {
            if (tokens.get(i).start == start) {
                return i;
            }
        }
        return -1;
    }

    private static boolean sameLine(GluaToken left, GluaToken right) {
        return left != null && right != null && left.range().getStartOffset() <= right.range().getStartOffset();
    }

    private static boolean isFunctionStatementMember(List<GluaToken> tokens, int receiverIndex) {
        int previousIndex = previousVisibleIndex(tokens, receiverIndex);
        return previousIndex >= 0 && tokens.get(previousIndex).text.equals("function");
    }

    private static Set<String> returnedTableNames(List<GluaToken> tokens) {
        Set<String> names = new java.util.LinkedHashSet<>();
        for (int i = 0; i < tokens.size(); i++) {
            if (!tokens.get(i).text.equals("return")) {
                continue;
            }
            int nameIndex = nextVisibleIndex(tokens, i);
            if (nameIndex >= 0 && tokens.get(nameIndex).isName()) {
                names.add(tokens.get(nameIndex).text);
            }
        }
        return names;
    }

    private static MemberDefinition indexedMemberFunctionDefinition(List<GluaToken> tokens, int receiverIndex, Set<String> exportedTables, String member) {
        if (!exportedTables.contains(tokens.get(receiverIndex).text)) {
            return null;
        }
        int openIndex = nextVisibleIndex(tokens, receiverIndex);
        int keyIndex = nextVisibleIndex(tokens, openIndex);
        int closeIndex = nextVisibleIndex(tokens, keyIndex);
        if (openIndex < 0 || keyIndex < 0 || closeIndex < 0 || !tokens.get(openIndex).text.equals("[") || !tokens.get(closeIndex).text.equals("]")) {
            return null;
        }
        if (!member.isBlank() && !stringTokenValue(tokens.get(keyIndex)).equals(member)) {
            return null;
        }
        String name = stringTokenValue(tokens.get(keyIndex));
        return isIndexedFunctionDefinition(tokens, closeIndex)
            ? new MemberDefinition(name, tokens.get(keyIndex).start, tokens.get(keyIndex).end)
            : null;
    }

    private static MemberDefinition indexedMemberValueDefinition(List<GluaToken> tokens, int receiverIndex, Set<String> exportedTables, String member) {
        if (!exportedTables.contains(tokens.get(receiverIndex).text)) {
            return null;
        }
        int openIndex = nextVisibleIndex(tokens, receiverIndex);
        int keyIndex = nextVisibleIndex(tokens, openIndex);
        int closeIndex = nextVisibleIndex(tokens, keyIndex);
        int equalsIndex = nextVisibleIndex(tokens, closeIndex);
        if (openIndex < 0 || keyIndex < 0 || closeIndex < 0 || equalsIndex < 0 || !tokens.get(openIndex).text.equals("[") || !tokens.get(closeIndex).text.equals("]") || !tokens.get(equalsIndex).text.equals("=")) {
            return null;
        }
        String name = stringTokenValue(tokens.get(keyIndex));
        if (name.isBlank() || (!member.isBlank() && !name.equals(member)) || isIndexedFunctionDefinition(tokens, closeIndex)) {
            return null;
        }
        return new MemberDefinition(name, tokens.get(keyIndex).start, tokens.get(keyIndex).end);
    }

    private static MemberDefinition tableLiteralMemberFunctionDefinition(List<GluaToken> tokens, Set<String> exportedTables, String member) {
        for (String tableName : exportedTables) {
            for (TableRange range : tableConstructorRanges(tokens, tableName)) {
                MemberDefinition definition = tableFieldFunctionDefinition(tokens, range.openIndex + 1, range.closeIndex, member);
                if (definition != null) {
                    return definition;
                }
            }
        }
        return null;
    }

    private static List<MemberDefinition> tableFieldFunctionDefinitions(List<GluaToken> tokens, int startIndex, int endIndex, String member) {
        List<MemberDefinition> definitions = new ArrayList<>();
        for (int i = startIndex; i < endIndex; i++) {
            GluaToken token = tokens.get(i);
            if (token.text.equals("[")) {
                int keyIndex = nextVisibleIndex(tokens, i);
                int closeIndex = nextVisibleIndex(tokens, keyIndex);
                String name = keyIndex >= 0 ? stringTokenValue(tokens.get(keyIndex)) : "";
                if (closeIndex < endIndex && tokens.get(closeIndex).text.equals("]") && !name.isBlank() && (member.isBlank() || name.equals(member)) && isIndexedFunctionDefinition(tokens, closeIndex)) {
                    definitions.add(new MemberDefinition(name, tokens.get(keyIndex).start, tokens.get(keyIndex).end));
                }
                continue;
            }
            if (token.isName() && (member.isBlank() || token.text.equals(member)) && isBareTableFieldFunctionDefinition(tokens, i, endIndex)) {
                definitions.add(new MemberDefinition(token.text, token.start, token.end));
            }
        }
        return definitions;
    }

    private static List<MemberDefinition> tableFieldValueDefinitions(List<GluaToken> tokens, int startIndex, int endIndex, String member) {
        List<MemberDefinition> definitions = new ArrayList<>();
        int depth = 0;
        for (int i = startIndex; i < endIndex; i++) {
            GluaToken token = tokens.get(i);
            if (depth == 0 && token.text.equals("[")) {
                int keyIndex = nextVisibleIndex(tokens, i);
                int closeIndex = nextVisibleIndex(tokens, keyIndex);
                int equalsIndex = nextVisibleIndex(tokens, closeIndex);
                String name = keyIndex >= 0 ? stringTokenValue(tokens.get(keyIndex)) : "";
                if (closeIndex < endIndex && equalsIndex < endIndex && tokens.get(closeIndex).text.equals("]") && tokens.get(equalsIndex).text.equals("=") && !name.isBlank() && !name.equals("_glua_const") && (member.isBlank() || name.equals(member)) && !isIndexedFunctionDefinition(tokens, closeIndex)) {
                    definitions.add(new MemberDefinition(name, tokens.get(keyIndex).start, tokens.get(keyIndex).end));
                }
            } else if (depth == 0 && token.isName() && !token.text.equals("_glua_const") && (member.isBlank() || token.text.equals(member)) && isBareTableFieldValueDefinition(tokens, i, endIndex)) {
                definitions.add(new MemberDefinition(token.text, token.start, token.end));
            }
            if (isOpenDelimiter(token.text)) {
                depth++;
                continue;
            }
            if (isCloseDelimiter(token.text) && depth > 0) {
                depth--;
            }
        }
        return definitions;
    }

    private static List<TableRange> tableConstructorRanges(List<GluaToken> tokens, String tableName) {
        java.util.ArrayList<TableRange> ranges = new java.util.ArrayList<>();
        for (int i = 0; i < tokens.size(); i++) {
            if (!tokens.get(i).text.equals(tableName)) {
                continue;
            }
            int equalsIndex = nextVisibleIndex(tokens, i);
            int openIndex = nextVisibleIndex(tokens, equalsIndex);
            if (equalsIndex < 0 || openIndex < 0 || !tokens.get(equalsIndex).text.equals("=") || !tokens.get(openIndex).text.equals("{")) {
                continue;
            }
            int closeIndex = matchingDelimiterIndex(tokens, openIndex);
            if (closeIndex > openIndex) {
                ranges.add(new TableRange(openIndex, closeIndex));
            }
        }
        return ranges;
    }

    private static MemberDefinition tableFieldFunctionDefinition(List<GluaToken> tokens, int startIndex, int endIndex, String member) {
        for (int i = startIndex; i < endIndex; i++) {
            GluaToken token = tokens.get(i);
            if (token.text.equals("[")) {
                int keyIndex = nextVisibleIndex(tokens, i);
                int closeIndex = nextVisibleIndex(tokens, keyIndex);
                if (closeIndex < endIndex && tokens.get(closeIndex).text.equals("]") && stringTokenValue(tokens.get(keyIndex)).equals(member) && isIndexedFunctionDefinition(tokens, closeIndex)) {
                    return new MemberDefinition(member, tokens.get(keyIndex).start, tokens.get(keyIndex).end);
                }
                continue;
            }
            if (token.text.equals(member) && isBareTableFieldFunctionDefinition(tokens, i, endIndex)) {
                return new MemberDefinition(member, token.start, token.end);
            }
        }
        return null;
    }

    private static boolean isIndexedFunctionDefinition(List<GluaToken> tokens, int closeBracketIndex) {
        return indexedFunctionIndex(tokens, closeBracketIndex) >= 0;
    }

    private static int indexedFunctionIndex(List<GluaToken> tokens, int closeBracketIndex) {
        int equalsIndex = nextVisibleIndex(tokens, closeBracketIndex);
        int functionIndex = nextVisibleIndex(tokens, equalsIndex);
        return equalsIndex >= 0 && functionIndex >= 0 && tokens.get(equalsIndex).text.equals("=") && tokens.get(functionIndex).text.equals("function") ? functionIndex : -1;
    }

    private static boolean isBareTableFieldFunctionDefinition(List<GluaToken> tokens, int keyIndex, int endIndex) {
        int equalsIndex = nextVisibleIndex(tokens, keyIndex);
        int functionIndex = nextVisibleIndex(tokens, equalsIndex);
        return equalsIndex > keyIndex && functionIndex < endIndex && tokens.get(equalsIndex).text.equals("=") && tokens.get(functionIndex).text.equals("function");
    }

    private static boolean isBareTableFieldValueDefinition(List<GluaToken> tokens, int keyIndex, int endIndex) {
        int equalsIndex = nextVisibleIndex(tokens, keyIndex);
        return equalsIndex > keyIndex && equalsIndex < endIndex && tokens.get(equalsIndex).text.equals("=") && !isBareTableFieldFunctionDefinition(tokens, keyIndex, endIndex);
    }

    private static int matchingDelimiterIndex(List<GluaToken> tokens, int openIndex) {
        String open = tokens.get(openIndex).text;
        String close = open.equals("{") ? "}" : open.equals("[") ? "]" : open.equals("(") ? ")" : "";
        if (close.isBlank()) {
            return -1;
        }
        int depth = 0;
        for (int i = openIndex; i < tokens.size(); i++) {
            if (tokens.get(i).text.equals(open)) {
                depth++;
                continue;
            }
            if (tokens.get(i).text.equals(close)) {
                depth--;
                if (depth == 0) {
                    return i;
                }
            }
        }
        return -1;
    }

    private static String stringTokenValue(GluaToken token) {
        if (token == null || !token.type.equals("string") || token.text.length() < 2) {
            return "";
        }
        return token.text.substring(1, token.text.length() - 1);
    }

    private static boolean isOpenDelimiter(String text) {
        return text.equals("(") || text.equals("{") || text.equals("[");
    }

    private static boolean isCloseDelimiter(String text) {
        return text.equals(")") || text.equals("}") || text.equals("]");
    }

    private static Path resolveModule(PsiFile file, String moduleName) {
        if (moduleName == null || moduleName.isBlank() || file.getVirtualFile() == null) {
            return null;
        }
        Path currentDir = Path.of(file.getVirtualFile().getPath()).getParent();
        Path projectDir = file.getProject().getBasePath() == null ? currentDir : Path.of(file.getProject().getBasePath());
        return resolveModule(Path.of(file.getVirtualFile().getPath()), projectDir, moduleName);
    }

    private static Path resolveModule(Path currentFile, Path projectDir, String moduleName) {
        return resolveModule(currentFile, projectDir, moduleName, Set.of());
    }

    private static Path resolveModule(Path currentFile, Path projectDir, String moduleName, Set<Path> knownFiles) {
        if (moduleName == null || moduleName.isBlank() || currentFile == null) {
            return null;
        }
        String relative = moduleName.replace('.', '/');
        Path currentDir = currentFile.getParent();
        for (Path root : List.of(currentDir)) {
            if (root == null) {
                continue;
            }
            Path resolved = resolveModuleUnderRoot(root, relative, knownFiles);
            if (resolved != null) {
                return resolved;
            }
        }
        if (projectDir != null) {
            for (String prefix : List.of("", "lua", "src")) {
                Path root = prefix.isBlank() ? projectDir : projectDir.resolve(prefix);
                Path resolved = resolveModuleUnderRoot(root, relative, knownFiles);
                if (resolved != null) {
                    return resolved;
                }
            }
        }
        return null;
    }

    private static Map<Path, String> workspaceLuaSources(Path projectDir) {
        Map<Path, String> sources = new LinkedHashMap<>();
        if (projectDir == null || !Files.exists(projectDir)) {
            return sources;
        }
        try (Stream<Path> stream = Files.walk(projectDir)) {
            stream
                .filter(Files::isRegularFile)
                .filter(path -> !hasSkippedSegment(projectDir, path))
                .filter(path -> path.toString().endsWith(".lua") || path.toString().endsWith(".glua"))
                .sorted()
                .forEach(path -> {
                    sources.put(path.normalize(), sourceText(path));
                });
        } catch (IOException ignored) {
        }
        return sources;
    }

    private static String sourceText(Path path) {
        VirtualFile file = LocalFileSystem.getInstance().findFileByNioFile(path);
        if (file != null) {
            com.intellij.openapi.editor.Document document = FileDocumentManager.getInstance().getDocument(file);
            if (document != null) {
                return document.getCharsSequence().toString();
            }
        }
        try {
            return Files.readString(path, StandardCharsets.UTF_8);
        } catch (IOException ignored) {
            return "";
        }
    }

    private static boolean hasSkippedSegment(Path root, Path path) {
        Path relative;
        try {
            relative = root.relativize(path);
        } catch (IllegalArgumentException ignored) {
            return false;
        }
        Set<String> skipped = Set.of(".git", ".gradle", ".idea", ".vscode", "build", "dist", "node_modules", "out");
        for (Path segment : relative) {
            if (skipped.contains(segment.toString())) {
                return true;
            }
        }
        return false;
    }

    private static Path resolveModuleUnderRoot(Path root, String relative) {
        return resolveModuleUnderRoot(root, relative, Set.of());
    }

    private static Path resolveModuleUnderRoot(Path root, String relative, Set<Path> knownFiles) {
        for (String suffix : List.of(".glua", ".lua", "/init.glua", "/init.lua")) {
            Path candidate = root.resolve(relative + suffix).normalize();
            if (knownFiles.contains(candidate) || Files.exists(candidate)) {
                return candidate;
            }
        }
        return null;
    }

    private static PsiFile psiFile(Project project, Path path) {
        VirtualFile file = LocalFileSystem.getInstance().refreshAndFindFileByNioFile(path);
        return file == null ? null : PsiManager.getInstance(project).findFile(file);
    }

    private static String unquote(String value) {
        if (value == null || value.length() < 2) {
            return value;
        }
        return value.substring(1, value.length() - 1);
    }

    private static int lineEnd(CharSequence source, int offset) {
        for (int i = offset; i < source.length(); i++) {
            if (source.charAt(i) == '\n' || source.charAt(i) == '\r') {
                return i;
            }
        }
        return source.length();
    }

    private static int previousVisibleIndex(List<GluaToken> tokens, int index) {
        for (int i = index - 1; i >= 0; i--) {
            GluaToken token = tokens.get(i);
            if (!token.type.equals("space") && !token.type.equals("comment")) {
                return i;
            }
        }
        return -1;
    }

    private static int nextVisibleIndex(List<GluaToken> tokens, int index) {
        for (int i = index + 1; i < tokens.size(); i++) {
            GluaToken token = tokens.get(i);
            if (!token.type.equals("space") && !token.type.equals("comment")) {
                return i;
            }
        }
        return -1;
    }

    record Target(PsiFile file, PsiElement element, int start, int end, Path path, String name) {
    }

    record MemberDefinition(String name, int start, int end) {
    }

    record ExportedDefinition(String receiver, String member, int start, int end) {
    }

    record CallerTarget(Path path, int start, int end) {
    }

    record ModuleExportSnapshot(Path sourcePath, String text, Set<String> exportedTables, List<ExportedMember> members, Set<String> constMembers) {
    }

    record ExportedMember(String name, String callStyle, String kind, int start, int end, Path sourcePath, String signature, String detail, String documentation) {
    }

    private record TableRange(int openIndex, int closeIndex) {
    }
}

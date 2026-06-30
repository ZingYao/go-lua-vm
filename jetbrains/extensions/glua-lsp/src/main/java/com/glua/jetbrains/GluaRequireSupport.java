package com.glua.jetbrains;

import com.intellij.openapi.project.Project;
import com.intellij.openapi.vfs.LocalFileSystem;
import com.intellij.openapi.vfs.VirtualFile;
import com.intellij.psi.PsiElement;
import com.intellij.psi.PsiFile;
import com.intellij.psi.PsiManager;

import java.nio.file.Files;
import java.nio.file.Path;
import java.util.List;

final class GluaRequireSupport {
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

    static Target requiredMemberAt(PsiFile file, int offset) {
        List<GluaToken> tokens = GluaLexerUtil.scan(file.getText());
        int index = GluaLexerUtil.tokenIndexAt(tokens, offset);
        int separatorIndex = previousVisibleIndex(tokens, index);
        int receiverIndex = previousVisibleIndex(tokens, separatorIndex);
        if (index < 0 || receiverIndex < 0 || separatorIndex < 0 || !tokens.get(index).isName() || !tokens.get(separatorIndex).text.equals(".") || !tokens.get(receiverIndex).isName()) {
            return null;
        }
        String receiver = tokens.get(receiverIndex).text;
        String member = tokens.get(index).text;
        Path modulePath = requiredModuleForReceiver(file, tokens, receiver);
        if (modulePath == null) {
            return null;
        }
        PsiFile targetFile = psiFile(file.getProject(), modulePath);
        if (targetFile == null) {
            return null;
        }
        MemberDefinition definition = exportedMemberDefinition(targetFile.getText(), receiver, member);
        if (definition == null) {
            return null;
        }
        PsiElement element = targetFile.findElementAt(definition.start);
        return new Target(targetFile, element == null ? targetFile : element, definition.start, definition.end, modulePath, receiver + "." + member);
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
        for (int i = 0; i < tokens.size(); i++) {
            int separatorIndex = nextVisibleIndex(tokens, i);
            int memberIndex = nextVisibleIndex(tokens, separatorIndex);
            if (memberIndex < 0) {
                continue;
            }
            if (tokens.get(i).text.equals(receiver) && tokens.get(separatorIndex).text.equals(".") && tokens.get(memberIndex).text.equals(member)) {
                MemberDefinition definition = memberDefinitionAt(text, tokens.get(memberIndex).start);
                if (definition != null) {
                    return definition;
                }
            }
        }
        return null;
    }

    private static Path resolveModule(PsiFile file, String moduleName) {
        if (moduleName == null || moduleName.isBlank() || file.getVirtualFile() == null) {
            return null;
        }
        String relative = moduleName.replace('.', '/');
        Path currentDir = Path.of(file.getVirtualFile().getPath()).getParent();
        Path projectDir = file.getProject().getBasePath() == null ? currentDir : Path.of(file.getProject().getBasePath());
        for (Path root : List.of(currentDir, projectDir)) {
            if (root == null) {
                continue;
            }
            for (String suffix : List.of(".glua", ".lua", "/init.glua", "/init.lua")) {
                Path candidate = root.resolve(relative + suffix).normalize();
                if (Files.exists(candidate)) {
                    return candidate;
                }
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
}

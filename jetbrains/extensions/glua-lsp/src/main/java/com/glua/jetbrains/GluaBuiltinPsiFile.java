package com.glua.jetbrains;

import com.intellij.openapi.application.PathManager;
import com.intellij.openapi.vfs.LocalFileSystem;
import com.intellij.openapi.vfs.VirtualFile;
import com.intellij.openapi.project.Project;
import com.intellij.psi.PsiElement;
import com.intellij.psi.PsiFile;
import com.intellij.psi.PsiFileFactory;
import com.intellij.psi.PsiManager;

import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;

final class GluaBuiltinPsiFile {
    private GluaBuiltinPsiFile() {
    }

    static PsiElement create(Project project, String name, GluaBuiltin builtin) {
        StringBuilder text = new StringBuilder();
        text.append("-- ").append(builtin.description).append('\n');
        for (String param : builtin.params) {
            text.append("-- @param ").append(param).append('\n');
        }
        text.append("-- @return ").append(builtin.returns).append("\n\n");
        if (!builtin.example.isBlank()) {
            text.append("-- @example\n");
            for (String line : builtin.example.split("\\R")) {
                text.append("-- ").append(line).append('\n');
            }
            text.append('\n');
        }
        text.append("-- ").append(name).append(": ").append(builtin.signature).append('\n');
        int functionStart = text.length();
        text.append("function ").append(name).append("()\nend\n");
        PsiFile file = physicalFile(project, name, text.toString());
        if (file == null) {
            file = PsiFileFactory.getInstance(project).createFileFromText(name + ".lua", GluaFileType.INSTANCE, text);
        }
        int nameStart = functionStart + "function ".length() + name.lastIndexOf('.') + 1;
        PsiElement element = file.findElementAt(nameStart);
        return element == null ? file : element;
    }

    private static PsiFile physicalFile(Project project, String name, String text) {
        try {
            Path directory = Path.of(PathManager.getSystemPath(), "glua-builtin-docs");
            Files.createDirectories(directory);
            Path path = directory.resolve(safeFileName(name) + ".glua");
            Files.writeString(path, text, StandardCharsets.UTF_8);
            VirtualFile file = LocalFileSystem.getInstance().refreshAndFindFileByNioFile(path);
            if (file == null) {
                return null;
            }
            return PsiManager.getInstance(project).findFile(file);
        } catch (Exception ignored) {
            return null;
        }
    }

    private static String safeFileName(String name) {
        return name.replaceAll("[^A-Za-z0-9._-]", "_");
    }
}

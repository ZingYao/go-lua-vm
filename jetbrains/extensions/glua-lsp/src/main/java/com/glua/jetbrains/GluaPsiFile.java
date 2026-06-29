package com.glua.jetbrains;

import com.intellij.extapi.psi.PsiFileBase;
import com.intellij.openapi.fileTypes.FileType;
import com.intellij.psi.FileViewProvider;
import org.jetbrains.annotations.NotNull;

public final class GluaPsiFile extends PsiFileBase {
    public GluaPsiFile(@NotNull FileViewProvider viewProvider) {
        super(viewProvider, GluaLanguage.INSTANCE);
    }

    @Override
    public @NotNull FileType getFileType() {
        return GluaFileType.INSTANCE;
    }

    @Override
    public String toString() {
        return "GLua File";
    }
}

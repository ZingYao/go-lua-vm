package com.glua.jetbrains;

import com.intellij.openapi.diagnostic.Logger;
import com.intellij.platform.backend.documentation.DocumentationTarget;
import com.intellij.platform.backend.documentation.DocumentationTargetProvider;
import com.intellij.psi.PsiElement;
import com.intellij.psi.PsiFile;
import org.jetbrains.annotations.NotNull;

import java.util.List;

public final class GluaDocumentationTargetProvider implements DocumentationTargetProvider {
    private static final Logger LOG = Logger.getInstance(GluaDocumentationTargetProvider.class);

    @Override
    public List<? extends DocumentationTarget> documentationTargets(@NotNull PsiFile file, int offset) {
        if (file.getFileType() != GluaFileType.INSTANCE) {
            return List.of();
        }
        String name = GluaAnalysis.builtinTargetAt(file.getText(), offset);
        GluaBuiltin builtin = name == null ? null : GluaBuiltinCatalog.getInstance().get(name);
        if (builtin == null) {
            LOG.info("glua documentation target miss offset=" + offset);
            return List.of();
        }
        PsiElement navigation = GluaBuiltinPsiFile.create(file.getProject(), name, builtin);
        LOG.info("glua documentation target=" + name + ", offset=" + offset);
        return List.of(new GluaDocumentationTarget(name, builtin, navigation));
    }
}

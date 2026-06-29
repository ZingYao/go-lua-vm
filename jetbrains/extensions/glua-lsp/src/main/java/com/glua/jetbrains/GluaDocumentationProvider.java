package com.glua.jetbrains;

import com.intellij.lang.documentation.AbstractDocumentationProvider;
import com.intellij.openapi.diagnostic.Logger;
import com.intellij.openapi.editor.Editor;
import com.intellij.openapi.util.Key;
import com.intellij.psi.PsiElement;
import com.intellij.psi.PsiFile;
import org.jetbrains.annotations.Nullable;

public final class GluaDocumentationProvider extends AbstractDocumentationProvider {
    private static final Logger LOG = Logger.getInstance(GluaDocumentationProvider.class);
    private static final Key<String> BUILTIN_TARGET = Key.create("glua.builtin.documentation.target");

    @Override
    public @Nullable PsiElement getCustomDocumentationElement(Editor editor, PsiFile file, @Nullable PsiElement contextElement, int targetOffset) {
        if (file == null || file.getFileType() != GluaFileType.INSTANCE) {
            return null;
        }
        String name = GluaAnalysis.builtinTargetAt(file.getText(), targetOffset);
        GluaBuiltin builtin = name == null ? null : GluaBuiltinCatalog.getInstance().get(name);
        if (builtin == null) {
            LOG.info("glua custom doc miss offset=" + targetOffset);
            return null;
        }
        PsiElement element = GluaBuiltinPsiFile.create(file.getProject(), name, builtin);
        element.putUserData(BUILTIN_TARGET, name);
        LOG.info("glua custom doc target=" + name + ", offset=" + targetOffset);
        return element;
    }

    @Override
    public @Nullable String generateDoc(PsiElement element, @Nullable PsiElement originalElement) {
        String name = builtinName(element, originalElement);
        GluaBuiltin builtin = name == null ? null : GluaBuiltinCatalog.getInstance().get(name);
        LOG.info("glua doc " + (builtin == null ? "miss" : "target=" + name));
        return builtin == null ? null : builtin.markdown(name);
    }

    @Override
    public @Nullable String generateHoverDoc(PsiElement element, @Nullable PsiElement originalElement) {
        String name = builtinName(element, originalElement);
        GluaBuiltin builtin = name == null ? null : GluaBuiltinCatalog.getInstance().get(name);
        LOG.info("glua hover doc " + (builtin == null ? "miss" : "target=" + name));
        return builtin == null ? null : builtin.markdown(name);
    }

    @Override
    public @Nullable String getQuickNavigateInfo(PsiElement element, PsiElement originalElement) {
        String name = builtinName(element, originalElement);
        GluaBuiltin builtin = name == null ? null : GluaBuiltinCatalog.getInstance().get(name);
        LOG.info("glua quick doc " + (builtin == null ? "miss" : "target=" + name));
        return builtin == null ? null : builtin.quickInfo();
    }

    private static @Nullable String builtinName(@Nullable PsiElement element, @Nullable PsiElement originalElement) {
        String direct = userDataTarget(element);
        if (direct != null) {
            return direct;
        }
        direct = userDataTarget(originalElement);
        if (direct != null) {
            return direct;
        }
        PsiElement target = originalElement == null ? element : originalElement;
        if (target == null || target.getContainingFile() == null || target.getContainingFile().getFileType() != GluaFileType.INSTANCE) {
            return null;
        }
        int offset = target.getTextRange() == null ? 0 : target.getTextRange().getStartOffset();
        return GluaAnalysis.builtinTargetAt(target.getContainingFile().getText(), offset);
    }

    private static @Nullable String userDataTarget(@Nullable PsiElement element) {
        return element == null ? null : element.getUserData(BUILTIN_TARGET);
    }
}

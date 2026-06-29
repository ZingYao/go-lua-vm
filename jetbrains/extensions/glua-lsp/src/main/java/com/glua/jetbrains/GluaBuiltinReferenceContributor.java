package com.glua.jetbrains;

import com.intellij.openapi.diagnostic.Logger;
import com.intellij.patterns.PlatformPatterns;
import com.intellij.psi.PsiElement;
import com.intellij.psi.PsiReference;
import com.intellij.psi.PsiReferenceContributor;
import com.intellij.psi.PsiReferenceProvider;
import com.intellij.psi.PsiReferenceRegistrar;
import com.intellij.util.ProcessingContext;
import org.jetbrains.annotations.NotNull;

public final class GluaBuiltinReferenceContributor extends PsiReferenceContributor {
    private static final Logger LOG = Logger.getInstance(GluaBuiltinReferenceContributor.class);

    @Override
    public void registerReferenceProviders(@NotNull PsiReferenceRegistrar registrar) {
        registrar.registerReferenceProvider(
            PlatformPatterns.psiElement().withLanguage(GluaLanguage.INSTANCE),
            new PsiReferenceProvider() {
                @Override
                public PsiReference @NotNull [] getReferencesByElement(@NotNull PsiElement element, @NotNull ProcessingContext context) {
                    if (element.getContainingFile() == null || element.getContainingFile().getFileType() != GluaFileType.INSTANCE) {
                        return PsiReference.EMPTY_ARRAY;
                    }
                    if (element.getTextRange() == null || element.getTextLength() == 0) {
                        return PsiReference.EMPTY_ARRAY;
                    }
                    String target = GluaAnalysis.builtinTargetAt(element.getContainingFile().getText(), element.getTextRange().getStartOffset());
                    if (target == null || GluaBuiltinCatalog.getInstance().get(target) == null) {
                        return PsiReference.EMPTY_ARRAY;
                    }
                    LOG.info("glua reference target=" + target + ", element=" + element.getText());
                    return new PsiReference[]{new GluaBuiltinReference(element, target)};
                }
            }
        );
    }
}

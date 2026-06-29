package com.glua.jetbrains;

import com.intellij.openapi.util.TextRange;
import com.intellij.psi.PsiElement;
import com.intellij.psi.PsiReferenceBase;
import org.jetbrains.annotations.NotNull;
import org.jetbrains.annotations.Nullable;

final class GluaBuiltinReference extends PsiReferenceBase<PsiElement> {
    private final String targetName;

    GluaBuiltinReference(@NotNull PsiElement element, @NotNull String targetName) {
        super(element, TextRange.from(0, element.getTextLength()));
        this.targetName = targetName;
    }

    @Override
    public @Nullable PsiElement resolve() {
        GluaBuiltin builtin = GluaBuiltinCatalog.getInstance().get(targetName);
        if (builtin == null) {
            return null;
        }
        return GluaBuiltinPsiFile.create(getElement().getProject(), targetName, builtin);
    }

    @Override
    public Object @NotNull [] getVariants() {
        return GluaBuiltinCatalog.getInstance().sortedNames().toArray();
    }
}

package com.glua.jetbrains;

import com.intellij.model.Pointer;
import com.intellij.platform.backend.documentation.DocumentationResult;
import com.intellij.platform.backend.documentation.DocumentationTarget;
import com.intellij.platform.backend.presentation.TargetPresentation;
import com.intellij.pom.Navigatable;
import com.intellij.psi.PsiElement;
import org.jetbrains.annotations.Nullable;

final class GluaDocumentationTarget implements DocumentationTarget {
    private final String name;
    private final GluaBuiltin builtin;
    private final PsiElement navigationElement;

    GluaDocumentationTarget(String name, GluaBuiltin builtin, PsiElement navigationElement) {
        this.name = name;
        this.builtin = builtin;
        this.navigationElement = navigationElement;
    }

    @Override
    public Pointer<? extends DocumentationTarget> createPointer() {
        return Pointer.hardPointer(this);
    }

    @Override
    public TargetPresentation computePresentation() {
        return TargetPresentation.builder(name).containerText("GLua builtin").presentation();
    }

    @Override
    public @Nullable Navigatable getNavigatable() {
        return navigationElement instanceof Navigatable navigatable ? navigatable : null;
    }

    @Override
    public String computeDocumentationHint() {
        return builtin.quickInfo();
    }

    @Override
    public DocumentationResult computeDocumentation() {
        return DocumentationResult.documentation(builtin.markdown(name));
    }
}

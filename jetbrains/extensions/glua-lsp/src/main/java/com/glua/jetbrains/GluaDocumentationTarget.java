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
    private final String documentation;
    private final String hint;
    private final String container;
    private final PsiElement navigationElement;

    GluaDocumentationTarget(String name, GluaBuiltin builtin, PsiElement navigationElement) {
        this.name = name;
        this.builtin = builtin;
        this.documentation = null;
        this.hint = null;
        this.container = "GLua builtin";
        this.navigationElement = navigationElement;
    }

    GluaDocumentationTarget(String name, String documentation, String hint, String container, PsiElement navigationElement) {
        this.name = name;
        this.builtin = null;
        this.documentation = documentation;
        this.hint = hint;
        this.container = container;
        this.navigationElement = navigationElement;
    }

    @Override
    public Pointer<? extends DocumentationTarget> createPointer() {
        return Pointer.hardPointer(this);
    }

    @Override
    public TargetPresentation computePresentation() {
        return TargetPresentation.builder(name).containerText(container).presentation();
    }

    @Override
    public @Nullable Navigatable getNavigatable() {
        return navigationElement instanceof Navigatable navigatable ? navigatable : null;
    }

    @Override
    public String computeDocumentationHint() {
        if (hint != null) {
            return hint;
        }
        return builtin.quickInfo();
    }

    @Override
    public DocumentationResult computeDocumentation() {
        if (documentation != null) {
            return DocumentationResult.documentation(documentation);
        }
        return DocumentationResult.documentation(builtin.markdown(name));
    }
}

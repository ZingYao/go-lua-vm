package com.glua.jetbrains;

import com.intellij.codeInsight.navigation.NavigationUtil;
import com.intellij.navigation.ItemPresentation;
import com.intellij.openapi.util.NlsSafe;
import com.intellij.psi.PsiElement;
import com.intellij.psi.PsiFile;
import com.intellij.psi.impl.FakePsiElement;
import org.jetbrains.annotations.Nullable;

import javax.swing.Icon;

final class GluaCallerNavigationElement extends FakePsiElement {
    private final PsiElement target;
    private final String name;
    private final String location;

    GluaCallerNavigationElement(PsiElement target, String name, String location) {
        this.target = target;
        this.name = name;
        this.location = location;
    }

    @Override
    public PsiElement getParent() {
        return target.getParent();
    }

    @Override
    public PsiFile getContainingFile() {
        return target.getContainingFile();
    }

    @Override
    public @NlsSafe String getName() {
        return name;
    }

    @Override
    public void navigate(boolean requestFocus) {
        NavigationUtil.activateFileWithPsiElement(target, requestFocus);
    }

    @Override
    public boolean canNavigate() {
        return true;
    }

    @Override
    public boolean canNavigateToSource() {
        return true;
    }

    @Override
    public ItemPresentation getPresentation() {
        return new ItemPresentation() {
            @Override
            public @Nullable String getPresentableText() {
                return name;
            }

            @Override
            public @Nullable String getLocationString() {
                return location;
            }

            @Override
            public @Nullable Icon getIcon(boolean unused) {
                return target.getIcon(0);
            }
        };
    }
}

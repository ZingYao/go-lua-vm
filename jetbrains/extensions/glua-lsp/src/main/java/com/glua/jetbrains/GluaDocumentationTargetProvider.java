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
        GluaRequireSupport.MemberDefinition localMember = GluaRequireSupport.memberDefinitionAt(file.getText(), offset);
        if (localMember != null) {
            GluaUserDocumentation.Entry documentation = GluaUserDocumentation.documentationAt(file.getText(), localMember.start(), localMember.end(), localMember.name());
            PsiElement navigation = file.findElementAt(localMember.start());
            return List.of(new GluaDocumentationTarget(
                documentation.name(),
                documentation.html(),
                documentation.quickInfo(),
                "GLua function",
                navigation == null ? file : navigation
            ));
        }
        GluaRequireSupport.MemberDefinition localFunction = GluaRequireSupport.functionDefinitionAt(file.getText(), offset);
        if (localFunction != null) {
            GluaUserDocumentation.Entry documentation = GluaUserDocumentation.documentationAt(file.getText(), localFunction.start(), localFunction.end(), localFunction.name());
            PsiElement navigation = file.findElementAt(localFunction.start());
            return List.of(new GluaDocumentationTarget(
                documentation.name(),
                documentation.html(),
                documentation.quickInfo(),
                "GLua function",
                navigation == null ? file : navigation
            ));
        }
        GluaRequireSupport.Target requiredMember = GluaRequireSupport.requiredMemberAt(file, offset);
        if (requiredMember != null) {
            GluaUserDocumentation.Entry documentation = GluaUserDocumentation.documentationAt(requiredMember.file().getText(), requiredMember.start(), requiredMember.end(), requiredMember.name());
            return List.of(new GluaDocumentationTarget(
                documentation.name(),
                documentation.html(),
                documentation.quickInfo(),
                "GLua function",
                requiredMember.element()
            ));
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

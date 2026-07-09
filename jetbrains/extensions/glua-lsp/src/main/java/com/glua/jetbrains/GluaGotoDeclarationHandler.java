package com.glua.jetbrains;

import com.intellij.codeInsight.navigation.actions.GotoDeclarationHandler;
import com.intellij.codeInsight.hint.HintManager;
import com.intellij.openapi.application.ApplicationManager;
import com.intellij.openapi.editor.Editor;
import com.intellij.openapi.diagnostic.Logger;
import com.intellij.openapi.project.Project;
import com.intellij.psi.PsiElement;
import org.jetbrains.annotations.Nullable;

import java.nio.file.Path;
import java.util.List;

public final class GluaGotoDeclarationHandler implements GotoDeclarationHandler {
    private static final Logger LOG = Logger.getInstance(GluaGotoDeclarationHandler.class);

    @Override
    public PsiElement @Nullable [] getGotoDeclarationTargets(@Nullable PsiElement sourceElement, int offset, Editor editor) {
        if (sourceElement == null || sourceElement.getContainingFile().getFileType() != GluaFileType.INSTANCE) {
            return null;
        }
        GluaRequireSupport.Target requiredModule = GluaRequireSupport.requiredModuleAt(sourceElement.getContainingFile(), offset);
        if (requiredModule != null) {
            LOG.info("glua goto required module target=" + requiredModule.path() + ", offset=" + offset);
            return new PsiElement[]{requiredModule.element()};
        }
        GluaRequireSupport.Target requiredMember = GluaRequireSupport.requiredMemberAt(sourceElement.getContainingFile(), offset);
        if (requiredMember != null) {
            LOG.info("glua goto required member target=" + requiredMember.path() + ", offset=" + offset);
            return new PsiElement[]{requiredMember.element()};
        }
        List<GluaRequireSupport.Target> memberCallers = GluaRequireSupport.requiredMemberCallersAt(sourceElement.getContainingFile(), offset);
        if (memberCallers != null) {
            if (memberCallers.isEmpty()) {
                ApplicationManager.getApplication().invokeLater(() ->
                    HintManager.getInstance().showInformationHint(editor, "没有找到调用方")
                );
                return PsiElement.EMPTY_ARRAY;
            }
            LOG.info("glua goto member callers count=" + memberCallers.size() + ", offset=" + offset);
            return memberCallers.stream()
                .map(target -> new GluaCallerNavigationElement(target.element(), target.name(), callerLocation(sourceElement.getProject(), target)))
                .toArray(PsiElement[]::new);
        }
        GluaRequireSupport.MemberDefinition localMember = GluaRequireSupport.localMemberReferenceDefinitionAt(sourceElement.getContainingFile().getText(), offset);
        if (localMember != null) {
            PsiElement element = sourceElement.getContainingFile().findElementAt(localMember.start());
            if (element != null) {
                LOG.info("glua goto local member target=" + localMember.name() + ", offset=" + offset);
                return new PsiElement[]{element};
            }
        }
        String target = GluaAnalysis.builtinTargetAt(editor.getDocument(), offset);
        if (target != null && GluaBuiltinCatalog.getInstance().get(target) != null) {
            LOG.info("glua goto builtin target=" + target + ", offset=" + offset);
            Project project = sourceElement.getProject();
            PsiElement element = GluaBuiltinPsiFile.create(project, target, GluaBuiltinCatalog.getInstance().get(target));
            return new PsiElement[]{element};
        }
        LOG.info("glua goto builtin miss offset=" + offset + ", element=" + sourceElement.getText() + ", context=" + context(editor, offset));
        String text = sourceElement.getText();
        GluaAnalysis.TextDefinition definition = GluaAnalysis.localDefinition(editor.getDocument(), text, offset);
        if (definition == null) {
            return null;
        }
        PsiElement element = sourceElement.getContainingFile().findElementAt(definition.start());
        return element == null ? null : new PsiElement[]{element};
    }

    private static String context(Editor editor, int offset) {
        CharSequence source = editor.getDocument().getCharsSequence();
        int start = Math.max(0, offset - 24);
        int end = Math.min(source.length(), offset + 24);
        return source.subSequence(start, end).toString().replace('\n', ' ');
    }

    private static String callerLocation(Project project, GluaRequireSupport.Target target) {
        Path path = target.path();
        String basePath = project.getBasePath();
        String label = path.getFileName() == null ? path.toString() : path.getFileName().toString();
        if (basePath != null) {
            Path base = Path.of(basePath).normalize();
            Path normalized = path.normalize();
            if (normalized.startsWith(base)) {
                label = base.relativize(normalized).toString();
            }
        }
        return label + ":" + oneBasedLine(target.file().getText(), target.start());
    }

    private static int oneBasedLine(CharSequence text, int offset) {
        int safeOffset = Math.max(0, Math.min(offset, text.length()));
        int line = 1;
        for (int i = 0; i < safeOffset; i++) {
            if (text.charAt(i) == '\n') {
                line++;
            }
        }
        return line;
    }
}

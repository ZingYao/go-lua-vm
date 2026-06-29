package com.glua.jetbrains;

import com.intellij.openapi.actionSystem.AnAction;
import com.intellij.openapi.actionSystem.AnActionEvent;
import com.intellij.openapi.actionSystem.CommonDataKeys;
import com.intellij.openapi.command.WriteCommandAction;
import com.intellij.openapi.editor.Document;
import com.intellij.openapi.project.Project;
import com.intellij.psi.PsiFile;
import org.jetbrains.annotations.NotNull;

public final class GluaFormatAction extends AnAction {
    @Override
    public void update(@NotNull AnActionEvent event) {
        PsiFile file = event.getData(CommonDataKeys.PSI_FILE);
        event.getPresentation().setEnabledAndVisible(file != null && file.getFileType() == GluaFileType.INSTANCE);
    }

    @Override
    public void actionPerformed(@NotNull AnActionEvent event) {
        Project project = event.getProject();
        Document document = event.getData(CommonDataKeys.EDITOR) == null ? null : event.getData(CommonDataKeys.EDITOR).getDocument();
        PsiFile file = event.getData(CommonDataKeys.PSI_FILE);
        if (project == null || document == null || file == null || file.getFileType() != GluaFileType.INSTANCE) {
            return;
        }
        String formatted = GluaFormatter.format(document.getText());
        WriteCommandAction.runWriteCommandAction(project, "Format GLua File", null, () ->
            document.replaceString(0, document.getTextLength(), formatted)
        );
    }
}

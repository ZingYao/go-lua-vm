package com.glua.jetbrains;

import com.intellij.lang.annotation.AnnotationHolder;
import com.intellij.lang.annotation.Annotator;
import com.intellij.lang.annotation.HighlightSeverity;
import com.intellij.openapi.util.TextRange;
import com.intellij.psi.PsiElement;
import com.intellij.psi.PsiFile;
import org.jetbrains.annotations.NotNull;

public final class GluaAnnotator implements Annotator {
    @Override
    public void annotate(@NotNull PsiElement element, @NotNull AnnotationHolder holder) {
        if (!(element instanceof PsiFile file) || file.getFileType() != GluaFileType.INSTANCE) {
            return;
        }
        GluaAnalysis.collectDiagnostics(file.getText(), (start, end, message) ->
            holder.newAnnotation(HighlightSeverity.ERROR, message)
                .range(TextRange.create(start, Math.max(start + 1, end)))
                .create()
        );
    }
}

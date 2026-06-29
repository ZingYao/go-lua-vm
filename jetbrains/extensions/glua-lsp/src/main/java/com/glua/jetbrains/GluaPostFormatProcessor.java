package com.glua.jetbrains;

import com.intellij.openapi.editor.Document;
import com.intellij.openapi.util.TextRange;
import com.intellij.psi.PsiDocumentManager;
import com.intellij.psi.PsiElement;
import com.intellij.psi.PsiFile;
import com.intellij.psi.codeStyle.CodeStyleSettings;
import com.intellij.psi.impl.source.codeStyle.PostFormatProcessor;
import org.jetbrains.annotations.NotNull;

public final class GluaPostFormatProcessor implements PostFormatProcessor {
    @Override
    public @NotNull PsiElement processElement(@NotNull PsiElement source, @NotNull CodeStyleSettings settings) {
        PsiFile file = source instanceof PsiFile ? (PsiFile) source : source.getContainingFile();
        if (file != null) {
            processText(file, file.getTextRange(), settings);
        }
        return source;
    }

    @Override
    public @NotNull TextRange processText(@NotNull PsiFile source, @NotNull TextRange rangeToReformat, @NotNull CodeStyleSettings settings) {
        if (source.getFileType() != GluaFileType.INSTANCE) {
            return rangeToReformat;
        }
        Document document = PsiDocumentManager.getInstance(source.getProject()).getDocument(source);
        if (document == null) {
            return rangeToReformat;
        }
        String formatted = GluaFormatter.format(document.getText());
        if (!formatted.equals(document.getText())) {
            document.replaceString(0, document.getTextLength(), formatted);
            PsiDocumentManager.getInstance(source.getProject()).commitDocument(document);
        }
        return TextRange.create(0, formatted.length());
    }
}

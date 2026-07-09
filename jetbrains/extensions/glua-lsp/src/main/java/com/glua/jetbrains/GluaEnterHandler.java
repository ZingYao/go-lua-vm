package com.glua.jetbrains;

import com.intellij.codeInsight.editorActions.enter.EnterHandlerDelegateAdapter;
import com.intellij.openapi.actionSystem.DataContext;
import com.intellij.openapi.editor.Document;
import com.intellij.openapi.editor.Editor;
import com.intellij.openapi.editor.actionSystem.EditorActionHandler;
import com.intellij.openapi.util.Ref;
import com.intellij.psi.PsiFile;
import org.jetbrains.annotations.NotNull;

public final class GluaEnterHandler extends EnterHandlerDelegateAdapter {
    @Override
    public @NotNull Result preprocessEnter(@NotNull PsiFile file,
                                           @NotNull Editor editor,
                                           @NotNull Ref<Integer> caretOffsetRef,
                                           @NotNull Ref<Integer> caretAdvance,
                                           @NotNull DataContext dataContext,
                                           EditorActionHandler originalHandler) {
        if (file.getFileType() != GluaFileType.INSTANCE) {
            return Result.Continue;
        }
        Document document = editor.getDocument();
        int offset = caretOffsetRef.get();
        GluaBlockEnterSupport.Expansion expansion = GluaBlockEnterSupport.expansion(document.getCharsSequence(), offset);
        if (expansion == null) {
            return Result.Continue;
        }
        document.insertString(offset, expansion.text());
        int caretOffset = offset + expansion.caretDelta();
        editor.getCaretModel().moveToOffset(caretOffset);
        caretOffsetRef.set(caretOffset);
        caretAdvance.set(0);
        return Result.Stop;
    }
}

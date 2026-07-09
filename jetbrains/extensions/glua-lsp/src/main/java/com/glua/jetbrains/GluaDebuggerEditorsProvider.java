package com.glua.jetbrains;

import com.intellij.openapi.editor.Document;
import com.intellij.openapi.editor.EditorFactory;
import com.intellij.openapi.fileTypes.FileType;
import com.intellij.openapi.project.Project;
import com.intellij.xdebugger.XSourcePosition;
import com.intellij.xdebugger.evaluation.EvaluationMode;
import com.intellij.xdebugger.XExpression;
import com.intellij.xdebugger.evaluation.XDebuggerEditorsProvider;
import org.jetbrains.annotations.NotNull;
import org.jetbrains.annotations.Nullable;

public final class GluaDebuggerEditorsProvider extends XDebuggerEditorsProvider {
    @Override
    public @NotNull FileType getFileType() {
        return GluaFileType.INSTANCE;
    }

    @Override
    public @NotNull Document createDocument(@NotNull Project project,
                                            @NotNull XExpression expression,
                                            @Nullable XSourcePosition sourcePosition,
                                            @NotNull EvaluationMode mode) {
        return EditorFactory.getInstance().createDocument(expression.getExpression());
    }

    @Override
    public @NotNull Document createDocument(@NotNull Project project,
                                            @NotNull String text,
                                            @Nullable XSourcePosition sourcePosition,
                                            @NotNull EvaluationMode mode) {
        return EditorFactory.getInstance().createDocument(text);
    }

    @Override
    public boolean isEvaluateExpressionFieldEnabled() {
        return false;
    }
}

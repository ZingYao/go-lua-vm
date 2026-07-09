package com.glua.jetbrains;

import com.intellij.openapi.editor.Document;
import com.intellij.openapi.application.ApplicationManager;
import com.intellij.openapi.project.Project;
import com.intellij.openapi.util.TextRange;
import com.intellij.openapi.vfs.LocalFileSystem;
import com.intellij.openapi.vfs.VirtualFile;
import com.intellij.xdebugger.XDebuggerUtil;
import com.intellij.xdebugger.XSourcePosition;
import com.intellij.xdebugger.evaluation.XDebuggerEvaluator;
import com.intellij.xdebugger.frame.XCompositeNode;
import com.intellij.xdebugger.frame.XExecutionStack;
import com.intellij.xdebugger.frame.XNamedValue;
import com.intellij.xdebugger.frame.XStackFrame;
import com.intellij.xdebugger.frame.XSuspendContext;
import com.intellij.xdebugger.frame.XValueChildrenList;
import com.intellij.xdebugger.frame.XValueModifier;
import com.intellij.xdebugger.frame.XValueNode;
import com.intellij.xdebugger.frame.XValuePlace;
import org.jetbrains.annotations.NotNull;
import org.jetbrains.annotations.Nullable;

import java.nio.file.Path;
import java.util.ArrayList;
import java.util.List;

public final class GluaSuspendContext extends XSuspendContext {
    private final GluaExecutionStack executionStack;

    public GluaSuspendContext(@NotNull Project project,
                              @NotNull GluaDapStackFrame frame,
                              @Nullable GluaDapClient dapHandler) {
        this.executionStack = new GluaExecutionStack(project, frame, dapHandler);
    }

    @Override
    public @NotNull XExecutionStack getActiveExecutionStack() {
        return executionStack;
    }

    @Override
    public XExecutionStack @NotNull [] getExecutionStacks() {
        return new XExecutionStack[]{executionStack};
    }

    private static final class GluaExecutionStack extends XExecutionStack {
        private final GluaStackFrame topFrame;

        private GluaExecutionStack(@NotNull Project project,
                                   @NotNull GluaDapStackFrame frame,
                                   @Nullable GluaDapClient dapHandler) {
            super("GLua main");
            this.topFrame = new GluaStackFrame(project, frame, dapHandler);
        }

        @Override
        public @NotNull XStackFrame getTopFrame() {
            return topFrame;
        }

        @Override
        public void computeStackFrames(int firstFrameIndex, @NotNull XStackFrameContainer container) {
            if (firstFrameIndex == 0) {
                container.addStackFrames(java.util.List.of(topFrame), true);
                return;
            }
            container.addStackFrames(java.util.List.of(), true);
        }
    }

    private static final class GluaStackFrame extends XStackFrame {
        private final GluaDapStackFrame frame;
        private final GluaDapClient dapHandler;
        private final XSourcePosition position;
        private final XDebuggerEvaluator evaluator;

        private GluaStackFrame(@NotNull Project project,
                               @NotNull GluaDapStackFrame frame,
                               @Nullable GluaDapClient dapHandler) {
            this.frame = frame;
            this.dapHandler = dapHandler;
            this.position = sourcePosition(project, frame);
            this.evaluator = new GluaVariableEvaluator(dapHandler);
        }

        @Override
        public @Nullable XSourcePosition getSourcePosition() {
            return position;
        }

        @Override
        public @Nullable XDebuggerEvaluator getEvaluator() {
            return evaluator;
        }

        @Override
        public void computeChildren(@NotNull XCompositeNode node) {
            List<GluaDapVariable> variables = dapHandler == null
                ? List.of()
                : dapHandler.currentVariables();
            if (variables.isEmpty()) {
                node.addChildren(XValueChildrenList.EMPTY, true);
                return;
            }
            XValueChildrenList children = new XValueChildrenList(variables.size());
            for (GluaDapVariable variable : variables) {
                children.add(new GluaVariableValue(variable, 1, dapHandler));
            }
            node.addChildren(children, true);
        }

        private static @Nullable XSourcePosition sourcePosition(@NotNull Project project, @NotNull GluaDapStackFrame frame) {
            if (frame.source().isBlank() || frame.line() <= 0) {
                return null;
            }
            for (Path path : sourcePathCandidates(project, frame.source())) {
                VirtualFile file = LocalFileSystem.getInstance().refreshAndFindFileByNioFile(path);
                if (file == null) {
                    file = LocalFileSystem.getInstance().findFileByNioFile(path);
                }
                if (file != null) {
                    return XDebuggerUtil.getInstance().createPosition(file, frame.line() - 1);
                }
            }
            return null;
        }

        private static @NotNull List<Path> sourcePathCandidates(@NotNull Project project, @NotNull String source) {
            List<Path> paths = new ArrayList<>();
            try {
                Path path = Path.of(source);
                if (!path.isAbsolute() && project.getBasePath() != null) {
                    // DAP 可能返回相对路径；按项目根目录解析后才能让 IDE 自动跳转到其它文件。
                    path = Path.of(project.getBasePath()).resolve(path).normalize();
                }
                paths.add(path);
                String normalized = path.toString();
                if (normalized.endsWith(".lua")) {
                    paths.add(Path.of(normalized.substring(0, normalized.length() - ".lua".length()) + ".glua"));
                } else if (normalized.endsWith(".glua")) {
                    paths.add(Path.of(normalized.substring(0, normalized.length() - ".glua".length()) + ".lua"));
                }
            } catch (RuntimeException ignored) {
                // 远程或非本地路径无法转成本机 Path 时返回空候选。
            }
            return paths;
        }
    }

    private static final class GluaVariableValue extends XNamedValue {
        private final GluaDapVariable variable;
        private final int parentVariablesReference;
        private final GluaDapClient dapHandler;

        private GluaVariableValue(@NotNull GluaDapVariable variable,
                                  int parentVariablesReference,
                                  @Nullable GluaDapClient dapHandler) {
            super(variable.name());
            this.variable = variable;
            this.parentVariablesReference = parentVariablesReference;
            this.dapHandler = dapHandler;
        }

        @Override
        public void computePresentation(@NotNull XValueNode node, @NotNull XValuePlace place) {
            String type = variable.type().isBlank() ? null : variable.type();
            node.setPresentation(null, type, variable.value(), variable.variablesReference() > 0);
        }

        @Override
        public void computeChildren(@NotNull XCompositeNode node) {
            if (dapHandler == null || variable.variablesReference() <= 0) {
                node.addChildren(XValueChildrenList.EMPTY, true);
                return;
            }
            dapHandler.requestVariables(variable.variablesReference(), variables -> {
                if (variables.isEmpty()) {
                    node.addChildren(XValueChildrenList.EMPTY, true);
                    return;
                }
                XValueChildrenList children = new XValueChildrenList(variables.size());
                for (GluaDapVariable child : variables) {
                    children.add(new GluaVariableValue(child, variable.variablesReference(), dapHandler));
                }
                node.addChildren(children, true);
            });
        }

        @Override
        public @Nullable XValueModifier getModifier() {
            if (dapHandler == null) {
                return null;
            }
            return new XValueModifier() {
                @Override
                public void setValue(@NotNull String expression, @NotNull XModificationCallback callback) {
                    dapHandler.setVariable(parentVariablesReference, variable.name(), expression, result -> {
                        ApplicationManager.getApplication().invokeLater(() -> {
                            if (result.success()) {
                                callback.valueModified();
                                return;
                            }
                            callback.errorOccurred(result.error());
                        });
                    });
                }
            };
        }
    }

    private static final class GluaVariableEvaluator extends XDebuggerEvaluator {
        private final GluaDapClient dapHandler;

        private GluaVariableEvaluator(@Nullable GluaDapClient dapHandler) {
            this.dapHandler = dapHandler;
        }

        @Override
        public void evaluate(@NotNull String expression,
                             @NotNull XEvaluationCallback callback,
                             @Nullable XSourcePosition expressionPosition) {
            String name = expression.trim();
            if (name.isEmpty() || dapHandler == null) {
                callback.invalidExpression(GluaUiText.text("No GLua variable is selected.", "未选择 GLua 变量。"));
                return;
            }
            for (GluaDapVariable variable : dapHandler.currentVariables()) {
                if (variable.name().equals(name)) {
                    callback.evaluated(new GluaVariableValue(variable, 1, dapHandler));
                    return;
                }
            }
            callback.invalidExpression(GluaUiText.text("GLua variable is not available in the current frame.", "当前栈帧中没有该 GLua 变量。"));
        }

        @Override
        public @Nullable TextRange getExpressionRangeAtOffset(@NotNull Project project,
                                                              @NotNull Document document,
                                                              int offset,
                                                              boolean sideEffectsAllowed) {
            CharSequence text = document.getCharsSequence();
            if (text.isEmpty()) {
                return null;
            }
            int safeOffset = Math.max(0, Math.min(offset, text.length() - 1));
            if (!isIdentifierChar(text.charAt(safeOffset)) && safeOffset > 0 && isIdentifierChar(text.charAt(safeOffset - 1))) {
                safeOffset--;
            }
            if (!isIdentifierChar(text.charAt(safeOffset))) {
                return null;
            }
            int start = safeOffset;
            while (start > 0 && isIdentifierChar(text.charAt(start - 1))) {
                start--;
            }
            int end = safeOffset + 1;
            while (end < text.length() && isIdentifierChar(text.charAt(end))) {
                end++;
            }
            return start < end ? TextRange.create(start, end) : null;
        }

        private static boolean isIdentifierChar(char ch) {
            return Character.isLetterOrDigit(ch) || ch == '_';
        }
    }
}

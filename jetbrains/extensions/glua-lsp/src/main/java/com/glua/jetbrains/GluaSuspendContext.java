package com.glua.jetbrains;

import com.intellij.openapi.vfs.LocalFileSystem;
import com.intellij.openapi.vfs.VirtualFile;
import com.intellij.xdebugger.XDebuggerUtil;
import com.intellij.xdebugger.XSourcePosition;
import com.intellij.xdebugger.frame.XCompositeNode;
import com.intellij.xdebugger.frame.XExecutionStack;
import com.intellij.xdebugger.frame.XNamedValue;
import com.intellij.xdebugger.frame.XStackFrame;
import com.intellij.xdebugger.frame.XSuspendContext;
import com.intellij.xdebugger.frame.XValueChildrenList;
import com.intellij.xdebugger.frame.XValueNode;
import com.intellij.xdebugger.frame.XValuePlace;
import org.jetbrains.annotations.NotNull;
import org.jetbrains.annotations.Nullable;

import java.nio.file.Path;
import java.util.List;

public final class GluaSuspendContext extends XSuspendContext {
    private final GluaExecutionStack executionStack;

    public GluaSuspendContext(@NotNull GluaDapStackFrame frame,
                              @Nullable GluaDapClient dapHandler) {
        this.executionStack = new GluaExecutionStack(frame, dapHandler);
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

        private GluaExecutionStack(@NotNull GluaDapStackFrame frame,
                                   @Nullable GluaDapClient dapHandler) {
            super("GLua main");
            this.topFrame = new GluaStackFrame(frame, dapHandler);
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

        private GluaStackFrame(@NotNull GluaDapStackFrame frame,
                               @Nullable GluaDapClient dapHandler) {
            this.frame = frame;
            this.dapHandler = dapHandler;
            this.position = sourcePosition(frame);
        }

        @Override
        public @Nullable XSourcePosition getSourcePosition() {
            return position;
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
                children.add(new GluaVariableValue(variable));
            }
            node.addChildren(children, true);
        }

        private static @Nullable XSourcePosition sourcePosition(@NotNull GluaDapStackFrame frame) {
            if (frame.source().isBlank() || frame.line() <= 0) {
                return null;
            }
            VirtualFile file = LocalFileSystem.getInstance().findFileByNioFile(Path.of(frame.source()));
            if (file == null) {
                return null;
            }
            return XDebuggerUtil.getInstance().createPosition(file, frame.line() - 1);
        }
    }

    private static final class GluaVariableValue extends XNamedValue {
        private final GluaDapVariable variable;

        private GluaVariableValue(@NotNull GluaDapVariable variable) {
            super(variable.name());
            this.variable = variable;
        }

        @Override
        public void computePresentation(@NotNull XValueNode node, @NotNull XValuePlace place) {
            String type = variable.type().isBlank() ? null : variable.type();
            node.setPresentation(null, type, variable.value(), false);
        }
    }
}

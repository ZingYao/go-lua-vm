package com.glua.jetbrains;

import com.intellij.execution.process.ProcessHandler;
import com.intellij.execution.filters.TextConsoleBuilderFactory;
import com.intellij.execution.ui.ConsoleView;
import com.intellij.execution.ui.ExecutionConsole;
import com.intellij.xdebugger.XDebugProcess;
import com.intellij.xdebugger.XDebugSession;
import com.intellij.xdebugger.breakpoints.XBreakpointHandler;
import com.intellij.xdebugger.breakpoints.XLineBreakpoint;
import com.intellij.xdebugger.evaluation.XDebuggerEditorsProvider;
import com.intellij.xdebugger.frame.XSuspendContext;
import org.jetbrains.annotations.NotNull;

public final class GluaDebugProcess extends XDebugProcess {
    private final ProcessHandler processHandler;
    private final GluaDapClient dapHandler;
    private final XDebuggerEditorsProvider editorsProvider = new GluaDebuggerEditorsProvider();

    public GluaDebugProcess(@NotNull XDebugSession session, @NotNull GluaDapRemoteProcessHandler processHandler) {
        super(session);
        this.dapHandler = processHandler;
        this.processHandler = processHandler;
        processHandler.setDebugProcess(this);
    }

    public GluaDebugProcess(@NotNull XDebugSession session, @NotNull GluaDapLaunchProcessHandler processHandler) {
        super(session);
        this.dapHandler = processHandler;
        this.processHandler = processHandler;
        processHandler.setDebugProcess(this);
    }

    @Override
    public @NotNull XDebuggerEditorsProvider getEditorsProvider() {
        return editorsProvider;
    }

    @Override
    public XBreakpointHandler<?> @NotNull [] getBreakpointHandlers() {
        if (dapHandler == null) {
            return XBreakpointHandler.EMPTY_ARRAY;
        }
        return new XBreakpointHandler<?>[]{new GluaBreakpointHandler(dapHandler)};
    }

    @Override
    protected @NotNull ProcessHandler doGetProcessHandler() {
        return processHandler;
    }

    @Override
    public @NotNull ExecutionConsole createConsole() {
        ConsoleView console = TextConsoleBuilderFactory.getInstance()
            .createBuilder(getSession().getProject())
            .getConsole();
        console.attachToProcess(processHandler);
        return console;
    }

    @Override
    public void stop() {
        processHandler.destroyProcess();
    }

    @Override
    public void resume() {
        sendDebugCommand("continue");
    }

    @Override
    public void resume(@NotNull XSuspendContext context) {
        resume();
    }

    @Override
    public void startStepOver() {
        sendDebugCommand("next");
    }

    @Override
    public void startStepOver(@NotNull XSuspendContext context) {
        startStepOver();
    }

    @Override
    public void startStepInto() {
        sendDebugCommand("stepIn");
    }

    @Override
    public void startStepInto(@NotNull XSuspendContext context) {
        startStepInto();
    }

    @Override
    public void startStepOut() {
        sendDebugCommand("stepOut");
    }

    @Override
    public void startStepOut(@NotNull XSuspendContext context) {
        startStepOut();
    }

    @Override
    public void startPausing() {
        sendDebugCommand("pause");
    }

    void onStopped(@NotNull GluaDapStackFrame frame) {
        getSession().positionReached(new GluaSuspendContext(frame, dapHandler));
    }

    void refreshVariables() {
        getSession().rebuildViews();
    }

    private void sendDebugCommand(@NotNull String command) {
        if (dapHandler != null) {
            dapHandler.sendControlCommand(command);
        }
    }

    private static final class GluaBreakpointHandler extends XBreakpointHandler<XLineBreakpoint<GluaBreakpointProperties>> {
        private final GluaDapClient dapHandler;

        private GluaBreakpointHandler(@NotNull GluaDapClient dapHandler) {
            super(GluaLineBreakpointType.class);
            this.dapHandler = dapHandler;
        }

        @Override
        public void registerBreakpoint(@NotNull XLineBreakpoint<GluaBreakpointProperties> breakpoint) {
            dapHandler.syncBreakpointsAsync();
        }

        @Override
        public void unregisterBreakpoint(@NotNull XLineBreakpoint<GluaBreakpointProperties> breakpoint, boolean temporary) {
            dapHandler.syncBreakpointsAsync();
        }
    }
}

package com.glua.jetbrains;

import com.intellij.execution.process.ProcessHandler;
import com.intellij.execution.filters.TextConsoleBuilderFactory;
import com.intellij.execution.ui.ConsoleView;
import com.intellij.execution.ui.ExecutionConsole;
import com.intellij.xdebugger.XDebugProcess;
import com.intellij.xdebugger.XDebugSession;
import com.intellij.xdebugger.breakpoints.XBreakpointHandler;
import com.intellij.xdebugger.evaluation.XDebuggerEditorsProvider;
import org.jetbrains.annotations.NotNull;

import java.util.concurrent.atomic.AtomicBoolean;

public final class GluaDebugProcess extends XDebugProcess {
    private final GluaDapAttachProcessHandler processHandler;
    private final XDebuggerEditorsProvider editorsProvider = new GluaDebuggerEditorsProvider();
    private final AtomicBoolean started = new AtomicBoolean(false);

    public GluaDebugProcess(@NotNull XDebugSession session, @NotNull String host, int port) {
        super(session);
        this.processHandler = new GluaDapAttachProcessHandler(host, port, message -> getSession().reportError(message));
    }

    @Override
    public void sessionInitialized() {
        super.sessionInitialized();
        if (started.compareAndSet(false, true)) {
            processHandler.startNotify();
        }
    }

    @Override
    public @NotNull XDebuggerEditorsProvider getEditorsProvider() {
        return editorsProvider;
    }

    @Override
    public XBreakpointHandler<?> @NotNull [] getBreakpointHandlers() {
        return XBreakpointHandler.EMPTY_ARRAY;
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
}

package com.glua.jetbrains;

import com.intellij.execution.ExecutionException;
import com.intellij.execution.configurations.GeneralCommandLine;
import com.intellij.execution.process.OSProcessHandler;
import com.intellij.execution.process.ProcessAdapter;
import com.intellij.execution.process.ProcessEvent;
import com.intellij.execution.process.ProcessOutputType;
import com.intellij.execution.process.ProcessOutputTypes;
import com.intellij.openapi.util.Key;
import com.intellij.openapi.vfs.LocalFileSystem;
import com.intellij.openapi.vfs.VirtualFile;
import org.jetbrains.annotations.NotNull;

import java.nio.file.Path;
import java.time.Duration;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.TimeUnit;

public final class GluaDapLaunchProcessHandler extends OSProcessHandler {
    static final String READY_PREFIX = "GLua DAP server listening on ";
    private final String commandText;
    private final String workDirectory;
    private final String listenAddress;
    private final CountDownLatch readyOrExit = new CountDownLatch(1);
    private final StringBuilder stdoutTail = new StringBuilder();
    private final StringBuilder stderrTail = new StringBuilder();
    private volatile ReadyTarget readyTarget;
    private volatile Integer exitCode;

    private GluaDapLaunchProcessHandler(@NotNull GeneralCommandLine commandLine,
                                        @NotNull String listenAddress) throws ExecutionException {
        super(commandLine);
        this.commandText = commandLine.getCommandLineString();
        this.workDirectory = commandLine.getWorkDirectory() == null ? "" : commandLine.getWorkDirectory().getAbsolutePath();
        this.listenAddress = listenAddress;
        addProcessListener(new ProcessAdapter() {
            @Override
            public void onTextAvailable(@NotNull ProcessEvent event, @NotNull Key outputType) {
                captureOutput(event.getText(), outputType);
            }

            @Override
            public void processTerminated(@NotNull ProcessEvent event) {
                exitCode = event.getExitCode();
                readyOrExit.countDown();
            }
        });
    }

    public static @NotNull GluaDapLaunchProcessHandler create(@NotNull String gluaExecutable,
                                                              @NotNull String program) throws ExecutionException {
        String executable = gluaExecutable.isBlank() ? "glua" : gluaExecutable;
        String listen = GluaDapRunConfiguration.INTERNAL_DAP_HOST + ":0";
        GeneralCommandLine commandLine = new GeneralCommandLine(executable, "--glua-dap-listen=" + listen, program);
        VirtualFile file = LocalFileSystem.getInstance().findFileByNioFile(Path.of(program));
        if (file != null && file.getParent() != null) {
            commandLine.withWorkDirectory(file.getParent().getPath());
        }
        return new GluaDapLaunchProcessHandler(commandLine, listen);
    }

    public @NotNull ReadyTarget awaitReady(@NotNull Duration timeout) throws ExecutionException {
        ReadyTarget current = readyTarget;
        if (current != null) {
            return current;
        }
        try {
            if (!readyOrExit.await(timeout.toMillis(), TimeUnit.MILLISECONDS)) {
                throw new ExecutionException(failureMessage("timeout waiting for GLua DAP ready marker"));
            }
        } catch (InterruptedException error) {
            Thread.currentThread().interrupt();
            throw new ExecutionException(failureMessage("interrupted while waiting for GLua DAP ready marker"), error);
        }
        current = readyTarget;
        if (current != null) {
            return current;
        }
        throw new ExecutionException(failureMessage("glua exited before GLua DAP ready marker"));
    }

    public @NotNull String failureMessage(@NotNull String reason) {
        StringBuilder builder = new StringBuilder();
        builder.append("GLua Debug launch failed: ").append(reason)
            .append(" | command=").append(commandText)
            .append(" | cwd=").append(workDirectory)
            .append(" | listen=").append(listenAddress);
        if (exitCode != null) {
            builder.append(" | exit=").append(exitCode);
        }
        String stderr = stderrTail.toString().trim();
        if (!stderr.isBlank()) {
            builder.append(" | stderr=").append(stderr);
        }
        String stdout = stdoutTail.toString().trim();
        if (!stdout.isBlank()) {
            builder.append(" | stdout=").append(stdout);
        }
        return builder.toString();
    }

    private void captureOutput(@NotNull String text, @NotNull Key outputType) {
        if (ProcessOutputType.isStderr(outputType) || outputType == ProcessOutputTypes.STDERR) {
            appendTail(stderrTail, text);
        } else {
            appendTail(stdoutTail, text);
        }
        ReadyTarget parsed = parseReadyTarget(stdoutTail + "\n" + stderrTail);
        if (parsed != null) {
            readyTarget = parsed;
            readyOrExit.countDown();
        }
    }

    private static void appendTail(StringBuilder builder, String text) {
        builder.append(text);
        int extra = builder.length() - 4000;
        if (extra > 0) {
            builder.delete(0, extra);
        }
    }

    static ReadyTarget parseReadyTarget(@NotNull String text) {
        String[] lines = text.split("\\R");
        for (String line : lines) {
            int index = line.indexOf(READY_PREFIX);
            if (index < 0) {
                continue;
            }
            String address = line.substring(index + READY_PREFIX.length()).trim();
            int colon = address.lastIndexOf(':');
            if (colon <= 0 || colon == address.length() - 1) {
                continue;
            }
            try {
                int port = Integer.parseInt(address.substring(colon + 1));
                if (port >= 1 && port <= 65535) {
                    return new ReadyTarget(address.substring(0, colon), port);
                }
            } catch (NumberFormatException ignored) {
                // 非法端口不是 ready 标记，继续检查后续行。
            }
        }
        return null;
    }

    public record ReadyTarget(@NotNull String host, int port) {
    }
}

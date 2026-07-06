package com.glua.jetbrains;

import com.intellij.execution.process.ProcessHandler;
import com.intellij.execution.process.ProcessOutputTypes;
import org.jetbrains.annotations.NotNull;

import java.io.ByteArrayOutputStream;
import java.io.OutputStream;
import java.net.InetSocketAddress;
import java.net.Socket;
import java.util.concurrent.atomic.AtomicBoolean;

public final class GluaDapAttachProcessHandler extends ProcessHandler {
    private final String host;
    private final int port;
    private final AtomicBoolean closed = new AtomicBoolean(false);

    public GluaDapAttachProcessHandler(@NotNull String host, int port) {
        this.host = host;
        this.port = port;
    }

    @Override
    public void startNotify() {
        super.startNotify();
        Thread worker = new Thread(this::checkConnection, "glua-dap-attach-check");
        worker.setDaemon(true);
        worker.start();
    }

    @Override
    protected void destroyProcessImpl() {
        if (closed.compareAndSet(false, true)) {
            notifyTextAvailable("GLua DAP attach check stopped.\n", ProcessOutputTypes.SYSTEM);
            notifyProcessTerminated(0);
        }
    }

    @Override
    protected void detachProcessImpl() {
        destroyProcessImpl();
    }

    @Override
    public boolean detachIsDefault() {
        return false;
    }

    @Override
    public @NotNull OutputStream getProcessInput() {
        return new ByteArrayOutputStream();
    }

    private void checkConnection() {
        notifyTextAvailable("GLua DAP attach target: " + host + ":" + port + "\n", ProcessOutputTypes.STDOUT);
        notifyTextAvailable("Checking TCP connectivity before handing this address to a DAP client...\n", ProcessOutputTypes.STDOUT);
        try (Socket socket = new Socket()) {
            socket.connect(new InetSocketAddress(host, port), 3000);
            notifyTextAvailable("Connected to GLua DAP server. Use the same host and port for a DAP-capable JetBrains debugger.\n", ProcessOutputTypes.STDOUT);
            terminate(0);
        } catch (Exception error) {
            notifyTextAvailable("Failed to connect to GLua DAP server: " + error.getMessage() + "\n", ProcessOutputTypes.STDERR);
            terminate(1);
        }
    }

    private void terminate(int exitCode) {
        if (closed.compareAndSet(false, true)) {
            notifyProcessTerminated(exitCode);
        }
    }
}

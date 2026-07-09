package com.glua.jetbrains;

import com.google.gson.JsonArray;
import com.google.gson.JsonElement;
import com.google.gson.JsonObject;
import com.google.gson.JsonParser;
import com.intellij.execution.process.ProcessHandler;
import com.intellij.execution.process.ProcessOutputTypes;
import com.intellij.openapi.application.ApplicationManager;
import com.intellij.openapi.project.Project;
import com.intellij.xdebugger.XDebuggerManager;
import com.intellij.xdebugger.breakpoints.XBreakpoint;
import com.intellij.xdebugger.breakpoints.XLineBreakpoint;
import org.jetbrains.annotations.NotNull;

import java.io.BufferedInputStream;
import java.io.BufferedWriter;
import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.io.OutputStreamWriter;
import java.net.InetSocketAddress;
import java.net.Socket;
import java.nio.charset.StandardCharsets;
import java.nio.file.Path;
import java.nio.file.Paths;
import java.util.ArrayList;
import java.util.Collections;
import java.util.LinkedHashMap;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Map;
import java.util.Set;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.function.Consumer;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

public final class GluaDapRemoteProcessHandler extends ProcessHandler implements GluaDapClient {
    private static final Pattern CONTENT_LENGTH_PATTERN = Pattern.compile("(?i)^Content-Length:\\s*(\\d+)\\s*$");
    private final Project project;
    private final String host;
    private final int port;
    private final String program;
    private final String workDirectory;
    private final AtomicBoolean closed = new AtomicBoolean(false);
    private final Set<String> syncedBreakpointSources = new LinkedHashSet<>();
    private volatile Socket dapSocket;
    private volatile BufferedWriter dapWriter;
    private volatile GluaDebugProcess debugProcess;
    private volatile boolean breakpointsMuted;
    private volatile List<GluaDapVariable> latestVariables = List.of();
    private final Map<Integer, List<GluaDapVariable>> variableCache = new LinkedHashMap<>();
    private final Map<Integer, List<Consumer<List<GluaDapVariable>>>> variableCallbacks = new LinkedHashMap<>();
    private final Map<Integer, Integer> variablesRequestReferences = new LinkedHashMap<>();
    private final Map<Integer, Consumer<GluaDapSetVariableResult>> setVariableCallbacks = new LinkedHashMap<>();
    private int nextClientSeq = 1;

    public GluaDapRemoteProcessHandler(@NotNull Project project,
                                       @NotNull String host,
                                       int port,
                                       @NotNull String program) {
        this.project = project;
        this.host = host.isBlank() ? "127.0.0.1" : host.trim();
        this.port = port >= 1 && port <= 65535 ? port : 5678;
        this.program = program;
        this.workDirectory = workDirectory(program);
    }

    @Override
    public void setDebugProcess(@NotNull GluaDebugProcess debugProcess) {
        this.debugProcess = debugProcess;
    }

    @Override
    public void sendControlCommand(@NotNull String command) {
        BufferedWriter writer = dapWriter;
        if (writer == null) {
            return;
        }
        Thread worker = new Thread(() -> {
            try {
                if (!"pause".equals(command)) {
                    sendBreakpoints(writer);
                }
                sendRequest(writer, command, "{}");
            } catch (IOException error) {
                reportFailure("GLua Debug command failed: " + readableError(error));
            }
        }, "glua-remote-dap-" + command);
        worker.setDaemon(true);
        worker.start();
    }

    @Override
    public void setBreakpointsMuted(boolean muted) {
        breakpointsMuted = muted;
    }

    @Override
    public void syncBreakpointsAsync() {
        BufferedWriter writer = dapWriter;
        if (writer == null) {
            return;
        }
        Thread worker = new Thread(() -> {
            try {
                sendBreakpoints(writer);
            } catch (IOException error) {
                reportFailure("GLua breakpoint sync failed: " + readableError(error));
            }
        }, "glua-remote-dap-breakpoint-sync");
        worker.setDaemon(true);
        worker.start();
    }

    @Override
    public @NotNull List<GluaDapVariable> currentVariables() {
        return latestVariables;
    }

    @Override
    public void requestVariables(int variablesReference, @NotNull Consumer<List<GluaDapVariable>> callback) {
        if (variablesReference <= 0) {
            callback.accept(List.of());
            return;
        }
        List<GluaDapVariable> cached;
        synchronized (variableCache) {
            cached = variableCache.get(variablesReference);
            if (cached != null) {
                callback.accept(cached);
                return;
            }
        }
        BufferedWriter writer = dapWriter;
        if (writer == null) {
            callback.accept(List.of());
            return;
        }
        Thread worker = new Thread(() -> {
            try {
                sendVariablesRequest(writer, variablesReference, callback);
            } catch (IOException error) {
                reportFailure("GLua variables request failed: " + readableError(error));
                callback.accept(List.of());
            }
        }, "glua-remote-dap-variables-" + variablesReference);
        worker.setDaemon(true);
        worker.start();
    }

    @Override
    public void setVariable(int variablesReference,
                            @NotNull String name,
                            @NotNull String value,
                            @NotNull Consumer<GluaDapSetVariableResult> callback) {
        BufferedWriter writer = dapWriter;
        if (writer == null) {
            callback.accept(GluaDapSetVariableResult.failure("GLua DAP connection is not ready."));
            return;
        }
        Thread worker = new Thread(() -> {
            try {
                sendSetVariableRequest(writer, variablesReference, name, value, callback);
            } catch (IOException error) {
                reportFailure("GLua setVariable request failed: " + readableError(error));
                callback.accept(GluaDapSetVariableResult.failure(readableError(error)));
            }
        }, "glua-remote-dap-set-variable");
        worker.setDaemon(true);
        worker.start();
    }

    @Override
    public void startNotify() {
        super.startNotify();
        Thread worker = new Thread(this::connectAndConfigure, "glua-remote-dap-client");
        worker.setDaemon(true);
        worker.start();
    }

    @Override
    protected void destroyProcessImpl() {
        terminate(0);
    }

    @Override
    protected void detachProcessImpl() {
        terminate(0);
    }

    @Override
    public boolean detachIsDefault() {
        return false;
    }

    @Override
    public @NotNull OutputStream getProcessInput() {
        return new ByteArrayOutputStream();
    }

    private void connectAndConfigure() {
        try {
            Socket socket = new Socket();
            socket.connect(new InetSocketAddress(host, port), 5000);
            dapSocket = socket;
            InputStream reader = new BufferedInputStream(socket.getInputStream());
            BufferedWriter writer = new BufferedWriter(new OutputStreamWriter(socket.getOutputStream(), StandardCharsets.UTF_8));
            dapWriter = writer;
            Thread readerThread = new Thread(() -> drainDapMessages(reader), "glua-remote-dap-reader");
            readerThread.setDaemon(true);
            readerThread.start();
            sendRequest(writer, "initialize", "{\"adapterID\":\"glua\",\"clientID\":\"intellij\",\"clientName\":\"IntelliJ IDEA\",\"linesStartAt1\":true,\"columnsStartAt1\":true,\"pathFormat\":\"path\"}");
            sendBreakpoints(writer);
            sendRequest(writer, "attach", "{\"program\":" + quoteJson(program) + "}");
            sendRequest(writer, "configurationDone", "{}");
        } catch (IOException error) {
            reportFailure(failureMessage(host, port, error).trim());
            terminate(1);
        }
    }

    private void sendBreakpoints(@NotNull BufferedWriter writer) throws IOException {
        Map<String, List<Integer>> breakpointsByFile = collectBreakpoints();
        Set<String> sources = breakpointSourcesToSync(breakpointsByFile);
        for (String source : sources) {
            List<Integer> lines = breakpointsByFile.getOrDefault(source, List.of());
            StringBuilder breakpointsJson = new StringBuilder("[");
            for (int index = 0; index < lines.size(); index++) {
                if (index > 0) {
                    breakpointsJson.append(',');
                }
                breakpointsJson.append("{\"line\":").append(lines.get(index)).append('}');
            }
            breakpointsJson.append(']');
            sendRequest(writer, "setBreakpoints", "{\"source\":{\"path\":" + quoteJson(source) + "},\"breakpoints\":" + breakpointsJson + "}");
        }
        rememberSyncedBreakpointSources(breakpointsByFile.keySet());
    }

    private @NotNull Set<String> breakpointSourcesToSync(@NotNull Map<String, List<Integer>> breakpointsByFile) {
        Set<String> sources = new LinkedHashSet<>();
        synchronized (syncedBreakpointSources) {
            sources.addAll(syncedBreakpointSources);
        }
        sources.addAll(breakpointsByFile.keySet());
        return sources;
    }

    private void rememberSyncedBreakpointSources(@NotNull Set<String> currentSources) {
        synchronized (syncedBreakpointSources) {
            syncedBreakpointSources.clear();
            syncedBreakpointSources.addAll(currentSources);
        }
    }

    private @NotNull Map<String, List<Integer>> collectBreakpoints() {
        Map<String, List<Integer>> result = new LinkedHashMap<>();
        var breakpointManager = XDebuggerManager.getInstance(project).getBreakpointManager();
        if (breakpointsMuted) {
            return result;
        }
        for (XBreakpoint<?> breakpoint : breakpointManager.getAllBreakpoints()) {
            if (!(breakpoint instanceof XLineBreakpoint<?> lineBreakpoint)) {
                continue;
            }
            if (!(lineBreakpoint.getType() instanceof GluaLineBreakpointType) || !lineBreakpoint.isEnabled()) {
                continue;
            }
            for (String path : breakpointPathAliases(normalizeFileUrl(lineBreakpoint.getFileUrl()))) {
                result.computeIfAbsent(path, ignored -> new ArrayList<>()).add(lineBreakpoint.getLine() + 1);
            }
        }
        return result;
    }

    private void drainDapMessages(@NotNull InputStream reader) {
        try {
            while (!closed.get()) {
                String message = readDapMessage(reader);
                if (message == null) {
                    terminate(0);
                    return;
                }
                handleDapMessage(message);
                logDapTraffic("<=", message);
            }
        } catch (IOException error) {
            if (!closed.get()) {
                reportFailure("GLua DAP read failed: " + readableError(error));
                terminate(1);
            }
        }
    }

    private void handleDapMessage(@NotNull String message) {
        JsonObject object;
        try {
            object = JsonParser.parseString(message).getAsJsonObject();
        } catch (RuntimeException ignored) {
            return;
        }
        String type = stringMember(object, "type");
        if ("event".equals(type) && "stopped".equals(stringMember(object, "event"))) {
            latestVariables = List.of();
            clearVariableCaches();
            sendControlCommand("stackTrace");
            return;
        }
        if ("response".equals(type) && "variables".equals(stringMember(object, "command"))) {
            handleVariablesResponse(object);
            return;
        }
        if ("response".equals(type) && "setVariable".equals(stringMember(object, "command"))) {
            handleSetVariableResponse(object);
            return;
        }
        if ("response".equals(type) && "stackTrace".equals(stringMember(object, "command"))) {
            GluaDapStackFrame frame = firstStackFrame(object);
            GluaDebugProcess process = debugProcess;
            if (frame != null && process != null) {
                process.onStopped(frame);
                requestVariables(1, variables -> {
                    latestVariables = variables;
                    process.refreshVariables();
                });
            }
        }
    }

    private synchronized int sendRequest(@NotNull BufferedWriter writer,
                                         @NotNull String command,
                                         @NotNull String argumentsJson) throws IOException {
        int seq = nextClientSeq++;
        String payload = "{\"seq\":" + seq
            + ",\"type\":\"request\""
            + ",\"command\":" + quoteJson(command)
            + ",\"arguments\":" + argumentsJson
            + "}";
        writer.write("Content-Length: " + payload.getBytes(StandardCharsets.UTF_8).length + "\r\n\r\n");
        writer.write(payload);
        writer.flush();
        logDapTraffic("=>", payload);
        return seq;
    }

    private synchronized int sendVariablesRequest(@NotNull BufferedWriter writer,
                                                  int variablesReference,
                                                  @NotNull Consumer<List<GluaDapVariable>> callback) throws IOException {
        int seq = nextClientSeq++;
        synchronized (variableCache) {
            variablesRequestReferences.put(seq, variablesReference);
            variableCallbacks.computeIfAbsent(variablesReference, ignored -> new ArrayList<>()).add(callback);
        }
        String payload = "{\"seq\":" + seq
            + ",\"type\":\"request\""
            + ",\"command\":\"variables\""
            + ",\"arguments\":{\"variablesReference\":" + variablesReference + "}"
            + "}";
        writer.write("Content-Length: " + payload.getBytes(StandardCharsets.UTF_8).length + "\r\n\r\n");
        writer.write(payload);
        writer.flush();
        logDapTraffic("=>", payload);
        return seq;
    }

    private synchronized int sendSetVariableRequest(@NotNull BufferedWriter writer,
                                                    int variablesReference,
                                                    @NotNull String name,
                                                    @NotNull String value,
                                                    @NotNull Consumer<GluaDapSetVariableResult> callback) throws IOException {
        int seq = nextClientSeq++;
        synchronized (variableCache) {
            setVariableCallbacks.put(seq, callback);
        }
        String payload = "{\"seq\":" + seq
            + ",\"type\":\"request\""
            + ",\"command\":\"setVariable\""
            + ",\"arguments\":{\"variablesReference\":" + variablesReference
            + ",\"name\":" + quoteJson(name)
            + ",\"value\":" + quoteJson(value)
            + "}"
            + "}";
        writer.write("Content-Length: " + payload.getBytes(StandardCharsets.UTF_8).length + "\r\n\r\n");
        writer.write(payload);
        writer.flush();
        logDapTraffic("=>", payload);
        return seq;
    }

    private void logDapTraffic(@NotNull String direction, @NotNull String payload) {
        if (!dapDebugLogEnabled()) {
            return;
        }
        String line = "GLua DAP " + direction + " " + payload + "\n";
        ApplicationManager.getApplication().invokeLater(() -> notifyTextAvailable(line, ProcessOutputTypes.STDOUT));
    }

    private static boolean dapDebugLogEnabled() {
        return ApplicationManager.getApplication().getService(GluaSettings.class).dapDebugLog();
    }

    private void handleVariablesResponse(@NotNull JsonObject object) {
        int requestSeq = intMember(object, "request_seq");
        List<GluaDapVariable> variables = parseVariables(object);
        List<Consumer<List<GluaDapVariable>>> callbacks;
        int reference;
        synchronized (variableCache) {
            reference = variablesRequestReferences.remove(requestSeq);
            if (reference <= 0) {
                reference = 1;
            }
            variableCache.put(reference, variables);
            callbacks = variableCallbacks.remove(reference);
        }
        if (reference == 1) {
            latestVariables = variables;
            GluaDebugProcess process = debugProcess;
            if (process != null) {
                process.refreshVariables();
            }
        }
        if (callbacks != null) {
            for (Consumer<List<GluaDapVariable>> callback : callbacks) {
                callback.accept(variables);
            }
        }
    }

    private void handleSetVariableResponse(@NotNull JsonObject object) {
        int requestSeq = intMember(object, "request_seq");
        Consumer<GluaDapSetVariableResult> callback;
        synchronized (variableCache) {
            callback = setVariableCallbacks.remove(requestSeq);
            variableCache.clear();
        }
        if (callback == null) {
            return;
        }
        JsonObject body = objectMember(object, "body");
        String error = body == null ? "" : stringMember(body, "error");
        if (!booleanMember(object, "success")) {
            if (error.isBlank()) {
                error = stringMember(object, "message");
            }
            callback.accept(GluaDapSetVariableResult.failure(error.isBlank() ? "GLua setVariable failed." : error));
            return;
        }
        if (!error.isBlank()) {
            callback.accept(GluaDapSetVariableResult.failure(error));
            return;
        }
        callback.accept(GluaDapSetVariableResult.ok());
    }

    private void clearVariableCaches() {
        synchronized (variableCache) {
            variableCache.clear();
            variableCallbacks.clear();
            variablesRequestReferences.clear();
            setVariableCallbacks.clear();
        }
    }

    private void terminate(int exitCode) {
        if (!closed.compareAndSet(false, true)) {
            return;
        }
        BufferedWriter writer = dapWriter;
        dapWriter = null;
        if (writer != null) {
            try {
                writer.close();
            } catch (IOException ignored) {
                // 关闭远程 DAP 写入端失败时，仍然结束 IDE 侧进程。
            }
        }
        Socket socket = dapSocket;
        if (socket != null) {
            try {
                socket.close();
            } catch (IOException ignored) {
                // 关闭远程 DAP socket 失败不影响 IDE 侧进程结束。
            }
        }
        notifyProcessTerminated(exitCode);
    }

    private void reportFailure(@NotNull String message) {
        String fullMessage = message.endsWith("\n") ? message : message + "\n";
        notifyTextAvailable(fullMessage, ProcessOutputTypes.STDERR);
        GluaDebugProcess process = debugProcess;
        if (process != null) {
            process.getSession().reportError(message);
        }
    }

    private @NotNull Set<String> breakpointPathAliases(@NotNull String path) {
        Set<String> aliases = new LinkedHashSet<>();
        if (path.isBlank()) {
            return aliases;
        }
        addPathWithExtensionAliases(aliases, path);
        try {
            Path absolute = Paths.get(path).toAbsolutePath().normalize();
            addPathWithExtensionAliases(aliases, absolute.toString());
            if (!workDirectory.isBlank()) {
                Path workDir = Paths.get(workDirectory).toAbsolutePath().normalize();
                if (absolute.startsWith(workDir)) {
                    addPathWithExtensionAliases(aliases, workDir.relativize(absolute).toString());
                }
            }
        } catch (RuntimeException ignored) {
            // 远程路径可能不是本机 Path，保留原始字符串即可。
        }
        aliases.remove("");
        return aliases;
    }

    private static @NotNull String workDirectory(@NotNull String program) {
        if (program.isBlank()) {
            return "";
        }
        try {
            Path parent = Path.of(program).toAbsolutePath().normalize().getParent();
            return parent == null ? "" : parent.toString();
        } catch (RuntimeException ignored) {
            return "";
        }
    }

    private static void addPathWithExtensionAliases(@NotNull Set<String> aliases, @NotNull String path) {
        String normalized = path.replace('\\', '/');
        aliases.add(normalized);
        if (normalized.endsWith(".lua")) {
            aliases.add(normalized.substring(0, normalized.length() - ".lua".length()) + ".glua");
        } else if (normalized.endsWith(".glua")) {
            aliases.add(normalized.substring(0, normalized.length() - ".glua".length()) + ".lua");
        }
    }

    private static @NotNull String normalizeFileUrl(@NotNull String fileUrl) {
        if (fileUrl.startsWith("file://")) {
            return fileUrl.substring("file://".length());
        }
        return fileUrl;
    }

    private static String readDapMessage(@NotNull InputStream reader) throws IOException {
        int contentLength = -1;
        while (true) {
            String line = readAsciiHeaderLine(reader);
            if (line == null) {
                return null;
            }
            if (line.isEmpty()) {
                break;
            }
            Matcher matcher = CONTENT_LENGTH_PATTERN.matcher(line);
            if (matcher.matches()) {
                contentLength = Integer.parseInt(matcher.group(1));
            }
        }
        if (contentLength < 0) {
            return null;
        }
        byte[] body = new byte[contentLength];
        int offset = 0;
        while (offset < contentLength) {
            int read = reader.read(body, offset, contentLength - offset);
            if (read < 0) {
                return null;
            }
            offset += read;
        }
        return new String(body, StandardCharsets.UTF_8);
    }

    private static String readAsciiHeaderLine(@NotNull InputStream reader) throws IOException {
        ByteArrayOutputStream line = new ByteArrayOutputStream();
        while (true) {
            int value = reader.read();
            if (value < 0) {
                return line.size() == 0 ? null : line.toString(StandardCharsets.US_ASCII);
            }
            if (value == '\n') {
                break;
            }
            if (value != '\r') {
                line.write(value);
            }
        }
        return line.toString(StandardCharsets.US_ASCII);
    }

    private static GluaDapStackFrame firstStackFrame(@NotNull JsonObject response) {
        JsonObject body = objectMember(response, "body");
        if (body == null) {
            return null;
        }
        JsonArray frames = arrayMember(body, "stackFrames");
        if (frames == null || frames.isEmpty() || !frames.get(0).isJsonObject()) {
            return null;
        }
        JsonObject frame = frames.get(0).getAsJsonObject();
        JsonObject source = objectMember(frame, "source");
        return new GluaDapStackFrame(source == null ? "" : stringMember(source, "path"), intMember(frame, "line"));
    }

    private static @NotNull List<GluaDapVariable> parseVariables(@NotNull JsonObject response) {
        JsonObject body = objectMember(response, "body");
        if (body == null) {
            return List.of();
        }
        JsonArray variables = arrayMember(body, "variables");
        if (variables == null || variables.isEmpty()) {
            return List.of();
        }
        List<GluaDapVariable> result = new ArrayList<>();
        for (JsonElement element : variables) {
            if (!element.isJsonObject()) {
                continue;
            }
            JsonObject variable = element.getAsJsonObject();
            String name = stringMember(variable, "name");
            if (!name.isBlank()) {
                result.add(new GluaDapVariable(name, stringMember(variable, "value"), stringMember(variable, "type"), intMember(variable, "variablesReference")));
            }
        }
        return Collections.unmodifiableList(result);
    }

    private static String quoteJson(@NotNull String text) {
        StringBuilder builder = new StringBuilder("\"");
        for (int index = 0; index < text.length(); index++) {
            char ch = text.charAt(index);
            switch (ch) {
                case '\\' -> builder.append("\\\\");
                case '"' -> builder.append("\\\"");
                case '\n' -> builder.append("\\n");
                case '\r' -> builder.append("\\r");
                case '\t' -> builder.append("\\t");
                default -> builder.append(ch);
            }
        }
        return builder.append('"').toString();
    }

    private static String readableError(@NotNull Exception error) {
        return error.getMessage() == null || error.getMessage().isBlank()
            ? error.getClass().getSimpleName()
            : error.getMessage();
    }

    static @NotNull String failureMessage(@NotNull String host, int port, @NotNull Exception error) {
        return "GLua DAP attach failed for " + host + ":" + port + ": " + readableError(error) + "\n"
            + "No GLua DAP server is listening at that address. Configure a glua executable for local launch, or start a compatible remote GLua DAP server and set the remote host/port.";
    }

    private static @NotNull String stringMember(@NotNull JsonObject object, @NotNull String name) {
        JsonElement element = object.get(name);
        return element == null || element.isJsonNull() ? "" : element.getAsString();
    }

    private static JsonObject objectMember(@NotNull JsonObject object, @NotNull String name) {
        JsonElement element = object.get(name);
        return element == null || !element.isJsonObject() ? null : element.getAsJsonObject();
    }

    private static JsonArray arrayMember(@NotNull JsonObject object, @NotNull String name) {
        JsonElement element = object.get(name);
        return element == null || !element.isJsonArray() ? null : element.getAsJsonArray();
    }

    private static int intMember(@NotNull JsonObject object, @NotNull String name) {
        JsonElement element = object.get(name);
        return element == null || !element.isJsonPrimitive() ? 0 : element.getAsInt();
    }

    private static boolean booleanMember(@NotNull JsonObject object, @NotNull String name) {
        JsonElement element = object.get(name);
        return element != null && element.isJsonPrimitive() && element.getAsBoolean();
    }
}

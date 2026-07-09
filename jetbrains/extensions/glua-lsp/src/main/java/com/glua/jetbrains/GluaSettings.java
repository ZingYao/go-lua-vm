package com.glua.jetbrains;

import com.intellij.openapi.components.PersistentStateComponent;
import com.intellij.openapi.components.Service;
import com.intellij.openapi.components.State;
import com.intellij.openapi.components.Storage;
import org.jetbrains.annotations.NotNull;
import org.jetbrains.annotations.Nullable;

import java.util.ArrayList;
import java.util.List;

@Service
@State(name = "GluaSettings", storages = @Storage("glua.xml"))
public final class GluaSettings implements PersistentStateComponent<GluaSettings.StateData> {
    public static final class StateData {
        public String docLanguage = "auto";
        public String syntax = "extended";
        public boolean events = true;
        public List<String> builtinDocs = new ArrayList<>();
        public String dapHost = "127.0.0.1";
        public int dapPort = 5678;
        public boolean useRemoteDap = false;
        public boolean dapDebugLog = false;
        public String gluaExecutable = "";
        public String gluacExecutable = "";
    }

    private StateData state = new StateData();

    @Override
    public @Nullable StateData getState() {
        return state;
    }

    @Override
    public void loadState(@NotNull StateData state) {
        this.state = state;
        if (this.state.builtinDocs == null) {
            this.state.builtinDocs = new ArrayList<>();
        }
    }

    public String docLanguage() {
        return state.docLanguage == null || state.docLanguage.isBlank() ? "auto" : state.docLanguage.trim();
    }

    public void setDocLanguage(String docLanguage) {
        state.docLanguage = docLanguage == null || docLanguage.isBlank() ? "auto" : docLanguage.trim();
    }

    public String syntax() {
        return state.syntax == null || state.syntax.isBlank() ? "extended" : state.syntax.trim();
    }

    public void setSyntax(String syntax) {
        state.syntax = syntax == null || syntax.isBlank() ? "extended" : syntax.trim();
    }

    public boolean events() {
        return state.events;
    }

    public void setEvents(boolean events) {
        state.events = events;
    }

    public List<String> builtinDocs() {
        return state.builtinDocs == null ? List.of() : List.copyOf(state.builtinDocs);
    }

    public void setBuiltinDocs(List<String> docs) {
        state.builtinDocs = docs == null ? new ArrayList<>() : new ArrayList<>(docs);
    }

    public String dapHost() {
        return state.dapHost == null || state.dapHost.isBlank() ? "127.0.0.1" : state.dapHost.trim();
    }

    public void setDapHost(String dapHost) {
        state.dapHost = dapHost == null || dapHost.isBlank() ? "127.0.0.1" : dapHost.trim();
    }

    public int dapPort() {
        return state.dapPort >= 1 && state.dapPort <= 65535 ? state.dapPort : 5678;
    }

    public void setDapPort(int dapPort) {
        state.dapPort = dapPort >= 1 && dapPort <= 65535 ? dapPort : 5678;
    }

    public boolean useRemoteDap() {
        return state.useRemoteDap;
    }

    public void setUseRemoteDap(boolean useRemoteDap) {
        state.useRemoteDap = useRemoteDap;
    }

    public boolean dapDebugLog() {
        return state.dapDebugLog;
    }

    public void setDapDebugLog(boolean dapDebugLog) {
        state.dapDebugLog = dapDebugLog;
    }

    public String gluaExecutable() {
        return state.gluaExecutable == null ? "" : state.gluaExecutable.trim();
    }

    public void setGluaExecutable(String gluaExecutable) {
        state.gluaExecutable = gluaExecutable == null ? "" : gluaExecutable.trim();
    }

    public String gluacExecutable() {
        return state.gluacExecutable == null ? "" : state.gluacExecutable.trim();
    }

    public void setGluacExecutable(String gluacExecutable) {
        state.gluacExecutable = gluacExecutable == null ? "" : gluacExecutable.trim();
    }
}

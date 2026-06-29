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
        public List<String> builtinDocs = new ArrayList<>();
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

    public List<String> builtinDocs() {
        return state.builtinDocs == null ? List.of() : List.copyOf(state.builtinDocs);
    }

    public void setBuiltinDocs(List<String> docs) {
        state.builtinDocs = docs == null ? new ArrayList<>() : new ArrayList<>(docs);
    }
}

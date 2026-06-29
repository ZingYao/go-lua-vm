package com.glua.jetbrains;

import com.google.gson.Gson;
import com.google.gson.JsonArray;
import com.google.gson.JsonElement;
import com.google.gson.JsonObject;
import com.intellij.openapi.application.ApplicationManager;
import com.intellij.openapi.diagnostic.Logger;

import java.io.InputStreamReader;
import java.io.Reader;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.ArrayList;
import java.util.Comparator;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Locale;
import java.util.Map;
import java.util.Objects;
import java.util.Set;

public final class GluaBuiltinCatalog {
    private static final Logger LOG = Logger.getInstance(GluaBuiltinCatalog.class);
    private static final Gson GSON = new Gson();
    private static final GluaBuiltinCatalog INSTANCE = new GluaBuiltinCatalog();

    private final Map<String, JsonObject> entries = new LinkedHashMap<>();
    private String resolvedLocale = "en";
    private boolean loaded;

    private GluaBuiltinCatalog() {
    }

    public static GluaBuiltinCatalog getInstance() {
        return INSTANCE;
    }

    public synchronized void reload() {
        entries.clear();
        GluaSettings currentSettings = settings();
        resolvedLocale = resolveLocale(currentSettings.docLanguage());
        loadDefault();
        for (String file : currentSettings.builtinDocs()) {
            loadPath(file);
        }
        loaded = true;
        LOG.info("glua builtin docs locale=" + resolvedLocale + ", entries=" + entries.size() + ", files=" + currentSettings.builtinDocs().size());
    }

    public synchronized Set<String> names() {
        ensureLoaded();
        return Set.copyOf(entries.keySet());
    }

    public synchronized List<String> sortedNames() {
        ensureLoaded();
        return entries.keySet().stream().sorted(Comparator.naturalOrder()).toList();
    }

    public synchronized GluaBuiltin get(String name) {
        ensureLoaded();
        JsonObject entry = entries.get(name);
        if (entry == null) {
            return null;
        }
        return new GluaBuiltin(
            localizedString(entry.get("signature")),
            localizedString(entry.get("description")),
            localizedList(entry.get("params")),
            localizedString(entry.get("returns")),
            localizedString(entry.get("example"))
        );
    }

    public synchronized String targetForMethod(String methodName, String moduleHint) {
        ensureLoaded();
        String exact = moduleHint == null || moduleHint.isBlank() ? "" : moduleHint + "." + methodName;
        if (!exact.isBlank() && entries.containsKey(exact)) {
            return exact;
        }
        List<String> matches = entries.keySet().stream()
            .filter(name -> name.endsWith("." + methodName))
            .toList();
        return matches.size() == 1 ? matches.get(0) : null;
    }

    public String locale() {
        ensureLoaded();
        return resolvedLocale;
    }

    private synchronized void ensureLoaded() {
        if (!loaded) {
            reload();
        }
    }

    private void loadDefault() {
        try (Reader reader = new InputStreamReader(
            Objects.requireNonNull(getClass().getResourceAsStream("/builtin-functions.json")),
            StandardCharsets.UTF_8
        )) {
            mergeCatalog(GSON.fromJson(reader, JsonObject.class), null);
        } catch (Exception error) {
            LOG.warn("failed to load default glua builtin docs", error);
        }
    }

    private void loadPath(String rawPath) {
        try {
            Path path = Path.of(rawPath).toAbsolutePath().normalize();
            if (!Files.exists(path)) {
                LOG.warn("glua builtin docs file does not exist: " + rawPath);
                return;
            }
            try (Reader reader = Files.newBufferedReader(path, StandardCharsets.UTF_8)) {
                mergeCatalog(GSON.fromJson(reader, JsonObject.class), inferLocale(path));
            }
        } catch (Exception error) {
            LOG.warn("failed to load glua builtin docs file: " + rawPath, error);
        }
    }

    private void mergeCatalog(JsonObject catalog, String localeHint) {
        if (catalog == null) {
            return;
        }
        String locale = stringValue(catalog.get("locale"));
        if (locale == null) {
            locale = stringValue(catalog.get("language"));
        }
        if (locale == null) {
            locale = localeHint;
        }
        JsonObject functions = objectValue(catalog.get("functions"));
        if (functions == null) {
            functions = objectValue(catalog.get("builtins"));
        }
        if (functions == null) {
            functions = catalog;
        }
        for (Map.Entry<String, JsonElement> item : functions.entrySet()) {
            if (!item.getValue().isJsonObject()) {
                continue;
            }
            JsonObject normalized = normalizeEntry(item.getValue().getAsJsonObject(), locale);
            JsonObject previous = entries.getOrDefault(item.getKey(), new JsonObject());
            entries.put(item.getKey(), mergeEntry(previous, normalized));
        }
    }

    private JsonObject mergeEntry(JsonObject base, JsonObject override) {
        JsonObject next = base.deepCopy();
        for (Map.Entry<String, JsonElement> item : override.entrySet()) {
            JsonElement baseValue = next.get(item.getKey());
            JsonElement overrideValue = item.getValue();
            if (baseValue != null && baseValue.isJsonObject() && overrideValue.isJsonObject()) {
                JsonObject merged = baseValue.getAsJsonObject().deepCopy();
                for (Map.Entry<String, JsonElement> localeItem : overrideValue.getAsJsonObject().entrySet()) {
                    merged.add(localeItem.getKey(), localeItem.getValue());
                }
                next.add(item.getKey(), merged);
            } else {
                next.add(item.getKey(), overrideValue);
            }
        }
        return next;
    }

    private JsonObject normalizeEntry(JsonObject entry, String locale) {
        if (locale == null || locale.isBlank()) {
            return entry;
        }
        String tag = normalizeLocale(locale);
        JsonObject next = new JsonObject();
        for (Map.Entry<String, JsonElement> field : entry.entrySet()) {
            if (List.of("signature", "description", "params", "returns", "example").contains(field.getKey()) && !hasLocaleMapShape(field.getValue())) {
                JsonObject wrapper = new JsonObject();
                wrapper.add(tag, field.getValue());
                next.add(field.getKey(), wrapper);
            } else {
                next.add(field.getKey(), field.getValue());
            }
        }
        return next;
    }

    private String localizedString(JsonElement value) {
        JsonElement selected = localizedValue(value);
        return selected == null || selected.isJsonNull() ? "" : selected.getAsString();
    }

    private List<String> localizedList(JsonElement value) {
        JsonElement selected = localizedValue(value);
        if (selected == null || selected.isJsonNull()) {
            return List.of();
        }
        if (!selected.isJsonArray()) {
            return List.of(selected.getAsString());
        }
        List<String> values = new ArrayList<>();
        JsonArray array = selected.getAsJsonArray();
        for (JsonElement element : array) {
            values.add(element.getAsString());
        }
        return values;
    }

    private JsonElement localizedValue(JsonElement value) {
        if (value == null || value.isJsonNull() || !value.isJsonObject()) {
            return value;
        }
        JsonObject object = value.getAsJsonObject();
        String normalized = normalizeLocale(resolvedLocale);
        if (object.has(normalized)) {
            return object.get(normalized);
        }
        if (object.has(resolvedLocale)) {
            return object.get(resolvedLocale);
        }
        if (object.has("en")) {
            return object.get("en");
        }
        return object.entrySet().stream().findFirst().map(Map.Entry::getValue).orElse(null);
    }

    private boolean hasLocaleMapShape(JsonElement value) {
        if (value == null || !value.isJsonObject()) {
            return false;
        }
        for (String key : value.getAsJsonObject().keySet()) {
            if (looksLikeLocale(key)) {
                return true;
            }
        }
        return false;
    }

    private String resolveLocale(String configured) {
        if (configured == null || configured.isBlank() || configured.equalsIgnoreCase("auto")) {
            return normalizeLocale(Locale.getDefault().toLanguageTag());
        }
        return normalizeLocale(configured);
    }

    private String normalizeLocale(String raw) {
        String value = raw == null || raw.isBlank() ? "en" : raw.trim().replace('_', '-');
        String lower = value.toLowerCase(Locale.ROOT);
        if (lower.equals("zh") || lower.equals("zh-cn") || lower.equals("zh-hans") || lower.startsWith("zh-hans-")) {
            return "zh-CN";
        }
        if (!lower.contains("-")) {
            return lower;
        }
        String[] parts = lower.split("-");
        StringBuilder builder = new StringBuilder(parts[0]);
        for (int i = 1; i < parts.length; i++) {
            builder.append('-');
            builder.append(parts[i].length() == 2 ? parts[i].toUpperCase(Locale.ROOT) : parts[i]);
        }
        return builder.toString();
    }

    private boolean looksLikeLocale(String raw) {
        return raw != null && raw.toLowerCase(Locale.ROOT).matches("[a-z]{2,3}([_-][a-z0-9]{2,8}){0,3}");
    }

    private String inferLocale(Path path) {
        String name = path.getFileName().toString();
        int dot = name.lastIndexOf('.');
        if (dot > 0) {
            name = name.substring(0, dot);
        }
        for (String part : name.split("[._-]+")) {
            if (looksLikeLocale(part)) {
                return part;
            }
        }
        return null;
    }

    private String stringValue(JsonElement element) {
        return element == null || element.isJsonNull() ? null : element.getAsString();
    }

    private JsonObject objectValue(JsonElement element) {
        return element != null && element.isJsonObject() ? element.getAsJsonObject() : null;
    }

    private GluaSettings settings() {
        return ApplicationManager.getApplication().getService(GluaSettings.class);
    }
}

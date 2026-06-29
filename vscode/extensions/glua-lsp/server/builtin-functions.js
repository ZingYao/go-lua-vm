const DEFAULT_BUILTIN_CATALOG = require("./builtin-functions.json");
const BASE_BUILTINS = Object.freeze(DEFAULT_BUILTIN_CATALOG.functions || {});

const BUILTIN_METHOD_INDEX = Object.create(null);
const EXTRA_BUILTINS = Object.create(null);
let activeLocale = "en";
const LOCALIZABLE_BUILTIN_FIELDS = new Set(["signature", "returns", "params", "description", "example"]);

function looksLikeLocale(rawLocale) {
  const raw = String(rawLocale || "").toLowerCase();
  return /^[a-z]{2,3}([_-][a-z0-9]{2,8}){0,3}$/.test(raw);
}

function normalizeLocale(rawLocale) {
  const raw = String(rawLocale || activeLocale || "en").toLowerCase();
  if (raw === "zh" || raw === "zh-cn" || raw === "zh_cn" || raw === "zh-hans" || raw === "zh-cn") {
    return "zh-CN";
  }
  if (raw.startsWith("en")) {
    return "en";
  }
  if (raw.startsWith("zh")) {
    return "zh-CN";
  }
  const parts = raw.replace(/_/g, "-").split("-").filter(Boolean);
  if (parts.length <= 1) {
    return raw;
  }
  return [parts[0], ...parts.slice(1).map((part) => part.length === 2 ? part.toUpperCase() : part)].join("-");
}

function cloneBuiltinList(values) {
  return values.map((value) => value);
}

function cloneBuiltinObject(value) {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return value;
  }
  return Object.assign({}, value);
}

function hasLocaleMapShape(value) {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return false;
  }
  return Object.keys(value).some((key) => looksLikeLocale(key));
}

function normalizeLocaleAwareEntry(definition, locale) {
  if (!definition || typeof definition !== "object" || Array.isArray(definition)) {
    return definition;
  }
  const localeTag = looksLikeLocale(locale) ? normalizeLocale(locale) : "";

  const next = Object.create(null);
  Object.entries(definition).forEach(([field, value]) => {
    if (LOCALIZABLE_BUILTIN_FIELDS.has(field) && localeTag && !hasLocaleMapShape(value)) {
      if (Array.isArray(value)) {
        next[field] = { [localeTag]: cloneBuiltinList(value) };
      } else {
        next[field] = { [localeTag]: cloneBuiltinObject(value) };
      }
      return;
    }
    next[field] = cloneBuiltinObject(value);
  });
  return next;
}

function normalizeBuiltinCatalog(catalog, localeHint) {
  if (!catalog || typeof catalog !== "object") {
    return [];
  }

  const locale = localeHint || catalog.locale || catalog.language;
  const bucket = Object.create(null);

  if (catalog.functions && typeof catalog.functions === "object") {
    bucket.definitions = catalog.functions;
  } else if (catalog.builtins && typeof catalog.builtins === "object") {
    bucket.definitions = catalog.builtins;
  }

  if (bucket.definitions) {
    return [{ definitions: bucket.definitions, locale: locale }];
  }

  const localeCandidates = Object.entries(catalog)
    .filter(([key, value]) => looksLikeLocale(key) && value && typeof value === "object" && !Array.isArray(value))
    .map(([key, value]) => ({ definitions: value, locale: key }));

  if (localeCandidates.length > 0) {
    return localeCandidates;
  }

  return [{ definitions: catalog, locale: locale }];
}

function mergeLocaleAwareValue(baseValue, overrideValue) {
  if (overrideValue === undefined) {
    return cloneBuiltinObject(baseValue);
  }
  if (overrideValue === null) {
    return null;
  }
  if (Array.isArray(overrideValue)) {
    return cloneBuiltinList(overrideValue);
  }
  if (typeof overrideValue === "object") {
    const baseLocale = (typeof baseValue === "object" && baseValue !== null && !Array.isArray(baseValue)) ? baseValue : {};
    return Object.assign({}, baseLocale, overrideValue);
  }
  return overrideValue;
}

function normalizeLocaleAwareValue(value, locale, fallbackLocale = activeLocale) {
  if (value === undefined || value === null) {
    return undefined;
  }
  if (typeof value === "string" || typeof value === "number" || typeof value === "boolean") {
    return value;
  }
  if (Array.isArray(value)) {
    return cloneBuiltinList(value);
  }
  if (typeof value === "object") {
    if (hasLocaleMapShape(value)) {
      const normalizedLocaleMap = Object.create(null);
      Object.keys(value).forEach((rawLocale) => {
        if (!looksLikeLocale(rawLocale)) {
          return;
        }
        normalizedLocaleMap[normalizeLocale(rawLocale)] = value[rawLocale];
      });
      const keys = Object.keys(normalizedLocaleMap);
      if (keys.length > 0) {
        const localeKeys = [
          normalizeLocale(locale),
          locale,
          normalizeLocale(fallbackLocale),
          fallbackLocale,
        ];
        const unique = [...new Set(localeKeys.filter(Boolean))];
        for (const key of unique) {
          if (key && normalizedLocaleMap[key] !== undefined) {
            return normalizeLocaleAwareValue(normalizedLocaleMap[key], null, null);
          }
        }
        if (normalizedLocaleMap.en !== undefined) {
          return normalizeLocaleAwareValue(normalizedLocaleMap.en, null, null);
        }
        if (normalizedLocaleMap.zh !== undefined) {
          return normalizeLocaleAwareValue(normalizedLocaleMap.zh, null, null);
        }
        if (normalizedLocaleMap["zh-CN"] !== undefined) {
          return normalizeLocaleAwareValue(normalizedLocaleMap["zh-CN"], null, null);
        }
        const first = keys[0];
        if (first) {
          return normalizeLocaleAwareValue(normalizedLocaleMap[first], null, null);
        }
      }
    }

    const localeKeys = [normalizeLocale(locale), locale, normalizeLocale(fallbackLocale), fallbackLocale];
    const unique = [...new Set(localeKeys.filter(Boolean))];
    for (const key of unique) {
      if (key && value[key] !== undefined) {
        return normalizeLocaleAwareValue(value[key], null, null);
      }
    }
    if (value.en !== undefined) {
      return normalizeLocaleAwareValue(value.en, null, null);
    }
    if (value["en-US"] !== undefined) {
      return normalizeLocaleAwareValue(value["en-US"], null, null);
    }
    if (value["zh-CN"] !== undefined) {
      return normalizeLocaleAwareValue(value["zh-CN"], null, null);
    }
    const first = Object.keys(value)[0];
    if (first) {
      return normalizeLocaleAwareValue(value[first], null, null);
    }
  }
  return value;
}

function toLocalizedBuiltin(info, locale) {
  if (!info || typeof info !== "object") {
    return info;
  }
  return {
    signature: normalizeLocaleAwareValue(info.signature, locale, activeLocale),
    returns: normalizeLocaleAwareValue(info.returns, locale, activeLocale),
    params: normalizeLocaleAwareValue(info.params, locale, activeLocale),
    description: normalizeLocaleAwareValue(info.description, locale, activeLocale),
    example: normalizeLocaleAwareValue(info.example, locale, activeLocale),
  };
}

function rebuildBuiltinMethodIndex() {
  Object.keys(BUILTIN_METHOD_INDEX).forEach((key) => delete BUILTIN_METHOD_INDEX[key]);
  Object.keys(getAllBuiltinMap()).forEach((name) => {
    const pos = name.lastIndexOf(".");
    if (pos < 0) {
      return;
    }
    const moduleName = name.slice(0, pos);
    const methodName = name.slice(pos + 1);
    const bucket = BUILTIN_METHOD_INDEX[methodName] || [];
    bucket.push({ qualified: name, moduleName });
    BUILTIN_METHOD_INDEX[methodName] = bucket;
  });
}

function mergeBuiltinEntry(name, source) {
  if (typeof source !== "object" || source === null) {
    return;
  }
  const prev = EXTRA_BUILTINS[name] || {};
  const next = Object.assign({}, prev);
  const sourceEntries = Object.entries(source);
  for (const [field, value] of sourceEntries) {
    next[field] = mergeLocaleAwareValue(prev[field], value);
  }
  EXTRA_BUILTINS[name] = next;
}

function applyBuiltinExtensionCatalog(catalog, localeHint) {
  if (!catalog || typeof catalog !== "object") {
    return;
  }
  const locales = normalizeBuiltinCatalog(catalog, localeHint);
  locales.forEach((item) => {
    if (!item || typeof item.definitions !== "object") {
      return;
    }
    const locale = normalizeLocale(item.locale || localeHint);
    const defs = item.definitions;
    Object.entries(defs).forEach(([name, def]) => {
      mergeBuiltinEntry(name, normalizeLocaleAwareEntry(def, locale));
    });
  });
  rebuildBuiltinMethodIndex();
}

function resetBuiltinExtensions() {
  Object.keys(EXTRA_BUILTINS).forEach((key) => delete EXTRA_BUILTINS[key]);
  rebuildBuiltinMethodIndex();
}

function setBuiltinLocale(locale) {
  activeLocale = normalizeLocale(locale);
}

function getBuiltinLocale() {
  return activeLocale;
}

function getAllBuiltinMap() {
  const merged = Object.create(null);
  Object.keys(BASE_BUILTINS).forEach((name) => {
    merged[name] = mergeBuiltinDef(BASE_BUILTINS[name], EXTRA_BUILTINS[name]);
  });
  Object.keys(EXTRA_BUILTINS).forEach((name) => {
    if (!merged[name]) {
      merged[name] = mergeBuiltinDef({}, EXTRA_BUILTINS[name]);
    }
  });
  return merged;
}

function mergeBuiltinDef(baseDef, extraDef) {
  const base = baseDef && typeof baseDef === "object" ? baseDef : {};
  const extra = extraDef && typeof extraDef === "object" ? extraDef : {};
  const merged = {
    signature: mergeLocaleAwareValue(base.signature, extra.signature),
    returns: mergeLocaleAwareValue(base.returns, extra.returns),
    params: mergeLocaleAwareValue(base.params, extra.params),
    description: mergeLocaleAwareValue(base.description, extra.description),
    example: mergeLocaleAwareValue(base.example, extra.example),
  };
  Object.keys(extra).forEach((key) => {
    if (["signature", "returns", "params", "description", "example"].includes(key)) {
      return;
    }
    if (merged[key] === undefined) {
      merged[key] = cloneBuiltinObject(extra[key]);
    }
  });
  return merged;
}

function builtinFunctionNames() {
  return Object.keys(getAllBuiltinMap()).sort();
}

function getBuiltinFunction(name, locale = activeLocale) {
  const target = getAllBuiltinMap()[name];
  if (!target) {
    return null;
  }
  const resolved = toLocalizedBuiltin(target, locale);
  if (!resolved) {
    return null;
  }
  return {
    signature: resolved.signature || "",
    returns: resolved.returns || "",
    params: cloneBuiltinList(resolved.params || []),
    description: resolved.description || "",
    example: resolved.example || "",
    _locale: normalizeLocale(locale),
  };
}

function getBuiltinFunctionByMethod(methodName, moduleHint) {
  const candidates = BUILTIN_METHOD_INDEX[methodName];
  if (!Array.isArray(candidates) || candidates.length === 0) {
    return null;
  }

  if (moduleHint) {
    const exact = `${moduleHint}.${methodName}`;
    if (getAllBuiltinMap()[exact]) {
      return exact;
    }
    const byHint = candidates.find((item) => item.moduleName === moduleHint);
    if (byHint) {
      return byHint.qualified;
    }
  }

  if (candidates.length === 1) {
    return candidates[0].qualified;
  }
  return null;
}

function makeBuiltinUri(name) {
  return `glua-builtin:///${name}.lua`;
}

function builtinMarkdownLabels(locale) {
  const normalized = normalizeLocale(locale || activeLocale);
  if (normalized === "zh-CN" || normalized.startsWith("zh")) {
    return {
      description: "说明",
      parameters: "参数",
      returns: "返回值",
      example: "示例",
    };
  }
  return {
    description: "Description",
    parameters: "Parameters",
    returns: "Returns",
    example: "Example",
  };
}

function formatBuiltinMarkdown(name, info) {
  if (!info) {
    return "";
  }
  const labels = builtinMarkdownLabels(info._locale);
  const params = info.params
    .map((param) => `- \`${param}\``)
    .join("\n");
  const blocks = [
    `\`\`\`lua`,
    `${info.signature}`,
    `\`\`\``,
    "",
    `**${labels.description}**\n${info.description}`,
    "",
    `**${labels.parameters}**`,
    params,
    "",
    `**${labels.returns}**\n- ${info.returns}`,
  ];
  const example = Array.isArray(info.example) ? info.example.join("\n") : String(info.example || "");
  if (example.trim() !== "") {
    blocks.push("", `**${labels.example}**`, "```lua", example, "```");
  }
  return blocks.join("\n");
}

function makeBuiltinStubContent(name, info) {
  if (!info) {
    return "";
  }
  return [
    `-- ${info.description}`,
    `-- @param`,
    ...info.params.map((param) => `-- ${param}`),
    `-- @return ${info.returns}`,
    ...(info.example ? [`-- @example`, ...String(Array.isArray(info.example) ? info.example.join("\n") : info.example).split("\n").map((line) => `-- ${line}`)] : []),
    "",
    `-- ${name}: ${info.signature}`,
    `function ${name}()`,
    `  -- body left intentionally empty for language-server jump target`,
    `end`,
  ].join("\n");
}

module.exports = {
  BASE_BUILTINS,
  builtinFunctionNames,
  getBuiltinFunction,
  getBuiltinFunctionByMethod,
  makeBuiltinUri,
  formatBuiltinMarkdown,
  makeBuiltinStubContent,
  setBuiltinLocale,
  getBuiltinLocale,
  applyBuiltinExtensionCatalog,
  resetBuiltinExtensions,
  getAllBuiltinMap,
};

setBuiltinLocale("en");
rebuildBuiltinMethodIndex();

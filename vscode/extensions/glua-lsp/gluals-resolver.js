const fs = require("fs");
const path = require("path");

const BUNDLED_TARGETS = new Map([
  ["darwin/amd64", ["darwin-amd64", "gluals"]],
  ["darwin/arm64", ["darwin-arm64", "gluals"]],
  ["win32/amd64", ["windows-amd64", "gluals.exe"]],
]);

function resolveGlualsExecutable(extensionPath, configuredPath, platform = process.platform, arch = process.arch) {
  const configured = String(configuredPath || "").trim();
  if (configured) {
    const resolved = path.resolve(configured);
    if (!fs.existsSync(resolved)) {
      throw new Error(`configured gluals executable does not exist: ${resolved}`);
    }
    return { path: resolved, bundled: false };
  }

  const normalizedArch = arch === "x64" ? "amd64" : arch;
  const target = BUNDLED_TARGETS.get(`${platform}/${normalizedArch}`);
  if (!target) {
    throw new Error(`gluals is not bundled for ${platform}/${arch}; configure glua.languageServerExecutable`);
  }
  const resolved = path.join(extensionPath, "bin", target[0], target[1]);
  if (!fs.existsSync(resolved)) {
    throw new Error(`bundled gluals executable is missing: ${resolved}`);
  }
  return { path: resolved, bundled: true };
}

module.exports = { BUNDLED_TARGETS, resolveGlualsExecutable };

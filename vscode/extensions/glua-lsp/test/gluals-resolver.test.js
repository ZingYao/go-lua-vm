const assert = require("assert");
const fs = require("fs");
const os = require("os");
const path = require("path");
const { resolveGlualsExecutable } = require("../gluals-resolver");

const root = fs.mkdtempSync(path.join(os.tmpdir(), "gluals-resolver-"));
for (const [directory, executable] of [
  ["darwin-amd64", "gluals"],
  ["darwin-arm64", "gluals"],
  ["linux-amd64", "gluals"],
  ["linux-arm64", "gluals"],
  ["windows-amd64", "gluals.exe"],
  ["windows-arm64", "gluals.exe"],
]) {
  const target = path.join(root, "bin", directory, executable);
  fs.mkdirSync(path.dirname(target), { recursive: true });
  fs.writeFileSync(target, "test");
}

assert.equal(resolveGlualsExecutable(root, "", "darwin", "amd64").path, path.join(root, "bin", "darwin-amd64", "gluals"));
assert.equal(resolveGlualsExecutable(root, "", "darwin", "arm64").path, path.join(root, "bin", "darwin-arm64", "gluals"));
assert.equal(resolveGlualsExecutable(root, "", "linux", "x64").path, path.join(root, "bin", "linux-amd64", "gluals"));
assert.equal(resolveGlualsExecutable(root, "", "linux", "arm64").path, path.join(root, "bin", "linux-arm64", "gluals"));
assert.equal(resolveGlualsExecutable(root, "", "win32", "x64").path, path.join(root, "bin", "windows-amd64", "gluals.exe"));
assert.equal(resolveGlualsExecutable(root, "", "win32", "arm64").path, path.join(root, "bin", "windows-arm64", "gluals.exe"));
assert.throws(() => resolveGlualsExecutable(root, "", "freebsd", "amd64"), /configure glua\.languageServerExecutable/);

const custom = path.join(root, "custom-gluals");
fs.writeFileSync(custom, "test");
assert.deepEqual(resolveGlualsExecutable(root, custom, "linux", "arm64"), { path: custom, bundled: false });

fs.rmSync(root, { recursive: true, force: true });
console.log("gluals resolver tests passed");

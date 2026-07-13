#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${repo_root}"

node scripts/test-playground-editor.js
node scripts/test-playground-workspace.js

node -e '
const fs = require("fs");
for (const file of [
  "tests/editor/golden-diagnostics.json",
  "tests/editor/golden-require-definition.json",
  "vscode/extensions/glua-lsp/server/builtin-functions.json",
  "jetbrains/extensions/glua-lsp/src/main/resources/builtin-functions.json",
]) {
  JSON.parse(fs.readFileSync(file, "utf8"));
  console.log(`json ok ${file}`);
}
'

(
  cd vscode/extensions/glua-lsp
  npm test
  npx --yes @vscode/vsce package --out /tmp/glua-lsp-vscode-test.vsix
)

jetbrains_mode="${CHECK_EDITOR_EXTENSIONS_JETBRAINS:-auto}"
if [[ "${jetbrains_mode}" == "0" || "${jetbrains_mode}" == "skip" ]]; then
  echo "skip JetBrains tests: CHECK_EDITOR_EXTENSIONS_JETBRAINS=${jetbrains_mode}"
  exit 0
fi

if ! command -v java >/dev/null 2>&1; then
  echo "skip JetBrains tests: java not found; install or select JDK 21 to run Gradle tests" >&2
  if [[ "${jetbrains_mode}" == "strict" || "${jetbrains_mode}" == "1" ]]; then
    exit 1
  fi
  exit 0
fi

java_version_output="$(java -version 2>&1 || true)"
echo "JetBrains Gradle JVM:"
echo "${java_version_output}"

(
  cd jetbrains/extensions/glua-lsp
  ./gradlew --no-daemon --no-configuration-cache test
)

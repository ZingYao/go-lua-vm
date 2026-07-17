#!/usr/bin/env bash
set -euo pipefail

if [[ "$#" -ne 2 ]]; then
  echo "usage: $0 <vscode|jetbrains> <package-path>" >&2
  exit 2
fi

package_kind="$1"
package_path="$2"

expected_entries=(
  "darwin-amd64/gluals"
  "darwin-arm64/gluals"
  "linux-amd64/gluals"
  "linux-arm64/gluals"
  "windows-amd64/gluals.exe"
  "windows-arm64/gluals.exe"
)

case "${package_kind}" in
  vscode)
    archive_entries="$(unzip -Z1 "${package_path}")"
    entry_prefix="extension/bin"
    ;;
  jetbrains)
    temporary_dir="$(mktemp -d)"
    trap 'rm -rf "${temporary_dir}"' EXIT
    unzip -q "${package_path}" -d "${temporary_dir}"
    plugin_jar="$(find "${temporary_dir}" -type f -name 'glua-jetbrains-*.jar' ! -name '*-searchableOptions.jar' -print -quit)"
    if [[ -z "${plugin_jar}" ]]; then
      echo "JetBrains plugin jar is missing from ${package_path}" >&2
      exit 1
    fi
    archive_entries="$(unzip -Z1 "${plugin_jar}")"
    entry_prefix="gluals"
    ;;
  *)
    echo "unsupported package kind: ${package_kind}" >&2
    exit 2
    ;;
esac

for expected_entry in "${expected_entries[@]}"; do
  packaged_entry="${entry_prefix}/${expected_entry}"
  if ! grep -Fxq "${packaged_entry}" <<< "${archive_entries}"; then
    echo "bundled gluals executable is missing from ${package_path}: ${packaged_entry}" >&2
    exit 1
  fi
done

echo "verified bundled gluals executables in ${package_path}"

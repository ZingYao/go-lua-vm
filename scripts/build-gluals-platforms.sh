#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${repo_root}"

vscode_output_root="vscode/extensions/glua-lsp/bin"
jetbrains_output_root="jetbrains/extensions/glua-lsp/src/main/resources/gluals"

expected_go_version="go1.26.4"
go_version="$(go version | awk '{print $3}')"
if [[ "${go_version}" != "${expected_go_version}" ]]; then
  IFS=':' read -r -a path_entries <<< "${PATH}"
  for path_entry in "${path_entries[@]}"; do
    candidate="${path_entry}/go"
    if [[ ! -x "${candidate}" ]]; then
      continue
    fi
    candidate_version="$(${candidate} version 2>/dev/null | awk '{print $3}' || true)"
    if [[ "${candidate_version}" != "${expected_go_version}" ]]; then
      continue
    fi
    export PATH="${path_entry}:${PATH}"
    hash -r
    go_version="${candidate_version}"
    break
  done
fi

if [[ "${go_version}" != "${expected_go_version}" ]]; then
  echo "go version mismatch: expected ${expected_go_version}, got ${go_version}" >&2
  echo "ensure PATH points to go1.26.4 before building gluals" >&2
  exit 1
fi

targets=(
  "darwin amd64"
  "darwin arm64"
  "linux amd64"
  "linux arm64"
  "windows amd64"
  "windows arm64"
)

for target in "${targets[@]}"; do
  os="${target%% *}"
  arch="${target##* }"
  vscode_out_dir="${vscode_output_root}/${os}-${arch}"
  jetbrains_out_dir="${jetbrains_output_root}/${os}-${arch}"
  out_name="gluals"
  if [[ "${os}" == "windows" ]]; then
    out_name="gluals.exe"
  fi

  mkdir -p "${vscode_out_dir}" "${jetbrains_out_dir}"
  CGO_ENABLED=0 GOOS="${os}" GOARCH="${arch}" go build -trimpath -o "${vscode_out_dir}/${out_name}" ./cmd/gluals
  cp "${vscode_out_dir}/${out_name}" "${jetbrains_out_dir}/${out_name}"
  echo "built ${vscode_out_dir}/${out_name}"
  echo "copied ${jetbrains_out_dir}/${out_name}"
done

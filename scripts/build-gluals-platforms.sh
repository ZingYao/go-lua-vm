#!/usr/bin/env bash
set -euo pipefail

output_root="vscode/extensions/glua-lsp/bin"

go_version="$(go version | awk '{print $3}')"
if [[ "${go_version}" != "go1.26.4" ]]; then
  echo "go version mismatch: expected go1.26.4, got ${go_version}" >&2
  echo "ensure PATH points to go1.26.4 before building gluals" >&2
  exit 1
fi

targets=(
  "darwin amd64"
  "darwin arm64"
  "linux amd64"
  "linux 386"
  "linux arm64"
  "linux arm"
  "windows amd64"
  "windows 386"
  "windows arm64"
)

for target in "${targets[@]}"; do
  os="${target%% *}"
  arch="${target##* }"
  out_dir_arch="${arch}"
  if [[ "${os}" == "linux" && "${arch}" == "arm" ]]; then
    out_dir_arch="armhf"
  fi
  out_dir="${output_root}/${os}-${out_dir_arch}"
  out_name="gluals"
  if [[ "${os}" == "windows" ]]; then
    out_name="gluals.exe"
  fi

  mkdir -p "${out_dir}"
  CGO_ENABLED=0 GOOS="${os}" GOARCH="${arch}" go build -trimpath -o "${out_dir}/${out_name}" ./cmd/gluals
  echo "built ${out_dir}/${out_name}"
done

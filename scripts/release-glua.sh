#!/usr/bin/env bash
set -euo pipefail

release_dir="${1:-dist}"
version="${GLUA_RELEASE_VERSION:-dev}"

go_version="$(go version | awk '{print $3}')"
if [[ "${go_version}" != "go1.26.4" ]]; then
  echo "go version mismatch: expected go1.26.4, got ${go_version}" >&2
  echo "ensure PATH points to go1.26.4 before building release artifacts" >&2
  exit 1
fi

platforms=(
  "darwin/amd64"
  "darwin/arm64"
  "linux/amd64"
  "linux/arm64"
  "windows/amd64"
)

mkdir -p "${release_dir}"

for platform in "${platforms[@]}"; do
  goos="${platform%/*}"
  goarch="${platform#*/}"
  artifact_name="glua-${version}-${goos}-${goarch}"
  binary_path="${release_dir}/${artifact_name}/glua"
  if [[ "${goos}" == "windows" ]]; then
    binary_path="${binary_path}.exe"
  fi

  mkdir -p "$(dirname "${binary_path}")"
  echo "building ${artifact_name}"
  CGO_ENABLED=0 GOOS="${goos}" GOARCH="${goarch}" go build -trimpath -o "${binary_path}" ./cmd/glua

  (
    cd "${release_dir}"
    if [[ -f "${artifact_name}.tar.gz" ]]; then
      rm -f "${artifact_name}.tar.gz"
    fi
    tar -czf "${artifact_name}.tar.gz" "${artifact_name}"
  )
done

echo "release artifacts written to ${release_dir}"

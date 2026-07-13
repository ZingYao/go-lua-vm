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
  "linux/amd64"
  "linux/386"
  "linux/arm64"
  "linux/arm/6"
  "linux/arm/7"
  "windows/amd64"
  "windows/386"
  "windows/arm64"
  "darwin/amd64"
  "darwin/arm64"
  "android/arm64"
)

mkdir -p "${release_dir}"

for platform in "${platforms[@]}"; do
  goos="${platform%%/*}"
  remainder="${platform#*/}"
  goarch="${remainder%%/*}"
  goarm=""
  if [[ "${remainder}" == */* ]]; then
    goarm="${remainder#*/}"
  fi
  if [[ "${goarch}" == "arm" && -n "${goarm}" ]]; then
    artifact_name="glua-${version}-${goos}-armv${goarm}"
  else
    artifact_name="glua-${version}-${goos}-${goarch}"
  fi
  binary_path="${release_dir}/${artifact_name}/glua"
  if [[ "${goos}" == "windows" ]]; then
    binary_path="${binary_path}.exe"
  fi

  mkdir -p "$(dirname "${binary_path}")"
  echo "building ${artifact_name}"
  go_env=(CGO_ENABLED=0 GOOS="${goos}" GOARCH="${goarch}")
  if [[ -n "${goarm}" ]]; then
    go_env+=(GOARM="${goarm}")
  fi
  env "${go_env[@]}" go build -trimpath -o "${binary_path}" ./cmd/glua
  cp LICENSE "${release_dir}/${artifact_name}/LICENSE"
  cp COMMERCIAL_LICENSE.md "${release_dir}/${artifact_name}/COMMERCIAL_LICENSE.md"

  (
    cd "${release_dir}"
    if [[ -f "${artifact_name}.tar.gz" ]]; then
      rm -f "${artifact_name}.tar.gz"
    fi
    tar -czf "${artifact_name}.tar.gz" "${artifact_name}"
  )
done

echo "release artifacts written to ${release_dir}"

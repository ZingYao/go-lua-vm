#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
target_goos="${TARGET_GOOS:-$(go env GOOS)}"
target_goarch="${TARGET_GOARCH:-$(go env GOARCH)}"
cc="${CC:-cc}"
build_dir="${BUILD_DIR:-${repo_root}/build/native-lpeg/${target_goos}-${target_goarch}}"
include_dir="${repo_root}/native/lua53/include"
source_dir="${repo_root}/third_party/lpeg"

sources=(
  "${source_dir}/lpvm.c"
  "${source_dir}/lpcap.c"
  "${source_dir}/lptree.c"
  "${source_dir}/lpcode.c"
  "${source_dir}/lpprint.c"
  "${source_dir}/lpcset.c"
)

echo "native lpeg source build"
echo "repo_root=${repo_root}"
echo "GOOS=${target_goos}"
echo "GOARCH=${target_goarch}"
echo "CGO_ENABLED=${CGO_ENABLED:-unset}"
echo "CC=${cc}"
echo "include_dir=${include_dir}"
echo "source_dir=${source_dir}"
echo "build_dir=${build_dir}"

if [[ ! -d "${include_dir}" ]]; then
  echo "Lua 5.3 public include directory not found: ${include_dir}" >&2
  exit 1
fi

if [[ ! -d "${source_dir}" ]]; then
  echo "LPeg source directory not found: ${source_dir}" >&2
  exit 1
fi

for source_file in "${sources[@]}"; do
  if [[ ! -f "${source_file}" ]]; then
    echo "LPeg source file not found: ${source_file}" >&2
    exit 1
  fi
done

read -r -a cc_parts <<<"${cc}"
if [[ "${#cc_parts[@]}" -eq 0 ]] || ! command -v "${cc_parts[0]}" >/dev/null 2>&1; then
  echo "skip: C compiler not found: ${cc}" >&2
  exit 0
fi

case "${target_goos}" in
  darwin)
    output_extensions=(".so" ".dylib")
    link_args=("-bundle" "-undefined" "dynamic_lookup")
    ;;
  linux)
    output_extensions=(".so")
    link_args=("-shared" "-fPIC")
    ;;
  windows)
    echo "skip: Windows LPeg build requires lua53.dll shim or import library, not implemented yet" >&2
    exit 0
    ;;
  *)
    echo "skip: unsupported native LPeg target GOOS=${target_goos}" >&2
    exit 0
    ;;
esac

mkdir -p "${build_dir}"

build_lpeg_module() {
  local extension="$1"
  local output_path="${build_dir}/lpeg${extension}"
  local args=(
    "-I" "${include_dir}"
    "-I" "${source_dir}"
    "-O2"
    "-DNDEBUG"
    "-std=c99"
    "-fPIC"
    "-Wall"
    "-o" "${output_path}"
  )

  args+=("${link_args[@]}")
  args+=("${sources[@]}")

  printf 'compile lpeg%s:' "${extension}"
  printf ' %q' "${cc_parts[@]}" "${args[@]}"
  printf '\n'

  "${cc_parts[@]}" "${args[@]}"
  echo "built ${output_path}"
}

for extension in "${output_extensions[@]}"; do
  build_lpeg_module "${extension}"
done

echo "native LPeg outputs:"
find "${build_dir}" -maxdepth 1 -type f -print | sort
echo "note: this script validates source compilation only; runtime require(\"lpeg\") acceptance is tracked separately."

#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
target_goos="${TARGET_GOOS:-$(go env GOOS)}"
target_goarch="${TARGET_GOARCH:-$(go env GOARCH)}"
cc="${CC:-cc}"
build_dir="${BUILD_DIR:-${repo_root}/build/native-fixtures/${target_goos}-${target_goarch}}"
include_dir="${repo_root}/native/lua53/include"
source_file="${repo_root}/tests/native_modules/fixtures/glua_native_smoke.c"

echo "native fixture build"
echo "repo_root=${repo_root}"
echo "GOOS=${target_goos}"
echo "GOARCH=${target_goarch}"
echo "CGO_ENABLED=${CGO_ENABLED:-unset}"
echo "CC=${cc}"
echo "include_dir=${include_dir}"
echo "source_file=${source_file}"
echo "build_dir=${build_dir}"

if [[ ! -f "${source_file}" ]]; then
  echo "fixture C source not found: ${source_file}" >&2
  exit 1
fi

if [[ ! -d "${include_dir}" ]]; then
  echo "Lua 5.3 public include directory not found: ${include_dir}" >&2
  exit 1
fi

if ! command -v "${cc}" >/dev/null 2>&1; then
  echo "skip: C compiler not found: ${cc}" >&2
  exit 0
fi

case "${target_goos}" in
  darwin)
    output_extensions=(".dylib" ".so")
    link_args=("-dynamiclib" "-undefined" "dynamic_lookup")
    ;;
  linux)
    output_extensions=(".so")
    link_args=("-shared" "-fPIC")
    ;;
  windows)
    echo "skip: Windows fixture build requires lua53.dll shim or import library, not implemented yet" >&2
    exit 0
    ;;
  *)
    echo "skip: unsupported native fixture target GOOS=${target_goos}" >&2
    exit 0
    ;;
esac

mkdir -p "${build_dir}"

build_module() {
  local module_name="$1"
  local extension="$2"
  local output_path="${build_dir}/${module_name}${extension}"
  local args=("-I" "${include_dir}" "-o" "${output_path}")

  args+=("${link_args[@]}")
  args+=("${source_file}")

  printf 'compile %s%s:' "${module_name}" "${extension}"
  printf ' %q' "${cc}" "${args[@]}"
  printf '\n'

  "${cc}" "${args[@]}"
  echo "built ${output_path}"
}

for extension in "${output_extensions[@]}"; do
  build_module "glua_native_smoke" "${extension}"
  build_module "glua_native_failopen" "${extension}"
done

echo "native fixture outputs:"
find "${build_dir}" -maxdepth 1 -type f -print | sort

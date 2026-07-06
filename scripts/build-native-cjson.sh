#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
target_goos="${TARGET_GOOS:-$(go env GOOS)}"
target_goarch="${TARGET_GOARCH:-$(go env GOARCH)}"
cc="${CC:-cc}"
build_dir="${BUILD_DIR:-${repo_root}/build/native-cjson/${target_goos}-${target_goarch}}"
include_dir="${repo_root}/native/lua53/include"
source_dir="${repo_root}/third_party/lua-cjson"

sources=(
  "${source_dir}/lua_cjson.c"
  "${source_dir}/strbuf.c"
  "${source_dir}/fpconv.c"
)

echo "native lua-cjson source build"
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
  echo "lua-cjson source directory not found: ${source_dir}" >&2
  exit 1
fi

for source_file in "${sources[@]}"; do
  if [[ ! -f "${source_file}" ]]; then
    echo "lua-cjson source file not found: ${source_file}" >&2
    exit 1
  fi
done

if ! command -v "${cc}" >/dev/null 2>&1; then
  echo "skip: C compiler not found: ${cc}" >&2
  exit 0
fi

case "${target_goos}" in
  darwin)
    output_extensions=(".so" ".dylib")
    link_args=("-dynamiclib" "-undefined" "dynamic_lookup")
    ;;
  linux)
    output_extensions=(".so")
    link_args=("-shared" "-fPIC")
    ;;
  windows)
    echo "skip: Windows lua-cjson build requires lua53.dll shim or import library, not implemented yet" >&2
    exit 0
    ;;
  *)
    echo "skip: unsupported native lua-cjson target GOOS=${target_goos}" >&2
    exit 0
    ;;
esac

mkdir -p "${build_dir}"

build_cjson_module() {
  local extension="$1"
  local output_path="${build_dir}/cjson${extension}"
  local args=(
    "-I" "${include_dir}"
    "-I" "${source_dir}"
    "-O2"
    "-Wall"
    "-DNDEBUG"
    "-fPIC"
    "-o" "${output_path}"
  )

  args+=("${link_args[@]}")
  args+=("${sources[@]}")

  printf 'compile lua-cjson%s:' "${extension}"
  printf ' %q' "${cc}" "${args[@]}"
  printf '\n'

  "${cc}" "${args[@]}"
  echo "built ${output_path}"
}

for extension in "${output_extensions[@]}"; do
  build_cjson_module "${extension}"
done

echo "native lua-cjson outputs:"
find "${build_dir}" -maxdepth 1 -type f -print | sort
echo "note: this script validates source compilation only; require(\"cjson\") runtime acceptance remains gated by Lua C API shim coverage."

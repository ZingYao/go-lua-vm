#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
target_goos="${TARGET_GOOS:-$(go env GOOS)}"
target_goarch="${TARGET_GOARCH:-$(go env GOARCH)}"
cc="${CC:-cc}"
build_dir="${BUILD_DIR:-${repo_root}/build/native-luasocket/${target_goos}-${target_goarch}}"
include_dir="${repo_root}/native/lua53/include"
source_dir="${repo_root}/third_party/luasocket"
source_src_dir="${source_dir}/src"

socket_sources=(
  "${source_src_dir}/luasocket.c"
  "${source_src_dir}/timeout.c"
  "${source_src_dir}/buffer.c"
  "${source_src_dir}/io.c"
  "${source_src_dir}/auxiliar.c"
  "${source_src_dir}/compat.c"
  "${source_src_dir}/options.c"
  "${source_src_dir}/inet.c"
  "${source_src_dir}/usocket.c"
  "${source_src_dir}/except.c"
  "${source_src_dir}/select.c"
  "${source_src_dir}/tcp.c"
  "${source_src_dir}/udp.c"
)

mime_sources=(
  "${source_src_dir}/mime.c"
  "${source_src_dir}/compat.c"
)

echo "native LuaSocket source build"
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

if [[ ! -d "${source_src_dir}" ]]; then
  echo "LuaSocket source directory not found: ${source_src_dir}" >&2
  exit 1
fi

for source_file in "${socket_sources[@]}" "${mime_sources[@]}"; do
  if [[ ! -f "${source_file}" ]]; then
    echo "LuaSocket source file not found: ${source_file}" >&2
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
    platform_cflags=("-DUNIX_HAS_SUN_LEN")
    ;;
  linux)
    output_extensions=(".so")
    link_args=("-shared" "-fPIC")
    platform_cflags=()
    ;;
  windows)
    echo "skip: Windows LuaSocket build requires lua53.dll shim or import library, not implemented yet" >&2
    exit 0
    ;;
  *)
    echo "skip: unsupported native LuaSocket target GOOS=${target_goos}" >&2
    exit 0
    ;;
esac

mkdir -p "${build_dir}/socket" "${build_dir}/mime"

build_luasocket_module() {
  local label="$1"
  local output_dir="$2"
  local extension="$3"
  shift 3

  local output_path="${output_dir}/core${extension}"
  local args=(
    "-I" "${include_dir}"
    "-I" "${source_src_dir}"
    "-O2"
    "-DNDEBUG"
    "-DLUASOCKET_NODEBUG"
    "-std=c99"
    "-fPIC"
    "-Wall"
  )

  args+=("${platform_cflags[@]}")
  args+=("-o" "${output_path}")
  args+=("${link_args[@]}")
  args+=("$@")

  printf 'compile LuaSocket %s%s:' "${label}" "${extension}"
  printf ' %q' "${cc_parts[@]}" "${args[@]}"
  printf '\n'

  "${cc_parts[@]}" "${args[@]}"
  echo "built ${output_path}"
}

for extension in "${output_extensions[@]}"; do
  build_luasocket_module "socket.core" "${build_dir}/socket" "${extension}" "${socket_sources[@]}"
  build_luasocket_module "mime.core" "${build_dir}/mime" "${extension}" "${mime_sources[@]}"
done

echo "native LuaSocket outputs:"
find "${build_dir}" -type f -print | sort
echo "note: this script validates source compilation only; run scripts/test-native-luasocket.sh for runtime require(\"socket\")/require(\"mime\") and loopback acceptance."

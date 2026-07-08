#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
host_goos="$(go env GOOS)"
host_goarch="$(go env GOARCH)"
target_goos="${TARGET_GOOS:-$(go env GOOS)}"
target_goarch="${TARGET_GOARCH:-$(go env GOARCH)}"
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

normalize_env_name() {
  echo "$1" | tr '[:lower:]/-' '[:upper:]__'
}

cc_variable_for_target() {
  echo "NATIVE_CC_$(normalize_env_name "${target_goos}_${target_goarch}")"
}

target_cc_for() {
  local cc_var="$1"
  local cc_value="${!cc_var:-}"

  if [[ -n "${cc_value}" ]]; then
    echo "${cc_value}"
    return 0
  fi

  if [[ -n "${CC+x}" ]]; then
    echo "${CC}"
    return 0
  fi

  if [[ "${target_goos}" == "${host_goos}" && "${target_goarch}" == "${host_goarch}" ]]; then
    echo "cc"
    return 0
  fi

  return 1
}

cc_var="$(cc_variable_for_target)"
cc=""
if cc="$(target_cc_for "${cc_var}")"; then
  :
fi

echo "native lpeg source build"
echo "repo_root=${repo_root}"
echo "host_GOOS=${host_goos}"
echo "host_GOARCH=${host_goarch}"
echo "GOOS=${target_goos}"
echo "GOARCH=${target_goarch}"
echo "CGO_ENABLED=${CGO_ENABLED:-unset}"
echo "CC=${cc:-unset}"
echo "CC variable=${cc_var}"
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

if [[ -z "${cc}" ]]; then
  echo "skip: no C compiler configured for ${target_goos}/${target_goarch}; set ${cc_var} or CC" >&2
  exit 0
fi

read -r -a cc_parts <<<"${cc}"
if [[ "${#cc_parts[@]}" -eq 0 ]] || ! command -v "${cc_parts[0]}" >/dev/null 2>&1; then
  echo "skip: C compiler not found for ${target_goos}/${target_goarch}: ${cc}" >&2
  exit 0
fi

lua53_import_lib=""
output_extensions=()
link_args=()
link_inputs=()
platform_cflags=()
case "${target_goos}" in
  darwin)
    output_extensions=(".so" ".dylib")
    link_args=("-bundle" "-undefined" "dynamic_lookup")
    ;;
  linux)
    output_extensions=(".so")
    link_args=("-shared" "-fPIC")
    ;;
  android)
    output_extensions=(".so")
    link_args=("-shared" "-fPIC" "-Wl,--allow-shlib-undefined")
    # Android app/native threads commonly have a much smaller C stack than macOS/Linux shells.
    # Keep LPeg's recursive capture expansion below the observed Android SIGSEGV threshold so
    # the official "subcapture nesting too deep" path is reported as a Lua error instead.
    platform_cflags=("-DMAXRECLEVEL=96")
    ;;
  windows)
    output_extensions=(".dll")
    link_args=("-shared" "-DLUA_BUILD_AS_DLL")
    import_build_dir="${LUA53_IMPORT_BUILD_DIR:-${build_dir}/lua53}"

    if [[ -n "${LUA53_IMPORT_LIB:-}" ]]; then
      if [[ ! -f "${LUA53_IMPORT_LIB}" ]]; then
        echo "Windows lua53 import library not found: ${LUA53_IMPORT_LIB}" >&2
        exit 1
      fi
      lua53_import_lib="${LUA53_IMPORT_LIB}"
    else
      TARGET_GOOS=windows TARGET_GOARCH="${target_goarch}" BUILD_DIR="${import_build_dir}" \
        "${repo_root}/scripts/build-native-windows-lua53-importlib.sh"

      for candidate in "${import_build_dir}/liblua53.dll.a" "${import_build_dir}/lua53.lib"; do
        if [[ -f "${candidate}" ]]; then
          lua53_import_lib="${candidate}"
          break
        fi
      done

      if [[ -z "${lua53_import_lib}" ]]; then
        echo "skip: Windows LPeg build requires lua53 import library; set LUA53_IMPORT_LIB or install llvm-dlltool, dlltool, lib.exe, or llvm-lib" >&2
        exit 0
      fi
    fi

    echo "lua53_import_lib=${lua53_import_lib}"
    link_inputs=("${lua53_import_lib}")
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

  args+=("${platform_cflags[@]}")
  args+=("${link_args[@]}")
  args+=("${sources[@]}")
  if [[ "${#link_inputs[@]}" -gt 0 ]]; then
    args+=("${link_inputs[@]}")
  fi

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

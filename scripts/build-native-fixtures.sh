#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
host_goos="$(go env GOOS)"
host_goarch="$(go env GOARCH)"
target_goos="${TARGET_GOOS:-$(go env GOOS)}"
target_goarch="${TARGET_GOARCH:-$(go env GOARCH)}"
build_dir="${BUILD_DIR:-${repo_root}/build/native-fixtures/${target_goos}-${target_goarch}}"
include_dir="${repo_root}/native/lua53/include"
source_file="${repo_root}/tests/native_modules/fixtures/glua_native_smoke.c"

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

echo "native fixture build"
echo "repo_root=${repo_root}"
echo "host_GOOS=${host_goos}"
echo "host_GOARCH=${host_goarch}"
echo "GOOS=${target_goos}"
echo "GOARCH=${target_goarch}"
echo "CGO_ENABLED=${CGO_ENABLED:-unset}"
echo "CC=${cc:-unset}"
echo "CC variable=${cc_var}"
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
case "${target_goos}" in
  darwin)
    output_extensions=(".dylib" ".so")
    link_args=("-dynamiclib" "-undefined" "dynamic_lookup")
    ;;
  linux)
    output_extensions=(".so")
    link_args=("-shared" "-fPIC")
    ;;
  android)
    output_extensions=(".so")
    link_args=("-shared" "-fPIC" "-Wl,--allow-shlib-undefined")
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
        echo "skip: Windows fixture build requires lua53 import library; set LUA53_IMPORT_LIB or install llvm-dlltool, dlltool, lib.exe, or llvm-lib" >&2
        exit 0
      fi
    fi

    echo "lua53_import_lib=${lua53_import_lib}"
    link_inputs=("${lua53_import_lib}")
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
  if [[ "${#link_inputs[@]}" -gt 0 ]]; then
    args+=("${link_inputs[@]}")
  fi

  printf 'compile %s%s:' "${module_name}" "${extension}"
  printf ' %q' "${cc_parts[@]}" "${args[@]}"
  printf '\n'

  "${cc_parts[@]}" "${args[@]}"
  echo "built ${output_path}"
}

for extension in "${output_extensions[@]}"; do
  build_module "glua_native_smoke" "${extension}"
  build_module "glua_native_failopen" "${extension}"
done

echo "native fixture outputs:"
find "${build_dir}" -maxdepth 1 -type f -print | sort

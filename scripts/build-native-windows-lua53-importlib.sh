#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
target_goos="${TARGET_GOOS:-windows}"
target_goarch="${TARGET_GOARCH:-$(go env GOARCH)}"
build_dir="${BUILD_DIR:-${repo_root}/build/native-windows-lua53/${target_goos}-${target_goarch}}"
def_file="${DEF_FILE:-${repo_root}/native/lua53/windows/lua53.def}"
requested_tool="${NATIVE_WINDOWS_IMPORT_TOOL:-}"
requested_kind="${NATIVE_WINDOWS_IMPORT_TOOL_KIND:-}"

echo "native Windows lua53 import library build"
echo "repo_root=${repo_root}"
echo "GOOS=${target_goos}"
echo "GOARCH=${target_goarch}"
echo "CGO_ENABLED=${CGO_ENABLED:-unset}"
echo "def_file=${def_file}"
echo "build_dir=${build_dir}"

expected_go_version="go1.26.4"
actual_go_version="$(go version | awk '{print $3}')"
if [[ "${actual_go_version}" != "${expected_go_version}" ]]; then
  echo "go version mismatch: expected ${expected_go_version}, got ${actual_go_version}" >&2
  echo "ensure PATH resolves go to ${expected_go_version} before running native Windows import library builds" >&2
  exit 1
fi

if [[ "${target_goos}" != "windows" ]]; then
  echo "skip: Windows lua53 import library build requires TARGET_GOOS=windows, got ${target_goos}" >&2
  exit 0
fi

if [[ ! -f "${def_file}" ]]; then
  echo "Windows lua53.def file not found: ${def_file}" >&2
  exit 1
fi

"${repo_root}/scripts/check-native-windows-def.sh"

dlltool_machine_for() {
  case "$1" in
    amd64)
      echo "i386:x86-64"
      ;;
    386)
      echo "i386"
      ;;
    arm64)
      echo "arm64"
      ;;
    arm)
      echo "arm"
      ;;
    *)
      return 1
      ;;
  esac
}

msvc_machine_for() {
  case "$1" in
    amd64)
      echo "x64"
      ;;
    386)
      echo "x86"
      ;;
    arm64)
      echo "arm64"
      ;;
    arm)
      echo "arm"
      ;;
    *)
      return 1
      ;;
  esac
}

tool_kind_for() {
  local tool_name="$1"
  local base_name

  base_name="$(basename "${tool_name}")"
  case "${base_name}" in
    *dlltool*)
      echo "dlltool"
      ;;
    lib.exe|llvm-lib)
      echo "msvc-lib"
      ;;
    *)
      return 1
      ;;
  esac
}

tool_command=()
tool_kind=""
if [[ -n "${requested_tool}" ]]; then
  read -r -a tool_command <<<"${requested_tool}"
  if [[ "${#tool_command[@]}" -eq 0 ]] || ! command -v "${tool_command[0]}" >/dev/null 2>&1; then
    echo "skip: Windows lua53 import library tool not found: ${requested_tool}" >&2
    exit 0
  fi
else
  for candidate in llvm-dlltool dlltool lib.exe llvm-lib; do
    if command -v "${candidate}" >/dev/null 2>&1; then
      tool_command=("${candidate}")
      break
    fi
  done

  if [[ "${#tool_command[@]}" -eq 0 ]]; then
    echo "skip: Windows lua53 import library build requires llvm-dlltool, dlltool, lib.exe, or llvm-lib" >&2
    exit 0
  fi
fi

if [[ -n "${requested_kind}" ]]; then
  case "${requested_kind}" in
    dlltool|msvc-lib)
      tool_kind="${requested_kind}"
      ;;
    *)
      echo "unsupported NATIVE_WINDOWS_IMPORT_TOOL_KIND=${requested_kind}; expected dlltool or msvc-lib" >&2
      exit 1
      ;;
  esac
else
  if ! tool_kind="$(tool_kind_for "${tool_command[0]}")"; then
    echo "unsupported Windows lua53 import library tool: ${tool_command[0]}" >&2
    echo "set NATIVE_WINDOWS_IMPORT_TOOL_KIND=dlltool or msvc-lib when using a wrapper command" >&2
    exit 1
  fi
fi

mkdir -p "${build_dir}"

case "${tool_kind}" in
  dlltool)
    if ! machine="$(dlltool_machine_for "${target_goarch}")"; then
      echo "skip: unsupported Windows dlltool target GOARCH=${target_goarch}" >&2
      exit 0
    fi
    output_path="${OUTPUT_PATH:-${build_dir}/liblua53.dll.a}"
    args=("-d" "${def_file}" "-l" "${output_path}" "-m" "${machine}")
    ;;
  msvc-lib)
    if ! machine="$(msvc_machine_for "${target_goarch}")"; then
      echo "skip: unsupported Windows MSVC import library target GOARCH=${target_goarch}" >&2
      exit 0
    fi
    output_path="${OUTPUT_PATH:-${build_dir}/lua53.lib}"
    args=("/def:${def_file}" "/machine:${machine}" "/out:${output_path}")
    ;;
esac

echo "tool=${tool_command[*]}"
echo "tool_kind=${tool_kind}"
echo "machine=${machine}"
echo "output_path=${output_path}"

printf 'generate Windows lua53 import library:'
printf ' %q' "${tool_command[@]}" "${args[@]}"
printf '\n'

"${tool_command[@]}" "${args[@]}"

if [[ ! -f "${output_path}" ]]; then
  echo "Windows lua53 import library was not produced: ${output_path}" >&2
  exit 1
fi

echo "built ${output_path}"
echo "note: this script only creates the Windows link-time import library; Windows .dll require runtime acceptance remains tracked separately."

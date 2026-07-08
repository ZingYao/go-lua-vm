#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
target_goos="${TARGET_GOOS:-$(go env GOOS)}"
target_goarch="${TARGET_GOARCH:-$(go env GOARCH)}"
build_dir="${BUILD_DIR:-${repo_root}/build/native-windows-lua53-shim/${target_goos}-${target_goarch}}"
def_file="${repo_root}/native/lua53/windows/lua53.def"
source_file="${build_dir}/lua53_proxy.c"
output_dll="${OUTPUT_DLL:-${build_dir}/lua53.dll}"
output_import_lib="${OUTPUT_IMPORT_LIB:-${build_dir}/liblua53.dll.a}"

echo "native Windows lua53 runtime shim build"
echo "repo_root=${repo_root}"
echo "GOOS=${target_goos}"
echo "GOARCH=${target_goarch}"
echo "CGO_ENABLED=${CGO_ENABLED:-unset}"
echo "build_dir=${build_dir}"

expected_go_version="go1.26.4"
actual_go_version="$(go version | awk '{print $3}')"
if [[ "${actual_go_version}" != "${expected_go_version}" ]]; then
  echo "go version mismatch: expected ${expected_go_version}, got ${actual_go_version}" >&2
  echo "ensure PATH resolves go to ${expected_go_version} before building native Windows lua53 shim" >&2
  exit 1
fi

if [[ "${target_goos}" != "windows" ]]; then
  echo "skip: Windows lua53 runtime shim build requires TARGET_GOOS=windows, got ${target_goos}" >&2
  exit 0
fi

case "${target_goarch}" in
  amd64)
    ;;
  *)
    echo "skip: unsupported Windows lua53 runtime shim target GOARCH=${target_goarch}" >&2
    exit 0
    ;;
esac

"${repo_root}/scripts/check-native-windows-def.sh"

cc_var="NATIVE_CC_WINDOWS_$(echo "${target_goarch}" | tr '[:lower:]-' '[:upper:]_')"
cc_value="${!cc_var:-${CC:-gcc}}"
read -r -a cc_command <<<"${cc_value}"
if ! command -v "${cc_command[0]}" >/dev/null 2>&1; then
  echo "skip: Windows lua53 runtime shim compiler not found: ${cc_command[0]}" >&2
  exit 0
fi

mkdir -p "${build_dir}"

mapfile -t symbols < <(
  sed -n '/^EXPORTS$/,$p' "${def_file}" \
    | tail -n +2 \
    | sed -E 's/;.*$//' \
    | awk '{$1=$1; print}' \
    | sed '/^$/d' \
    | sort -u
)

cat >"${source_file}" <<'CHEAD'
#include <windows.h>
#include <stddef.h>
#include <stdarg.h>
#include <stdio.h>
#include <stdlib.h>

typedef struct lua_State lua_State;
typedef ptrdiff_t lua_KContext;
typedef int (*lua_KFunction)(lua_State *L, int status, lua_KContext ctx);
typedef int (*glua_error_record_fn)(lua_State *L);
typedef int (*glua_argerror_record_fn)(lua_State *L, int arg, const char *extra);
typedef int (*glua_error_message_fn)(lua_State *L, const char *message);
typedef const char* (*glua_pushfstring_message_fn)(lua_State *L, const char *message);
typedef int (*glua_callk_record_fn)(lua_State *L, int argument_count, int result_count);
typedef void (*glua_error_jump_fn)(void);

static glua_error_record_fn glua_lua_error_record_ptr;
static glua_argerror_record_fn glua_luaL_argerror_record_ptr;
static glua_error_message_fn glua_luaL_error_message_ptr;
static glua_pushfstring_message_fn glua_lua_pushfstring_message_ptr;
static glua_callk_record_fn glua_lua_callk_record_ptr;
static glua_error_jump_fn glua_lua_error_jump_ptr;

static FARPROC glua_resolve(const char *name) {
  HMODULE module = GetModuleHandleW(NULL);
  FARPROC proc = module == NULL ? NULL : GetProcAddress(module, name);
  if (proc == NULL) { ExitProcess(127); }
  return proc;
}

__declspec(dllexport) void lua_callk(lua_State *L, int argument_count, int result_count, lua_KContext context, lua_KFunction continuation) {
  (void)context;
  (void)continuation;
  if (glua_lua_callk_record_ptr(L, argument_count, result_count) != 0) { glua_lua_error_jump_ptr(); }
}

__declspec(dllexport) int lua_error(lua_State *L) {
  glua_lua_error_record_ptr(L);
  glua_lua_error_jump_ptr();
  return 0;
}

__declspec(dllexport) int luaL_argerror(lua_State *L, int arg, const char *extra) {
  glua_luaL_argerror_record_ptr(L, arg, extra);
  glua_lua_error_jump_ptr();
  return 0;
}

__declspec(dllexport) int luaL_error(lua_State *L, const char *fmt, ...) {
  char stack_buffer[512];
  va_list args;
  va_start(args, fmt);
  int required = vsnprintf(NULL, 0, fmt, args);
  va_end(args);
  if (required < 0) { glua_luaL_error_message_ptr(L, "native luaL_error formatting failed"); glua_lua_error_jump_ptr(); return 0; }
  if ((size_t)required < sizeof(stack_buffer)) {
    va_start(args, fmt);
    vsnprintf(stack_buffer, sizeof(stack_buffer), fmt, args);
    va_end(args);
    stack_buffer[sizeof(stack_buffer) - 1] = '\0';
    glua_luaL_error_message_ptr(L, stack_buffer);
    glua_lua_error_jump_ptr();
    return 0;
  }
  char *heap_buffer = (char*)malloc((size_t)required + 1);
  if (heap_buffer == NULL) { glua_luaL_error_message_ptr(L, "native luaL_error memory allocation failed"); glua_lua_error_jump_ptr(); return 0; }
  va_start(args, fmt);
  vsnprintf(heap_buffer, (size_t)required + 1, fmt, args);
  va_end(args);
  heap_buffer[required] = '\0';
  glua_luaL_error_message_ptr(L, heap_buffer);
  free(heap_buffer);
  glua_lua_error_jump_ptr();
  return 0;
}

static const char* glua_push_formatted_string(lua_State *L, const char *fmt, va_list args) {
  char stack_buffer[512];
  if (fmt == NULL) { return glua_lua_pushfstring_message_ptr(L, ""); }
  va_list count_args;
  va_copy(count_args, args);
  int required = vsnprintf(NULL, 0, fmt, count_args);
  va_end(count_args);
  if (required < 0) { return glua_lua_pushfstring_message_ptr(L, "native lua_pushfstring formatting failed"); }
  if ((size_t)required < sizeof(stack_buffer)) {
    va_list format_args;
    va_copy(format_args, args);
    vsnprintf(stack_buffer, sizeof(stack_buffer), fmt, format_args);
    va_end(format_args);
    stack_buffer[sizeof(stack_buffer) - 1] = '\0';
    return glua_lua_pushfstring_message_ptr(L, stack_buffer);
  }
  char *heap_buffer = (char*)malloc((size_t)required + 1);
  if (heap_buffer == NULL) { return glua_lua_pushfstring_message_ptr(L, "native lua_pushfstring memory allocation failed"); }
  va_list format_args;
  va_copy(format_args, args);
  vsnprintf(heap_buffer, (size_t)required + 1, fmt, format_args);
  va_end(format_args);
  heap_buffer[required] = '\0';
  const char *result = glua_lua_pushfstring_message_ptr(L, heap_buffer);
  free(heap_buffer);
  return result;
}

__declspec(dllexport) const char *lua_pushvfstring(lua_State *L, const char *fmt, va_list argp) {
  return glua_push_formatted_string(L, fmt, argp);
}

__declspec(dllexport) const char *lua_pushfstring(lua_State *L, const char *fmt, ...) {
  va_list args;
  va_start(args, fmt);
  const char *result = glua_push_formatted_string(L, fmt, args);
  va_end(args);
  return result;
}
CHEAD

for symbol in "${symbols[@]}"; do
  case "${symbol}" in
    glua_*|lua_callk|lua_error|luaL_argerror|luaL_error|lua_pushfstring|lua_pushvfstring)
      continue
      ;;
  esac
  {
    printf 'void *target_%s __attribute__((used));\n' "${symbol}"
    printf '__declspec(dllexport) __attribute__((naked)) void %s(void) { __asm__("jmp *target_%s(%%rip)"); }\n' "${symbol}" "${symbol}"
  } >>"${source_file}"
done

cat >>"${source_file}" <<'CMAIN'
BOOL WINAPI DllMain(HINSTANCE instance, DWORD reason, LPVOID reserved) {
  (void)instance; (void)reserved;
  if (reason == DLL_PROCESS_ATTACH) {
    glua_lua_error_record_ptr = (glua_error_record_fn)glua_resolve("glua_lua_error_record");
    glua_luaL_argerror_record_ptr = (glua_argerror_record_fn)glua_resolve("glua_luaL_argerror_record");
    glua_luaL_error_message_ptr = (glua_error_message_fn)glua_resolve("glua_luaL_error_message");
    glua_lua_pushfstring_message_ptr = (glua_pushfstring_message_fn)glua_resolve("glua_lua_pushfstring_message");
    glua_lua_callk_record_ptr = (glua_callk_record_fn)glua_resolve("glua_lua_callk_record");
    glua_lua_error_jump_ptr = (glua_error_jump_fn)glua_resolve("glua_lua_error_jump");
CMAIN

for symbol in "${symbols[@]}"; do
  case "${symbol}" in
    glua_*|lua_callk|lua_error|luaL_argerror|luaL_error|lua_pushfstring|lua_pushvfstring)
      continue
      ;;
  esac
  printf '    target_%s = (void*)glua_resolve("%s");\n' "${symbol}" "${symbol}" >>"${source_file}"
done

cat >>"${source_file}" <<'CEND'
  }
  return TRUE;
}
CEND

printf 'compile lua53.dll runtime shim:'
printf ' %q' "${cc_command[@]}" -shared -O2 -o "${output_dll}" "${source_file}" "-Wl,--out-implib,${output_import_lib}"
printf '\n'
"${cc_command[@]}" -shared -O2 -o "${output_dll}" "${source_file}" "-Wl,--out-implib,${output_import_lib}"

if [[ ! -f "${output_dll}" || ! -f "${output_import_lib}" ]]; then
  echo "Windows lua53 runtime shim outputs missing" >&2
  echo "  dll=${output_dll}" >&2
  echo "  import_lib=${output_import_lib}" >&2
  exit 1
fi

echo "built ${output_dll}"
echo "built ${output_import_lib}"

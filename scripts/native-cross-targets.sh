#!/usr/bin/env bash

# Shared native_modules cross targets and C compiler defaults.
#
# Target syntax is GOOS/GOARCH or GOOS/GOARCH/GOARM. The list mirrors the
# release workflow matrix so native_modules compile checks do not quietly cover
# a smaller platform surface than the no-CGO CLI artifacts.

native_release_targets() {
  cat <<'TARGETS'
linux/amd64
linux/386
linux/arm64
linux/arm/6
linux/arm/7
windows/amd64
windows/386
windows/arm64
darwin/amd64
darwin/arm64
android/arm64
TARGETS
}

native_join_release_targets() {
  native_release_targets | paste -sd ' ' -
}

native_target_goos() {
  local target="$1"
  echo "${target%%/*}"
}

native_target_goarch() {
  local target="$1"
  local remainder="${target#*/}"
  echo "${remainder%%/*}"
}

native_target_goarm() {
  local target="$1"
  local remainder="${target#*/}"
  if [[ "${remainder}" == */* ]]; then
    echo "${remainder#*/}"
  fi
}

native_target_name() {
  local target="$1"
  local goos
  local goarch
  local goarm

  goos="$(native_target_goos "${target}")"
  goarch="$(native_target_goarch "${target}")"
  goarm="$(native_target_goarm "${target}")"
  if [[ "${goarch}" == "arm" && -n "${goarm}" ]]; then
    echo "${goos}-armv${goarm}"
    return 0
  fi

  echo "${goos}-${goarch}"
}

native_target_env_suffix() {
  local goos="$1"
  local goarch="$2"
  local goarm="${3:-}"
  local suffix

  if [[ "${goarch}" == "arm" && -n "${goarm}" ]]; then
    suffix="${goos}_armv${goarm}"
  else
    suffix="${goos}_${goarch}"
  fi

  echo "${suffix}" | tr '[:lower:]-' '[:upper:]_'
}

native_target_cc_var() {
  local goos="$1"
  local goarch="$2"
  local goarm="${3:-}"

  echo "NATIVE_CC_$(native_target_env_suffix "${goos}" "${goarch}" "${goarm}")"
}

native_export_if_unset() {
  local name="$1"
  local value="$2"

  if [[ -z "${!name:-}" ]]; then
    export "${name}=${value}"
  fi
}

native_configure_zig_compilers() {
  if ! command -v zig >/dev/null 2>&1; then
    return 0
  fi

  native_export_if_unset NATIVE_CC_LINUX_AMD64 "zig cc -target x86_64-linux-gnu"
  native_export_if_unset NATIVE_CC_LINUX_386 "zig cc -target x86-linux-gnu"
  native_export_if_unset NATIVE_CC_LINUX_ARM64 "zig cc -target aarch64-linux-gnu"
  native_export_if_unset NATIVE_CC_LINUX_ARMV6 "zig cc -target arm-linux-gnueabihf -mcpu=arm1176jzf_s"
  native_export_if_unset NATIVE_CC_LINUX_ARMV7 "zig cc -target arm-linux-gnueabihf -mcpu=cortex_a7"

  native_export_if_unset NATIVE_CC_WINDOWS_AMD64 "zig cc -target x86_64-windows-gnu"
  native_export_if_unset NATIVE_CC_WINDOWS_386 "zig cc -target x86-windows-gnu"
  native_export_if_unset NATIVE_CC_WINDOWS_ARM64 "zig cc -target aarch64-windows-gnu"

  # Zig can drive simple Darwin cross links when a usable Apple SDK is present.
  # GitHub-hosted macOS runners use the clang defaults below for Darwin targets.
  native_export_if_unset NATIVE_CC_DARWIN_AMD64 "zig cc -target x86_64-macos"
  native_export_if_unset NATIVE_CC_DARWIN_ARM64 "zig cc -target aarch64-macos"
}

native_configure_macos_compilers() {
  if [[ "$(go env GOOS 2>/dev/null || true)" != "darwin" ]]; then
    return 0
  fi

  if ! command -v xcrun >/dev/null 2>&1; then
    return 0
  fi

  native_export_if_unset NATIVE_CC_DARWIN_AMD64 "xcrun clang -arch x86_64"
  native_export_if_unset NATIVE_CC_DARWIN_ARM64 "xcrun clang -arch arm64"
}

native_android_ndk_home() {
  if [[ -n "${ANDROID_NDK_HOME:-}" ]]; then
    echo "${ANDROID_NDK_HOME}"
    return 0
  fi
  if [[ -n "${ANDROID_NDK_ROOT:-}" ]]; then
    echo "${ANDROID_NDK_ROOT}"
    return 0
  fi
  return 1
}

native_android_ndk_host_tag() {
  local host_goos
  local host_goarch

  host_goos="$(go env GOOS 2>/dev/null || true)"
  host_goarch="$(go env GOARCH 2>/dev/null || true)"
  case "${host_goos}/${host_goarch}" in
    linux/amd64)
      echo "linux-x86_64"
      ;;
    darwin/amd64)
      echo "darwin-x86_64"
      ;;
    darwin/arm64)
      echo "darwin-arm64"
      ;;
    windows/amd64)
      echo "windows-x86_64"
      ;;
    *)
      return 1
      ;;
  esac
}

native_configure_android_ndk_compilers() {
  local ndk_home
  local host_tag
  local clang_path

  if ! ndk_home="$(native_android_ndk_home)"; then
    return 0
  fi
  if ! host_tag="$(native_android_ndk_host_tag)"; then
    return 0
  fi

  clang_path="${ndk_home}/toolchains/llvm/prebuilt/${host_tag}/bin/aarch64-linux-android24-clang"
  if [[ -x "${clang_path}" ]]; then
    native_export_if_unset NATIVE_CC_ANDROID_ARM64 "${clang_path}"
    return 0
  fi

  if [[ -x "${clang_path}.cmd" ]]; then
    native_export_if_unset NATIVE_CC_ANDROID_ARM64 "${clang_path}.cmd"
  fi
}

native_configure_github_actions_toolchains() {
  native_export_if_unset NATIVE_CROSS_TARGETS "$(native_join_release_targets)"
  native_export_if_unset NATIVE_SOURCE_BUILD_TARGETS "$(native_join_release_targets)"
  native_configure_macos_compilers
  native_configure_android_ndk_compilers
  native_configure_zig_compilers
}

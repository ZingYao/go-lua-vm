#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "${repo_root}/scripts/native-cross-targets.sh"

mode="check"
for arg in "$@"; do
  case "${arg}" in
    --install)
      mode="install"
      ;;
    --emit-env)
      mode="emit-env"
      ;;
    --emit-github-env)
      mode="emit-github-env"
      ;;
    --help|-h)
      cat <<'USAGE'
Usage: scripts/bootstrap-native-toolchains.sh [--install] [--emit-env]

Checks or prepares the C toolchain environment used by Native CGO cross
compile scripts. The target matrix mirrors .github/workflows/release.yml.

  --install   install managed tools through mise when they are missing
  --emit-env         print shell export commands after discovering toolchains
  --emit-github-env  print GitHub Actions environment file entries
USAGE
      exit 0
      ;;
    *)
      echo "unknown argument: ${arg}" >&2
      exit 2
      ;;
  esac
done

if [[ "${mode}" == "install" ]]; then
  if command -v mise >/dev/null 2>&1; then
    mise install
    eval "$(mise env -s bash)"
  else
    echo "skip: mise not found; install mise or provide go/zig on PATH" >&2
  fi
fi

native_configure_github_actions_toolchains

selected_native_targets() {
  if [[ -n "${NATIVE_CROSS_TARGETS:-}" ]]; then
    for target in ${NATIVE_CROSS_TARGETS}; do
      echo "${target}"
    done
    return 0
  fi

  native_release_targets
}

if [[ "${mode}" == "emit-env" ]]; then
  echo "export NATIVE_CROSS_TARGETS='${NATIVE_CROSS_TARGETS}'"
  echo "export NATIVE_SOURCE_BUILD_TARGETS='${NATIVE_SOURCE_BUILD_TARGETS}'"
  while IFS= read -r target; do
    goos="$(native_target_goos "${target}")"
    goarch="$(native_target_goarch "${target}")"
    goarm="$(native_target_goarm "${target}")"
    cc_var="$(native_target_cc_var "${goos}" "${goarch}" "${goarm}")"
    if [[ -n "${!cc_var:-}" ]]; then
      printf "export %s='%s'\n" "${cc_var}" "${!cc_var}"
    fi
  done < <(selected_native_targets)
  exit 0
fi

if [[ "${mode}" == "emit-github-env" ]]; then
  echo "NATIVE_CROSS_TARGETS=${NATIVE_CROSS_TARGETS}"
  echo "NATIVE_SOURCE_BUILD_TARGETS=${NATIVE_SOURCE_BUILD_TARGETS}"
  while IFS= read -r target; do
    goos="$(native_target_goos "${target}")"
    goarch="$(native_target_goarch "${target}")"
    goarm="$(native_target_goarm "${target}")"
    cc_var="$(native_target_cc_var "${goos}" "${goarch}" "${goarm}")"
    if [[ -n "${!cc_var:-}" ]]; then
      printf "%s=%s\n" "${cc_var}" "${!cc_var}"
    fi
  done < <(selected_native_targets)
  exit 0
fi

echo "native toolchain bootstrap"
echo "repo_root=${repo_root}"
echo "targets=${NATIVE_CROSS_TARGETS}"
echo "go=$(go version 2>/dev/null || echo missing)"
echo "zig=$(zig version 2>/dev/null || echo missing)"

missing=0
while IFS= read -r target; do
  goos="$(native_target_goos "${target}")"
  goarch="$(native_target_goarch "${target}")"
  goarm="$(native_target_goarm "${target}")"
  target_name="$(native_target_name "${target}")"
  cc_var="$(native_target_cc_var "${goos}" "${goarch}" "${goarm}")"
  cc_value="${!cc_var:-}"

  if [[ -z "${cc_value}" ]]; then
    echo "missing ${target_name}: ${cc_var} is not configured"
    missing=$((missing + 1))
    continue
  fi

  read -r cc_executable _ <<<"${cc_value}"
  if command -v "${cc_executable}" >/dev/null 2>&1; then
    echo "ok ${target_name}: ${cc_var}=${cc_value}"
  else
    echo "missing ${target_name}: compiler executable not found in ${cc_value}"
    missing=$((missing + 1))
  fi
done < <(selected_native_targets)

if [[ "${missing}" -gt 0 ]]; then
  echo "native toolchain bootstrap completed with missing compilers: ${missing}" >&2
  exit 1
fi

echo "native toolchain bootstrap completed"

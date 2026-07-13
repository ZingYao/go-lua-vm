#!/usr/bin/env bash
set -euo pipefail

# 仅校验稳定的分配预算，不比较不同 CI 机器的 ns/op，避免把调度波动误判为性能退化。
repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
baseline_file="${repository_root}/scripts/performance-baseline.tsv"
output_file="$(mktemp "${TMPDIR:-/tmp}/glua-performance-baseline.XXXXXX")"
trap 'rm -f "${output_file}"' EXIT

CGO_ENABLED=0 go -C "${repository_root}" test ./runtime ./lua -run '^$' -bench '^(BenchmarkVMDispatch|BenchmarkProgressEventSyncDispatch|BenchmarkProgressEventAsyncQueueFlush)$' -benchmem -benchtime=10x -count=1 | tee "${output_file}"

status=0
while IFS=$'\t' read -r benchmark max_bytes max_allocs; do
  [[ -z "${benchmark}" || "${benchmark}" == \#* ]] && continue
  line="$(awk -v name="${benchmark}" '$1 ~ ("^" name "-") { print; exit }' "${output_file}")"
  if [[ -z "${line}" ]]; then
    echo "missing benchmark result: ${benchmark}" >&2
    status=1
    continue
  fi
  bytes="$(awk '{ for (field = 1; field <= NF; field++) if ($field == "B/op") print $(field - 1) }' <<<"${line}")"
  allocs="$(awk '{ for (field = 1; field <= NF; field++) if ($field == "allocs/op") print $(field - 1) }' <<<"${line}")"
  if [[ -z "${bytes}" || -z "${allocs}" || "${bytes}" -gt "${max_bytes}" || "${allocs}" -gt "${max_allocs}" ]]; then
    echo "performance allocation budget exceeded: ${benchmark} bytes=${bytes:-?}/${max_bytes} allocs=${allocs:-?}/${max_allocs}" >&2
    status=1
  fi
done <"${baseline_file}"

[[ "${status}" -eq 0 ]] || exit "${status}"
printf 'GLUA_PERFORMANCE_BASELINE_OK\n'

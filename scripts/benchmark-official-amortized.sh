#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/.." && pwd)"

lua_bin="${LUA_BIN:-lua}"
glua_bin="${GLUA_BIN:-${repo_root}/bin/glua}"
iterations="${BENCH_ITERATIONS:-5}"
warmup_iterations="${BENCH_WARMUP_ITERATIONS:-1}"
runtime_repetitions="${BENCH_RUNTIME_REPETITIONS:-30}"
compile_repetitions="${BENCH_COMPILE_REPETITIONS:-10}"

require_tool() {
  local label="$1"
  local path="$2"
  if ! command -v "${path}" >/dev/null 2>&1 && [[ ! -x "${path}" ]]; then
    echo "${label} not found or not executable: ${path}" >&2
    return 1
  fi
}

require_lua536() {
  local label="$1"
  local path="$2"
  local version
  version="$("${path}" -v 2>&1 || true)"
  if [[ "${version}" != *"Lua 5.3.6"* ]]; then
    echo "${label} must be Lua 5.3.6, got: ${version}" >&2
    echo "Set LUA_BIN to official Lua 5.3.6." >&2
    return 1
  fi
}

require_tool "official lua" "${lua_bin}"
require_tool "glua" "${glua_bin}"
require_lua536 "official lua" "${lua_bin}"

LUA_BIN="${lua_bin}" \
GLUA_BIN="${glua_bin}" \
BENCH_ITERATIONS="${iterations}" \
BENCH_WARMUP_ITERATIONS="${warmup_iterations}" \
BENCH_RUNTIME_REPETITIONS="${runtime_repetitions}" \
BENCH_COMPILE_REPETITIONS="${compile_repetitions}" \
python3 - <<'PY'
import os
import statistics
import subprocess
import tempfile
import textwrap
import time

lua_bin = os.environ["LUA_BIN"]
glua_bin = os.environ["GLUA_BIN"]
iterations = int(os.environ["BENCH_ITERATIONS"])
warmup_iterations = int(os.environ["BENCH_WARMUP_ITERATIONS"])
runtime_repetitions = int(os.environ["BENCH_RUNTIME_REPETITIONS"])
compile_repetitions = int(os.environ["BENCH_COMPILE_REPETITIONS"])

fixtures = {
    "arith_add_loop": """local sum = 0
for i = 1, 1000000 do
  sum = sum + i
end
return sum
""",
    "arith_mix_loop": """local sum = 0
for i = 1, 400000 do
  sum = sum + i * 3 - 7
  sum = sum // 2 + i % 5
end
return sum
""",
    "arith_chain_temp": """local sum = 0
for i = 1, 1000000 do
  sum = sum + i * 3 - 7
end
return sum
""",
    "table_rw": """local t = {}
for i = 1, 200000 do
  t[i] = i
end
local sum = 0
for i = 1, 200000 do
  sum = sum + t[i]
end
return sum
""",
    "function_call": """local function add(a, b) return a + b end
local sum = 0
for i = 1, 200000 do
  sum = add(sum, i)
end
return sum
""",
    "string_concat": """local s = ''
for i = 1, 8000 do
  s = s .. 'x'
end
return #s
""",
    "closure_upvalue": """local x = 0
local function inc(v) x = x + v; return x end
local sum = 0
for i = 1, 200000 do
  sum = inc(1)
end
return sum
""",
    "stdlib_math_string": """local sum = 0
for i = 1, 80000 do
  sum = sum + math.floor(math.sqrt(i)) + #string.format('%d', i)
end
return sum
""",
    "recursion": """local function fib(n)
  if n < 2 then return n end
  return fib(n - 1) + fib(n - 2)
end
local sum = 0
for i = 1, 16 do
  sum = sum + fib(15)
end
return sum
""",
}

compile_source = "\n".join(
    f"function f{index}(x) return x + {index} end" for index in range(3000)
) + "\nreturn f2999(1)\n"


def lua_literal(source):
    return "[==[\n" + source + "\n]==]"


def runtime_harness(name, source):
    return textwrap.dedent(
        f"""
        local repetitions = tonumber(arg[1])
        local bench = assert(load({lua_literal(source)}, "@{name}.lua"))
        local checksum = 0
        for _ = 1, repetitions do
          local result = bench()
          if type(result) == "number" then
            checksum = checksum + result
          end
        end
        if checksum == -1 then
          print(checksum)
        end
        """
    ).lstrip()


def compile_harness(source):
    return textwrap.dedent(
        f"""
        local repetitions = tonumber(arg[1])
        local source = {lua_literal(source)}
        local last
        for _ = 1, repetitions do
          last = assert(load(source, "@compile_3000_functions.lua"))
        end
        local result = last()
        if result ~= 3000 then
          error("unexpected compile result: " .. tostring(result))
        end
        """
    ).lstrip()


def run_once(command):
    start = time.perf_counter()
    result = subprocess.run(command, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    elapsed = time.perf_counter() - start
    if result.returncode != 0:
        raise RuntimeError(f"command failed with exit {result.returncode}: {command!r}")
    return elapsed


def median_time(command, count):
    for _ in range(warmup_iterations):
        run_once(command)
    values = [run_once(command) for _ in range(count)]
    return statistics.median(values)


with tempfile.TemporaryDirectory(prefix="go-lua-vm-bench-amortized-") as tmpdir:
    harness_paths = {}
    for name, source in fixtures.items():
        path = os.path.join(tmpdir, name + "_amortized.lua")
        with open(path, "w", encoding="utf-8") as handle:
            handle.write(runtime_harness(name, source))
        harness_paths[name] = path

    compile_path = os.path.join(tmpdir, "compile_3000_functions_amortized.lua")
    with open(compile_path, "w", encoding="utf-8") as handle:
        handle.write(compile_harness(compile_source))

    print(f"runtime_repetitions={runtime_repetitions}")
    print(f"compile_repetitions={compile_repetitions}")
    print(f"iterations={iterations}")
    print("| 用例 | 官方工具中位数 | 本项目中位数 | 本项目/官方 |")
    print("| --- | ---: | ---: | ---: |")
    for name, path in harness_paths.items():
        official = median_time([lua_bin, path, str(runtime_repetitions)], iterations)
        project = median_time([glua_bin, path, str(runtime_repetitions)], iterations)
        print(f"| `{name}` | {official:.6f}s | {project:.6f}s | {project / official:.2f}x |")

    official_compile = median_time([lua_bin, compile_path, str(compile_repetitions)], iterations)
    project_compile = median_time([glua_bin, compile_path, str(compile_repetitions)], iterations)
    print(f"| `compile_3000_functions` | {official_compile:.6f}s | {project_compile:.6f}s | {project_compile / official_compile:.2f}x |")
PY

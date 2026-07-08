#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
target_goos="${TARGET_GOOS:-$(go env GOOS)}"
target_goarch="${TARGET_GOARCH:-$(go env GOARCH)}"
build_dir="${BUILD_DIR:-${repo_root}/build/native-luasocket/${target_goos}-${target_goarch}}"
if [[ "${target_goos}" == "windows" && -z "${GLUA_BIN:-}" ]]; then
  glua_bin="${build_dir}/glua-native.exe"
else
  glua_bin="${GLUA_BIN:-${build_dir}/glua-native}"
fi
source_src_dir="${repo_root}/third_party/luasocket/src"
source_test_dir="${repo_root}/third_party/luasocket/test"
official_build_dir="${NATIVE_LUASOCKET_OFFICIAL_BUILD_DIR:-${build_dir}-official-debug}"
official_timeout="${NATIVE_LUASOCKET_OFFICIAL_TIMEOUT:-240}"
work_dir="$(mktemp -d "${TMPDIR:-/tmp}/go-lua-vm-native-luasocket.XXXXXX")"

cleanup() {
  rm -rf "${work_dir}"
}
trap cleanup EXIT

echo "native LuaSocket runtime acceptance"
echo "repo_root=${repo_root}"
echo "GOOS=${target_goos}"
echo "GOARCH=${target_goarch}"
echo "CGO_ENABLED=${CGO_ENABLED:-unset}"
echo "GLUA_BIN=${glua_bin}"
echo "BUILD_DIR=${build_dir}"
echo "OFFICIAL_BUILD_DIR=${official_build_dir}"

expected_go_version="go1.26.4"
actual_go_version="$(go version | awk '{print $3}')"
if [[ "${actual_go_version}" != "${expected_go_version}" ]]; then
  echo "go version mismatch: expected ${expected_go_version}, got ${actual_go_version}" >&2
  echo "ensure PATH resolves go to ${expected_go_version} before running native LuaSocket acceptance" >&2
  exit 1
fi

case "${target_goos}" in
  darwin)
    runtime_extensions=(".so" ".dylib")
    ;;
  linux)
    runtime_extensions=(".so")
    ;;
  windows)
    runtime_extensions=(".dll")
    ;;
  *)
    echo "skip: unsupported native LuaSocket runtime target GOOS=${target_goos}" >&2
    exit 0
    ;;
esac

export CGO_ENABLED=1

if [[ -z "${GLUA_BIN:-}" ]]; then
  mkdir -p "$(dirname "${glua_bin}")"
  echo "build native glua: ${glua_bin}"
  go build -tags native_modules -trimpath -o "${glua_bin}" ./cmd/glua
elif [[ ! -x "${glua_bin}" ]]; then
  echo "GLUA_BIN is not executable: ${glua_bin}" >&2
  exit 1
fi

if [[ "${target_goos}" == "windows" ]]; then
  BUILD_DIR="${build_dir}" CGO_ENABLED=1 "${repo_root}/scripts/build-native-windows-lua53-shim.sh"
  export LUA53_IMPORT_LIB="${build_dir}/liblua53.dll.a"
fi

BUILD_DIR="${build_dir}" CGO_ENABLED=1 "${repo_root}/scripts/build-native-luasocket.sh"

if [[ ! -d "${source_test_dir}" ]]; then
  echo "LuaSocket official test directory not found: ${source_test_dir}" >&2
  exit 1
fi

lua_string_literal() {
  local text="$1"
  text="${text//\\/\\\\}"
  text="${text//\"/\\\"}"
  printf '"%s"' "${text}"
}

runtime_path() {
  local path="$1"
  if [[ "${target_goos}" == "windows" ]]; then
    cygpath -m "${path}"
    return 0
  fi
  echo "${path}"
}

compat_lua_dir="${work_dir}/lua"
mkdir -p "${compat_lua_dir}/socket"
for module_name in dict ftp headers http smtp tp url; do
  if [[ -f "${source_src_dir}/${module_name}.lua" ]]; then
    cp "${source_src_dir}/${module_name}.lua" "${compat_lua_dir}/socket/${module_name}.lua"
  fi
done

package_path="$(runtime_path "${source_src_dir}")/?.lua;$(runtime_path "${compat_lua_dir}")/?.lua;$(runtime_path "${work_dir}")/missing/?.lua"
package_path_literal="$(lua_string_literal "${package_path}")"

run_official_luasocket_tests() {
  local extension="$1"
  local module_build_dir="$2"
  local suffix_name="${extension#.}"
  local cpath_pattern="$(runtime_path "${module_build_dir}")/?${extension}"
  local package_cpath_literal
  package_cpath_literal="$(lua_string_literal "${cpath_pattern}")"
  local official_run_dir="${work_dir}/luasocket_official_${suffix_name}"
  local offline_runner="${official_run_dir}/official_offline_${suffix_name}.lua"
  local harness_source="${official_run_dir}/run_official_pair.py"

  mkdir -p "${official_run_dir}"
  cp "${source_test_dir}"/*.lua "${official_run_dir}/"
  if [[ "${target_goos}" == "windows" ]]; then
    sed -i 's/c:connect("10[.]0[.]0[.]1", 81)/c:connect("127.0.0.1", 1)/' "${official_run_dir}/testclnt.lua"
    sed -i 's/socket[.]connect("host[.]is[.]invalid", 1)/socket.connect("256.256.256.256", 1)/' "${official_run_dir}/testclnt.lua"
  fi

  cat >"${offline_runner}" <<EOF
package.path = ${package_path_literal}
package.cpath = ${package_cpath_literal}

local tests = {
  "excepttest.lua",
  "ltn12test.lua",
  "mimetest.lua",
  "stufftest.lua",
  "urltest.lua",
  "test_getaddrinfo.lua",
}

for _, test_file in ipairs(tests) do
  print("run LuaSocket official offline test", test_file)
  dofile(test_file)
end

print("native LuaSocket official offline tests passed", "${extension}")
EOF

  echo "run native LuaSocket official offline tests (${extension}): ${offline_runner}"
  (cd "${official_run_dir}" && "${glua_bin}" "${offline_runner}")

  cat >"${harness_source}" <<'PY'
import json
import os
import pathlib
import socket
import subprocess
import sys
import time


def write_text(path, text):
    pathlib.Path(path).write_text(text, encoding="utf-8")


def terminate(process):
    if process.poll() is not None:
        return
    process.terminate()
    try:
        process.wait(timeout=5)
    except subprocess.TimeoutExpired:
        process.kill()
        process.wait(timeout=5)


glua_bin, package_path, package_cpath, run_dir, timeout_text = sys.argv[1:]
timeout_seconds = int(timeout_text)
run_path = pathlib.Path(run_dir)

with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as probe:
    probe.bind(("127.0.0.1", 0))
    port = probe.getsockname()[1]

server_wrapper = run_path / "official_server.lua"
client_wrapper = run_path / "official_client.lua"

common = (
    "package.path = " + json.dumps(package_path, ensure_ascii=False) + "\n"
    "package.cpath = " + json.dumps(package_cpath, ensure_ascii=False) + "\n"
    "host = '127.0.0.1'\n"
    "port = " + json.dumps(str(port)) + "\n"
)
write_text(server_wrapper, common + "dofile('testsrvr.lua')\n")
write_text(client_wrapper, common + "dofile('testclnt.lua')\n")

server = subprocess.Popen(
    [glua_bin, str(server_wrapper)],
    cwd=run_dir,
    stdout=subprocess.PIPE,
    stderr=subprocess.PIPE,
    text=True,
)

try:
    deadline = time.time() + 10
    while time.time() < deadline:
        if server.poll() is not None:
            out, err = server.communicate(timeout=1)
            sys.stdout.write(out)
            sys.stderr.write(err)
            raise SystemExit("LuaSocket official server exited before accepting clients")
        try:
            with socket.create_connection(("127.0.0.1", port), timeout=0.2):
                pass
            break
        except OSError:
            time.sleep(0.1)
    else:
        raise SystemExit("LuaSocket official server did not open its port in time")

    client = subprocess.run(
        [glua_bin, str(client_wrapper)],
        cwd=run_dir,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        text=True,
        timeout=timeout_seconds,
    )

    try:
        server_out, server_err = server.communicate(timeout=10)
    except subprocess.TimeoutExpired:
        terminate(server)
        server_out, server_err = server.communicate(timeout=1)

    sys.stdout.write(server_out)
    sys.stderr.write(server_err)
    sys.stdout.write(client.stdout)
    sys.stderr.write(client.stderr)

    combined = client.stdout + client.stderr + server_out + server_err
    if client.returncode != 0 or "ERROR:" in combined:
        raise SystemExit(client.returncode if client.returncode else 1)
finally:
    terminate(server)
PY

  if [[ "${target_goos}" == "windows" ]]; then
    echo "note: Windows LuaSocket official client/server long test is not part of strict runtime; curated TCP/UDP loopback and official offline tests already ran."
    return 0
  fi

  echo "run native LuaSocket official client/server tests (${extension})"
  python3 "${harness_source}" "${glua_bin}" "${package_path}" "${cpath_pattern}" "${official_run_dir}" "${official_timeout}"
  echo "native LuaSocket official client/server tests passed (${extension})"
}

for extension in "${runtime_extensions[@]}"; do
  socket_module_path="${build_dir}/socket/core${extension}"
  mime_module_path="${build_dir}/mime/core${extension}"
  if [[ ! -f "${socket_module_path}" ]]; then
    echo "LuaSocket socket.core module output missing for ${extension}: ${socket_module_path}" >&2
    exit 1
  fi
  if [[ ! -f "${mime_module_path}" ]]; then
    echo "LuaSocket mime.core module output missing for ${extension}: ${mime_module_path}" >&2
    exit 1
  fi

  suffix_name="${extension#.}"
  cpath_pattern="$(runtime_path "${build_dir}")/?${extension}"
  package_cpath_literal="$(lua_string_literal "${cpath_pattern}")"
  acceptance_source="${work_dir}/luasocket_acceptance_${suffix_name}.lua"

  cat >"${acceptance_source}" <<EOF
package.path = ${package_path_literal}
package.cpath = ${package_cpath_literal}

local mime = assert(require("mime"))
assert(type(mime) == "table")
assert(type(mime.b64) == "function")
assert(type(mime.unb64) == "function")
assert(type(mime.qp) == "function")
assert(type(mime.unqp) == "function")
assert(type(mime.encode) == "function")
assert(type(mime.decode) == "function")
assert(type(mime.wrap) == "function")
local encoded = assert(mime.b64("hello"))
assert(encoded == "aGVsbG8=", encoded)
assert(assert(mime.unb64(encoded)) == "hello")
local quoted_printable = assert(mime.qp("hello=world", nil, "\\r\\n"))
assert(assert(mime.unqp(quoted_printable)) == "hello=world")

local ltn12 = assert(require("ltn12"))
local filter_source = ltn12.source.string("filter text")
local filter_sink, filter_chunks = ltn12.sink.table()
local filter_chain = ltn12.source.chain(
  filter_source,
  ltn12.filter.chain(mime.encode("base64"), mime.wrap("base64"), mime.decode("base64"))
)
assert(ltn12.pump.all(filter_chain, filter_sink))
assert(table.concat(filter_chunks) == "filter text")

local socket = assert(require("socket"))
assert(type(socket) == "table")
assert(type(socket.tcp) == "function")
assert(type(socket.udp) == "function")
assert(type(socket.dns) == "table")
assert(type(socket.select) == "function")

local url = assert(require("socket.url"))
local parsed_url = assert(url.parse("http://example.com/a?b=c"))
assert(parsed_url.scheme == "http")
assert(parsed_url.host == "example.com")

local function checked(value, err, label)
  if not value then
    error(label .. ": " .. tostring(err), 2)
  end
  return value
end

local function close_all(...)
  for i = 1, select("#", ...) do
    local handle = select(i, ...)
    if handle and type(handle.close) == "function" then
      pcall(function() handle:close() end)
    end
  end
end

local tcp = checked(socket.tcp(), nil, "socket.tcp")
checked(tcp:close(), nil, "tcp close")
local udp = checked(socket.udp(), nil, "socket.udp")
checked(udp:close(), nil, "udp close")

local hostname = checked(socket.dns.gethostname(), nil, "dns gethostname")
assert(type(hostname) == "string" and #hostname > 0)
local localhost_ip = checked(socket.dns.toip("localhost"), nil, "dns toip localhost")
assert(type(localhost_ip) == "string" and #localhost_ip > 0)
local localhost_addresses = checked(socket.dns.getaddrinfo("localhost"), nil, "dns getaddrinfo localhost")
assert(type(localhost_addresses) == "table" and #localhost_addresses > 0)

local server = checked(socket.bind("127.0.0.1", 0), nil, "tcp bind")
server:settimeout(2)
local host, port = server:getsockname()
checked(host, port, "tcp getsockname")
assert(type(host) == "string", type(host))
local port_number = assert(tonumber(port), type(port))

local client
local accepted
client = checked(socket.tcp(), nil, "tcp client")
client:settimeout(2)
local connected, connect_err = client:connect(host, port)
checked(connected, connect_err, "tcp connect")
local peer_host, peer_port = client:getpeername()
checked(peer_host, peer_port, "tcp getpeername")
accepted = checked(server:accept(), nil, "tcp accept")
accepted:settimeout(2)
client:settimeout(2)
checked(client:send("ping\\n"), nil, "tcp client send")
local readable, _, select_err = socket.select({accepted}, nil, 2)
checked(readable, select_err, "socket select readable")
assert(readable[1] == accepted)
local line = checked(accepted:receive("*l"), nil, "tcp server receive")
assert(line == "ping", line)
checked(accepted:send("pong\\n"), nil, "tcp server send")
local reply = checked(client:receive("*l"), nil, "tcp client receive")
assert(reply == "pong", reply)
close_all(client, accepted, server)

local receiver = checked(socket.udp(), nil, "udp receiver")
receiver:settimeout(2)
checked(receiver:setsockname("127.0.0.1", 0), nil, "udp setsockname")
local recv_host, recv_port = receiver:getsockname()
checked(recv_host, recv_port, "udp getsockname")
assert(type(recv_host) == "string", type(recv_host))
local recv_port_number = assert(tonumber(recv_port), type(recv_port))

local sender = checked(socket.udp(), nil, "udp sender")
sender:settimeout(2)
checked(sender:sendto("udp-ping", recv_host, recv_port_number), nil, "udp sendto")
local datagram, from_host, from_port = receiver:receivefrom()
checked(datagram, from_host, "udp receivefrom")
assert(datagram == "udp-ping", datagram)
assert(type(from_host) == "string", type(from_host))
assert(tonumber(from_port), type(from_port))
close_all(sender, receiver)

local connected_receiver = checked(socket.udp(), nil, "udp connected receiver")
connected_receiver:settimeout(2)
checked(connected_receiver:setsockname("127.0.0.1", 0), nil, "udp connected setsockname")
local connected_host, connected_port = connected_receiver:getsockname()
checked(connected_host, connected_port, "udp connected getsockname")
local connected_sender = checked(socket.udp(), nil, "udp connected sender")
connected_sender:settimeout(2)
checked(connected_sender:setpeername(connected_host, tonumber(connected_port)), nil, "udp setpeername")
checked(connected_sender:send("udp-connected"), nil, "udp connected send")
local connected_datagram = checked(connected_receiver:receivefrom(), nil, "udp connected receive")
assert(connected_datagram == "udp-connected", connected_datagram)
close_all(connected_sender, connected_receiver)

print("native LuaSocket runtime acceptance passed", "${extension}", socket._VERSION or "unknown", encoded, reply, datagram, connected_datagram, port_number, recv_port_number)
EOF

  echo "run native LuaSocket acceptance (${extension}): ${acceptance_source}"
  "${glua_bin}" "${acceptance_source}"
done

echo "build native LuaSocket debug modules for official tests: ${official_build_dir}"
BUILD_DIR="${official_build_dir}" NATIVE_LUASOCKET_DEBUG=1 CGO_ENABLED=1 "${repo_root}/scripts/build-native-luasocket.sh"

for extension in "${runtime_extensions[@]}"; do
  socket_module_path="${official_build_dir}/socket/core${extension}"
  mime_module_path="${official_build_dir}/mime/core${extension}"
  if [[ ! -f "${socket_module_path}" ]]; then
    echo "LuaSocket debug socket.core module output missing for ${extension}: ${socket_module_path}" >&2
    exit 1
  fi
  if [[ ! -f "${mime_module_path}" ]]; then
    echo "LuaSocket debug mime.core module output missing for ${extension}: ${mime_module_path}" >&2
    exit 1
  fi

  run_official_luasocket_tests "${extension}" "${official_build_dir}"
done

echo "native LuaSocket runtime acceptance passed"

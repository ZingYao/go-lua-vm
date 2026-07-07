#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
target_goos="${TARGET_GOOS:-$(go env GOOS)}"
target_goarch="${TARGET_GOARCH:-$(go env GOARCH)}"
build_dir="${BUILD_DIR:-${repo_root}/build/native-luasocket/${target_goos}-${target_goarch}}"
glua_bin="${GLUA_BIN:-${build_dir}/glua-native}"
source_src_dir="${repo_root}/third_party/luasocket/src"
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
    echo "skip: Windows LuaSocket runtime acceptance requires lua53.dll shim or import library, not implemented yet" >&2
    exit 0
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

BUILD_DIR="${build_dir}" CGO_ENABLED=1 "${repo_root}/scripts/build-native-luasocket.sh"

lua_string_literal() {
  local text="$1"
  text="${text//\\/\\\\}"
  text="${text//\"/\\\"}"
  printf '"%s"' "${text}"
}

package_path_literal="$(lua_string_literal "${source_src_dir}/?.lua;${work_dir}/missing/?.lua")"

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
  cpath_pattern="${build_dir}/?${extension}"
  package_cpath_literal="$(lua_string_literal "${cpath_pattern}")"
  acceptance_source="${work_dir}/luasocket_acceptance_${suffix_name}.lua"

  cat >"${acceptance_source}" <<EOF
package.path = ${package_path_literal}
package.cpath = ${package_cpath_literal}

local mime = assert(require("mime"))
assert(type(mime) == "table")
assert(type(mime.b64) == "function")
assert(type(mime.unb64) == "function")
local encoded = assert(mime.b64("hello"))
assert(encoded == "aGVsbG8=", encoded)
assert(assert(mime.unb64(encoded)) == "hello")

local socket = assert(require("socket"))
assert(type(socket) == "table")
assert(type(socket.tcp) == "function")
assert(type(socket.udp) == "function")

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
accepted = checked(server:accept(), nil, "tcp accept")
accepted:settimeout(2)
client:settimeout(2)
checked(client:send("ping\\n"), nil, "tcp client send")
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

print("native LuaSocket runtime acceptance passed", "${extension}", socket._VERSION or "unknown", encoded, reply, datagram, port_number, recv_port_number)
EOF

  echo "run native LuaSocket acceptance (${extension}): ${acceptance_source}"
  "${glua_bin}" "${acceptance_source}"
done

echo "native LuaSocket runtime acceptance passed"

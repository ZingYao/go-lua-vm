#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
target_goarch="${TARGET_GOARCH:-arm64}"
android_api="${ANDROID_API_LEVEL:-35}"
build_dir="${BUILD_DIR:-${repo_root}/build/native-android-real/${target_goarch}}"
device_dir="${ANDROID_DEVICE_DIR:-/data/local/tmp/glua-native-real-modules}"
glua_bin="${GLUA_BIN:-${build_dir}/glua-native}"
luasocket_official_timeout="${NATIVE_LUASOCKET_OFFICIAL_TIMEOUT:-900}"

if [[ "${target_goarch}" != "arm64" ]]; then
  echo "Android real module acceptance currently supports TARGET_GOARCH=arm64, got ${target_goarch}" >&2
  exit 1
fi

expected_go_version="go1.26.4"
actual_go_version="$(go version | awk '{print $3}')"
if [[ "${actual_go_version}" != "${expected_go_version}" ]]; then
  echo "go version mismatch: expected ${expected_go_version}, got ${actual_go_version}" >&2
  echo "ensure PATH resolves go to ${expected_go_version} before running Android real module acceptance" >&2
  exit 1
fi

find_android_cc() {
  local candidate

  if [[ -n "${CC:-}" ]]; then
    echo "${CC}"
    return 0
  fi

  candidate="aarch64-linux-android${android_api}-clang"
  if command -v "${candidate}" >/dev/null 2>&1; then
    echo "${candidate}"
    return 0
  fi

  if [[ -n "${ANDROID_NDK_HOME:-}" ]]; then
    for candidate in "${ANDROID_NDK_HOME}"/toolchains/llvm/prebuilt/*/bin/aarch64-linux-android"${android_api}"-clang; do
      if [[ -x "${candidate}" ]]; then
        echo "${candidate}"
        return 0
      fi
    done
  fi

  return 1
}

android_cc=""
if ! android_cc="$(find_android_cc)"; then
  echo "Android clang not found; install Android NDK and expose aarch64-linux-android${android_api}-clang on PATH or set CC" >&2
  exit 1
fi

if [[ -n "${ADB_SERIAL:-}" ]]; then
  adb_command=(adb -s "${ADB_SERIAL}")
else
  adb_command=(adb)
fi

echo "Android native real module acceptance"
echo "repo_root=${repo_root}"
echo "GOOS=android"
echo "GOARCH=${target_goarch}"
echo "ANDROID_API_LEVEL=${android_api}"
echo "CC=${android_cc}"
echo "BUILD_DIR=${build_dir}"
echo "ANDROID_DEVICE_DIR=${device_dir}"

mkdir -p "${build_dir}"

echo "build Android native glua: ${glua_bin}"
GOOS=android GOARCH="${target_goarch}" CGO_ENABLED=1 CC="${android_cc}" \
  go build -tags native_modules -trimpath -o "${glua_bin}" ./cmd/glua

echo "build Android lua-cjson"
TARGET_GOOS=android TARGET_GOARCH="${target_goarch}" BUILD_DIR="${build_dir}/cjson" CGO_ENABLED=1 CC="${android_cc}" \
  "${repo_root}/scripts/build-native-cjson.sh"

echo "build Android LPeg"
TARGET_GOOS=android TARGET_GOARCH="${target_goarch}" BUILD_DIR="${build_dir}/lpeg" CGO_ENABLED=1 CC="${android_cc}" \
  "${repo_root}/scripts/build-native-lpeg.sh"

echo "build Android LuaSocket release modules"
TARGET_GOOS=android TARGET_GOARCH="${target_goarch}" BUILD_DIR="${build_dir}/luasocket-release" CGO_ENABLED=1 CC="${android_cc}" \
  "${repo_root}/scripts/build-native-luasocket.sh"

echo "build Android LuaSocket debug modules for official tests"
TARGET_GOOS=android TARGET_GOARCH="${target_goarch}" BUILD_DIR="${build_dir}/luasocket-debug" NATIVE_LUASOCKET_DEBUG=1 CGO_ENABLED=1 CC="${android_cc}" \
  "${repo_root}/scripts/build-native-luasocket.sh"

required_outputs=(
  "${build_dir}/cjson/cjson.so"
  "${build_dir}/lpeg/lpeg.so"
  "${build_dir}/luasocket-release/socket/core.so"
  "${build_dir}/luasocket-release/mime/core.so"
  "${build_dir}/luasocket-debug/socket/core.so"
  "${build_dir}/luasocket-debug/mime/core.so"
)

for output_path in "${required_outputs[@]}"; do
  if [[ ! -f "${output_path}" ]]; then
    echo "required Android module output missing: ${output_path}" >&2
    exit 1
  fi
done

work_dir="$(mktemp -d "${TMPDIR:-/tmp}/go-lua-vm-android-real.XXXXXX")"
cleanup() {
  rm -rf "${work_dir}"
}
trap cleanup EXIT

luasocket_android_test_dir="${work_dir}/luasocket-test-android"
mkdir -p "${luasocket_android_test_dir}"
cp -R "${repo_root}/third_party/luasocket/test/." "${luasocket_android_test_dir}/"
# Some Android networks resolve host.is.invalid through a local DNS proxy instead of failing.
# Keep the official client/server script intact except for that environment-dependent hostname.
sed 's/"host\.is\.invalid"/"invalid host name"/g' \
  "${repo_root}/third_party/luasocket/test/testclnt.lua" \
  >"${luasocket_android_test_dir}/testclnt.lua"
perl -0pi -e 's/data, err = socket\.connect\("", port\)\n/data, err = socket.connect("", port)\n    if data then\n        local peer = data:getpeername()\n        if peer ~= host and peer ~= "127.0.0.1" and peer ~= "::1" then\n            data:close()\n            data = nil\n            pass("empty host resolved outside localhost; using explicit host...")\n            data, err = socket.connect(host, port)\n            assert(data, err)\n            return\n        end\n    end\n/' \
  "${luasocket_android_test_dir}/testclnt.lua"

lua_string_literal() {
  local text="$1"
  text="${text//\\/\\\\}"
  text="${text//\"/\\\"}"
  printf '"%s"' "${text}"
}

cjson_cpath="${device_dir}/cjson/?.so"
lpeg_path="${device_dir}/lpeg/?.lua"
lpeg_cpath="${device_dir}/lpeg/?.so"
luasocket_src_path="${device_dir}/luasocket/src/?.lua"
luasocket_test_path="${device_dir}/luasocket/test/?.lua"
luasocket_release_cpath="${device_dir}/luasocket-release/?.so"
luasocket_debug_cpath="${device_dir}/luasocket-debug/?.so"
missing_path="${device_dir}/missing/?.lua"

cjson_script="${work_dir}/android_cjson_acceptance.lua"
cat >"${cjson_script}" <<EOF
package.path = $(lua_string_literal "${missing_path}")
package.cpath = $(lua_string_literal "${cjson_cpath}")

local cjson = assert(require("cjson"))
assert(cjson._NAME == "cjson", cjson._NAME)
assert(type(cjson.encode) == "function")
assert(type(cjson.decode) == "function")
assert(type(cjson.null) == "userdata")

local encoded = cjson.encode({ a = 1, b = true, c = cjson.null, list = { 1, 2, "x" } })
local decoded = cjson.decode(encoded)
assert(decoded.a == 1, decoded.a)
assert(decoded.b == true, tostring(decoded.b))
assert(decoded.c == cjson.null, tostring(decoded.c))
assert(decoded.list[1] == 1 and decoded.list[2] == 2 and decoded.list[3] == "x")
assert(cjson.decode("1") == 1)
assert(cjson.decode("true") == true)
assert(cjson.decode("null") == cjson.null)

local ok, message = pcall(cjson.decode, "{")
assert(ok == false, "invalid JSON unexpectedly decoded")
assert(type(message) == "string" and string.find(message, "Expected", 1, true), message)

local encode_ok, encode_message = pcall(cjson.encode, function() end)
assert(encode_ok == false, "function unexpectedly encoded")
assert(type(encode_message) == "string" and string.find(encode_message, "Cannot serialise", 1, true), encode_message)

print("Android lua-cjson acceptance passed", cjson._NAME, cjson._VERSION, encoded)
EOF

lpeg_script="${work_dir}/android_lpeg_acceptance.lua"
cat >"${lpeg_script}" <<EOF
package.path = $(lua_string_literal "${lpeg_path};${missing_path}")
package.cpath = $(lua_string_literal "${lpeg_cpath}")

local lpeg = assert(require("lpeg"))
local P, R, S, C = lpeg.P, lpeg.R, lpeg.S, lpeg.C
assert(lpeg.match(P("abc"), "abcdef") == 4)
assert(lpeg.match(P(1)^0, "abcd") == 5)
assert(lpeg.match(R("az")^1 * -1, "abc") == 4)
assert(lpeg.match(S("ab")^1, "abba!") == 5)
assert(lpeg.match(C(R("az")^1), "lua53") == "lua")
assert(lpeg.match(P(false) + "a", "a") == 2)
dofile($(lua_string_literal "${device_dir}/lpeg/test.lua"))
print("Android LPeg full official test passed")
EOF

luasocket_release_script="${work_dir}/android_luasocket_release_acceptance.lua"
cat >"${luasocket_release_script}" <<EOF
package.path = $(lua_string_literal "${luasocket_src_path};${missing_path}")
package.cpath = $(lua_string_literal "${luasocket_release_cpath}")

local mime = assert(require("mime"))
assert(type(mime.b64) == "function")
assert(type(mime.unb64) == "function")
assert(type(mime.qp) == "function")
assert(type(mime.unqp) == "function")
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
assert(type(socket.tcp) == "function")
assert(type(socket.udp) == "function")
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
local client = checked(socket.tcp(), nil, "tcp client")
client:settimeout(2)
checked(client:connect(host, tonumber(port)), nil, "tcp connect")
local accepted = checked(server:accept(), nil, "tcp accept")
accepted:settimeout(2)
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
local sender = checked(socket.udp(), nil, "udp sender")
sender:settimeout(2)
checked(sender:sendto("udp-ping", recv_host, tonumber(recv_port)), nil, "udp sendto")
local datagram, from_host, from_port = receiver:receivefrom()
checked(datagram, from_host, "udp receivefrom")
assert(datagram == "udp-ping", datagram)
assert(type(from_host) == "string")
assert(tonumber(from_port), type(from_port))
close_all(sender, receiver)

print("Android LuaSocket release acceptance passed", socket._VERSION or "unknown", encoded, reply, datagram)
EOF

luasocket_offline_script="${work_dir}/android_luasocket_official_offline.lua"
cat >"${luasocket_offline_script}" <<EOF
package.path = $(lua_string_literal "${luasocket_src_path};${luasocket_test_path};${missing_path}")
package.cpath = $(lua_string_literal "${luasocket_debug_cpath}")

local tests = {
  "excepttest.lua",
  "ltn12test.lua",
  "mimetest.lua",
  "stufftest.lua",
  "urltest.lua",
  "test_getaddrinfo.lua",
}

for _, test_file in ipairs(tests) do
  print("run Android LuaSocket official offline test", test_file)
  dofile($(lua_string_literal "${device_dir}/luasocket/test/") .. test_file)
end

print("Android LuaSocket official offline tests passed")
EOF

if [[ -n "${NATIVE_LUASOCKET_ANDROID_PORT:-}" ]]; then
  official_port="${NATIVE_LUASOCKET_ANDROID_PORT}"
else
  official_port="$((30000 + RANDOM % 20000))"
fi
luasocket_server_script="${work_dir}/android_luasocket_official_server.lua"
cat >"${luasocket_server_script}" <<EOF
package.path = $(lua_string_literal "${luasocket_src_path};${luasocket_test_path};${missing_path}")
package.cpath = $(lua_string_literal "${luasocket_debug_cpath}")
host = "127.0.0.1"
port = "${official_port}"
dofile($(lua_string_literal "${device_dir}/luasocket/test/testsrvr.lua"))
EOF

luasocket_client_script="${work_dir}/android_luasocket_official_client.lua"
cat >"${luasocket_client_script}" <<EOF
package.path = $(lua_string_literal "${luasocket_src_path};${luasocket_test_path};${missing_path}")
package.cpath = $(lua_string_literal "${luasocket_debug_cpath}")
host = "127.0.0.1"
port = "${official_port}"
dofile($(lua_string_literal "${device_dir}/luasocket/test/testclnt.lua"))
EOF

push_dir() {
  local local_dir="$1"
  local remote_dir="$2"
  "${adb_command[@]}" shell "rm -rf $(printf '%q' "${remote_dir}"); mkdir -p $(printf '%q' "${remote_dir}")"
  "${adb_command[@]}" push "${local_dir}/." "${remote_dir}/" >/dev/null
}

"${adb_command[@]}" shell "rm -rf $(printf '%q' "${device_dir}"); mkdir -p $(printf '%q' "${device_dir}")"
"${adb_command[@]}" push "${glua_bin}" "${device_dir}/glua-native" >/dev/null
"${adb_command[@]}" shell "chmod 755 $(printf '%q' "${device_dir}/glua-native")"

push_dir "${build_dir}/cjson" "${device_dir}/cjson"
push_dir "${build_dir}/lpeg" "${device_dir}/lpeg"
"${adb_command[@]}" push "${repo_root}/third_party/lpeg/test.lua" "${repo_root}/third_party/lpeg/re.lua" "${device_dir}/lpeg/" >/dev/null
push_dir "${build_dir}/luasocket-release" "${device_dir}/luasocket-release"
push_dir "${build_dir}/luasocket-debug" "${device_dir}/luasocket-debug"
push_dir "${repo_root}/third_party/luasocket/src" "${device_dir}/luasocket/src"
"${adb_command[@]}" shell "cd $(printf '%q' "${device_dir}/luasocket/src") && mkdir -p socket && for module in ftp headers http smtp tp url; do cp \"\${module}.lua\" \"socket/\${module}.lua\"; done"
push_dir "${luasocket_android_test_dir}" "${device_dir}/luasocket/test"
"${adb_command[@]}" shell "cp $(printf '%q' "${device_dir}/luasocket/test")/*.lua $(printf '%q' "${device_dir}")/"
"${adb_command[@]}" push "${cjson_script}" "${lpeg_script}" "${luasocket_release_script}" "${luasocket_offline_script}" "${luasocket_server_script}" "${luasocket_client_script}" "${device_dir}/" >/dev/null

echo "device info:"
"${adb_command[@]}" shell "getprop ro.product.model; getprop ro.product.cpu.abi; getprop ro.build.version.release; getprop ro.build.version.sdk; uname -a"

echo "run Android lua-cjson acceptance"
"${adb_command[@]}" shell "cd $(printf '%q' "${device_dir}") && ./glua-native android_cjson_acceptance.lua"

echo "run Android LPeg official acceptance"
"${adb_command[@]}" shell "cd $(printf '%q' "${device_dir}") && ./glua-native android_lpeg_acceptance.lua"

echo "run Android LuaSocket release acceptance"
"${adb_command[@]}" shell "cd $(printf '%q' "${device_dir}") && ./glua-native android_luasocket_release_acceptance.lua"

echo "run Android LuaSocket official offline tests"
"${adb_command[@]}" shell "cd $(printf '%q' "${device_dir}") && ./glua-native android_luasocket_official_offline.lua"

echo "run Android LuaSocket official client/server tests"
"${adb_command[@]}" shell "cd $(printf '%q' "${device_dir}") && rm -f official-server.out official-server.err official-server.pid && (./glua-native android_luasocket_official_server.lua >official-server.out 2>official-server.err & echo \$! >official-server.pid)"
sleep 2
set +e
"${adb_command[@]}" shell "cd $(printf '%q' "${device_dir}") && timeout ${luasocket_official_timeout} ./glua-native android_luasocket_official_client.lua" >"${work_dir}/official-client.out" 2>"${work_dir}/official-client.err"
client_status=$?
set -e
"${adb_command[@]}" shell "cd $(printf '%q' "${device_dir}") && if [ -f official-server.pid ]; then kill \$(cat official-server.pid) >/dev/null 2>&1 || true; fi"
"${adb_command[@]}" shell "cd $(printf '%q' "${device_dir}") && cat official-server.out official-server.err" >"${work_dir}/official-server.combined" 2>/dev/null || true
cat "${work_dir}/official-server.combined"
cat "${work_dir}/official-client.out"
cat "${work_dir}/official-client.err" >&2
if [[ "${client_status}" -ne 0 ]]; then
  echo "Android LuaSocket official client/server tests failed with status ${client_status}" >&2
  exit "${client_status}"
fi
if grep -q "ERROR:" "${work_dir}/official-server.combined" "${work_dir}/official-client.out" "${work_dir}/official-client.err"; then
  echo "Android LuaSocket official client/server tests reported ERROR" >&2
  exit 1
fi

echo "Android native real module acceptance passed"

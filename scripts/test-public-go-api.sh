#!/usr/bin/env bash
set -euo pipefail

# 解析仓库根目录并在仓库外创建临时 module，真实验证 internal 边界和公开依赖。
repository_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
temporary_module="$(mktemp -d "${TMPDIR:-/tmp}/glua-public-api.XXXXXX")"
trap 'rm -rf "${temporary_module}"' EXIT

cat >"${temporary_module}/go.mod" <<EOF
module example.com/glua-public-api-test

go 1.26

require github.com/ZingYao/go-lua-vm v1.0.0

replace github.com/ZingYao/go-lua-vm => ${repository_root}
EOF

cat >"${temporary_module}/main_test.go" <<'EOF'
package publicapitest

import (
	"errors"
	"testing"

	"github.com/ZingYao/go-lua-vm/lua"
)

func TestPublicAPI(t *testing.T) {
	state := lua.NewState()
	defer state.Close()
	if err := lua.OpenLibs(state); err != nil {
		t.Fatal(err)
	}
	hostSource, err := lua.ChunkProgressEventSource("external-host")
	if err != nil {
		t.Fatal(err)
	}

	called := false
	listenerID, err := lua.SetProgressEvent(state, hostSource, "host.ready", func(context lua.ProgressEventContext) error {
		called = context.Payload.String == "ok"
		return nil
	}, lua.ProgressEventOptions{Priority: 10, Group: "external"})
	if err != nil {
		t.Fatal(err)
	}
	if err := lua.DoString(state, `
local encoded = glua.json.encode({ value = 42 })
assert(glua.json.decode(encoded).value == 42)
assert(glua.hash.sha256("glua") ~= "")
glua.event.callProgress("host.ready", "ok")
`); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("Go callback was not invoked from Lua")
	}
	listener, found, err := lua.GetProgressEvent(state, listenerID)
	if err != nil || !found || listener.Group != "external" {
		t.Fatalf("listener=%#v found=%v err=%v", listener, found, err)
	}
	summary, err := lua.ListProgressEvents(state, hostSource)
	if err != nil || summary.TotalListeners != 1 || summary.ListenerLimit <= 0 || summary.QueuedTaskLimit <= 0 || summary.TasksPerDrainLimit <= 0 {
		t.Fatalf("summary=%#v err=%v", summary, err)
	}
	queueID, err := lua.SetProgressEvent(state, hostSource, "host.queue", func(lua.ProgressEventContext) error {
		return nil
	}, lua.ProgressEventOptions{Async: true, QueueLimit: 1, Overflow: "error"})
	if err != nil {
		t.Fatal(err)
	}
	if err := lua.CallProgressEventAsync(state, hostSource, "host.queue"); err != nil {
		t.Fatal(err)
	}
	err = lua.CallProgressEventAsync(state, hostSource, "host.queue")
	var queueFull *lua.ProgressEventQueueFullError
	if !errors.Is(err, lua.ErrProgressEventQueueFull) || !errors.As(err, &queueFull) || queueFull.EventID != queueID {
		t.Fatalf("queue full err=%#v", err)
	}
	summary, err = lua.ListProgressEvents(state, hostSource)
	if err != nil || summary.RejectedTasks != 1 {
		t.Fatalf("queue summary=%#v err=%v", summary, err)
	}
	removed, err := lua.RemoveProgressEvent(state, listenerID)
	if err != nil || !removed {
		t.Fatalf("removed=%v err=%v", removed, err)
	}
}
EOF

# 使用当前项目选定的 Go 命令执行外部 module，避免工作目录改变 PATH 工具链选择。
go -C "${temporary_module}" mod tidy
CGO_ENABLED=0 go -C "${temporary_module}" test ./...

printf 'GLUA_PUBLIC_GO_API_OK\n'

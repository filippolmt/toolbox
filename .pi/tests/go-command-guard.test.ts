import assert from "node:assert/strict";
import test from "node:test";
import goCommandGuard from "../extensions/go-command-guard.ts";

test("blocks direct Go commands and points agents at Make targets", async () => {
  const handlers = new Map<string, (event: any, ctx: any) => Promise<any>>();
  goCommandGuard({ on: (name: string, handler: any) => handlers.set(name, handler) } as any);
  const call = handlers.get("tool_call")!;
  const ctx = {};

  for (const command of [
    "go test ./...",
    "gofmt -w main.go",
    "cd internal && go test ./...",
    "env GOFLAGS=-mod=mod /usr/local/go/bin/go test ./...",
    "printf source | gofmt",
  ]) {
    const blocked = await call({ toolName: "bash", input: { command } }, ctx);
    assert.equal(blocked.block, true, command);
    assert.match(blocked.reason, /make go-/);
  }

  assert.equal(await call({ toolName: "bash", input: { command: "make go-check" } }, ctx), undefined);
  assert.equal(await call({ toolName: "bash", input: { command: "printf 'go test ./...'" } }, ctx), undefined);
  assert.equal(await call({ toolName: "read", input: { path: "main.go" } }, ctx), undefined);
});

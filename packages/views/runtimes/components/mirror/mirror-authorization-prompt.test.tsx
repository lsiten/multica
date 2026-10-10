import { describe, expect, it, vi } from "vitest";
import { fireEvent, screen } from "@testing-library/react";
import { renderWithI18n } from "../../../test/i18n";
import { MirrorAuthorizationPrompt } from "./mirror-authorization-prompt";
import type { MirrorAuthorizationRequest } from "./video-session-types";

const base: MirrorAuthorizationRequest = { type: "mirror-authorization:request", request_id: "approval", kind: "cli", title: "Codex operation approval", message: "protocol params", expires_at: "2030-01-01T00:00:00Z" };

describe("local operation consent", () => {
  it("shows an MCP call and its arguments before allowing an explicit decision", () => {
    const decide = vi.fn();
    const target = 'multica-llm2jev\nAllow multica_llm2jev_verify_completion?\n{"evidence":["real evidence"]}';
    renderWithI18n(<MirrorAuthorizationPrompt request={{ ...base, operation: { kind: "mcp", target, details: "local MCP approval params" } }} onDecision={decide} pending={false} failed={false} onDismiss={vi.fn()} />, { locale: "zh-Hans" });
    expect(screen.getByText(/multica_llm2jev_verify_completion/)).toHaveTextContent("real evidence");
    expect(decide).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: "允许" })).toBeEnabled();
    fireEvent.click(screen.getByRole("button", { name: "允许" }));
    expect(decide).toHaveBeenCalledWith(true);
  });

  it("names deletions and new files before consent without making the reader interpret protocol IDs", () => {
    const decide = vi.fn();
    renderWithI18n(<MirrorAuthorizationPrompt request={{ ...base, operation: { kind: "files", target: "file changes", details: '{"itemId":"file-op"}', files: [{ path: "old.ts", kind: "delete" }, { path: "new.ts", kind: "add" }] } }} onDecision={decide} pending={false} failed={false} onDismiss={vi.fn()} />, { locale: "zh-Hans" });
    expect(screen.getByText('"old.ts"').closest("li")).toHaveTextContent("删除");
    expect(screen.getByText('"new.ts"').closest("li")).toHaveTextContent("新建");
    expect(screen.getByText('{"itemId":"file-op"}').closest("details")).not.toHaveAttribute("open");
    fireEvent.click(screen.getByRole("button", { name: "允许修改" }));
    expect(decide).toHaveBeenCalledWith(true);
  });

  it("shows the actual network and path scope, including the turn duration", () => {
    renderWithI18n(<MirrorAuthorizationPrompt request={{ ...base, operation: { kind: "permissions", target: "raw permissions", details: "local params", permissions: { network_enabled: true, read_paths: ["/repo"], write_paths: ["/repo/result"] } } }} onDecision={vi.fn()} pending={false} failed={false} onDismiss={vi.fn()} />, { locale: "zh-Hans" });
    expect(screen.getByText("允许访问网络")).toBeVisible();
    expect(screen.getByText('"/repo/result"').closest("li")).toHaveTextContent("写入");
    expect(screen.getByText("权限在当前轮次内有效。拒绝后不授予这些权限。")).toBeVisible();
  });

  it("explains expiry and prevents a late grant", () => {
    renderWithI18n(<MirrorAuthorizationPrompt request={{ ...base, expires_at: "2020-01-01T00:00:00Z" }} onDecision={vi.fn()} pending={false} failed={false} onDismiss={vi.fn()} />, { locale: "zh-Hans" });
    expect(screen.getByRole("button", { name: "允许" })).toBeDisabled();
    expect(screen.getByText("请求已过期。请让智能体重新发起。")).toBeVisible();
    expect(screen.getByRole("button", { name: "关闭请求" })).toBeVisible();
  });
});

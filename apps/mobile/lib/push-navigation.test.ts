// @vitest-environment node
import { describe, expect, it } from "vitest";
import { pushNavigationTarget } from "./push-navigation";

const user = "a78e6859-eb82-4c6d-b976-edccffbc6fe3";
const issue = "351539d4-fbf6-465d-8112-ac9ea1c0cfe0";
const inbox = "627ed78e-4818-4c73-9b49-e3ebaa522f38";
const data = { recipient_id: user, workspace_slug: "dev", type: "mentioned", issue_id: issue, inbox_id: inbox };

describe("push destinations", () => {
  it("opens an issue and preserves action requests as actionable inbox details", () => {
    expect(pushNavigationTarget(data, user)).toEqual({ pathname: "/[workspace]/issue/[id]", params: { workspace: "dev", id: issue } });
    expect(pushNavigationTarget({ ...data, type: "human_action_requested" }, user)).toEqual({ pathname: "/[workspace]/inbox/[id]", params: { workspace: "dev", id: inbox } });
  });
  it("opens the specified chat session without accepting arbitrary links", () => {
    expect(pushNavigationTarget({ ...data, chat_session_id: issue, url: "https://untrusted.test" }, user)).toEqual({ pathname: "/[workspace]/(tabs)/chat", params: { workspace: "dev", session: issue } });
    expect(pushNavigationTarget({ ...data, workspace_slug: "../logout" }, user)).toBeNull();
    expect(pushNavigationTarget(data, issue)).toBeNull();
    expect(pushNavigationTarget({ ...data, issue_id: 42 }, user)).toBeNull();
  });
});

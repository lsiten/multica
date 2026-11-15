// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  jevDecisionLogStateFromParams,
  withJevDecisionLogState,
} from "./jev-decision-log-url-state";

describe("Jev decision log URL state", () => {
  it("restores filters, page size and the server snapshot", () => {
    expect(
      jevDecisionLogStateFromParams(
        new URLSearchParams(
          "jev_q=report&jev_agent=reviewer&jev_source=remote&jev_result=error&jev_from=2026-10-01&jev_to=2026-10-03&jev_offset=40&jev_limit=50&jev_as_of=snapshot",
        ),
      ),
    ).toEqual({
      filters: {
        q: "report",
        agent: "reviewer",
        source: "remote",
        result_class: "error",
        from: "2026-10-01",
        to: "2026-10-03",
      },
      offset: 40,
      limit: 50,
      asOf: "snapshot",
    });
  });

  it("fails closed for future enum and malformed page values", () => {
    expect(
      jevDecisionLogStateFromParams(
        new URLSearchParams(
          "jev_source=future&jev_result=future&jev_from=invalid&jev_to=2026-02-31&jev_offset=-1&jev_limit=999",
        ),
      ),
    ).toEqual({
      filters: {
        q: "",
        agent: "",
        source: "",
        result_class: "",
        from: "",
        to: "",
      },
      offset: 0,
      limit: 20,
      asOf: "",
    });
  });

  it("writes only active values and preserves unrelated parameters", () => {
    const result = withJevDecisionLogState(
      new URLSearchParams("tab=jev_logs&keep=1&jev_q=old"),
      {
        filters: {
          q: "new report",
          agent: "",
          source: "local",
          result_class: "",
          from: "",
          to: "",
        },
        offset: 20,
        limit: 50,
        asOf: "snapshot",
      },
    );

    expect(result.toString()).toBe(
      "tab=jev_logs&keep=1&jev_q=new+report&jev_source=local&jev_offset=20&jev_limit=50&jev_as_of=snapshot",
    );
  });
});

import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { NavigationProvider } from "./context";
import type { NavigationAdapter } from "./types";
import { useSearchState } from "./use-search-state";

function makeAdapter(overrides: Partial<NavigationAdapter> = {}): NavigationAdapter {
  return {
    push: vi.fn(),
    replace: vi.fn(),
    back: vi.fn(),
    pathname: "/acme/applications",
    searchParams: new URLSearchParams(),
    hash: "",
    getShareableUrl: (path) => path,
    ...overrides,
  };
}

function Probe({ defaultValue = "" }: { readonly defaultValue?: string }) {
  const [value, setValue] = useSearchState("view", defaultValue);
  return (
    <div>
      <output>{value}</output>
      <button type="button" onClick={() => setValue("board")}>board</button>
      <button type="button" onClick={() => setValue("")}>default</button>
    </div>
  );
}

function SharedProbe() {
  const [view, setView] = useSearchState("view", "projects");
  const [query, setQuery] = useSearchState("q");
  return (
    <div>
      <output>{view}</output>
      <output>{query}</output>
      <button type="button" onClick={() => setView("board")}>
        board
      </button>
      <input
        aria-label="query"
        value={query}
        onChange={(event) => setQuery(event.target.value)}
      />
    </div>
  );
}

describe("useSearchState", () => {
  it("reads the current URL value", () => {
    const adapter = makeAdapter({
      searchParams: new URLSearchParams("view=board&q=deploy"),
    });

    render(
      <NavigationProvider value={adapter}>
        <Probe />
      </NavigationProvider>,
    );

    expect(screen.getByRole("status")).toHaveTextContent("board");
  });

  it("replaces the URL without dropping sibling parameters or the hash", () => {
    const replace = vi.fn();
    const adapter = makeAdapter({
      replace,
      searchParams: new URLSearchParams("view=projects&q=deploy"),
      hash: "#results",
    });

    render(
      <NavigationProvider value={adapter}>
        <Probe />
      </NavigationProvider>,
    );
    fireEvent.click(screen.getByRole("button", { name: "board" }));

    expect(replace).toHaveBeenCalledWith(
      "/acme/applications?view=board&q=deploy#results",
    );
    expect(adapter.push).not.toHaveBeenCalled();
  });

  it("removes a parameter when the caller returns to the default value", () => {
    const replace = vi.fn();
    const adapter = makeAdapter({
      replace,
      searchParams: new URLSearchParams("view=board&q=deploy"),
    });

    render(
      <NavigationProvider value={adapter}>
        <Probe defaultValue="projects" />
      </NavigationProvider>,
    );
    fireEvent.click(screen.getByRole("button", { name: "default" }));

    expect(replace).toHaveBeenCalledWith("/acme/applications?q=deploy");
  });

  it("preserves a parameter committed by another control before navigation settles", () => {
    const replace = vi.fn();
    const adapter = makeAdapter({ replace });

    render(
      <NavigationProvider value={adapter}>
        <SharedProbe />
      </NavigationProvider>,
    );
    fireEvent.click(screen.getByRole("button", { name: "board" }));
    fireEvent.change(screen.getByRole("textbox", { name: "query" }), {
      target: { value: "deploy" },
    });

    expect(replace).toHaveBeenLastCalledWith(
      "/acme/applications?view=board&q=deploy",
    );
  });
});

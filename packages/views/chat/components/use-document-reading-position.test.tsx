import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, expect, it } from "vitest";
import { useChatWorkspaceLayoutStore } from "@multica/core/chat";
import { useDocumentReadingPosition } from "./use-document-reading-position";

function Document({ scope, ready, legacyScope }: { scope: string; ready: boolean; legacyScope?: string }) {
  const ref = useDocumentReadingPosition(scope, ready, legacyScope);
  return <div ref={ref} data-testid="reading-area" style={{ overflow: "auto", height: 100 }}><div style={{ height: 2000 }}>Long document</div></div>;
}

beforeEach(() => useChatWorkspaceLayoutStore.setState({ readingPositions: {}, layouts: {} }));

it("restores each document after loading and retains its reading position across switches", async () => {
  useChatWorkspaceLayoutStore.getState().setReadingPosition("first", 640);
  const { rerender } = render(<Document key="first" scope="first" ready={false} />);
  expect(screen.getByTestId("reading-area").scrollTop).toBe(0);
  rerender(<Document key="first" scope="first" ready />);
  expect(screen.getByTestId("reading-area").scrollTop).toBe(640);
  fireEvent.scroll(screen.getByTestId("reading-area"), { target: { scrollTop: 880 } });
  await waitFor(() => expect(useChatWorkspaceLayoutStore.getState().readingPositions.first).toBe(880));

  rerender(<Document key="second" scope="second" ready />);
  expect(screen.getByTestId("reading-area").scrollTop).toBe(0);
  rerender(<Document key="first" scope="first" ready />);

  expect(screen.getByTestId("reading-area").scrollTop).toBe(880);
});

it("restores a migrated file's legacy position until that file has its own saved position", () => {
  useChatWorkspaceLayoutStore.getState().setReadingPosition("old-file-path", 880);
  const { rerender } = render(<Document scope="file-identity" legacyScope="old-file-path" ready />);
  expect(screen.getByTestId("reading-area").scrollTop).toBe(880);

  rerender(<Document key="other" scope="other" ready />);
  useChatWorkspaceLayoutStore.getState().setReadingPosition("file-identity", 1200);
  rerender(<Document key="file" scope="file-identity" legacyScope="old-file-path" ready />);

  expect(screen.getByTestId("reading-area").scrollTop).toBe(1200);
});

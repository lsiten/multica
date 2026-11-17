import { indexedTextRange, indexCommentText } from "../../issues/components/comment-annotation-selection";

const HIGHLIGHT_NAME = "chat-message-search";

export function clearChatSearchMatch() {
  if (typeof CSS !== "undefined" && CSS.highlights) CSS.highlights.delete(HIGHLIGHT_NAME);
}

/** Virtuoso reveals the row first; then align the actual matching text. */
export function revealChatSearchMatch(container: HTMLElement | null, messageId: string, term: string): boolean {
  const row = Array.from(container?.querySelectorAll<HTMLElement>("[data-chat-message-id]") ?? [])
    .find((element) => element.dataset.chatMessageId === messageId);
  if (!row) return false;
  if (!term.trim()) { row.scrollIntoView({ block: "start", behavior: "instant" }); return true; }
  const index = indexCommentText(row);
  const { text } = index;
  const start = text.toLocaleLowerCase().indexOf(term.toLocaleLowerCase());
  if (start < 0) { row.scrollIntoView({ block: "start", behavior: "instant" }); return true; }
  const range = indexedTextRange(index, start, term.length);
  if (!range) return false;
  const rectangle = range.getBoundingClientRect();
  if (!rectangle.height) return false;
  if (typeof Highlight !== "undefined" && typeof CSS !== "undefined" && CSS.highlights) CSS.highlights.set(HIGHLIGHT_NAME, new Highlight(range));
  for (let parent = row.parentElement; parent; parent = parent.parentElement) {
    if (parent.scrollHeight > parent.clientHeight && /auto|scroll/.test(getComputedStyle(parent).overflowY)) {
      parent.scrollBy({ top: rectangle.top - parent.getBoundingClientRect().top - parent.clientHeight / 3, behavior: "instant" });
      break;
    }
  }
  return true;
}

import { search } from "@codemirror/search";
import type { Extension } from "@codemirror/state";

/**
 * CodeMirror's basic setup includes search commands but keeps the search panel
 * closed. Keeping the explicit extension here documents that Mod-f is part of
 * every document/code editor contract.
 */
export const basicSetupSearch: Extension = search({ top: true });

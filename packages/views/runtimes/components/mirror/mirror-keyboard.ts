export function remoteKeyCombo(event: Pick<KeyboardEvent,
  "altKey" | "ctrlKey" | "isComposing" | "key" | "metaKey" | "shiftKey"
>): string | null {
  if (event.isComposing) return null;
  const key = normalizeKey(event.key);
  if (!key) return null;
  const modifiers = [
    event.metaKey ? "cmd" : "",
    event.ctrlKey ? "ctrl" : "",
    event.altKey ? "alt" : "",
    event.shiftKey ? "shift" : "",
  ].filter(Boolean);
  if (!modifiers.length) return key;
  return [...modifiers, key].join("+");
}

export function isPrintableRemoteText(event: Pick<KeyboardEvent,
  "altKey" | "ctrlKey" | "isComposing" | "key" | "metaKey"
>): boolean {
  return !event.isComposing &&
    !event.altKey &&
    !event.ctrlKey &&
    !event.metaKey &&
    [...event.key].length === 1;
}

function normalizeKey(key: string): string | null {
  switch (key) {
    case " ":
      return "space";
    case "Enter":
      return "return";
    case "Escape":
      return "escape";
    case "Tab":
      return "tab";
    case "Backspace":
      return "delete";
    case "ArrowUp":
      return "up";
    case "ArrowDown":
      return "down";
    case "ArrowLeft":
      return "left";
    case "ArrowRight":
      return "right";
    case "Control":
    case "Meta":
    case "Alt":
    case "Shift":
    case "CapsLock":
    case "Process":
    case "Dead":
      return null;
    default:
      if (/^F(?:[1-9]|1[0-2])$/.test(key)) return key.toLowerCase();
      return [...key].length === 1 ? key.toLowerCase() : key.toLowerCase();
  }
}

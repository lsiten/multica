import type { HumanRequest, HumanRequestAnswer } from "../types/human-request";

/** Match the whole reply against the exact offered revision, not fragments of a discussion. */
export function matchHumanTextAnswer(request: HumanRequest, text: string, explicitInput = false): HumanRequestAnswer | null {
  if (request.status !== "pending" || !request.can_respond || request.payload.response_mode !== "chat_or_card" || Date.parse(request.expires_at) <= Date.now()) return null;
  const trimmed = text.trim();
  if (!trimmed || text.length > 8000) return null;
  if (request.payload.kind === "input") return explicitInput ? { revision: request.revision, decision: "input", answer: text } : null;
  if (request.payload.kind !== "choice") return null;
  const matched = new Set<string>();
  request.payload.choices.forEach((choice, index) => {
    if (trimmed === choice.label.trim() || trimmed.toUpperCase() === String.fromCharCode(65 + index) || trimmed === String(index + 1)) matched.add(choice.id);
  });
  const ordinal = /^(?:选|选择)?第?([1-4])(?:项|个选项)?$/.exec(trimmed);
  const offered = ordinal ? request.payload.choices[Number(ordinal[1]) - 1] : undefined;
  if (offered) matched.add(offered.id);
  if (matched.size !== 1) return null;
  return { revision: request.revision, decision: "choice", answer: [...matched][0] };
}

export function humanTextAnswerLabel(request: HumanRequest, answer: HumanRequestAnswer): string {
  return request.payload.choices.find(choice => choice.id === answer.answer)?.label ?? answer.answer ?? "";
}

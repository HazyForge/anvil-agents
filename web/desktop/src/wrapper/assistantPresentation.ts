const STATUS_JSON_PREFIX = "ANVIL_AGENT_RUN_STATUS_JSON=";

export type AssistantPresentation = {
  reasoning: string;
  reply: string;
};

function jsonObjectEnd(text: string, from: number): number {
  const start = text.indexOf("{", from);
  if (start < 0) {
    return -1;
  }
  let depth = 0;
  for (let i = start; i < text.length; i++) {
    const ch = text[i];
    if (ch === "{") {
      depth += 1;
    } else if (ch === "}") {
      depth -= 1;
      if (depth === 0) {
        return i + 1;
      }
    }
  }
  return -1;
}

/** Drop harness protocol lines so they stay in runner activity, not the bubble. */
export function stripStatusJSON(text: string): string {
  if (!text) {
    return "";
  }
  let out = "";
  let from = 0;
  while (from < text.length) {
    const index = text.indexOf(STATUS_JSON_PREFIX, from);
    if (index < 0) {
      out += text.slice(from);
      break;
    }
    out += text.slice(from, index);
    const end = jsonObjectEnd(text, index + STATUS_JSON_PREFIX.length);
    from = end > index ? end : index + STATUS_JSON_PREFIX.length;
  }
  return out.replace(/[ \t]+\n/g, "\n").replace(/\n{3,}/g, "\n\n").trim();
}

/**
 * Separate grok-build process narration + STATUS_JSON from the user-visible
 * reply. Clean answers (standing assistant, no protocol trailer) stay intact.
 */
export function splitAssistantPresentation(content: string): AssistantPresentation {
  const text = content ?? "";
  const last = text.lastIndexOf(STATUS_JSON_PREFIX);
  if (last < 0) {
    return { reasoning: "", reply: text.trim() };
  }
  const jsonEnd = jsonObjectEnd(text, last + STATUS_JSON_PREFIX.length);
  const after = stripStatusJSON(jsonEnd > last ? text.slice(jsonEnd) : "");
  const reasoning = stripStatusJSON(text.slice(0, last));
  if (!after) {
    return { reasoning: "", reply: stripStatusJSON(text) };
  }
  return { reasoning, reply: after };
}

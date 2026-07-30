export interface OrderedCapabilitySelection {
  type: "atomic" | "set";
  name: string;
}

function asRecord(value: unknown): Record<string, unknown> {
  return typeof value === "object" && value ? value as Record<string, unknown> : {};
}

export function preserveNamedReference(prior: unknown, name: string): Record<string, unknown> {
  const previous = asRecord(prior);
  return String(previous.name ?? "") === name ? { ...previous, name } : { name };
}

export function preserveOrderedNamedReferences(prior: unknown, names: string[]): Record<string, unknown>[] {
  const previous = Array.isArray(prior) ? prior.map(asRecord) : [];
  return names.map((name) => preserveNamedReference(previous.find((ref) => String(ref.name ?? "") === name), name));
}

export function preserveCapabilitySelections(
  prior: unknown,
  selections: OrderedCapabilitySelection[],
  atomicKey: string,
  setKey: string,
): Record<string, unknown>[] {
  const previous = Array.isArray(prior) ? prior.map(asRecord) : [];
  return selections.map((selection) => {
    const key = selection.type === "atomic" ? atomicKey : setKey;
    const match = previous.find((candidate) => {
      const ref = asRecord(candidate[key]);
      return String(ref.name ?? "") === selection.name;
    });
    return {
      ...(match ?? {}),
      [key]: preserveNamedReference(match?.[key], selection.name),
    };
  });
}

import { apiURL } from "../api/client";

export type UIConfig = {
  productTitle: string;
  defaultNamespaces: string[];
  oidc: {
    issuer: string;
    clientId: string;
    audiences: string[];
    scopes: string[];
  };
  composition: {
    readEnabled: boolean;
    writeEnabled: boolean;
  };
};

let cached: UIConfig | null = null;
let inflight: Promise<UIConfig> | null = null;

export async function loadUIConfig(force = false): Promise<UIConfig> {
  if (!force && cached) {
    return cached;
  }
  if (!force && inflight) {
    return inflight;
  }
  inflight = (async () => {
    const response = await fetch(apiURL("/ui-config.json"), {
      headers: { Accept: "application/json" },
      cache: "no-store",
    });
    if (!response.ok) {
      throw new Error(`ui-config unavailable (${response.status})`);
    }
    const body = (await response.json()) as UIConfig;
    if (!body?.oidc?.issuer || !body?.oidc?.clientId) {
      throw new Error("ui-config missing oidc.issuer or oidc.clientId");
    }
    const composition = (body as UIConfig).composition;
    cached = {
      productTitle: body.productTitle || "Anvil Agents Console",
      defaultNamespaces: Array.isArray(body.defaultNamespaces) ? body.defaultNamespaces : [],
      oidc: {
        issuer: body.oidc.issuer.replace(/\/+$/, ""),
        clientId: body.oidc.clientId,
        audiences: Array.isArray(body.oidc.audiences) ? body.oidc.audiences : [],
        scopes:
          Array.isArray(body.oidc.scopes) && body.oidc.scopes.length > 0
            ? body.oidc.scopes
            : ["openid", "profile", "email", "offline_access"],
      },
      composition: {
        readEnabled: Boolean(composition?.readEnabled),
        writeEnabled: Boolean(composition?.writeEnabled),
      },
    };
    return cached;
  })();
  try {
    return await inflight;
  } finally {
    inflight = null;
  }
}

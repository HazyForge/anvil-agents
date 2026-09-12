import { apiURL } from "../api/client";
import { PRODUCT_TITLE } from "../product";

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
  controls?: {
    readEnabled: boolean;
    writeEnabled: boolean;
  };
  runs: {
    createEnabled: boolean;
  };
  chat?: {
    enabled: boolean;
    councilEnabled?: boolean;
    path?: string;
    councilPath?: string;
  };
  desktop?: {
    wrapper?: boolean;
    kubernetes?: boolean;
    productName?: string;
    stubSession?: boolean;
    oidcRedirectPath?: string;
  };
};

let cached: UIConfig | null = null;
let inflight: Promise<UIConfig> | null = null;

export function clearUIConfigCache(): void {
  cached = null;
  inflight = null;
}

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
    const composition = body.composition;
    const controls = body.controls;
    const runs = body.runs;
    const chat = body.chat;
    const desktop = (body as UIConfig).desktop;
    cached = {
      productTitle: PRODUCT_TITLE,
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
      controls: {
        readEnabled: Boolean(controls?.readEnabled),
        writeEnabled: Boolean(controls?.writeEnabled),
      },
      runs: {
        createEnabled: Boolean(runs?.createEnabled),
      },
      chat: {
        enabled: Boolean(chat?.enabled),
        councilEnabled: Boolean(chat?.councilEnabled ?? chat?.enabled),
        path: chat?.path,
        councilPath: chat?.councilPath,
      },
      desktop: {
        wrapper: Boolean(desktop?.wrapper),
        kubernetes: Boolean(desktop?.kubernetes),
        productName: typeof desktop?.productName === "string" ? desktop.productName : PRODUCT_TITLE,
        stubSession: Boolean(desktop?.stubSession),
        oidcRedirectPath:
          typeof desktop?.oidcRedirectPath === "string" && desktop.oidcRedirectPath.startsWith("/")
            ? desktop.oidcRedirectPath
            : undefined,
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

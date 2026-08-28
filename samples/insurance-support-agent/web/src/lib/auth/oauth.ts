import { useCallback, useEffect, useRef, useState } from "react";

import { CONFIG } from "@/lib/config";
import type { Auth, AuthStatus } from "./types";

const TOKEN_KEY = "insurance.accessToken";
const NAME_KEY = "insurance.userName";
const VERIFIER_KEY = "insurance.pkceVerifier";
const STATE_KEY = "insurance.oauthState";

interface Discovery {
  authorization_endpoint: string;
  token_endpoint: string;
  end_session_endpoint?: string;
}

const redirectUri = () => window.location.origin + "/";

async function discover(): Promise<Discovery> {
  const res = await fetch(`${CONFIG.issuer}/.well-known/openid-configuration`);
  if (!res.ok) throw new Error(`OpenID discovery failed (${res.status}) at ${CONFIG.issuer}`);
  return (await res.json()) as Discovery;
}

function base64Url(bytes: Uint8Array): string {
  let s = "";
  for (const b of bytes) s += String.fromCharCode(b);
  return btoa(s).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

function randomString(bytes = 32): string {
  const buf = new Uint8Array(bytes);
  crypto.getRandomValues(buf);
  return base64Url(buf);
}

async function challenge(verifier: string): Promise<string> {
  const digest = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(verifier));
  return base64Url(new Uint8Array(digest));
}

function nameFromIdToken(idToken: string | undefined): string | undefined {
  if (!idToken) return undefined;
  try {
    const part = idToken.split(".")[1].replace(/-/g, "+").replace(/_/g, "/");
    const claims = JSON.parse(atob(part)) as Record<string, string>;
    return claims.given_name || claims.name || claims.preferred_username;
  } catch {
    return undefined;
  }
}

export function useOauthAuth(): Auth {
  const [status, setStatus] = useState<AuthStatus>("loading");
  const [error, setError] = useState<string | undefined>();
  const [name, setName] = useState<string | undefined>();
  const token = useRef<string | null>(null);
  const started = useRef(false);

  useEffect(() => {
    if (started.current) return;
    started.current = true;

    const params = new URLSearchParams(window.location.search);
    const code = params.get("code");

    if (params.get("error")) {
      setError(params.get("error_description") || params.get("error") || "sign-in failed");
      setStatus("signed-out");
      window.history.replaceState({}, "", redirectUri());
      return;
    }

    if (!code) {
      const held = sessionStorage.getItem(TOKEN_KEY);
      if (held) {
        token.current = held;
        setName(sessionStorage.getItem(NAME_KEY) ?? undefined);
        setStatus("signed-in");
      } else {
        setStatus("signed-out");
      }
      return;
    }

    void (async () => {
      try {
        if (params.get("state") !== sessionStorage.getItem(STATE_KEY)) {
          throw new Error("State mismatch — possible CSRF, sign-in aborted.");
        }
        const verifier = sessionStorage.getItem(VERIFIER_KEY);
        if (!verifier) throw new Error("Missing PKCE verifier — start the sign-in again.");

        const meta = await discover();
        const res = await fetch(meta.token_endpoint, {
          method: "POST",
          headers: { "Content-Type": "application/x-www-form-urlencoded" },
          body: new URLSearchParams({
            grant_type: "authorization_code",
            code,
            redirect_uri: redirectUri(),
            client_id: CONFIG.clientId,
            code_verifier: verifier,
          }),
        });
        if (!res.ok) throw new Error(`Token exchange failed (${res.status}): ${await res.text()}`);

        const tokens = (await res.json()) as { access_token: string; id_token?: string };
        const who = nameFromIdToken(tokens.id_token);

        sessionStorage.removeItem(VERIFIER_KEY);
        sessionStorage.removeItem(STATE_KEY);
        sessionStorage.setItem(TOKEN_KEY, tokens.access_token);
        if (who) sessionStorage.setItem(NAME_KEY, who);

        token.current = tokens.access_token;
        setName(who);
        setStatus("signed-in");
      } catch (err) {
        setError((err as Error).message);
        setStatus("signed-out");
      } finally {
        window.history.replaceState({}, "", redirectUri());
      }
    })();
  }, []);

  const signIn = useCallback(() => {
    void (async () => {
      try {
        setError(undefined);
        const meta = await discover();
        const verifier = randomString();
        const state = randomString(16);
        sessionStorage.setItem(VERIFIER_KEY, verifier);
        sessionStorage.setItem(STATE_KEY, state);

        const url = new URL(meta.authorization_endpoint);
        url.searchParams.set("response_type", "code");
        url.searchParams.set("client_id", CONFIG.clientId);
        url.searchParams.set("redirect_uri", redirectUri());
        url.searchParams.set("scope", CONFIG.scopes);
        url.searchParams.set("state", state);
        url.searchParams.set("code_challenge", await challenge(verifier));
        url.searchParams.set("code_challenge_method", "S256");
        window.location.assign(url.toString());
      } catch (err) {
        setError((err as Error).message);
      }
    })();
  }, []);

  const signOut = useCallback(() => {
    token.current = null;
    sessionStorage.removeItem(TOKEN_KEY);
    sessionStorage.removeItem(NAME_KEY);
    setName(undefined);
    setStatus("signed-out");

    void (async () => {
      try {
        const meta = await discover();
        if (!meta.end_session_endpoint) return;
        const url = new URL(meta.end_session_endpoint);
        url.searchParams.set("post_logout_redirect_uri", redirectUri());
        url.searchParams.set("client_id", CONFIG.clientId);
        window.location.assign(url.toString());
      } catch {
        // Providers without an end_session_endpoint stay signed out locally.
      }
    })();
  }, []);

  return {
    mode: "oauth",
    status,
    user: name ? { name } : undefined,
    signIn,
    signOut,
    getAccessToken: async () => token.current,
    error,
  };
}

import type { Auth } from "./types";

const noop = () => {};

export function useNoneAuth(): Auth {
  return {
    mode: "none",
    status: "signed-in",
    signIn: noop,
    signOut: noop,
    getAccessToken: async () => null,
  };
}

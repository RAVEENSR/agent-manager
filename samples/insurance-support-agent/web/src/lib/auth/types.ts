export type AuthStatus = "loading" | "signed-out" | "signed-in" | "error";

export interface Auth {
  mode: string;
  status: AuthStatus;
  user?: { name?: string };
  signIn(): void;
  signOut(): void;
  getAccessToken(): Promise<string | null>;
  error?: string;
}

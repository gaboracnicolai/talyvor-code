// Credential migration + load, pure and vscode-free so it is unit-testable under the Node test
// harness. The extension used to read API keys from PLAINTEXT `talyvor.*ApiKey` settings, which sync
// across machines, land in dotfiles repos, and show up in screen shares. This moves them to VS Code's
// SecretStorage (the OS keychain), mirroring what the JetBrains plugin already does with PasswordSafe.
//
// The vscode bindings live in config.ts; everything decision-making is here.

// SECRET_KEYS are the settings keys that hold credentials and must never live in plaintext. Order is
// stable so migration/logging is deterministic.
export const SECRET_KEYS = ["lensApiKey", "trackApiKey", "docsApiKey", "githubToken"] as const;
export type SecretKey = (typeof SECRET_KEYS)[number];

// SecretStore is the slice of vscode.SecretStorage this module needs.
export interface SecretStore {
  get(key: string): Promise<string | undefined>;
  store(key: string, value: string): Promise<void>;
  delete(key: string): Promise<void>;
}

// PlaintextStore is the slice of the workspace-config surface this module needs: read the legacy
// plaintext value, and clear it (cfg.update(key, undefined, Global) removes it so later reads are "").
export interface PlaintextStore {
  read(key: string): string;
  clear(key: string): Promise<void>;
}

// migrateSecrets moves any plaintext credential into the secret store and clears the plaintext.
// Returns the keys it migrated (for a one-line log). The invariant is NEVER LOSE THE KEY:
//   - store BEFORE clear, so a crash between the two leaves the plaintext intact for the next run;
//   - a non-empty plaintext always wins (a user who re-typed a key into settings gets it migrated),
//     and an empty plaintext is a no-op (so a second run — plaintext already cleared — keeps the
//     already-migrated secret). Both make the operation idempotent and loss-free.
export async function migrateSecrets(
  settings: PlaintextStore,
  secrets: SecretStore,
): Promise<string[]> {
  const migrated: string[] = [];
  for (const key of SECRET_KEYS) {
    const plaintext = settings.read(key).trim();
    if (plaintext === "") continue; // nothing to migrate; any existing secret is left untouched
    await secrets.store(key, plaintext); // store FIRST — the secret is safe before we drop the plaintext
    await settings.clear(key); // then remove the plaintext copy
    migrated.push(key);
  }
  return migrated;
}

// loadSecrets reads every credential out of the secret store into a plain record the (synchronous)
// config reader can serve without another async hop. Missing keys become "".
export async function loadSecrets(secrets: SecretStore): Promise<Record<SecretKey, string>> {
  const out = {} as Record<SecretKey, string>;
  for (const key of SECRET_KEYS) {
    out[key] = (await secrets.get(key)) ?? "";
  }
  return out;
}

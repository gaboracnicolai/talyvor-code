// Tests for the credential migration: plaintext settings → SecretStorage. Pure Node (no vscode),
// matching the repo's self-contained-runner convention. In-memory fakes stand in for VS Code's
// SecretStorage and the workspace-config surface.

import {
  SECRET_KEYS,
  migrateSecrets,
  loadSecrets,
  type SecretStore,
  type PlaintextStore,
} from "../src/secrets-pure";

function assert(cond: unknown, msg: string): asserts cond {
  if (!cond) throw new Error("ASSERT: " + msg);
}

// A fake SecretStorage (VS Code's context.secrets).
class FakeSecrets implements SecretStore {
  private m = new Map<string, string>();
  async get(key: string): Promise<string | undefined> {
    return this.m.get(key);
  }
  async store(key: string, value: string): Promise<void> {
    this.m.set(key, value);
  }
  async delete(key: string): Promise<void> {
    this.m.delete(key);
  }
  peek(key: string): string | undefined {
    return this.m.get(key);
  }
}

// A fake plaintext settings surface. `read` mirrors cfg.get<string>(key, ""); `clear` mirrors
// cfg.update(key, undefined, Global) which removes the key (subsequent reads return "").
class FakeSettings implements PlaintextStore {
  cleared: string[] = [];
  constructor(private m: Record<string, string>) {}
  read(key: string): string {
    return this.m[key] ?? "";
  }
  async clear(key: string): Promise<void> {
    delete this.m[key];
    this.cleared.push(key);
  }
}

// (a) a key in plaintext settings is migrated to SecretStorage AND the plaintext is cleared.
async function testMigratesAndClears(): Promise<void> {
  const settings = new FakeSettings({ lensApiKey: "tlv_live_secret" });
  const secrets = new FakeSecrets();

  const migrated = await migrateSecrets(settings, secrets);

  assert(migrated.includes("lensApiKey"), "lensApiKey reported as migrated");
  assert(secrets.peek("lensApiKey") === "tlv_live_secret", "secret stored securely");
  assert(settings.read("lensApiKey") === "", "plaintext setting cleared");
  assert(settings.cleared.includes("lensApiKey"), "clear() was called for the key");
}

// (b) after migration the extension can still authenticate — loadSecrets returns the value the
// clients are constructed with.
async function testAuthenticatesAfterMigration(): Promise<void> {
  const settings = new FakeSettings({ lensApiKey: "K1", trackApiKey: "K2", docsApiKey: "K3", githubToken: "K4" });
  const secrets = new FakeSecrets();

  await migrateSecrets(settings, secrets);
  const loaded = await loadSecrets(secrets);

  assert(loaded.lensApiKey === "K1", "lens key available post-migration");
  assert(loaded.trackApiKey === "K2", "track key available");
  assert(loaded.docsApiKey === "K3", "docs key available");
  assert(loaded.githubToken === "K4", "github token available");
  for (const k of SECRET_KEYS) assert(settings.read(k) === "", `${k} plaintext cleared`);
}

// (c) running the migration twice does NOT lose the key (idempotent, crash-safe).
async function testIdempotentNoLoss(): Promise<void> {
  const settings = new FakeSettings({ lensApiKey: "persist_me" });
  const secrets = new FakeSecrets();

  const first = await migrateSecrets(settings, secrets);
  assert(first.includes("lensApiKey"), "first run migrates");

  const second = await migrateSecrets(settings, secrets);
  assert(second.length === 0, "second run migrates nothing (plaintext already cleared)");
  assert(secrets.peek("lensApiKey") === "persist_me", "key survives a second run — never lost");
}

// A user who re-enters a key in settings after migration: the new plaintext is migrated over and
// cleared (settings is legacy input, SecretStorage is authoritative afterward).
async function testReEnteredPlaintextReMigrates(): Promise<void> {
  const settings = new FakeSettings({ lensApiKey: "old" });
  const secrets = new FakeSecrets();
  await migrateSecrets(settings, secrets);

  // user pastes a fresh key into settings.json
  (settings as unknown as { m: Record<string, string> }).m.lensApiKey = "new";
  const migrated = await migrateSecrets(settings, secrets);
  assert(migrated.includes("lensApiKey"), "re-entered plaintext is re-migrated");
  assert(secrets.peek("lensApiKey") === "new", "SecretStorage updated to the fresh value");
  assert(settings.read("lensApiKey") === "", "plaintext cleared again");
}

// Empty plaintext + no existing secret: nothing happens (a brand-new user with no key set).
async function testNoKeyNoOp(): Promise<void> {
  const settings = new FakeSettings({});
  const secrets = new FakeSecrets();
  const migrated = await migrateSecrets(settings, secrets);
  assert(migrated.length === 0, "no keys, nothing migrated");
  assert(secrets.peek("lensApiKey") === undefined, "no secret written");
}

async function main(): Promise<void> {
  await testMigratesAndClears();
  await testAuthenticatesAfterMigration();
  await testIdempotentNoLoss();
  await testReEnteredPlaintextReMigrates();
  await testNoKeyNoOp();
  // eslint-disable-next-line no-console
  console.log("ok (5 tests)");
}

main().catch((err) => {
  // eslint-disable-next-line no-console
  console.error(err);
  process.exit(1);
});

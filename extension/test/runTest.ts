// Test harness. `npm test` compiles the project (tsc) and runs
// this file, which discovers every compiled `*.test.js` sibling
// and executes it in its own child process.
//
// Each test file is a self-contained runner: it invokes its
// suite in a top-level `main()` and calls `process.exit(1)` on
// failure. Running them as separate processes (rather than
// `require`-ing them in-process) keeps that exit-on-failure
// isolated — one failing file can't abort the others, and we get
// a clean per-file tally.

import { spawnSync } from "child_process";
import { readdirSync } from "fs";
import { join } from "path";

function main(): void {
  const dir = __dirname; // out/test
  const files = readdirSync(dir)
    .filter((f) => f.endsWith(".test.js"))
    .sort();

  let failed = 0;
  for (const file of files) {
    const res = spawnSync(process.execPath, [join(dir, file)], {
      stdio: "inherit",
    });
    if (res.status !== 0) {
      failed++;
      // eslint-disable-next-line no-console
      console.error(`FAIL ${file}`);
    }
  }

  // eslint-disable-next-line no-console
  console.log(`\n${files.length - failed}/${files.length} test files passed`);
  if (failed > 0) {
    process.exit(1);
  }
}

main();

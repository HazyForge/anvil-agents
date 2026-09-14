// Exercise Prime's actual kernel transport without a model or credentials.
import { mkdtemp, readFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { ReplKernelManager } from '/usr/local/lib/node_modules/prime-agent/dist/core/kernel/repl-manager.js';

const cwd = await mkdtemp(join(tmpdir(), 'prime-kernel-proof-'));
const kernel = new ReplKernelManager({ cwd });
const signal = AbortSignal.timeout(30000);
try {
  await kernel.start({ signal });
  const result = await kernel.execute(`from pathlib import Path
import subprocess, rlm, edit, agent_message
value = sum(i*i for i in range(1, 26))
Path('proof.txt').write_text(str(value))
print(subprocess.check_output(['cat', 'proof.txt'], text=True))`, { signal });
  if (result.status !== 'ok' || !result.stdout.includes('5525') || (await readFile(join(cwd, 'proof.txt'), 'utf8')) !== '5525') {
    throw new Error(`Native Prime kernel file/shell test failed (${result.status})`);
  }
  console.log('PRIME_NATIVE_KERNEL_TOOLS_READY');
} finally {
  await kernel.kill();
  await rm(cwd, { recursive: true, force: true });
}

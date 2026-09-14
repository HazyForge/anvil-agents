"""Install the Python runtime shipped with the pinned native Prime package."""
import json
from pathlib import Path
import subprocess
import sys

package = Path('/usr/local/lib/node_modules/prime-agent')
module = (package / 'dist/core/kernel/bootstrap.js').as_uri()
extras = json.loads(subprocess.check_output([
    'node', '--input-type=module', '-e',
    f'import {{DEFAULT_RLM_EXTRA_UV_ARGS}} from {json.dumps(module)};'
    'console.log(JSON.stringify(DEFAULT_RLM_EXTRA_UV_ARGS));',
], text=True))
skills = sorted(str(path.parent) for path in (package / 'dist/skills').glob('*/pyproject.toml'))
subprocess.run([
    sys.executable, '-m', 'pip', 'install', '--no-cache-dir',
    str(package / 'dist/prime-agent-runtime'), 'dill', *extras, *skills,
], check=True)

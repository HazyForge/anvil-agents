# Install Anvil Agents Desktop

**Anvil Agents Desktop** is the workstation wrapper for the anvil-agents OIDC
API and for local harness CLIs. The process binary stays `anvil-desktop`.
User-visible name, shortcuts, and window title are **Anvil Agents Desktop**.

This is not Anvil Desktop, Anvil Hub, or a kube UI. Remote API origin is **Anvil
Primaris** or another configured `apiOrigin`. Local harness processes never
receive the OIDC token.

## Build artifacts

From a checkout:

```bash
make desktop-package
```

Writes `dist/desktop/`:

| File | What it is |
| --- | --- |
| `Anvil-Agents-Desktop-Setup-0.1.0-windows-amd64.exe` | Per-user Windows installer (no Administrator) |
| `Anvil-Agents-Desktop-0.1.0-windows-amd64.zip` | Portable Windows tree + `install.ps1` |
| `anvil-desktop-0.1.0-linux-amd64.tar.gz` | Linux binary + `install.sh` |
| `README.txt` | Same install notes bundled in the archives |
| `SHA256SUMS` | Checksums |

`VERSION` and `OUTPUT` override the default `0.1.0` and `dist/desktop`.

## Windows (workstation)

Preferred: run `Anvil-Agents-Desktop-Setup-*.exe`. It extracts to
`%LOCALAPPDATA%\Programs\AnvilAgentsDesktop` and creates a Start Menu shortcut
**Anvil Agents Desktop** that starts `anvil-desktop.exe --open`.

Or unzip the zip and run:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\install.ps1
```

Uninstall: `Anvil-Agents-Desktop-Setup.exe --uninstall` or
`install.ps1 -Uninstall`.

The host listens on **`http://127.0.0.1:1738`** only. Set API origin to
`https://agents.anvil.hazyforge.io`. Sign-in uses `{apiOrigin}/ui-config.json`
(issuer/audience/client id). Until GitOps writes `desktop.oidcClientId`, that
document's `oidc.clientId` is the console PKCE app. Register
`http://127.0.0.1:1738/auth/callback` on the desktop PKCE client when it is
assigned (`anvil-agents-desktop` / Native key `anvil_agents_desktop`).

## Linux

```bash
tar -xzf anvil-desktop-0.1.0-linux-amd64.tar.gz
PREFIX=$HOME/.local ./install.sh
# from a checkout: ./hack/install-anvil-desktop.sh
```

## Operate on WSL

On the **Local** page, choose **Operate on WSL**.

- **Already in Ubuntu WSL2** (this workstation today): Desktop is a Linux
  process. Operate on WSL uses the distro PATH (`~/.local/bin` has grok and
  Codex). `wsl.exe` is not required.
- **Windows-hosted `anvil-desktop.exe`**: Desktop locates `wsl.exe`, uses the
  default distro (or a named distro from prefs), and discovers/invokes catalog
  CLIs with `wsl.exe --exec` so grok/Codex/OpenCode run **inside WSL**, not on
  native Windows PATH (which does not have those CLIs).

The OIDC token stays in the desktop session. It is never copied into WSL
argv, env, or prompt files.

## After install

1. Launch Anvil Agents Desktop.
2. Set OIDC API origin to `https://agents.anvil.hazyforge.io` (or another `apiOrigin`).
3. Sign in with OIDC (Authorization Code + PKCE). Production IdP is Zitadel.
4. On Local, choose Operate on WSL if you work in WSL.
5. Wrapper → local-harness delegates without copying the bearer token.

Optional Electron wrap (`web/desktop/electron`) spawns the `anvil-desktop`
sidecar from `extraResources` when packaged with electron-builder. The Go
setup.exe does not require Electron.

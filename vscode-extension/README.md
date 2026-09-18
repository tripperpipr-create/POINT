# Point IDE integration

Built-in Point integration: custom home, fast project switcher and a full-page Russian Agent Hub. It includes a 2-step Master-first onboarding (connection or built-in engine, then Master policy); Companion and agents are optional post-onboarding capabilities. The Hub also provides local/remote provider presets including LLMux, arbitrary model IDs, agent roles/goals/prompts/rules, explicit permissions, per-agent context windows, rolling run memory, structured quests, reusable flows, an incremental local code index, custom typed tools, immutable execution and file-change history, safe rollback, and approvals for commands and patches.

Point also contributes a language-neutral navigation layer: Search Everywhere (`Ctrl+N`), file and workspace-symbol search, a stable Find Usages view (`Alt+F7`), JetBrains-style navigation keybindings, directory-shaped Git changes, and an on-demand Open VSX language support catalog. Heavy language servers are never started for unrelated languages.

## Architecture

- Point owns the editor, tabs, project tree, terminal, keybindings and workspace trust.
- A multi-root window has one explicit active Point project. The core, local index, agent attachments and IDE observations are isolated to that root; other roots remain available to the editor, Git and run configurations, and can be selected without reopening the workspace.
- The built-in integration owns the full editor-width Agent Hub, the dedicated Companion chat in the right sidebar, the compact peek chat, and the lifecycle of the local Point core.
- The bundled Go service stores profiles, indexed code metadata and run/change history locally, talks to Ollama or an OpenAI-compatible endpoint, and enforces workspace boundaries.
- Agent Hub creates profiles from built-in templates, controls each tool explicitly, supports clone/import/export workflows and builds strict parameterized process tools without an implicit shell.
- Cursor Agent CLI is started only as an interactive terminal session, preserving its own confirmation flow.
- A deterministic completion gate rejects unsupported success claims, gives one correction episode and requires a recognized test/build/lint/static-analysis command with exit zero and no timeout after the newest accepted change when verification is available.
- The Russian read-only launch preview shows that completion contract before starting and highlights criteria that cannot be satisfied with the selected tool permissions.
- User-authored command/process tools can be explicitly designated as completion evidence; the builder explains the exit-code contract and the immutable run snapshot preserves the trust decision.
- Run diagnostics replay local history without another model call and expose token/latency/context-compaction/tool/approval/patch/verification/completion evidence plus recent-run comparison.
- Approved commands and custom tools are bracketed by bounded workspace snapshots. Exact text mutations appear in the artifact chronicle with their source and safe rollback; sensitive/binary/oversized changes are detected without storing their contents and are marked non-revertible.
- Redis is optional. Without it the service uses the built-in in-memory event bus.

## Install

Point ships this extension built in. A standalone VSIX is generated only for extension development and testing.

The package includes the Windows x64 `point-core.exe`. For development, `localAgent.backendPath` may point to another compatible core build.

## Development

1. From the repository root, build the core with `go build -o vscode-extension/bin/point-core.exe ./cmd/server`.
2. Run `cd vscode-extension`, `npm ci`, then `npm run check`.
3. Open `vscode-extension` in a compatible extension host and press `F5`.

Cursor support is built from the pinned `@cursor/sdk` development dependency. Its open-source JS dependencies are bundled into `dist/cursor-deps.cjs`; production keeps only `@cursor/sdk-win32-x64` for native sandbox/tree-sitter assets. Loading the Point extension does not load the SDK — it is required on the first Cursor status, login, or run. The Hub UI is vanilla JS: edit `ui/client/` and `ui/layers/`, then rebuild; `media/main.js`, `media/style.css` and `media/rpg-tokens.css` are generated artifacts.

Onboarding completion is stored per folder in `workspaceState` (`point.agentOnboardingComplete`). Incremental index updates and Hub-ready handshake refuse to restart `point-core` after Stop (`allowStart: false`). `Ctrl+Shift+L` opens the compact Companion chat; `Ctrl+Alt+;` opens the full right-sidebar thread. Both surfaces share one in-flight request, transcript, queue, streaming state, and Stop action.

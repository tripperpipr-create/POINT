# Auto Master and host Fast Agent

Expected behavior: new chats use auto; one system Fast Agent writes directly on the host; projectless chats own folders under ~/POINT/Chats; skills are discovered lazily.

Boundaries: core, persistence, HTTP API, extension and source webview. Existing Docker changes are preserved.

Acceptance: route selection, no Docker calls for Fast Agent, target pinned across project changes, isolated chat history, host command cancellation, skill revision attribution, source gates and separate live IDE acceptance.

Implemented on 2026-10-01:

- Master modes are auto (default), discuss, plan and fast. Legacy execute migrates to auto, agent to fast. Auto can answer, clarify, propose a plan awaiting approval, or start a limited host task.
- The global system-fast profile uses the per-run host_live executor and writes in the pinned target folder. Docker settings do not select its executor. Writer leases, patch content checks, cancellation, request replay and explicit ASK/DENY policies remain enforced.
- POINT chats own ~/POINT/Chats/<chatId>, use the common database and appear under the projectless chat group. Rename keeps the folder; conversation deletion preserves files. Continuing in an explicitly selected project copies conversation context.
- Skill discovery uses compact catalogs and read_skill, pinned library revisions, load attribution and repeated-read suppression. Disabled or retired skills are excluded; skills do not add tool permissions.
- The extension can start a separate POINT runtime without an open project, follows scoped master/Fast events, and exposes Fast Agent settings and chat files.

Final main checks: focused Go tests in app and agent passed, including host writes with the Docker backend selected, restart/replay, scope pinning and explicit ASK policy. Complete HTTP API, storage, orchestrator and tools package tests passed. go vet ./... and staticcheck ./... passed; unused functions from replaced routes were removed. Core/helpers and Hub bundle built; Hub smokes passed 85/85. UI contracts, webview message routes and documentation inventory (258 API routes) passed.

Remaining verification: live IDE/model acceptance has not been performed. The complete Go gate is not claimed green: an earlier full run failed before compatibility repairs; focused checks passed after those repairs. The existing five release-contract failures remain: threat row count, sandbox attestation, go.mod provenance and the diff-view/master-inbox module budgets. The user requested completion after the main tests.

Automatic forwarding of master attachments to the Fast Agent model is disabled: automatic approval review rejected forwarding potentially sensitive content to another model endpoint. Explicitly supplied Fast Agent context and files in its target folder remain available.

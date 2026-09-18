// The Guild is no longer an embedded activity-bar view. Keep the historical
// entry point, but make it exercise the current separate-window flow: opening
// the Hub, completing first-run setup, hiring a Project Agent and verifying the
// resulting roster. The optional old screenshot argument is intentionally not
// used here; the canonical Agent Hub verifier owns the live DOM contract.
const endpoint = process.argv[2];
if (!endpoint) throw new Error('Usage: node verify-point-guild-roster.mjs <endpoint> [legacy-output.png]');

process.argv[3] = 'character';
await import(`./verify-point-agent-nav.mjs?guild=${Date.now()}`);

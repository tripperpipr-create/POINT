import assert from 'node:assert/strict'
import fs from 'node:fs'
const source = fs.readFileSync(new URL('../vscode-extension/ui/client/completion-verdict.js', import.meta.url), 'utf8')
const { completionStatus, pendingAcceptanceText, verificationReuseText } = await import(`data:text/javascript;base64,${Buffer.from(source).toString('base64')}`)
const historical = { status: 'accepted_after_revision', acceptancePassed: true, evidence: { status: 'needs_review', criteria: [{ criterionId: 'http', text: 'Проверить HTTP-контракт', status: 'needs_review' }] } }
assert.equal(completionStatus(historical), 'needs_review')
assert.match(pendingAcceptanceText(historical), /Проверить HTTP-контракт/)
assert.equal(historical.status, 'accepted_after_revision')
assert.equal(completionStatus({ status: 'accepted_after_revision', acceptancePassed: false }), 'implementation_ready')
assert.equal(completionStatus({ ...historical, evidence: { status: 'blocked' } }), 'rejected')
assert.equal(completionStatus({ status: 'preparation_failed', verification: { ran: true, passed: false, needsReview: true } }), 'preparation_failed')
assert.equal(completionStatus({ status: 'rejected', verification: { ran: true, passed: false, needsReview: true } }), 'rejected')
assert.match(verificationReuseText({ verificationService: { wouldReuse: 'v1', agrees: false } }), /разошлись/)
assert.match(verificationReuseText({ verificationService: { reusedFrom: 'v1' } }), /v1/)
console.log('completion verdict smoke passed')

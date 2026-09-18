'use strict'

// @connectrpc/connect-node only imports undici to polyfill Headers on Node <18.
// Cursor SDK requires Node >=22.13, where the native implementation is always
// present, so bundling the full legacy HTTP stack would be dead weight.
module.exports = { Headers: globalThis.Headers }

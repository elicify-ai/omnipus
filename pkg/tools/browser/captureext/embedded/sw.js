// Omnipus capture extension service worker.
//
// All capture logic lives in the extension's own page (encoder.html /
// encoder.js), opened directly by the gateway via CDP once the extension is
// loaded (Extensions.loadUnpacked). This service worker carries no capture
// logic of its own — MV3 requires a background context to be declared when
// the manifest specifies one, and having it present keeps the extension
// alive/installable, but it is intentionally inert beyond a keepalive-safe
// install listener.
'use strict';

chrome.runtime.onInstalled.addListener(() => {
  // Nothing to initialize — encoder.html drives the whole capture flow.
});

// Q4 addition: CDP command translation for the input bridge. Given a
// {type, x, y, ...} event forwarded from the Go relay (originally a JSON
// message the viewer sent on the WebRTC "input" data channel), build and
// send the matching CDP Input.dispatchMouseEvent / Input.dispatchKeyEvent
// call against the captured tab's (metronome) session, and a thin
// Runtime.evaluate wrapper used both by the real input path is NOT (that's
// mouse/key only) and by /debug/evaluate verification tooling.
'use strict';

// Simple keycode/code table for the "keep mapping simple, spike scope" set:
// printable chars + Enter/Backspace/arrows. Printable chars are handled
// generically (charCodeAt), these are the named non-printable keys.
const NAMED_KEYS = {
  Enter: { windowsVirtualKeyCode: 13, code: 'Enter' },
  Backspace: { windowsVirtualKeyCode: 8, code: 'Backspace' },
  ArrowLeft: { windowsVirtualKeyCode: 37, code: 'ArrowLeft' },
  ArrowRight: { windowsVirtualKeyCode: 39, code: 'ArrowRight' },
  ArrowUp: { windowsVirtualKeyCode: 38, code: 'ArrowUp' },
  ArrowDown: { windowsVirtualKeyCode: 40, code: 'ArrowDown' },
  ' ': { windowsVirtualKeyCode: 32, code: 'Space' },
};

const MOUSE_CDP_TYPE = {
  mousemove: 'mouseMoved',
  mousedown: 'mousePressed',
  mouseup: 'mouseReleased',
  wheel: 'mouseWheel',
};

function mouseButtonName(button) {
  if (button === 2) return 'right';
  if (button === 1) return 'middle';
  return 'left';
}

// dispatchMouse sends one Input.dispatchMouseEvent to the captured tab.
// params: {type, x, y, button, deltaX, deltaY} (button/delta only meaningful
// for the relevant sub-types; Go always sends the full shape, harmless to
// ignore the rest).
async function dispatchMouse(cdp, sessionId, params) {
  const cdpType = MOUSE_CDP_TYPE[params.type];
  if (!cdpType) throw new Error('dispatchMouse: unknown event type ' + params.type);

  const args = {
    type: cdpType,
    x: params.x,
    y: params.y,
    button: cdpType === 'mouseMoved' ? 'none' : mouseButtonName(params.button),
    clickCount: cdpType === 'mousePressed' || cdpType === 'mouseReleased' ? 1 : 0,
  };
  if (cdpType === 'mouseWheel') {
    args.deltaX = params.deltaX || 0;
    args.deltaY = params.deltaY || 0;
  }
  await cdp.send('Input.dispatchMouseEvent', args, sessionId);
  return { dispatched: 'mouse', cdpType };
}

// dispatchKey sends one Input.dispatchKeyEvent to the captured tab.
// params: {type: keydown|keyup, key, code, keyCode}. Printable single-char
// keydowns carry `text`/`unmodifiedText` so Chrome actually fires the
// input/beforeinput events (bare keyDown without text does NOT type a
// character -- CDP quirk, load-bearing for the "text echo" verification
// page to see anything).
async function dispatchKey(cdp, sessionId, params) {
  const cdpType = params.type === 'keydown' ? 'keyDown' : 'keyUp';
  const named = NAMED_KEYS[params.key];
  const isPrintable = typeof params.key === 'string' && params.key.length === 1 && !named;

  const args = {
    type: cdpType,
    key: params.key,
    code: params.code || (named && named.code) || params.key,
    windowsVirtualKeyCode:
      params.keyCode || (named && named.windowsVirtualKeyCode) || (isPrintable ? params.key.charCodeAt(0) : 0),
  };
  if ((isPrintable || params.key === ' ') && cdpType === 'keyDown') {
    args.text = params.key;
    args.unmodifiedText = params.key;
  }
  await cdp.send('Input.dispatchKeyEvent', args, sessionId);
  return { dispatched: 'key', cdpType };
}

// evaluateOnTab runs a Runtime.evaluate expression on the captured tab's
// session, used for verification (reading window.__events back).
async function evaluateOnTab(cdp, sessionId, expression) {
  return cdp.eval(sessionId, expression, { userGesture: false });
}

module.exports = { dispatchMouse, dispatchKey, evaluateOnTab };

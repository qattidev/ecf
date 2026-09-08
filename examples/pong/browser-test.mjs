// Optional browser smoke test. Requires Node 22+ and Chrome running with
// --remote-debugging-port=9222 and a temporary --user-data-dir.
// Start the Pong server separately, then run: node examples/pong/browser-test.mjs
import assert from "node:assert/strict";
import { mkdir, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

const appURL = process.env.PONG_URL || "http://127.0.0.1:8080";
const debuggerURL = process.env.PONG_DEBUGGER_URL || "http://127.0.0.1:9222";
const output = process.env.PONG_SCREENSHOTS || join(tmpdir(), "ecf-pong-screenshots");
const pages = [];
const pause = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

async function openPage() {
  const response = await fetch(`${debuggerURL}/json/new?about:blank`, { method: "PUT" });
  assert.ok(response.ok, "Cannot create a Chrome tab");
  const target = await response.json();
  const socket = new WebSocket(target.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => { socket.onopen = resolve; socket.onerror = reject; });
  let id = 0;
  const pending = new Map();
  const exceptions = [];
  socket.onmessage = (event) => {
    const message = JSON.parse(event.data);
    if (message.method === "Runtime.exceptionThrown") exceptions.push(message.params.exceptionDetails);
    if (message.method === "Runtime.consoleAPICalled" && message.params.type === "error") exceptions.push(message.params.args);
    const call = pending.get(message.id);
    if (!call) return;
    pending.delete(message.id);
    clearTimeout(call.timeout);
    if (message.error) call.reject(new Error(JSON.stringify(message.error)));
    else call.resolve(message.result);
  };
  const page = {
    target, socket, exceptions,
    send(method, params = {}) {
      return new Promise((resolve, reject) => {
        const requestID = ++id;
        const timeout = setTimeout(() => { pending.delete(requestID); reject(new Error(`CDP timeout: ${method}`)); }, 10000);
        pending.set(requestID, { resolve, reject, timeout });
        socket.send(JSON.stringify({ id: requestID, method, params }));
      });
    },
    async evaluate(expression) {
      const result = await this.send("Runtime.evaluate", { expression, returnByValue: true, awaitPromise: true });
      if (result.exceptionDetails) throw new Error(JSON.stringify(result.exceptionDetails));
      return result.result.value;
    },
    async wait(expression, timeout = 5000) {
      const deadline = Date.now() + timeout;
      while (Date.now() < deadline) {
        if (await this.evaluate(expression)) return;
        await pause(40);
      }
      throw new Error(`Browser condition timed out: ${expression}`);
    },
    async screenshot(name) {
      const metrics = await this.send("Page.getLayoutMetrics");
      const size = metrics.cssContentSize;
      const result = await this.send("Page.captureScreenshot", {
        format: "png", captureBeyondViewport: true,
        clip: { x: 0, y: 0, width: size.width, height: size.height, scale: 1 },
      });
      const path = join(output, name);
      await writeFile(path, Buffer.from(result.data, "base64"));
      console.log(`Screenshot: ${path}`);
    },
    async close() {
      if (this.closed) return;
      this.closed = true;
      await fetch(`${debuggerURL}/json/close/${target.id}`);
      socket.close();
    },
  };
  pages.push(page);
  await page.send("Page.enable");
  await page.send("Runtime.enable");
  await page.send("Emulation.setDeviceMetricsOverride", { width: 1440, height: 1100, deviceScaleFactor: 1, mobile: false });
  // Observe the real network stream from test code; the game needs no test hooks.
  await page.send("Page.addScriptToEvaluateOnNewDocument", { source: `
    window.__pongState = null;
    const OriginalEventSource = window.EventSource;
    window.EventSource = class extends OriginalEventSource {
      constructor(...args) {
        super(...args);
        this.addEventListener("state", event => { window.__pongState = JSON.parse(event.data); });
        this.addEventListener("welcome", event => { window.__pongSession = JSON.parse(event.data).session; });
      }
    };
  ` });
  await page.send("Page.navigate", { url: appURL });
  await page.wait("window.__pongState !== null");
  return page;
}

async function key(page, type, key, code, keyCode) {
  await page.send("Input.dispatchKeyEvent", { type, key, code, windowsVirtualKeyCode: keyCode, nativeVirtualKeyCode: keyCode });
}

try {
  await mkdir(output, { recursive: true });
  const left = await openPage();
  assert.equal(await left.evaluate("__pongState.you"), 0, "Run the test on an otherwise empty Pong server");
  assert.equal(await left.evaluate("__pongState.paddles[1].human"), false);
  await left.send("Page.bringToFront");
  await key(left, "keyDown", "s", "KeyS", 83);
  await left.wait("__pongState.paddles[0].y > 380");
  await key(left, "keyUp", "s", "KeyS", 83);
  await left.wait("__pongState.phase === 'playing'");
  await left.screenshot("pong-desktop.png");
  console.log("PASS: solo game, streamed state, and keyboard movement");

  const right = await openPage();
  await right.wait("__pongState.you === 1 && __pongState.paddles.every(p => p.human)");
  await left.wait("__pongState.paddles[1].human");
  await right.send("Page.bringToFront");
  await key(right, "keyDown", "ArrowUp", "ArrowUp", 38);
  await right.wait("__pongState.paddles[1].y < 220");
  await key(right, "keyUp", "ArrowUp", "ArrowUp", 38);
  assert.equal(await right.evaluate("__pongState.paddles[0].y"), 300, "Right player must not control left paddle");
  console.log("PASS: second browser takes the right paddle and controls only its seat");

  const spectator = await openPage();
  await spectator.wait("__pongState.you === -1 && document.getElementById('restart').disabled");
  assert.equal(await spectator.evaluate(`fetch('/api/restart', {method:'POST',headers:{'X-Pong-Session':__pongSession}}).then(r=>r.status)`), 403);
  await right.close();
  await spectator.wait("__pongState.you === 1 && !document.getElementById('restart').disabled");
  await spectator.close();
  await left.wait("!__pongState.paddles[1].human");
  console.log("PASS: spectators, disconnect promotion, and bot fallback");

  await left.send("Page.bringToFront");
  const round = await left.evaluate("__pongState.round");
  await left.evaluate("document.getElementById('restart').click()");
  await left.wait(`__pongState.round > ${round} && __pongState.score.every(s => s === 0)`);
  const desktopCourt = await left.evaluate("(() => {const r=document.getElementById('court').getBoundingClientRect(); return {x:r.x,y:r.y,w:r.width,h:r.height};})()");
  await left.send("Input.dispatchMouseEvent", { type: "mouseMoved", x: desktopCourt.x+desktopCourt.w*.3, y: desktopCourt.y+desktopCourt.h*.2 });
  await left.wait("__pongState.paddles[0].y < 180");
  console.log("PASS: mouse control");
  await left.send("Emulation.setDeviceMetricsOverride", { width: 390, height: 844, deviceScaleFactor: 2, mobile: true });
  await left.send("Emulation.setTouchEmulationEnabled", { enabled: true, maxTouchPoints: 1 });
  await pause(100);
  assert.equal(await left.evaluate("document.documentElement.scrollWidth <= window.innerWidth"), true, "Mobile page overflows horizontally");
  const rect = await left.evaluate("(() => {const r=document.getElementById('court').getBoundingClientRect(); return {x:r.x,y:r.y,w:r.width,h:r.height};})()");
  await left.send("Input.dispatchTouchEvent", { type: "touchStart", touchPoints: [{ x: rect.x+rect.w*.3, y: rect.y+rect.h*.8 }] });
  await left.wait("__pongState.paddles[0].y > 420");
  await left.send("Input.dispatchTouchEvent", { type: "touchEnd", touchPoints: [] });
  await left.screenshot("pong-mobile.png");
  console.log("PASS: restart, mobile layout, and touch control");

  // Deliberately miss every serve to exercise actual scoring and the victory UI.
  const beforeMatch = await left.evaluate("__pongState.round");
  await left.evaluate("document.getElementById('restart').click()");
  await left.wait(`__pongState.round > ${beforeMatch}`);
  await left.send("Input.dispatchTouchEvent", { type: "touchStart", touchPoints: [{ x: rect.x+rect.w*.3, y: rect.y+rect.h*.99 }] });
  await left.send("Input.dispatchTouchEvent", { type: "touchEnd", touchPoints: [] });
  await left.wait("__pongState.score[1] > 0", 6000);
  await left.wait("__pongState.phase === 'finished' && __pongState.score[1] === 7", 25000);
  assert.equal(await left.evaluate("document.getElementById('playAgain').hidden"), false);
  const finishedRound = await left.evaluate("__pongState.round");
  await left.evaluate("document.getElementById('playAgain').click()");
  await left.wait(`__pongState.round > ${finishedRound} && __pongState.score.every(s => s === 0)`);
  console.log("PASS: scoring, match victory, and play-again flow");

  for (const page of pages) assert.deepEqual(page.exceptions, [], "Browser JavaScript error");
  console.log("PASS: no browser JavaScript exceptions");
} finally {
  await Promise.allSettled(pages.map((page) => page.close()));
}

(() => {
  "use strict";

  const byId = (id) => document.getElementById(id);
  const canvas = byId("court");
  const ctx = canvas.getContext("2d");
  const reducedMotion = window.matchMedia("(prefers-reduced-motion: reduce)");
  const keys = new Set();
  const movementKeys = new Set(["KeyW", "KeyS", "ArrowUp", "ArrowDown"]);
  let session = "";
  let sequence = 0;
  let connected = false;
  let latest = null;
  let samples = [];
  let trail = [];
  let target = null;
  let inputPending = false;
  let urgentInput = false;
  let restartPending = false;
  let lastStateAt = 0;
  let inputWarning = false;

  function text(id, value) {
    const element = byId(id);
    if (element.textContent !== value) element.textContent = value;
  }

  function connection(live) {
    connected = live;
    byId("connection").dataset.state = live ? "live" : "connecting";
    text("connectionText", live ? "Connected to court" : "Reconnecting…");
    if (!live) { session = ""; keys.clear(); target = null; }
    updateUI();
  }

  const stream = new EventSource("/api/events");
  stream.addEventListener("welcome", (event) => {
    session = JSON.parse(event.data).session;
    sequence = 0;
    latest = null;
    samples = [];
    trail = [];
    keys.clear();
    target = null;
    connection(true);
    text("notice", "");
  });
  stream.addEventListener("state", (event) => {
    const state = JSON.parse(event.data);
    const now = performance.now();
    if (!latest || state.round !== latest.round || state.phase !== latest.phase || state.score.some((score, side) => score !== latest.score[side])) {
      samples = [];
      trail = [];
    }
    if (latest && latest.you !== state.you) { keys.clear(); target = null; }
    latest = state;
    lastStateAt = now;
    samples.push({ at: now, state });
    if (samples.length > 8) samples.shift();
    updateUI();
  });
  stream.onerror = () => connection(false);

  function label(side) {
    if (latest.you === side) return "YOU";
    return latest.paddles[side].human ? `PLAYER ${side + 1}` : "COMPUTER";
  }

  function updateUI() {
    const playing = connected && latest && latest.you >= 0;
    byId("restart").disabled = !playing || restartPending;
    byId("playAgain").disabled = !playing || restartPending;
    byId("playAgain").hidden = true;
    if (!connected || !latest) {
      byId("overlay").hidden = false;
      text("overlayEyebrow", latest ? "A QUICK TIMEOUT" : "GET COMFORTABLE");
      text("overlayTitle", latest ? "Rejoining the court…" : "Finding your paddle…");
      text("overlayDetail", "The game will connect automatically.");
      text("seatHint", "Connecting to the court…");
      return;
    }
    text("leftLabel", label(0));
    text("rightLabel", label(1));
    text("leftScore", String(latest.score[0]).padStart(2, "0"));
    text("rightScore", String(latest.score[1]).padStart(2, "0"));
    text("rally", String(latest.rally).padStart(2, "0"));
    text("bestRally", String(latest.bestRally).padStart(2, "0"));
    text("targetScore", `FIRST TO ${latest.targetScore}`);
    const humans = latest.paddles.filter((p) => p.human).length;
    text("matchMode", latest.you < 0 ? "COURTSIDE · SPECTATING" : humans === 2 ? "TWO-PLAYER MATCH" : "SOLO · VS COMPUTER");
    text("seatHint", latest.you < 0 ? "You’re watching. A free paddle is yours when a player leaves." : `You’re on the ${latest.you === 0 ? "left" : "right"}. Move with keys, mouse, or touch.`);
    const showOverlay = latest.phase !== "playing";
    byId("overlay").hidden = !showOverlay;
    if (latest.phase === "serve") {
      text("overlayEyebrow", latest.you < 0 ? "NEXT RALLY" : `YOUR PADDLE IS ON THE ${latest.you === 0 ? "LEFT" : "RIGHT"}`);
      text("overlayTitle", `Ball in ${Math.max(1, Math.ceil(latest.countdown))}`);
      text("overlayDetail", "Find your position. Make the next one count.");
    } else if (latest.phase === "finished") {
      text("overlayEyebrow", "THAT’S A MATCH");
      text("overlayTitle", latest.you === latest.winner ? "Nicely played. You win!" : latest.you >= 0 ? "One more round?" : `${label(latest.winner)} wins!`);
      text("overlayDetail", `${latest.score[0]} — ${latest.score[1]} · Best rally: ${latest.bestRally}`);
      byId("playAgain").hidden = !playing;
    } else if (latest.phase === "waiting") {
      text("overlayEyebrow", "COURT IS OPEN");
      text("overlayTitle", "Waiting for a player");
      text("overlayDetail", "A match starts as soon as someone joins.");
    }
  }

  function axis() {
    return Number(keys.has("KeyS") || keys.has("ArrowDown")) - Number(keys.has("KeyW") || keys.has("ArrowUp"));
  }

  async function sendInput(urgent = false) {
    if (urgent) urgentInput = true;
    if (inputPending || !connected || !session || !latest || latest.you < 0) return;
    if (document.hidden && !urgentInput) return;
    urgentInput = false;
    inputPending = true;
    const token = session;
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), 1500);
    try {
      const response = await fetch("/api/input", {
        method: "POST",
        headers: { "Content-Type": "application/json", "X-Pong-Session": token },
        body: JSON.stringify({ sequence: ++sequence, axis: axis(), target }),
        signal: controller.signal,
      });
      if (!response.ok && token === session) {
        inputWarning = true;
        text("notice", "Controls interrupted. Retrying…");
      } else if (token === session && inputWarning) {
        inputWarning = false;
        text("notice", "");
      }
    } catch {
      if (connected && token === session) {
        inputWarning = true;
        text("notice", "Controls interrupted. Retrying…");
      }
    } finally {
      clearTimeout(timeout);
      inputPending = false;
      if (urgentInput) sendInput(true);
    }
  }
  setInterval(sendInput, 40);

  function release() { keys.clear(); target = null; sendInput(true); }
  window.addEventListener("keydown", (event) => {
    if (event.target instanceof HTMLInputElement || event.ctrlKey || event.metaKey || event.altKey) return;
    if (movementKeys.has(event.code)) {
      event.preventDefault();
      keys.add(event.code);
      target = null;
      if (!event.repeat) sendInput(true);
    } else if (event.code === "Space" && latest?.phase === "finished" && latest.you >= 0) {
      event.preventDefault();
      restart();
    }
  });
  window.addEventListener("keyup", (event) => {
    if (!movementKeys.has(event.code)) return;
    event.preventDefault();
    keys.delete(event.code);
    sendInput(true);
  });
  window.addEventListener("blur", release);
  document.addEventListener("visibilitychange", () => { if (document.hidden) release(); });
  canvas.addEventListener("pointermove", (event) => {
    if (event.pointerType !== "mouse" && event.buttons === 0) return;
    const rect = canvas.getBoundingClientRect();
    target = Math.max(0, Math.min(1, (event.clientY - rect.top) / rect.height));
    keys.clear();
  });
  canvas.addEventListener("pointerdown", (event) => {
    canvas.focus({ preventScroll: true });
    canvas.setPointerCapture(event.pointerId);
    const rect = canvas.getBoundingClientRect();
    target = Math.max(0, Math.min(1, (event.clientY - rect.top) / rect.height));
    keys.clear();
    sendInput(true);
  });
  canvas.addEventListener("pointercancel", release);

  async function restart() {
    if (restartPending || !session || !latest || latest.you < 0) return;
    restartPending = true;
    release();
    updateUI();
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), 2000);
    try {
      const response = await fetch("/api/restart", { method: "POST", headers: { "X-Pong-Session": session }, signal: controller.signal });
      if (!response.ok) text("notice", "Couldn’t start a new match. Try again.");
    } catch { text("notice", "Couldn’t reach the court. Try again."); }
    finally { clearTimeout(timeout); restartPending = false; updateUI(); }
  }
  byId("restart").addEventListener("click", restart);
  byId("playAgain").addEventListener("click", restart);
  byId("invite").addEventListener("click", async () => {
    try {
      await navigator.clipboard.writeText(window.location.href);
      inputWarning = false;
      text("notice", "Court link copied. Open it in another browser to play together.");
    } catch {
      byId("sharePanel").hidden = false;
      byId("shareURL").value = window.location.href;
      byId("shareURL").select();
    }
  });

  function positions(now) {
    if (!latest) return null;
    const renderAt = now - 50;
    let before = samples[0];
    let after = before;
    for (const sample of samples) {
      after = sample;
      if (sample.at >= renderAt) break;
      before = sample;
    }
    const weight = after.at === before.at ? 1 : Math.max(0, Math.min(1, (renderAt-before.at)/(after.at-before.at)));
    const mix = (a, b) => a + (b-a)*weight;
    return {
      ball: { x: mix(before.state.ball.x, after.state.ball.x), y: mix(before.state.ball.y, after.state.ball.y) },
      paddles: before.state.paddles.map((p, side) => ({ x: p.x, y: mix(p.y, after.state.paddles[side].y) })),
    };
  }

  function draw(now) {
    const rect = canvas.getBoundingClientRect();
    const dpr = Math.min(window.devicePixelRatio || 1, 2);
    const width = Math.round(rect.width*dpr);
    const height = Math.round(rect.height*dpr);
    if (canvas.width !== width || canvas.height !== height) { canvas.width = width; canvas.height = height; }
    const w = latest?.width || 960;
    const h = latest?.height || 600;
    ctx.setTransform(width/w, 0, 0, height/h, 0, 0);
    ctx.fillStyle = "#193830";
    ctx.fillRect(0, 0, w, h);
    ctx.strokeStyle = "#e1edc50a";
    ctx.lineWidth = 1;
    ctx.beginPath();
    for (let x = 0; x < w; x += 40) { ctx.moveTo(x, 0); ctx.lineTo(x, h); }
    for (let y = 0; y < h; y += 40) { ctx.moveTo(0, y); ctx.lineTo(w, y); }
    ctx.stroke();
    ctx.strokeStyle = "#dce8c826";
    ctx.strokeRect(19, 19, w-38, h-38);
    ctx.setLineDash([9, 13]);
    ctx.lineWidth = 2;
    ctx.beginPath(); ctx.moveTo(w/2, 22); ctx.lineTo(w/2, h-22); ctx.stroke();
    ctx.setLineDash([]);
    ctx.strokeStyle = "#dce8c810";
    ctx.beginPath(); ctx.arc(w/2, h/2, 62, 0, Math.PI*2); ctx.stroke();

    const view = positions(now) || { paddles: [{x: 48, y: h/2}, {x: w-48, y: h/2}], ball: {x: w/2, y: h/2} };
    const pw = latest?.paddleWidth || 14;
    const ph = latest?.paddleHeight || 100;
    for (const [side, paddle] of view.paddles.entries()) {
      ctx.fillStyle = side === 0 ? "#d9f284" : "#ffae86";
      ctx.shadowColor = side === 0 ? "#d9f28425" : "#ffae8625";
      ctx.shadowBlur = 17;
      ctx.beginPath(); ctx.roundRect(paddle.x-pw/2, paddle.y-ph/2, pw, ph, 4); ctx.fill();
      ctx.shadowBlur = 0;
    }
    if (latest?.phase === "playing" && connected && !reducedMotion.matches) {
      trail.push(view.ball);
      if (trail.length > 9) trail.shift();
      trail.forEach((p, index) => {
        ctx.fillStyle = `rgba(255, 248, 221, ${index/trail.length*0.13})`;
        ctx.beginPath(); ctx.arc(p.x, p.y, (latest.ballRadius || 9)*index/trail.length, 0, Math.PI*2); ctx.fill();
      });
    } else { trail = []; }
    ctx.fillStyle = "#fff8dd";
    ctx.shadowColor = "#fff8dd40";
    ctx.shadowBlur = 16;
    ctx.beginPath(); ctx.arc(view.ball.x, view.ball.y, latest?.ballRadius || 9, 0, Math.PI*2); ctx.fill();
    ctx.shadowBlur = 0;
    if (connected && latest && now-lastStateAt > 3000) {
      text("connectionText", "Waiting for the court…");
    } else if (connected) { text("connectionText", "Connected to court"); }
    requestAnimationFrame(draw);
  }
  requestAnimationFrame(draw);
})();

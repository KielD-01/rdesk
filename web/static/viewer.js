// rdesk browser viewer.
"use strict";

const $ = (id) => document.getElementById(id);
const enc = new TextEncoder();

const ui = {
  login: $("login"), form: $("connect-form"), id: $("host-id"), pw: $("password"),
  btn: $("connect-btn"), status: $("status"),
  session: $("session"), stage: $("stage"), video: $("screen"), overlay: $("overlay"),
  toolbar: $("toolbar"), hostName: $("host-name"), stats: $("stats"), fitLabel: $("fit-label"),
  audioBtn: $("audio-btn"), files: $("files"), filesPath: $("files-path"), filesList: $("files-list"),
  filesUp: $("files-up"), upload: $("upload-input"), transfers: $("transfers"),
};

let S = null; // current session state

// ---------- helpers ----------

function setStatus(text, error = false) {
  ui.status.textContent = text;
  ui.status.classList.toggle("error", error);
}

function fingerprint(sdp) {
  for (const line of sdp.split("\n")) {
    const l = line.trim();
    if (l.startsWith("a=fingerprint:")) return l.slice(14).trim().split(/\s+/).join(" ").toLowerCase();
  }
  return "";
}

function normalizePassword(pw) { return pw.trim().replace(/[\s-]/g, ""); }

async function proof(password, role, nonce, offerFP, answerFP) {
  const keyBytes = await crypto.subtle.digest("SHA-256", enc.encode("rdesk-pw|" + normalizePassword(password)));
  const key = await crypto.subtle.importKey("raw", keyBytes, { name: "HMAC", hash: "SHA-256" }, false, ["sign"]);
  const sig = await crypto.subtle.sign("HMAC", key, enc.encode(`rdesk-v1|${role}|${nonce}|${offerFP}|${answerFP}`));
  return [...new Uint8Array(sig)].map((b) => b.toString(16).padStart(2, "0")).join("");
}

function fmtBytes(n) {
  if (n < 1024) return n + " B";
  const u = ["KB", "MB", "GB", "TB"];
  let i = -1;
  do { n /= 1024; i++; } while (n >= 1024 && i < u.length - 1);
  return n.toFixed(n < 10 ? 1 : 0) + " " + u[i];
}

function formatId(v) {
  const d = v.replace(/\D/g, "").slice(0, 9);
  return d.replace(/(\d{3})(?=\d)/g, "$1 ").trim();
}

// ---------- connect ----------

ui.id.addEventListener("input", () => { ui.id.value = formatId(ui.id.value); });

const fromHash = location.hash.slice(1).replace(/\D/g, "");
if (fromHash) { ui.id.value = formatId(fromHash); ui.pw.focus(); } else { ui.id.focus(); }

ui.form.addEventListener("submit", (e) => {
  e.preventDefault();
  const id = ui.id.value.replace(/\D/g, "");
  if (id.length !== 9) { setStatus("ID складається з 9 цифр", true); return; }
  if (!window.isSecureContext) { setStatus("Потрібен HTTPS (або localhost) для криптографії браузера", true); return; }
  connect(id, ui.pw.value);
});

function connect(id, password) {
  ui.btn.disabled = true;
  setStatus("З'єднання з сервером…");
  const wsProto = location.protocol === "https:" ? "wss:" : "ws:";
  const ws = new WebSocket(`${wsProto}//${location.host}/ws/viewer`);
  S = {
    id, password, ws, pc: null, control: null, input: null, pendingCandidates: [], remoteSet: false,
    authed: false, ended: false, nonce: "", hostInfo: null, pressed: new Set(), stats: null,
    clipLast: null, clipPending: null, lsPath: "", downloads: new Map(),
  };
  const s = S;

  ws.onopen = () => ws.send(JSON.stringify({ t: "connect", id }));
  ws.onmessage = (ev) => {
    let m;
    try { m = JSON.parse(ev.data); } catch { return; }
    if (s !== S) return;
    switch (m.t) {
      case "ready": setStatus("Хост знайдено, встановлюю з'єднання…"); setupPeer(s, m.ice || []); break;
      case "signal": onSignal(s, m.data); break;
      case "bye": if (!s.authed) fail(s, "Хост закрив з'єднання"); break;
      case "error": fail(s, translateError(m.error)); break;
    }
  };
  ws.onerror = () => { if (!s.pc) fail(s, "Не вдалося з'єднатися з сервером"); };
  ws.onclose = () => { if (!s.pc && !s.ended) fail(s, "Сервер закрив з'єднання"); };
}

function translateError(e = "") {
  if (e.includes("offline or busy")) return "Хост не в мережі (перевірте ID) або зайнятий";
  if (e.includes("too many")) return "Забагато спроб. Зачекайте хвилину";
  if (e.includes("went offline")) return "Хост від'єднався";
  return e || "Помилка";
}

function sendSignal(s, data) {
  if (s.ws.readyState === WebSocket.OPEN) s.ws.send(JSON.stringify({ t: "signal", data }));
}

function setupPeer(s, ice) {
  // ?relay=1 forces the TURN relay (useful to test it).
  const forceRelay = new URLSearchParams(location.search).get("relay") === "1";
  const pc = new RTCPeerConnection({ iceServers: ice, iceTransportPolicy: forceRelay ? "relay" : "all" });
  s.pc = pc;
  const stream = new MediaStream();
  pc.ontrack = (ev) => {
    stream.addTrack(ev.track);
    ui.video.srcObject = stream;
  };
  pc.onicecandidate = (ev) => {
    if (ev.candidate) sendSignal(s, { candidate: ev.candidate.toJSON() });
  };
  pc.ondatachannel = (ev) => {
    const dc = ev.channel;
    if (dc.label === "control") { s.control = dc; dc.onmessage = (e) => onControl(s, e.data); }
    else if (dc.label === "input") { s.input = dc; }
    else if (dc.label === "download") { receiveDownload(s, dc); }
  };
  pc.onconnectionstatechange = () => {
    if (s !== S) return;
    const st = pc.connectionState;
    if (st === "connected" && !s.authed) setStatus("Перевірка пароля…");
    if (st === "disconnected" && s.authed) showOverlay("З'єднання втрачено, відновлюю…");
    if (st === "connected" && s.authed) hideOverlay();
    if (st === "failed") fail(s, s.authed ? "З'єднання розірвано" : "Не вдалося встановити пряме з'єднання або relay");
  };
}

async function onSignal(s, data) {
  if (!s.pc || !data) return;
  try {
    if (data.sdp && data.sdp.type === "offer") {
      await s.pc.setRemoteDescription(data.sdp);
      s.remoteSet = true;
      for (const c of s.pendingCandidates) await s.pc.addIceCandidate(c).catch(() => {});
      s.pendingCandidates = [];
      const answer = await s.pc.createAnswer();
      await s.pc.setLocalDescription(answer);
      sendSignal(s, { sdp: { type: "answer", sdp: s.pc.localDescription.sdp } });
    }
    if (data.candidate && data.candidate.candidate) {
      if (s.remoteSet) await s.pc.addIceCandidate(data.candidate).catch(() => {});
      else s.pendingCandidates.push(data.candidate);
    }
  } catch (err) {
    fail(s, "Помилка WebRTC: " + err.message);
  }
}

function fingerprints(s) {
  return [fingerprint(s.pc.remoteDescription.sdp), fingerprint(s.pc.localDescription.sdp)];
}

function sendControl(s, m) {
  if (s.control && s.control.readyState === "open") s.control.send(JSON.stringify(m));
}

async function onControl(s, data) {
  let m;
  try { m = JSON.parse(data); } catch { return; }
  switch (m.t) {
    case "hello": {
      s.nonce = m.nonce;
      const [offerFP, answerFP] = fingerprints(s);
      sendControl(s, { t: "auth", proof: await proof(s.password, "viewer", m.nonce, offerFP, answerFP) });
      break;
    }
    case "auth-fail":
      fail(s, "Невірний пароль");
      break;
    case "auth-ok": {
      const [offerFP, answerFP] = fingerprints(s);
      const want = await proof(s.password, "host", s.nonce, offerFP, answerFP);
      if (want !== m.proof) { fail(s, "Хост не підтвердив пароль — можлива атака «людина посередині». З'єднання розірвано."); return; }
      s.authed = true;
      s.password = null;
      s.hostInfo = m;
      startSession(s);
      break;
    }
    case "screen": break; // the video element adapts by itself
    case "clip": receiveClipboard(s, m.text || ""); break;
    case "ls": renderListing(s, m); break;
    case "get-error": toast(`Не вдалося завантажити ${m.path}: ${m.error}`); break;
  }
}

function fail(s, msg) {
  if (s !== S || s.ended) return;
  s.ended = true;
  teardown(s);
  if (s.authed) {
    showOverlay(msg, true);
  } else {
    ui.session.hidden = true;
    ui.login.hidden = false;
    ui.btn.disabled = false;
    setStatus(msg, true);
  }
}

function teardown(s) {
  try { s.ws.close(); } catch {}
  try { s.pc && s.pc.close(); } catch {}
  clearInterval(s.stats);
  if (document.fullscreenElement) document.exitFullscreen().catch(() => {});
}

function disconnect() {
  const s = S;
  if (!s) return;
  s.ended = true;
  teardown(s);
  S = null;
  ui.video.srcObject = null;
  ui.session.hidden = true;
  ui.files.hidden = true;
  hideOverlay();
  ui.login.hidden = false;
  ui.btn.disabled = false;
  ui.pw.value = "";
  setStatus("Від'єднано. Одноразовий пароль на хості змінився.");
  ui.pw.focus();
}

function showOverlay(msg, final = false) {
  ui.overlay.innerHTML = "";
  const box = document.createElement("div");
  box.className = "box";
  const p = document.createElement("div");
  p.textContent = msg;
  box.append(p);
  if (final) {
    const b = document.createElement("button");
    b.textContent = "До підключення";
    b.onclick = () => { S = null; disconnect_ui(); };
    box.append(b);
  }
  ui.overlay.append(box);
  ui.overlay.hidden = false;
}

function disconnect_ui() {
  ui.video.srcObject = null;
  ui.session.hidden = true;
  ui.files.hidden = true;
  hideOverlay();
  ui.login.hidden = false;
  ui.btn.disabled = false;
  ui.pw.value = "";
  setStatus("");
  ui.pw.focus();
}

function hideOverlay() { ui.overlay.hidden = true; }

// ---------- session ----------

function startSession(s) {
  ui.login.hidden = true;
  ui.session.hidden = false;
  const info = s.hostInfo;
  ui.hostName.textContent = `${info.name || "host"} · ${({ linux: "Linux", windows: "Windows", darwin: "macOS" })[info.os] || info.os}`;
  document.title = `${info.name || s.id} — rdesk`;
  const feats = new Set(info.features || []);
  ui.audioBtn.hidden = !feats.has("audio");
  // The connect click was a user gesture, so sound may start right away.
  ui.video.muted = false;
  ui.video.play().catch(() => { ui.video.muted = true; ui.video.play().catch(() => {}); updateAudioBtn(); });
  updateAudioBtn();
  ui.video.focus();
  s.stats = setInterval(() => updateStats(s), 1000);
  syncClipboardToHost(s);
  armToolbarAutoHide();
}

function updateAudioBtn() { ui.audioBtn.textContent = ui.video.muted ? "🔇" : "🔊"; }

let lastStats = null;
async function updateStats(s) {
  if (!s.pc) return;
  const r = await s.pc.getStats();
  let fps = 0, bytes = 0, ts = 0, rtt = null, pairId = null, local = null, remote = null;
  const byId = new Map();
  r.forEach((v) => {
    byId.set(v.id, v);
    if (v.type === "inbound-rtp" && v.kind === "video") { fps = v.framesPerSecond || 0; bytes = v.bytesReceived; ts = v.timestamp; }
    if (v.type === "transport" && v.selectedCandidatePairId) pairId = v.selectedCandidatePairId;
  });
  if (!pairId) r.forEach((v) => { if (v.type === "candidate-pair" && v.nominated && v.state === "succeeded") pairId = v.id; });
  const pair = pairId && byId.get(pairId);
  if (pair) {
    rtt = pair.currentRoundTripTime;
    local = byId.get(pair.localCandidateId);
    remote = byId.get(pair.remoteCandidateId);
  }
  let kbps = 0;
  if (lastStats && ts > lastStats.ts) kbps = ((bytes - lastStats.bytes) * 8) / (ts - lastStats.ts);
  lastStats = { bytes, ts };
  // Chrome may label a relayed local candidate "prflx"; match it by port.
  const relayPorts = new Set();
  byId.forEach((v) => { if (v.type === "local-candidate" && v.candidateType === "relay") relayPorts.add(v.port); });
  const relay = (local && (local.candidateType === "relay" || relayPorts.has(local.port))) || (remote && remote.candidateType === "relay");
  ui.stats.innerHTML = "";
  const kind = document.createElement("span");
  kind.className = relay ? "relay" : "p2p";
  kind.textContent = relay ? "relay" : "P2P";
  ui.stats.append(kind, ` · ${Math.round(fps)} fps · ${kbps >= 1000 ? (kbps / 1000).toFixed(1) + " Mbit/s" : Math.round(kbps) + " kbit/s"}${rtt != null ? ` · ${Math.round(rtt * 1000)} ms` : ""}`);
}

// toolbar
let idleTimer = null;
function armToolbarAutoHide() {
  const wake = () => {
    ui.toolbar.classList.remove("idle");
    clearTimeout(idleTimer);
    idleTimer = setTimeout(() => { if (!ui.toolbar.matches(":hover") && ui.files.hidden) ui.toolbar.classList.add("idle"); }, 2500);
  };
  ui.session.addEventListener("pointermove", (e) => { if (e.clientY < 70) wake(); });
  wake();
}

ui.session.addEventListener("click", (e) => {
  const b = e.target.closest("button[data-act]");
  if (!b || !S) return;
  const s = S;
  switch (b.dataset.act) {
    case "fullscreen":
      if (document.fullscreenElement) document.exitFullscreen();
      else ui.session.requestFullscreen().then(() => navigator.keyboard && navigator.keyboard.lock && navigator.keyboard.lock().catch(() => {})).catch(() => {});
      break;
    case "fit": {
      const native = ui.stage.classList.toggle("native");
      ui.fitLabel.textContent = native ? "1:1" : "Вписати";
      break;
    }
    case "cad":
      for (const c of ["ControlLeft", "AltLeft", "Delete"]) sendInput(s, { t: "k", c, d: true });
      for (const c of ["Delete", "AltLeft", "ControlLeft"]) sendInput(s, { t: "k", c, d: false });
      break;
    case "files":
      ui.files.hidden = !ui.files.hidden;
      if (!ui.files.hidden) sendControl(s, { t: "ls", path: s.lsPath });
      break;
    case "audio":
      ui.video.muted = !ui.video.muted;
      ui.video.play().catch(() => {});
      updateAudioBtn();
      break;
    case "disconnect": disconnect(); break;
  }
  ui.video.focus();
});

// ---------- input ----------

function sendInput(s, m) {
  if (s && s.authed && s.input && s.input.readyState === "open") s.input.send(JSON.stringify(m));
}

// Map a client point to normalised coordinates of the remote screen,
// accounting for letterboxing of object-fit: contain.
function toRemote(ev) {
  const v = ui.video;
  const r = v.getBoundingClientRect();
  const vw = v.videoWidth, vh = v.videoHeight;
  if (!vw || !vh) return null;
  let w = r.width, h = r.height, x0 = r.left, y0 = r.top;
  if (!ui.stage.classList.contains("native")) {
    const scale = Math.min(r.width / vw, r.height / vh);
    w = vw * scale; h = vh * scale;
    x0 += (r.width - w) / 2; y0 += (r.height - h) / 2;
  }
  const x = (ev.clientX - x0) / w, y = (ev.clientY - y0) / h;
  if (x < 0 || y < 0 || x > 1 || y > 1) return null;
  return { x, y };
}

let moveQueued = null;
function queueMove(p) {
  if (!moveQueued) requestAnimationFrame(() => { if (moveQueued) sendInput(S, { t: "mm", ...moveQueued }); moveQueued = null; });
  moveQueued = p;
}

ui.video.addEventListener("pointermove", (e) => {
  const p = toRemote(e);
  if (p) queueMove(p);
});
ui.video.addEventListener("pointerdown", (e) => {
  e.preventDefault();
  ui.video.focus();
  ui.video.setPointerCapture(e.pointerId);
  const p = toRemote(e);
  if (!p) return;
  moveQueued = null;
  sendInput(S, { t: "mm", ...p });
  sendInput(S, { t: "mb", b: e.button, d: true });
});
ui.video.addEventListener("pointerup", (e) => {
  e.preventDefault();
  const p = toRemote(e);
  if (p) { moveQueued = null; sendInput(S, { t: "mm", ...p }); }
  sendInput(S, { t: "mb", b: e.button, d: false });
});
ui.video.addEventListener("contextmenu", (e) => e.preventDefault());
ui.video.addEventListener("auxclick", (e) => e.preventDefault());

let wheelAcc = { x: 0, y: 0 };
ui.video.addEventListener("wheel", (e) => {
  e.preventDefault();
  const k = e.deltaMode === 1 ? 33 : e.deltaMode === 2 ? 600 : 1;
  wheelAcc.x += e.deltaX * k;
  wheelAcc.y += e.deltaY * k;
  const step = 50;
  const dx = Math.trunc(wheelAcc.x / step), dy = Math.trunc(wheelAcc.y / step);
  if (dx || dy) {
    wheelAcc.x -= dx * step; wheelAcc.y -= dy * step;
    sendInput(S, { t: "wh", dx, dy });
  }
}, { passive: false });

function keyHandler(e) {
  const s = S;
  if (!s || !s.authed || ui.session.hidden) return;
  if (e.target instanceof HTMLInputElement) return; // typing in our own UI
  if (!e.code || e.code === "Unidentified") return;
  e.preventDefault();
  const down = e.type === "keydown";
  if (down) s.pressed.add(e.code); else s.pressed.delete(e.code);
  sendInput(s, { t: "k", c: e.code, d: down });
}
document.addEventListener("keydown", keyHandler);
document.addEventListener("keyup", keyHandler);

window.addEventListener("blur", () => {
  const s = S;
  if (!s || !s.authed) return;
  s.pressed.clear();
  sendInput(s, { t: "release" });
});

// ---------- clipboard ----------

async function syncClipboardToHost(s) {
  if (!s || !s.authed || !navigator.clipboard || !navigator.clipboard.readText) return;
  try {
    const t = await navigator.clipboard.readText();
    if (t && t !== s.clipLast) { s.clipLast = t; sendControl(s, { t: "clip", text: t }); }
  } catch { /* permission denied or not focused */ }
}

async function receiveClipboard(s, text) {
  s.clipLast = text;
  if (!document.hasFocus()) { s.clipPending = text; return; }
  try { await navigator.clipboard.writeText(text); } catch { s.clipPending = text; }
}

window.addEventListener("focus", async () => {
  const s = S;
  if (!s || !s.authed) return;
  if (s.clipPending != null) {
    const t = s.clipPending;
    s.clipPending = null;
    try { await navigator.clipboard.writeText(t); return; } catch {}
  }
  syncClipboardToHost(s);
});

// ---------- files ----------

function renderListing(s, m) {
  ui.filesList.innerHTML = "";
  if (m.error) {
    const li = document.createElement("li");
    li.className = "msg";
    li.textContent = m.error;
    ui.filesList.append(li);
    return;
  }
  s.lsPath = m.path;
  s.lsParent = m.parent || "";
  ui.filesPath.value = m.path;
  const sepChar = m.path.includes("\\") && !m.path.includes("/") ? "\\" : "/";
  for (const e of m.entries || []) {
    const li = document.createElement("li");
    const icon = document.createElement("span");
    icon.textContent = e.dir ? "📁" : "📄";
    const name = document.createElement("span");
    name.className = "name";
    name.textContent = e.name;
    li.append(icon, name);
    if (!e.dir) {
      const size = document.createElement("span");
      size.className = "size";
      size.textContent = fmtBytes(e.size || 0);
      li.append(size);
    }
    const full = m.path.endsWith(sepChar) ? m.path + e.name : m.path + sepChar + e.name;
    li.title = e.dir ? "Відкрити" : "Завантажити";
    li.onclick = () => sendControl(s, e.dir ? { t: "ls", path: full } : { t: "get", path: full });
    ui.filesList.append(li);
  }
  if (!(m.entries || []).length) {
    const li = document.createElement("li");
    li.className = "msg";
    li.textContent = "Порожньо";
    ui.filesList.append(li);
  }
}

ui.filesUp.onclick = () => { if (S && S.lsParent) sendControl(S, { t: "ls", path: S.lsParent }); };
ui.filesPath.addEventListener("keydown", (e) => { if (e.key === "Enter" && S) sendControl(S, { t: "ls", path: ui.filesPath.value }); });

function transferRow(label) {
  const div = document.createElement("div");
  div.className = "transfer";
  const t = document.createElement("div");
  t.textContent = label;
  const p = document.createElement("progress");
  p.max = 1; p.value = 0;
  div.append(t, p);
  ui.transfers.append(div);
  return {
    progress: (v) => { p.value = v; },
    done: (msg) => { t.textContent = msg; p.remove(); setTimeout(() => div.remove(), 4000); },
  };
}

function toast(msg) { transferRow(msg).done(msg); }

function receiveDownload(s, dc) {
  dc.binaryType = "arraybuffer";
  let meta = null, got = 0, row = null;
  const chunks = [];
  dc.onmessage = (e) => {
    if (typeof e.data === "string") {
      const m = JSON.parse(e.data);
      if (m.t === "meta") { meta = m; row = transferRow(`⬇ ${m.name} (${fmtBytes(m.size)})`); }
      else if (m.t === "done" && meta) {
        const url = URL.createObjectURL(new Blob(chunks));
        const a = document.createElement("a");
        a.href = url; a.download = meta.name;
        document.body.append(a); a.click(); a.remove();
        setTimeout(() => URL.revokeObjectURL(url), 30000);
        row.done(`✓ ${meta.name} завантажено`);
        dc.close();
      } else if (m.t === "error") { row && row.done(`✗ ${m.error}`); dc.close(); }
      return;
    }
    chunks.push(e.data);
    got += e.data.byteLength;
    if (row && meta.size) row.progress(got / meta.size);
  };
}

async function uploadFile(s, file) {
  const dc = s.pc.createDataChannel("upload", { ordered: true });
  dc.binaryType = "arraybuffer";
  const row = transferRow(`⬆ ${file.name} (${fmtBytes(file.size)})`);
  const ready = new Promise((resolve, reject) => {
    dc.onmessage = (e) => {
      const m = JSON.parse(e.data);
      if (m.t === "ready") resolve(m);
      else if (m.t === "done") row.done(`✓ ${file.name} → ${m.path}`);
      else if (m.t === "error") { reject(new Error(m.error)); row.done(`✗ ${file.name}: ${m.error}`); }
    };
    dc.onclose = () => reject(new Error("channel closed"));
  });
  await new Promise((r) => { dc.onopen = r; });
  dc.send(JSON.stringify({ name: file.name, size: file.size }));
  try { await ready; } catch { return; }
  const CHUNK = 16 * 1024;
  dc.bufferedAmountLowThreshold = 1 << 20;
  let off = 0;
  while (off < file.size) {
    if (dc.readyState !== "open") return;
    if (dc.bufferedAmount > 4 << 20) await new Promise((r) => { dc.onbufferedamountlow = r; });
    const buf = await file.slice(off, off + CHUNK).arrayBuffer();
    dc.send(buf);
    off += buf.byteLength;
    row.progress(off / file.size);
  }
}

ui.upload.addEventListener("change", () => {
  if (!S) return;
  for (const f of ui.upload.files) uploadFile(S, f);
  ui.upload.value = "";
});

ui.stage.addEventListener("dragover", (e) => { if (S && S.authed) { e.preventDefault(); ui.stage.classList.add("dragover"); } });
ui.stage.addEventListener("dragleave", () => ui.stage.classList.remove("dragover"));
ui.stage.addEventListener("drop", (e) => {
  e.preventDefault();
  ui.stage.classList.remove("dragover");
  if (!S || !S.authed) return;
  for (const f of e.dataTransfer.files) uploadFile(S, f);
});

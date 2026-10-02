// Оболочка gcli: терминальная логика в браузере.
// Говорит с тем же сервером, что и TUI: /v1/status, /v1/history,
// /v1/message, /v1/events (SSE). Токен приходит из ?token= либо вводится
// на экране подключения и живёт в localStorage.
"use strict";

const $ = (id) => document.getElementById(id);

const state = {
  token: "",
  running: false,
  viewing: null,      // id просматриваемой сессии (null = текущая)
  currentMsg: null,   // карточка текущего ответа ассистента
  currentReason: null,// блок размышлений текущего ответа
  tools: new Map(),   // pending tool card по имени+времени
  thinkT0: 0,
  thinkTimer: null,
};

/* ---------- токен и связь ---------- */

function tokenFromURL() {
  const q = new URLSearchParams(location.search);
  return q.get("token") || "";
}

function saveToken(t) {
  state.token = t;
  localStorage.setItem("gcli_token", t);
}

async function api(path, opts = {}) {
  const headers = Object.assign({ Authorization: "Bearer " + state.token }, opts.headers || {});
  const res = await fetch(path, Object.assign({}, opts, { headers }));
  if (res.status === 401) { showGate("токен не подошёл — вставьте верный"); throw new Error("401"); }
  if (!res.ok) {
    let msg = "HTTP " + res.status;
    try { msg = (await res.json()).error || msg; } catch (_) {}
    throw new Error(msg);
  }
  return res.json();
}

function setConn(ok) {
  $("conn").classList.toggle("conn-on", ok);
  $("conn").classList.toggle("conn-off", !ok);
}

/* ---------- экран токена ---------- */

function showGate(err) {
  $("root").classList.add("hidden");
  $("gate").classList.remove("hidden");
  $("gate-err").textContent = err || "";
  $("gate-token").focus();
}

async function enterRoot() {
  $("gate").classList.add("hidden");
  $("root").classList.remove("hidden");
  try {
    await refreshStatus();
    setConn(true);
  } catch (e) {
    if (e.message !== "401") setConn(false);
    return;
  }
  await Promise.all([refreshHistory(), refreshSessions(), refreshMission()]);
  connectSSE();
}

/* ---------- статус и шапка ---------- */

async function refreshStatus() {
  const st = await api("/v1/status");
  $("ver").textContent = "v" + st.version;
  $("model-chip").textContent = st.provider + " · " + st.model;
  $("mode-chip").classList.toggle("hidden", !st.agent_mode);
  $("workdir").textContent = st.work_dir;
  state.running = !!st.running;
  setComposer();
}

/* ---------- лента ---------- */

function el(tag, cls, text) {
  const n = document.createElement(tag);
  if (cls) n.className = cls;
  if (text !== undefined) n.textContent = text;
  return n;
}

function scrollDown() {
  const c = $("chat");
  c.scrollTop = c.scrollHeight;
}

function hideEmpty() {
  $("empty").classList.add("hidden");
}

// addMessage — карточка сообщения истории или живого потока.
function addMessage(role, content, opts = {}) {
  hideEmpty();
  const wrap = el("div", "msg " + role);

  const head = el("div", "msg-head");
  const mark = el("span", "mark", "◆");
  const who = el("span", null,
    role === "user" ? "вы" :
    role === "assistant" ? (opts.sub ? opts.sub : "gcli") : "инструмент");
  head.append(mark, who);
  wrap.append(head);

  if (role === "assistant" && opts.reasoning) {
    wrap.append(buildReason(opts.reasoning));
  }
  if (content) {
    const body = el("div", "msg-body", content);
    if (role === "user") { body.classList.add("bubble"); }
    wrap.append(body);
  }
  $("chat").append(wrap);
  scrollDown();
  return wrap;
}

// buildReason — свёрнутый блок размышлений (как Ctrl+O в TUI).
function buildReason(text) {
  const det = el("details", "reason");
  const sum = el("summary");
  const body = el("div", "reason-body", text);
  det.append(sum, body);
  return det;
}

// addTool — карточка вызова инструмента: «◆ name args» + «⎿ результат».
function addTool(name, args, kind) {
  hideEmpty();
  const card = el("div", "tool wait");
  const head = el("div", "tool-head");
  head.append(
    el("span", "mark", "◆"),
    el("span", "tool-name", name),
    el("span", "tool-args", args || "")
  );
  const res = el("div", "tool-res");
  res.append(el("span", "tick", "⎿"), el("span", "txt", "работает..."));
  card.append(head, res);
  $("chat").append(card);
  scrollDown();
  return card;
}

function finishTool(card, status, detail) {
  if (!card) return;
  card.classList.remove("wait");
  card.classList.add(status === "ok" ? "ok" : status === "denied" ? "denied" : "fail");
  const txt = card.querySelector(".txt");
  txt.textContent = detail || "готово";
  txt.className = "txt";
  const st = el("span", "st", status);
  card.querySelector(".tool-res").append(st);
}

/* ---------- история и сессии ---------- */

async function refreshHistory(sessionID) {
  const q = sessionID ? "?session=" + encodeURIComponent(sessionID) : "";
  const h = await api("/v1/history" + q);
  $("chat").innerHTML = "";
  state.currentMsg = null;
  state.currentReason = null;

  let pendingTool = {};
  for (const m of h.messages) {
    if (m.role === "user") {
      addMessage("user", m.content);
    } else if (m.role === "assistant") {
      const hasCalls = m.tool_calls && m.tool_calls.length;
      if (m.content || m.reasoning || !hasCalls) {
        addMessage("assistant", m.content, { reasoning: m.reasoning, sub: m.sub });
      }
      if (hasCalls) for (const tc of m.tool_calls) {
        pendingTool[tc.name] = addTool(tc.name, shortArgs(tc.args));
      }
    } else if (m.role === "tool") {
      const card = pendingTool[m.name];
      finishTool(card, "ok", m.content);
      delete pendingTool[m.name];
    }
  }
  $("view-chip").classList.toggle("hidden", !sessionID);
  $("back-current").classList.toggle("hidden", !sessionID);
  markActiveSession(sessionID || h.id);
}

function shortArgs(args) {
  // В истории аргументы лежат сырым JSON; для строки заголовка — выжимка.
  try {
    const m = JSON.parse(args);
    for (const k of ["command", "path", "pattern", "query", "url", "title", "input"]) {
      if (typeof m[k] === "string" && m[k]) return m[k].slice(0, 90);
    }
    if (Array.isArray(m.todos)) return m.todos.length + " задач";
    if (typeof m.task === "string" && m.task) return m.task.slice(0, 90);
  } catch (_) {}
  return args ? String(args).slice(0, 90) : "";
}

async function refreshSessions() {
  const list = await api("/v1/sessions");
  const ul = $("sessions");
  ul.innerHTML = "";
  for (const s of list) {
    const li = el("li", null, s.title || s.id);
    li.append(el("span", "n", s.messages + ""));
    li.onclick = () => {
      state.viewing = s.id;
      refreshHistory(s.id).catch(toast);
    };
    ul.append(li);
  }
  markActiveSession(null);
}

function markActiveSession(id) {
  for (const li of $("sessions").children) {
    li.classList.toggle("active",
      state.viewing ? state.viewing === id : li.textContent.startsWith(id || ""));
  }
}

$("back-current").onclick = () => {
  state.viewing = null;
  refreshHistory().catch(toast);
};

/* ---------- миссия ---------- */

async function refreshMission() {
  const m = await api("/v1/mission");
  const card = $("mission-card");
  const mission = m.mission || {};
  const active = mission.active || mission.status === "running" ||
    (m.status && m.status.state === "running");
  if (!active) { card.classList.add("hidden"); return; }
  card.classList.remove("hidden");
  $("mission-obj").textContent = mission.objective || mission.goal || "—";
  const meta = [];
  if (mission.mode) meta.push("режим: " + mission.mode);
  if (m.status && m.status.elapsed) meta.push("время: " + m.status.elapsed);
  if (m.status && m.status.spent_tokens) meta.push("токены: " + m.status.spent_tokens);
  $("mission-meta").textContent = meta.join("\n");
}

$("mission-stop").onclick = async () => {
  try { await api("/v1/mission/stop", { method: "POST" }); toast("миссия остановлена"); }
  catch (e) { toast(e.message); }
  refreshMission().catch(() => {});
};

/* ---------- SSE ---------- */

let sse = null;
function connectSSE() {
  if (sse) sse.close();
  sse = new EventSource("/v1/events?token=" + encodeURIComponent(state.token));
  sse.onopen = () => setConn(true);
  sse.onerror = () => { setConn(false); };
  sse.onmessage = (ev) => {
    let msg;
    try { msg = JSON.parse(ev.data); } catch (_) { return; }
    setConn(true);
    handleEvent(msg.event, msg.data);
  };
}

function handleEvent(ev, data) {
  switch (ev) {
    case "turn_start":
      state.running = true;
      setComposer();
      if (state.viewing) { state.viewing = null; refreshHistory().catch(() => {}); }
      startThinking();
      break;

    case "reason":
      if (state.viewing) break;
      if (!state.currentMsg) {
        state.currentMsg = addMessage("assistant", "");
        state.currentReason = buildReason("");
        state.currentMsg.append(state.currentReason);
      }
      state.currentReason.querySelector(".reason-body").textContent += data;
      break;

    case "delta":
      stopThinking();
      if (state.viewing) break;
      if (!state.currentMsg) state.currentMsg = addMessage("assistant", "");
      let body = state.currentMsg.querySelector(".msg-body");
      if (!body) { body = el("div", "msg-body"); state.currentMsg.append(body); }
      body.textContent += data;
      scrollDown();
      break;

    case "tool_start":
      stopThinking();
      if (state.viewing) break;
      state.currentMsg = null;
      state.currentReason = null;
      state.tools.set(data.name, addTool(data.name, data.args, data.kind));
      break;

    case "tool_done":
      if (state.viewing) break;
      finishTool(state.tools.get(data.name), data.status, data.detail);
      state.tools.delete(data.name);
      break;

    case "turn_end":
      stopThinking();
      state.running = false;
      state.currentMsg = null;
      state.currentReason = null;
      setComposer();
      // Обновим списки: сессия получила новые сообщения, миссия — счётчики.
      refreshSessions().catch(() => {});
      refreshMission().catch(() => {});
      if (data.error) toast(data.error);
      break;
  }
}

/* ---------- «thinking...» ---------- */

function startThinking() {
  $("think-line").classList.remove("hidden");
  state.thinkT0 = Date.now();
  state.thinkTimer = setInterval(() => {
    $("think-time").textContent = Math.round((Date.now() - state.thinkT0) / 1000) + "s";
  }, 500);
}

function stopThinking() {
  $("think-line").classList.add("hidden");
  if (state.thinkTimer) { clearInterval(state.thinkTimer); state.thinkTimer = null; }
}

/* ---------- отправка ---------- */

function setComposer() {
  $("send").disabled = state.running;
  $("input").disabled = state.running;
  $("input").placeholder = state.running
    ? "агент работает — ждём..."
    : "чем помочь? — Enter отправить, Shift+Enter перенос";
}

async function send() {
  const text = $("input").value.trim();
  if (!text || state.running) return;
  $("input").value = "";
  autosize();
  addMessage("user", text);
  state.running = true;
  setComposer();
  startThinking();
  try {
    await api("/v1/message", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ text }),
    });
  } catch (e) {
    stopThinking();
    state.running = false;
    setComposer();
    if (e.message !== "401") toast(e.message);
  }
}

$("send").onclick = send;
$("input").addEventListener("keydown", (e) => {
  if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); send(); }
});

function autosize() {
  const t = $("input");
  t.style.height = "auto";
  t.style.height = Math.min(t.scrollHeight, 180) + "px";
}
$("input").addEventListener("input", autosize);

/* ---------- прочее ---------- */

let toastTimer = null;
function toast(msg) {
  const t = $("toast");
  t.textContent = msg;
  t.classList.remove("hidden");
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => t.classList.add("hidden"), 3500);
}

for (const b of document.querySelectorAll(".hint")) {
  b.onclick = () => {
    $("input").value = b.dataset.fill;
    autosize();
    $("input").focus();
  };
}

$("gate-ok").onclick = tryToken;
$("gate-token").addEventListener("keydown", (e) => {
  if (e.key === "Enter") tryToken();
});

async function tryToken() {
  const t = $("gate-token").value.trim();
  if (!t) { showGate("введите токен"); return; }
  saveToken(t);
  try {
    $("gate").classList.add("hidden");
    $("root").classList.remove("hidden");
    await enterRoot();
  } catch (e) {
    if (e.message === "401") return; // showGate уже показан
    showGate("сервер недоступен: " + e.message);
  }
}

/* ---------- старт ---------- */

(async function init() {
  const urlToken = tokenFromURL();
  if (urlToken) {
    saveToken(urlToken);
    // Токен из URL убираем: он светился в адресной строке достаточно.
    history.replaceState(null, "", location.pathname);
  }
  const saved = localStorage.getItem("gcli_token");
  if (!saved) { showGate(""); return; }
  saveToken(saved);
  $("root").classList.remove("hidden");
  try {
    await enterRoot();
  } catch (e) {
    if (e.message !== "401") showGate("сервер недоступен: " + e.message);
  }
})();

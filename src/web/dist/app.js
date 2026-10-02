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
  sessionID: "",      // id текущей сессии (из /v1/status)
  currentMsg: null,   // карточка текущего ответа ассистента
  currentReason: null,// блок размышлений текущего ответа
  tools: new Map(),   // незавершённые карточки инструментов по id вызова
  thinkT0: 0,
  thinkTimer: null,
  // historyEpoch — защита от гонки «SSE против fetch»: живые события и
  // новые обновления истории делают устаревший запрос, и его результат
  // не затирает уже отрисованные карточки (раньше первые дельты ответа
  // стирались завершившимся fetch-ем после turn_start).
  historyEpoch: 0,
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
  // allSettled, а не Promise.all: отказ одного списка (истории, сессий,
  // миссии) раньше отменял connectSSE целиком — оболочка показывала
  // «сервер недоступен» при валидном токене.
  await Promise.allSettled([refreshHistory(), refreshSessions(), refreshMission()]);
  connectSSE();
}

/* ---------- статус и шапка ---------- */

async function refreshStatus() {
  const st = await api("/v1/status");
  $("ver").textContent = "v" + st.version;
  $("model-chip").textContent = st.provider + " · " + st.model;
  $("mode-chip").classList.toggle("hidden", !st.agent_mode);
  $("workdir").textContent = st.work_dir;
  state.sessionID = st.session || "";
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
  // Эпоха: если пока мы ждали ответ, пришли живые события или кто-то ещё
  // запросил историю — наш ответ устарел, и затирать ленту им нельзя.
  const epoch = ++state.historyEpoch;
  const q = sessionID ? "?session=" + encodeURIComponent(sessionID) : "";
  const h = await api("/v1/history" + q);
  if (epoch !== state.historyEpoch) return;
  $("chat").innerHTML = "";
  state.currentMsg = null;
  state.currentReason = null;
  state.tools.clear();

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
        pendingTool[tc.id || tc.name] = addTool(tc.name, shortArgs(tc.args));
      }
    } else if (m.role === "tool") {
      const card = pendingTool[m.call_id || m.name];
      finishTool(card, "ok", m.content);
      delete pendingTool[m.call_id || m.name];
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
    li.dataset.id = s.id;
    li.append(el("span", "n", s.messages + ""));
    li.onclick = () => {
      state.viewing = s.id;
      refreshHistory(s.id).catch(toast);
    };
    ul.append(li);
  }
  markActiveSession(state.viewing || state.sessionID);
}

// markActiveSession — подсветить конкретный элемент списка: раньше условие
// не смотрело на сам элемент, и подсветка была либо «везде», либо нигде.
function markActiveSession(id) {
  for (const li of $("sessions").children) {
    li.classList.toggle("active", !!id && li.dataset.id === id);
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
  const status = mission.status || {};
  const active = !!mission.active || status.state === "running";
  if (!active) { card.classList.add("hidden"); return; }
  card.classList.remove("hidden");
  $("mission-obj").textContent = mission.objective || "—";
  const meta = [];
  if (mission.mode) meta.push("режим: " + mission.mode);
  if (status.elapsed) meta.push("время: " + status.elapsed);
  if (status.spent_tokens) meta.push("токены: " + status.spent_tokens);
  $("mission-meta").textContent = meta.join("\n");
}

$("mission-stop").onclick = async () => {
  try { await api("/v1/mission/stop", { method: "POST" }); toast("миссия остановлена"); }
  catch (e) { toast(e.message); }
  refreshMission().catch(() => {});
};

/* ---------- SSE ---------- */

let sse = null;
let sseErrCount = 0;
function connectSSE() {
  if (sse) sse.close();
  sseErrCount = 0;
  sse = new EventSource("/v1/events?token=" + encodeURIComponent(state.token));
  sse.onopen = () => { sseErrCount = 0; setConn(true); };
  sse.onerror = async () => {
    setConn(false);
    // Три ошибки подряд — проверяем токен: сервер мог перезапуститься с
    // новым. Раньше 401 вешал бесконечный reconnect, и на экран токена
    // оболочка не возвращалась никогда.
    sseErrCount++;
    if (sseErrCount < 3) return;
    sseErrCount = 0;
    try {
      const res = await fetch("/v1/status", { headers: { Authorization: "Bearer " + state.token } });
      if (res.status === 401) { sse.close(); showGate("сервер перезапущен — вставьте токен"); }
    } catch (_) {}
  };
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
      state.historyEpoch++; // живые события делают летящий fetch истории устаревшим
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
      state.historyEpoch++;
      if (!state.currentMsg) state.currentMsg = addMessage("assistant", "");
      let body = state.currentMsg.querySelector(".msg-body");
      if (!body) { body = el("div", "msg-body"); state.currentMsg.append(body); }
      body.textContent += data;
      scrollDown();
      break;

    case "tool_start":
      stopThinking();
      if (state.viewing) break;
      state.historyEpoch++;
      state.currentMsg = null;
      state.currentReason = null;
      // Ключ — id вызова: два одноимённых параллельных вызова раньше
      // склеивались в одну карточку, и вторая навсегда висела «работает...».
      state.tools.set(data.id || data.name, addTool(data.name, data.args, data.kind));
      break;

    case "tool_done":
      if (state.viewing) break;
      state.historyEpoch++;
      {
        const key = data.id || data.name;
        const card = state.tools.get(key);
        if (card) { finishTool(card, data.status, data.detail); state.tools.delete(key); }
      }
      break;

    case "turn_end":
      stopThinking();
      state.running = false;
      state.currentMsg = null;
      state.currentReason = null;
      setComposer();
      // Пересинхронизация с авторитетной историей: всё, что стримилоcь
      // живьём (в том числе потерянный из-за гонки кусок), гарантированно
      // попадает в ленту.
      if (!state.viewing) refreshHistory().catch(() => {});
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

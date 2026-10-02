// gCLI Desktop — десктоп-оболочка поверх того же движка, что и TUI.
//
// Архитектура повторяет схему OpenCode: сервер один (gcli -serve),
// клиенты разные. Здесь клиентом становится Electron-окно с встроенной
// оболочкой /ui/. Возможны два режима:
//
//   1. Автономный (по умолчанию): приложение само запускает gcli
//      -serve на локальном порту и открывает окно.
//   2. Подключение: GCLI_URL=http://127.0.0.1:8642 -.join к уже
//      запущенному серверу (например, к серверу миссии).
//
// Бинарник gcli ищется в таком порядке:
//   $GCLI_BIN → desktop/bin/gcli(.exe) → process.resourcesPath/bin → PATH.
"use strict";

const { app, BrowserWindow, Menu, shell } = require("electron");
const { spawn } = require("child_process");
const crypto = require("crypto");
const http = require("http");
const path = require("path");
const fs = require("fs");

const ATTACH_URL = process.env.GCLI_URL || "";
const GCLI_BIN = process.env.GCLI_BIN || "";
const PORT = process.env.GCLI_PORT || "8642";
const TOKEN = process.env.GCLI_TOKEN || crypto.randomBytes(16).toString("hex");

let win = null;
let gcliProc = null;
let quitRequested = false;

/* ---------- поиск бинарника ---------- */

function findGcli() {
  const exe = process.platform === "win32" ? "gcli.exe" : "gcli";
  const candidates = [
    GCLI_BIN,
    path.join(__dirname, "bin", exe),                   // desktop/bin/gcli
    path.join(process.resourcesPath || "", "bin", exe), // внутри сборки
  ];
  // В репозитории сборка кладёт бинарники в ../builds (gcli-6.2.0-*): берём свежий.
  try {
    const buildsDir = path.join(__dirname, "..", "builds");
    const m = fs.readdirSync(buildsDir).filter((f) => f.startsWith("gcli-")).sort().pop();
    if (m) candidates.push(path.join(buildsDir, m));
  } catch (_) {}
  candidates.push("/usr/local/bin/" + exe);
  for (const c of candidates) {
    if (!c) continue;
    try { if (fs.existsSync(c) && fs.statSync(c).isFile()) return c; } catch (_) {}
  }
  return exe; // из PATH
}

/* ---------- ожидание готовности сервера ---------- */

function waitForServer(url, timeoutMs) {
  const t0 = Date.now();
  return new Promise((resolve, reject) => {
    const probe = () => {
      const req = http.get(url, (res) => {
        res.resume();
        if (res.statusCode === 200) return resolve();
        retry();
      });
      req.on("error", retry);
      req.setTimeout(1500, () => { req.destroy(); retry(); });
    };
    const retry = () => {
      if (Date.now() - t0 > timeoutMs) return reject(new Error("сервер не поднялся за " + timeoutMs + " мс"));
      setTimeout(probe, 300);
    };
    probe();
  });
}

/* ---------- окно ---------- */

function createWindow(baseURL) {
  win = new BrowserWindow({
    width: 1240,
    height: 820,
    minWidth: 860,
    minHeight: 560,
    backgroundColor: "#141417", // уголь — фирменный фон «УГОЛЬ»
    title: "gCLI",
    autoHideMenuBar: true,
    icon: path.join(__dirname, "icon.png"),
    webPreferences: {
      preload: path.join(__dirname, "preload.js"),
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: true,
    },
  });

  win.loadURL(baseURL);

  // Внешние ссылки — в системный браузер, не внутрь окна агента.
  win.webContents.setWindowOpenHandler(({ url: target }) => {
    if (target.startsWith(baseURL)) return { action: "allow" };
    shell.openExternal(target);
    return { action: "deny" };
  });
  win.webContents.on("will-navigate", (e, target) => {
    if (!target.startsWith(baseURL)) {
      e.preventDefault();
      shell.openExternal(target);
    }
  });

  win.on("closed", () => { win = null; });
}

/* ---------- меню: минимум, без стандартной каши ---------- */

function buildMenu() {
  const isMac = process.platform === "darwin";
  const template = [
    ...(isMac ? [{ role: "appMenu" }] : []),
    {
      label: "Вид",
      submenu: [
        { role: "reload", label: "Перезагрузить" },
        { role: "forceReload" },
        { type: "separator" },
        { role: "zoomIn", label: "Крупнее" },
        { role: "zoomOut", label: "Мельче" },
        { role: "resetZoom", label: "Обычный размер" },
        { type: "separator" },
        { role: "toggleDevTools", label: "Инструменты разработчика" },
      ],
    },
    {
      label: "Правка",
      submenu: [
        { role: "undo", label: "Отменить" },
        { role: "redo", label: "Вернуть" },
        { type: "separator" },
        { role: "cut", label: "Вырезать" },
        { role: "copy", label: "Копировать" },
        { role: "paste", label: "Вставить" },
        { role: "selectAll", label: "Выделить всё" },
      ],
    },
  ];
  Menu.setApplicationMenu(Menu.buildFromTemplate(template));
}

/* ---------- жизненный цикл ---------- */

const gotLock = app.requestSingleInstanceLock();
if (!gotLock) {
  app.quit();
} else {
  app.on("second-instance", () => {
    if (win) { if (win.isMinimized()) win.restore(); win.focus(); }
  });

  app.whenReady().then(async () => {
    buildMenu();
    let baseURL;
    if (ATTACH_URL) {
      // Режим подключения: сервер уже живёт своей жизнью.
      const origin = ATTACH_URL.replace(/\/+$/, "");
      baseURL = origin + "/ui/?token=" + encodeURIComponent(TOKEN);
      createWindow(baseURL);
    } else {
      const bin = findGcli();
      gcliProc = spawn(bin, ["-serve", "127.0.0.1:" + PORT, "-serve-token", TOKEN, "-no-color"], {
        stdio: ["ignore", "pipe", "pipe"],
      });
      gcliProc.stdout.on("data", (d) => process.env.GCLI_DEBUG && process.stdout.write("[gcli] " + d));
      gcliProc.stderr.on("data", (d) => process.stderr.write("[gcli!] " + d));
      gcliProc.on("exit", (code) => {
        if (!quitRequested && win) {
          win.setTitle("gCLI — сервер остановлен (" + code + ")");
        }
      });
      const statusURL = "http://127.0.0.1:" + PORT + "/v1/status?token=" + TOKEN;
      try {
        await waitForServer(statusURL, 20000);
      } catch (e) {
        // Сервер не поднялся: всё равно открываем окно — там экран
        // токена покажет, что связь не установлена.
        console.error("gCLI Desktop:", e.message);
      }
      const origin = "http://127.0.0.1:" + PORT;
      createWindow(origin + "/ui/?token=" + encodeURIComponent(TOKEN));
    }
    app.on("activate", () => {
      if (BrowserWindow.getAllWindows().length === 0) {
        const origin = ATTACH_URL ? ATTACH_URL.replace(/\/+$/, "") : "http://127.0.0.1:" + PORT;
        createWindow(origin + "/ui/?token=" + encodeURIComponent(TOKEN));
      }
    });
  });

  app.on("window-all-closed", () => {
    quitRequested = true;
    app.quit();
  });

  app.on("before-quit", () => {
    quitRequested = true;
    if (gcliProc) {
      try { gcliProc.kill(); } catch (_) {}
      gcliProc = null;
    }
  });
}

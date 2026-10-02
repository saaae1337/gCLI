// preload: минимум. Оболочке (/ui/) ничего из Node не нужно — она
// работает с HTTP-сервером gcli; мост оставлен пустым на будущее
// (версии, интеграция с тайтлбаром).
"use strict";

const { contextBridge } = require("electron");

contextBridge.exposeInMainWorld("gcliDesktop", {
  platform: process.platform,
  electron: process.versions.electron,
});

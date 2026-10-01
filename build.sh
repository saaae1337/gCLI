#!/usr/bin/env bash
# build.sh — кросс-сборка gcli для всех целевых платформ.
#
# Собирает из ../src бинарники в ../builds, проставляет версию в сборку
# и пересчитывает SHA256SUMS.txt. Работает из любой ОС (нужен только Go).
#
#   ./build.sh              все платформы
#   ./build.sh test         прогнать тесты
#   ./build.sh vet          go vet
#   ./build.sh fmt          gofmt -l по дереву
#   ./build.sh clean        убрать бинарники из builds/
#   ./build.sh one [GOOS] [GOARCH]
set -euo pipefail

SRC_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/src"
OUT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/builds"
VERSION="${GCLI_VERSION:-5.5.0}"
LDFLAGS="-s -w -X main.buildVersion=$VERSION"

# Версия в core/core.go — источник правды для всего, что читает core.Version:
# User-Agent, MCP handshake, баннер без ldflags. Если она разошлась с
# $VERSION, в бинарнике окажутся два разных номера: имя файла и -v скажут одно,
# а User-Agent и MCP-сервер — другое. Ловим это до сборки, а не после.
core_version() { grep -oE '^var Version = "[^"]+"' "$SRC_DIR/core/core.go" | grep -oE '[0-9][^"]*' || true; }

# GOOS/GOARCH/имя файла. Первые четыре — то, что реально отдаётся пользователю.
TARGETS=(
  "windows amd64 gcli-$VERSION-windows-amd64.exe"
  "windows arm64 gcli-$VERSION-windows-arm64.exe"
  "linux   amd64 gcli-$VERSION-linux-amd64"
  "darwin  amd64 gcli-$VERSION-darwin-amd64"
  "darwin  arm64 gcli-$VERSION-darwin-arm64"
)

# sha256 — BSD и GNU варианты (Linux/macOS против Git Bash).
sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
  else
    shasum -a 256 "$1" | awk '{print $1}'
  fi
}

cmd_test() {
  echo "▎ Тесты"
  (cd "$SRC_DIR" && go test ./...)
}

cmd_vet() {
  echo "▎ go vet"
  (cd "$SRC_DIR" && go vet ./...)
}

cmd_fmt() {
  echo "▎ gofmt"
  local bad
  bad="$(cd "$SRC_DIR" && gofmt -l . || true)"
  if [ -n "$bad" ]; then
    echo "НЕ отформатированы файлы:"; echo "$bad"; return 1
  fi
  echo "Всё отформатировано."
}

cmd_clean() {
  echo "▎ Чистка builds/"
  rm -f "$OUT_DIR"/gcli-* 2>/dev/null || true
  # хвосты "~" — отложенные при перезаписи запущенные бинарники (Windows)
  rm -f "$OUT_DIR"/gcli-*"~" 2>/dev/null || true
  echo "Готово."
}

cmd_one() {
  local goos="${1:-$(go env GOOS)}" goarch="${2:-$(go env GOARCH)}"
  local ext=""
  [ "$goos" = "windows" ] && ext=".exe"
  local out="$OUT_DIR/gcli-$VERSION-$goos-$goarch$ext"
  mkdir -p "$OUT_DIR"
  echo "▎ $goos/$goarch"
  # Если бинарник этой платформы сейчас запущен (сборка изнутри него),
  # Windows не даст его перезаписать — снимаем хвост "~" заранее.
  rm -f "$out~" 2>/dev/null || true
  (cd "$SRC_DIR" && CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
     go build -trimpath -ldflags "$LDFLAGS" -o "$out" .)
  echo "  $(basename "$out")"
}

cmd_all() {
  echo "▎ Сборка $VERSION"
  local cv
  cv="$(core_version)"
  if [ -z "$cv" ]; then
    echo "Не нашёл Version в core/core.go — проверь строку 'var Version = ...'"; return 1
  fi
  if [ "$cv" != "$VERSION" ]; then
    echo "Версии разошлись: core/core.go = $cv, сборка = $VERSION"
    echo "Поправь одну из них (или задай GCLI_VERSION=$cv) и повтори."
    return 1
  fi
  mkdir -p "$OUT_DIR"
  # Бинарники прошлых версий — не артефакты этого релиза. Без этого в
  # SHA256SUMS.txt попали бы и 5.0.3, и 5.1.0, и проверка сумм у пользователя
  # ругалась бы на файлы, которых он не качал.
  drop_other_versions "$VERSION"
  for t in "${TARGETS[@]}"; do
    # shellcheck disable=SC2086
    set -- $t
    cmd_one "$1" "$2"
  done
  write_sums
}

# drop_other_versions — убрать из builds/ бинарники с версией не этой сборки.
drop_other_versions() {
  local keep="$1" stale
  stale="$(cd "$OUT_DIR" && ls -1 2>/dev/null | grep -E '^gcli-[0-9]' | grep -vE "^gcli-$keep[-~]" || true)"
  if [ -n "$stale" ]; then
    echo "$stale" | while read -r f; do
      [ -n "$f" ] && rm -f "$OUT_DIR/$f" && echo "  удалён устаревший $f"
    done
  fi
}

write_sums() {
  # Только настоящие артефакты сборки: без "~" (отложенные копии запущенных
  # бинарников — их Windows создаёт при неудачной перезаписи) и без служебных
  # файлов каталога. Раньше "~" попадал в файл сумм.
  (cd "$OUT_DIR" && ls -1 | grep -E '^gcli-[0-9][^~]*$' | sort | while read -r f; do
    printf '%s  %s\n' "$(sha256_of "$f")" "$f"
  done) > "$OUT_DIR/SHA256SUMS.txt"
  echo "▎ SHA256SUMS.txt обновлён ($(grep -c . "$OUT_DIR/SHA256SUMS.txt") файлов)"
}

case "${1:-all}" in
  all)   cmd_all ;;
  test)  cmd_test ;;
  vet)   cmd_vet ;;
  fmt)   cmd_fmt ;;
  clean) cmd_clean ;;
  one)   shift; cmd_one "$@" ;;
  *)     echo "Неизвестная команда: $1"; sed -n '2,12p' "$0"; exit 1 ;;
esac

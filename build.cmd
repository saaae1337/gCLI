@echo off
rem build.cmd — сборка gcli для всех платформ из обычной командной строки.
rem Обёртка над build.sh: тот же результат, но через Git Bash.
setlocal

where bash >nul 2>&1
if errorlevel 1 (
    echo [build] Git Bash не найден ^(команды bash^).
    echo [build] Поставьте Git for Windows или запустите сборку вручную:
    echo [build]   cd src ^&^& set GOOS=windows ^&^& go build -ldflags "-s -w" -o ..\builds\gcli.exe .
    exit /b 1
)

rem %~dp0 — каталог этого файла, с обратными слэшами; приводим к /c/gcli
set "SCRIPT=%~dp0build.sh"
set "SCRIPT=%SCRIPT:\=/%"
echo [build] %SCRIPT% %*
bash "%SCRIPT%" %*
exit /b %errorlevel%

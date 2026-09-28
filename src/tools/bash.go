package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"gcli/core"
)

// dangerRules — регулярки потенциально опасных команд.
// Для них подтверждение только «да/нет», без «всегда».
var dangerRules = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\brm\s+(-[a-z]+\s+)*(-[a-z]*[rf][a-z]*)(\s+\S+)*\s+(/|~|\$HOME|\.\.)`),
	regexp.MustCompile(`(?i)\brm\s+-[a-z]*r[a-z]*f[a-z]*\s+/(?:\s|$)`),
	regexp.MustCompile(`(?i)\bsudo\b`),
	regexp.MustCompile(`(?i)\b(mkfs(\.\w+)?|shutdown|reboot|halt|poweroff|init\s+[06])\b`),
	regexp.MustCompile(`(?i)\bdd\b[^|]*\bof=/dev/`),
	regexp.MustCompile(`(?i):\(\)\s*\{.*\};\s*:`),
	regexp.MustCompile(`(?i)\bchmod\s+-R\s+777\s+/(?:\s|$)`),
	regexp.MustCompile(`(?i)>\s*/dev/(sd|nvme|hd)`),
	regexp.MustCompile(`(?i)\b(curl|wget)\b[^|]*\|\s*(sudo\s+)?(ba|z|da)?sh\b`),
	regexp.MustCompile(`(?i)\bgit\s+push\b[^;|&]*(--force|-f)\b`),
	regexp.MustCompile(`(?i)\bgit\s+(reset\s+--hard|clean\s+-[a-z]*f)`),
	regexp.MustCompile(`(?i)\bnpm\s+publish\b|\bpip\s+upload\b|\bcargo\s+publish\b`),
	regexp.MustCompile(`(?i)\bdocker\s+(rm|rmi|system\s+prune|volume\s+rm)\b`),
	regexp.MustCompile(`(?i)\bkubectl\s+delete\b`),
	regexp.MustCompile(`(?i)\bhistory\s+-c\b|\btruncate\s+-s\s*0\b`),
	// Windows-специфичное.
	regexp.MustCompile(`(?i)\bformat\s+[a-z]:`),
	regexp.MustCompile(`(?i)\b(rd|rmdir|del)\b[^&|]*\s/s\b`),
	regexp.MustCompile(`(?i)\bremove-item\b[^&|]*-recurse`),
	regexp.MustCompile(`(?i)\breg\s+(delete|add)\s+hk`),
	regexp.MustCompile(`(?i)\bdiskpart\b`),
	regexp.MustCompile(`(?i)\bnet\s+user\b.*(/add|\/domain) `),
}

// IsDangerous — команда потенциально опасна (нужен строгий режим подтверждения).
func IsDangerous(cmd string) bool {
	for _, re := range dangerRules {
		if re.MatchString(cmd) {
			return true
		}
	}
	return false
}

// ShellCommand — выбрать оболочку для выполнения команды.
func ShellCommand(cmd string) (name string, args []string) {
	if custom := strings.TrimSpace(os.Getenv("GCLI_SHELL")); custom != "" {
		base := strings.ToLower(filepath.Base(custom))
		switch {
		case strings.Contains(base, "powershell"), strings.Contains(base, "pwsh"):
			return custom, []string{"-NoProfile", "-Command", cmd}
		case strings.Contains(base, "cmd"):
			return custom, []string{"/c", cmd}
		default:
			return custom, []string{"-c", cmd}
		}
	}
	if runtime.GOOS == "windows" {
		// Git Bash / WSL обычно доступны и дают куда более предсказуемый вывод.
		for _, cand := range []string{
			filepath.Join(os.Getenv("ProgramFiles"), "Git", "bin", "bash.exe"),
			filepath.Join(os.Getenv("ProgramFiles(x86)"), "Git", "bin", "bash.exe"),
			filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "Git", "bin", "bash.exe"),
		} {
			if cand == "" {
				continue
			}
			if st, err := os.Stat(cand); err == nil && !st.IsDir() {
				return cand, []string{"-c", cmd}
			}
		}
		if p, err := exec.LookPath("bash"); err == nil {
			return p, []string{"-c", cmd}
		}
		return "cmd", []string{"/c", cmd}
	}
	if p, err := exec.LookPath("bash"); err == nil {
		return p, []string{"-c", cmd}
	}
	return "sh", []string{"-c", cmd}
}

// ShellName — имя оболочки по умолчанию (для /status).
func ShellName() string {
	n, _ := ShellCommand("echo")
	return filepath.Base(n)
}

// maxBashOutput — сколько символов вывода команды отдаём модели.
const maxBashOutput = 16000

// hBash — выполнить shell-команду.
func (r *Registry) hBash(ctx context.Context, m map[string]any) (Result, error) {
	cmd := ArgStr(m, "command")
	if strings.TrimSpace(cmd) == "" {
		return Result{}, fmt.Errorf("укажи command")
	}
	timeout := core.Clamp(ArgInt(m, "timeout_sec", 60), 5, 600)
	workdir := r.workDir
	if wd := ArgStr(m, "workdir"); wd != "" {
		workdir = r.resolvePath(wd)
	}

	if r.env.Confirm != nil {
		ok := r.env.Confirm(ConfirmReq{Kind: ConfirmExec, Detail: cmd, Reason: "выполнение команды"})
		if !ok {
			return Result{Text: "Команда отклонена пользователем", Summary: "отклонено"}, nil
		}
	}

	cctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()
	shell, sargs := ShellCommand(cmd)
	cmdEx := exec.CommandContext(cctx, shell, sargs...)
	cmdEx.Dir = workdir
	cmdEx.Env = append(os.Environ(), "GCLI=1")

	outBytes, err := cmdEx.CombinedOutput()
	out := string(outBytes)
	lines := len(core.SplitLines(out))
	out = core.TruncateUTF8(out, maxBashOutput/2, maxBashOutput/2)

	if err != nil {
		var ee *exec.ExitError
		switch {
		case errors.As(err, &ee):
			out += fmt.Sprintf("\n[код выхода: %d]", ee.ExitCode())
		case cctx.Err() == context.DeadlineExceeded:
			return Result{
				Text:    fmt.Sprintf("Таймаут %ds. Частичный вывод:\n%s", timeout, out),
				Summary: fmt.Sprintf("таймаут %ds", timeout),
			}, nil
		case ctx.Err() != nil:
			return Result{Error: "команда прервана"}, nil
		default:
			return Result{}, fmt.Errorf("%v", err)
		}
	}
	if strings.TrimSpace(out) == "" {
		out = "[нет вывода]"
	}
	return Result{
		Text:    out,
		Summary: fmt.Sprintf("%d строк вывода · %s", lines, core.OneLine(firstLine(out))),
	}, nil
}

func firstLine(s string) string {
	l := core.SplitLines(s)
	if len(l) == 0 {
		return ""
	}
	return l[0]
}

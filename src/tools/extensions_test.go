package tools

import (
	"context"
	"strings"
	"testing"
)

// TestExtCmdReadOnlyMode — в режиме «только чтение» разрешены только
// расширения с read_only=true. Раньше условие было перевёрнуто: чтение
// блокировалось, а запись выполнялась.
func TestExtCmdReadOnlyMode(t *testing.T) {
	r := New(Env{WorkDir: t.TempDir(), ReadOnly: true})
	ext := Extension{Name: "probe"}

	write := r.extCmdHandler(ext, ExtTool{Name: "writer", Command: "echo wrote"})
	res, err := write(context.Background(), nil)
	if err != nil {
		t.Fatalf("запись вернула ошибку вместо отказа: %v", err)
	}
	if res.Error == "" {
		t.Fatal("расширение-запись выполнилось в режиме «только чтение»")
	}

	read := r.extCmdHandler(ext, ExtTool{Name: "reader", Command: "echo read-ok", ReadOnly: true})
	res, err = read(context.Background(), nil)
	if err != nil {
		t.Fatalf("read-only расширение не выполнилось: %v", err)
	}
	if res.Error != "" || !strings.Contains(res.Text, "read-ok") {
		t.Errorf("read-only расширение заблокировано: %+v", res)
	}
}

// TestRegisterExtReportsCollision — второй результат registerExt должен
// честно говорить, зарегистрирован ли инструмент: по нему считается
// количество подключённых, и раньше счётчик врал при коллизии имён.
func TestRegisterExtReportsCollision(t *testing.T) {
	r := New(Env{WorkDir: t.TempDir()})
	if _, ok, _ := r.registerExt("a", "dup", "", "{}", "ext", nil); !ok {
		t.Fatal("первый инструмент не зарегистрирован")
	}
	// Коллизия «dup» занимает запасное имя x_dup.
	if _, ok, _ := r.registerExt("b", "dup", "", "{}", "ext", nil); !ok {
		t.Fatal("запасное имя x_dup не зарегистрировано")
	}
	_, ok, warns := r.registerExt("c", "dup", "", "{}", "ext", nil)
	if ok {
		t.Error("третий инструмент с тем же именем зарегистрирован, хотя оба имени заняты")
	}
	if len(warns) == 0 {
		t.Error("пропуск коллизии не отражён предупреждением")
	}
}

// TestExtCmdDefaultTimeout — расширение без timeout_sec живёт обещанные
// 60 секунд, а не 5: Clamp(0,…)=5 делал ветку дефолта мёртвой, и любую
// долгую команду (go build, npm install) убивало сразу после запуска.
func TestExtCmdDefaultTimeout(t *testing.T) {
	r := New(Env{WorkDir: t.TempDir()})
	ext := Extension{Name: "slow"}
	h := r.extCmdHandler(ext, ExtTool{Name: "sleeper", Command: "sleep 6; echo дожил"})
	res, err := h(context.Background(), nil)
	if err != nil {
		t.Fatalf("выполнение сорвалось: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("команда убита по таймауту вместо дефолтных 60s: %q", res.Error)
	}
	if !strings.Contains(res.Text, "дожил") {
		t.Errorf("вывод потерян: %q", res.Text)
	}
}

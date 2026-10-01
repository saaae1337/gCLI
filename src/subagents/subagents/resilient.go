package subagents

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gcli/core"
)

// ---------- Прочность субагентов ----------
//
// Наблюдение из собственной работы: параллельный запуск трёх субагентов
// дважды упал — два получили «stream error: STREAM_CLOSED; received from
// peer», один вернул обрывок вместо отчёта. Причина системная:
//
//   - главный агент переживает такие обрывы (в agent.callModel есть повтор
//     при providers.IsRetryable), а субагент — нет: тот же вызов идёт из
//     agent.RunSubagent, но без ретрая, и ошибка уходит наверх как есть;
//   - «лимит итераций» и «обрыв сети» обрабатывались одинаково — а
//     при обрыве сети финальные ходы (agent.finalize) не запускались вовсе,
//     потому что цикл падал раньше, чем набирался лимит.
//
// Оба дефекта закрыты здесь: ретрай транзиентных обрывов и принудительная
// добивка отчёта, если он пустой, обрезанный или оказался мыслью вслух.

// transientMarkers — признаки временного сбоя инфраструктуры.
var transientMarkers = []string{
	"stream_closed",
	"stream error",
	"stream id",
	"connection reset",
	"connection refused",
	"connection closed",
	"broken pipe",
	"unexpected eof",
	"io.eof",
	"eof",
	"http2:",
	"goaway",
	"server closed idle",
	"timeout",
	"deadline exceeded",
	"temporarily unavailable",
	"try again",
	"rate limit",
	"too many requests",
	"429",
	"500",
	"502",
	"503",
	"504",
	"gateway",
	"bad gateway",
	"service unavailable",
	"overloaded",
	"capacity",
	"tls handshake",
	"no such host",
	"i/o timeout",
}

// IsTransient — временный ли это сбой (сеть, перегрузка провайдера).
//
// Проверка строковая, а не по типу ошибки: ошибки приходят от разных
// провайдеров и транспортов, и половина приходит в обёртке retryErr,
// половина — строкой из декодера SSE. Ложное срабатывание стоит одного
// лишнего повтора, ложное отсутствие — потерянного отчёта.
func IsTransient(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false // пользователь отменил — повтор неуместен
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	s := strings.ToLower(err.Error())
	for _, m := range transientMarkers {
		if strings.Contains(s, m) {
			return true
		}
	}
	return false
}

// Quality — качество отчёта субагента.
type Quality int

const (
	// ReportOK — отчёт пригоден.
	ReportOK Quality = iota
	// ReportEmpty — отчёта нет вовсе.
	ReportEmpty
	// ReportNarrative — вместо отчёта модель оставила мысль вслух
	// («сейчас ещё посмотрю…»). Для главного агента это шум.
	ReportNarrative
	// ReportTruncated — отчёт оборвался на полуслове (нет завершения).
	ReportTruncated
	// ReportNoSections — отчёт читаемый, но без обязательных секций формата.
	// Для главного агента это почти то же, что обрывок: непонятно, что
	// сделано, что осталось и что вообще проверялось.
	ReportNoSections
	// ReportUnverified — форма в порядке, но утверждения не подтверждены
	// журналом инструментов: субагент сослался на то, чего не открывал.
	ReportUnverified
)

// AssessReport — оценить пригодность отчёта.
//
// Детекторы намеренно осторожны: бросить нормальный отчёт дороже, чем
// пропустить сомнительный, поэтому спорные случаи трактуются как OK.
func AssessReport(full string) Quality {
	t := strings.TrimSpace(full)
	if t == "" || t == "(пусто)" {
		return ReportEmpty
	}
	// Явная реплика о неудаче — это честный отчёт, а не пустой.
	if hasFailureMarker(t) {
		return ReportOK
	}
	lines := core.SplitLines(t)
	if len(lines) == 1 && len([]rune(t)) < 160 && !hasStructure(t) {
		return ReportNarrative
	}
	if !endsSettled(t) {
		return ReportTruncated
	}
	return ReportOK
}

// hasFailureMarker — отчёт прямо сообщает о сбое.
func hasFailureMarker(s string) bool {
	l := strings.ToLower(s)
	for _, m := range []string{
		"не смог", "не удалось", "прерван", "ошибка", "сбой",
		"не вернул", "закончил работу без результата", "таймаут",
	} {
		if strings.Contains(l, m) {
			return true
		}
	}
	return false
}

// hasStructure — есть ли признаки структурированного отчёта.
func hasStructure(s string) bool {
	for _, l := range core.SplitLines(s) {
		l = strings.TrimSpace(l)
		switch {
		case strings.HasPrefix(l, "#"),
			strings.HasPrefix(l, "- "),
			strings.HasPrefix(l, "* "),
			strings.HasPrefix(l, "• "),
			strings.HasPrefix(l, "1."), strings.HasPrefix(l, "2."):
			return true
		}
	}
	return false
}

// unfinishedTail — хвост, характерный для мысли на полуслове.
var unfinishedTail = []string{
	"сейчас", "далее", "теперь", "затем", "потом", "дальше",
	"посмотрю", "проверю", "сделаю", "начну", "попробую",
	"let me", "now i", "next,", "i'll",
}

// endsSettled — выглядит ли отчёт завершённым.
//
// Незавершённый хвост («...сейчас ещё посмотрю») — главный источник
// бесполезных отчётов: лимит итераций обрывал цикл ровно на такой фразе.
func endsSettled(s string) bool {
	lines := core.SplitLines(s)
	if len(lines) == 0 {
		return false
	}
	tail := strings.ToLower(strings.TrimSpace(lines[len(lines)-1]))
	if tail == "" {
		tail = strings.ToLower(strings.TrimSpace(lines[len(lines)-2]))
	}
	if tail == "" {
		return false
	}
	// Последняя строка не должна быть ударной фразой о намерении.
	for _, u := range unfinishedTail {
		if strings.HasSuffix(tail, u) || strings.Contains(tail, u+" ") && len([]rune(tail)) < 90 {
			// «сделаю» в середине отчёта — нормально; в самом конце — нет.
			if strings.HasSuffix(tail, u) || strings.HasSuffix(tail, u+",") ||
				strings.HasSuffix(tail, u+".") || strings.HasSuffix(tail, u+"…") {
				return false
			}
		}
	}
	// Оборванное многоточием без завершённого знака.
	if strings.HasSuffix(tail, "...") || strings.HasSuffix(tail, "…") {
		return false
	}
	return true
}

// QualityText — объяснение проблемы, которое уходит в промпт добивки.
func QualityText(q Quality) string {
	switch q {
	case ReportEmpty:
		return "Предыдущая попытка закончилась без отчёта."
	case ReportNarrative:
		return "Предыдущая попытка закончилась мыслью вслух, а не отчётом."
	case ReportTruncated:
		return "Предыдущая попытка оборвалась на полуслове."
	case ReportNoSections:
		return "Предыдущая попытка дала текст без обязательных секций отчёта."
	case ReportUnverified:
		return "Предыдущий отчёт не подтвердился: часть ссылок субагент не открывал."
	}
	return ""
}

// RetryHint — добавка к задаче при повторной попытке.
func RetryHint(err error, q Quality) string {
	return RetryHintAudit(err, q, nil)
}

// RetryHintAudit — RetryHint плюс адресный список неподтверждённых мест.
//
// Отдельная функция, потому что слой прочности видит вердикт проверки, а не сам журнал: подсказка «открой pool.go:39» стоит
// больше, чем общие слова «пиши аккуратнее». Без списка модель не догадается,
// в чём именно ошибкась, и повтор воспроизводит тот же текст.
func RetryHintAudit(err error, q Quality, audit *GroundingReport) string {
	var b strings.Builder
	b.WriteString("\n\n[Система] Это повторная попытка. ")
	if err != nil {
		fmt.Fprintf(&b, "Предыдущая попытка упала: %s.\n", core.Truncate(err.Error(), 200))
	}
	if t := QualityText(q); t != "" {
		b.WriteString(t + "\n")
	}
	b.WriteString("Что делать сейчас:\n")
	b.WriteString("1. Не начинай исследование заново — используй то, что уже есть в твоей истории.\n")
	b.WriteString("2. Больше не расширяй задачу: нужен только ответ на исходный вопрос.\n")
	b.WriteString("3. Пиши отчёт сразу: конкретно, по фактам, со ссылками на файлы и строки.\n")
	b.WriteString("4. Отчёт обязан быть самодостаточным: заказчик не видит твоего контекста.\n")
	b.WriteString("5. Если часть работы не удалась — так и напиши, что именно не вышло, и не выдавай это за успех.")
	b.WriteString(GroundHint(audit))
	return b.String()
}

// Resilience — настройки прочности.
type Resilience struct {
	// Attempts — сколько попыток на запуск (1 = без ретрая).
	Attempts int
	// Backoff — пауза перед повтором; растёт с номером попытки.
	Backoff time.Duration
	// MaxBackoff — потолок паузы.
	MaxBackoff time.Duration
}

// DefaultResilience — настройки по умолчанию.
func DefaultResilience() Resilience {
	return Resilience{Attempts: 2, Backoff: 2 * time.Second, MaxBackoff: 8 * time.Second}
}

// Wrap — обернуть Runner в слой прочности.
//
// Повтор делается только когда это имеет смысл: сбой инфраструктуры
// (IsTransient) либо негодный отчёт (AssessReport). Отмена пользователем,
// нехватка прав и пул — не повторяются: там повтор просто съедал бы время.
func Wrap(r Runner, rs Resilience) Runner {
	if r == nil || rs.Attempts <= 1 {
		return r
	}
	backoff := rs.Backoff
	if backoff <= 0 {
		backoff = 2 * time.Second
	}
	maxBackoff := rs.MaxBackoff
	if maxBackoff <= 0 {
		maxBackoff = 8 * time.Second
	}
	return func(ctx context.Context, spec Spec) (Outcome, error) {
		var lastErr error
		var lastQ Quality = ReportOK
		// lastOut — результат последней попытки. После исчерпания попыток
		// возвращаем именно его: даже негодный отчёт полезен главному
		// агенту, который сам разберётся, что делать дальше.
		var lastOut Outcome

		for attempt := 1; attempt <= rs.Attempts; attempt++ {
			if err := ctx.Err(); err != nil {
				if lastErr != nil {
					return Outcome{}, lastErr
				}
				return Outcome{}, err
			}

			cur := spec
			if attempt > 1 {
				// lastOut, а не out: подсказка составляется по результату
				// ПРЕДЫДУЩЕЙ попытки, а текущий out появится только ниже.
				cur.RetryHint = RetryHintAudit(lastErr, lastQ, lastOut.Audit)
			}

			out, err := r(ctx, cur)
			lastOut = out

			if err == nil {
				q := JudgeReport(spec, out)
				if q == ReportOK {
					out.Retries = attempt - 1
					return out, nil
				}
				lastQ, lastErr = q, nil
			} else {
				lastErr = err
				lastQ = ReportOK
				if !IsTransient(err) {
					// Не временный сбой — повтор не поможет.
					out.Retries = attempt - 1
					return out, err
				}
			}

			// Между попытками — пауза с экспоненциальным ростом.
			if attempt < rs.Attempts {
				d := backoff << uint(attempt-1)
				if d > maxBackoff {
					d = maxBackoff
				}
				timer := time.NewTimer(d)
				select {
				case <-ctx.Done():
					timer.Stop()
				case <-timer.C:
				}
				// Отмена пользователем не повторяется: выходим наружу сразу.
				if ctx.Err() != nil {
					return Outcome{}, firstErr(lastErr, ctx.Err())
				}
			}
		}

		// Попытки исчерпаны. Дополнительного вызова раннера здесь НЕТ:
		// он был бы девятым «повтором», который не считается, платится
		// провайдеру и делает поле Retries бессмысленным. Вместо этого
		// возвращаем результат последней попытки, помечая его как
		// негодный: главный агент должен понимать, что произошло, иначе
		// он решит, будто субагент молчал.
		out := lastOut
		out.Retries = rs.Attempts - 1
		if out.Full == "" {
			out.Full = degradedReport(out.Full, spec, nil, lastErr, ReportEmpty, rs.Attempts)
			return out, firstErr(lastErr, errors.New("субагент не вернул отчёт после всех попыток"))
		}
		out.Full = degradedReport(out.Full, spec, lastErr, nil, JudgeReport(spec, out), rs.Attempts)
		return out, lastErr
	}
}

// firstErr — первая непустая ошибка из списка (для отчёта после отмены).
func firstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

// pluralAttempts — «2 попытки», «5 попыток», «1 попытка».
//
// Отдельная функция, а не условие на месте: этот текст читает и человек, и
// модель, а опечатка в объяснении сбоя стоит дороже трёх строк кода.
func pluralAttempts(n int) string {
	switch {
	case n%10 == 1 && n%100 != 11:
		return fmt.Sprintf("%d попытка", n)
	case n%10 >= 2 && n%10 <= 4 && (n%100 < 12 || n%100 > 14):
		return fmt.Sprintf("%d попытки", n)
	default:
		return fmt.Sprintf("%d попыток", n)
	}
}

// degradedReport — собрать внятный отчёт вместо пустоты.
func degradedReport(full string, spec Spec, err, lastErr error, q Quality, attempts int) string {
	var b strings.Builder
	if strings.TrimSpace(full) != "" {
		b.WriteString(strings.TrimSpace(full))
		b.WriteString("\n\n---\n")
	}
	// Согласование обязательно: «после 2 попыток» читается как опечатка,
	// а этот текст главный агент показывает пользователю как объяснение,
	// почему субагент не справился.
	fmt.Fprintf(&b, "⚠ Отчёт неполный: субагент «%s» не смог сдать результат после %s.\n",
		spec.Type.Label(), pluralAttempts(attempts))
	if err != nil {
		fmt.Fprintf(&b, "Причина: %s\n", core.Truncate(err.Error(), 300))
	} else if lastErr != nil {
		fmt.Fprintf(&b, "Причина: %s\n", core.Truncate(lastErr.Error(), 300))
	} else if t := QualityText(q); t != "" {
		fmt.Fprintf(&b, "Причина: %s\n", t)
	}
	b.WriteString("Что делать главному агенту: не полагаться на этот результат — либо поставить задачу заново " +
		"с более узкой формулировкой и меньшим числом шагов, либо выполнить работу самому.")
	return b.String()
}

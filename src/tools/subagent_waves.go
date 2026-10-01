package tools

import (
	"context"
	"fmt"
	"strings"

	"gcli/subagents"
)

// ---------- Запуск пачки по плану зависимостей ----------
//
// runBatch умеет «много сразу». runSpawnWaves умеет «сначала это, потом то, что
// от этого зависит» — и то и другое в одном вызове spawn_agents.
//
// Три вещи, которые здесь нельзя упростить:
//
//  1. Узел, чья зависимость не выполнилась, не запускается вовсе. Молча
//     отдать ему пустой ввод — значит оплатить минуту работы модели, чтобы
//     услышать «не знаю».
//  2. Пропуск каскадом: если реализация не удалась, тесты после неё тоже не
//     имеют смысла, а ревью кода, которого нет, — тем более.
//  3. Вывод зависимости подставляется в задачу следующего субагента. Иначе
//     зависимость была бы чистой формальностью: «дождись плана» без самого
//     плана означало бы ждать и не знать.

// spawnDepsBudget — сколько символов выводов зависимостей уходит в задачу
// одного субагента.
//
// Ограничение по контексту, а не по памяти: пара отчётов по 2000 символов —
// это уже заметная часть окна субагента, и начиная с этого момента он
// перестаёт читать собственный код в пользу чужих слов. Обрезанный вывод
// остаётся полезным (начало и суть), а нехватка видна по обрыву.
const spawnDepsBudget = 2000

// depsBlockHeader — шапка блока входных данных.
//
// Формулировка намеренно даёт субагенту право ответить «мне не хватает»:
// зависимость передаёт ЧУЖИЕ выводы, и модель, решившая, что их достаточно,
// выдумает недостающее. Явное разрешение сказать «не хватает» дешевле правки
// на основе выдуманного входа.
const depsBlockHeader = "[Выводы субагентов, которых ты ждёшь — это входные данные для твоей задачи. " +
	"Если их не хватает или они противоречат друг другу, скажи об этом прямо, а не додумывай.]\n"

// spawnSpec — узел пачки субагентов до развёртки в волны.
type spawnSpec struct {
	// label — подпись в отчёте: имя, либо «роль#номер», либо «auto#номер».
	label string
	// depsIdx — индексы узлов, от которых этот зависит (из DAG). Заполняется
	// планировщиком, чтобы запуск и отчёт о пропусках смотрели в один список.
	depsIdx []int
	// run — запуск с подставленным блоком выводов зависимостей.
	run func(ctx context.Context, depsText string) (Result, error)
}

// plainTargets — цели для пачки без зависимостей.
func plainTargets(specs []spawnSpec) []target {
	out := make([]target, 0, len(specs))
	for _, s := range specs {
		s := s
		out = append(out, target{label: s.label, run: func(ctx context.Context) (Result, error) {
			return s.run(ctx, "")
		}})
	}
	return out
}

// runSpawnWaves — выполнить пачку волнами.
//
// Контекст общий для всех волен: истёк он — не запускается ни одна следующая
// волна, а её узлы попадают в отчёт как пропущенные. Иначе вызов молчал бы
// после отмены, и модель решила бы, что субагенты потерялись.
func (r *Registry) runSpawnWaves(ctx context.Context, dag *subagents.DAG, specs []spawnSpec, par int) (Result, error) {
	items := make([]batchItem, len(specs))
	// ok — узел отработал (в том числе «с ошибкой»): его вывод можно брать как
	// входные данные для следующей волны. Ошибка здесь не повод пропускать
	// зависимых: частичный отчёт полезнее, а пустой вход вреден.
	ok := make([]bool, len(specs))
	skipped := make([]bool, len(specs))
	for i := range specs {
		items[i].label = specs[i].label
	}

	for w, wave := range dag.Waves {
		// Пропуск и отмена разбираются ДО сборки целей: заведённую цель
		// нельзя «выполнить», её можно только не запускать.
		var targets []target
		// idx[k] — узел пачки, которому соответствует targets[k].
		//
		// Сопоставлять по номеру места в волне нельзя: в targets попадают не все
		// узлы волны (пропущенные при отмене или блокировке), и тогда отчёт
		// узла «tests» уехал бы в секцию узла «rev» — молча и неверно. Карта
		// строится при сборке целей и живёт ровно столько, сколько целей.
		var idx []int
		for _, i := range wave {
			if ctx.Err() != nil {
				skipped[i] = true
				items[i].warn = "отменено: " + ctx.Err().Error()
				continue
			}
			if blocked, why := depsFailed(i, specs, ok, skipped); blocked {
				skipped[i] = true
				items[i].warn = why
				continue
			}
			s := specs[i]
			depsText := depsBlock(i, specs, ok, items)
			targets = append(targets, target{label: s.label, run: func(ctx context.Context) (Result, error) {
				return s.run(ctx, depsText)
			}})
			idx = append(idx, i)
		}
		if len(targets) == 0 {
			continue
		}
		waveItems, err := r.runTargets(ctx, "spawn_agents", par, targets)
		if err != nil {
			return Result{}, err
		}
		for k, i := range idx {
			items[i] = waveItems[k]
			items[i].label = specs[i].label
			// Узел отработал — даже «с ошибкой»: его вывод всё ещё полезен как
			// вход для следующей волны (см. depsFailed).
			ok[i] = true
		}
		// Сводка по волне видна пользователю сразу: при трёх волнах иначе он
		// смотрит в пустоту до самого конца и не понимает, идёт ли дело.
		r.progress(ProgressEvent{
			Title: "spawn_agents",
			Label: fmt.Sprintf("волна %d/%d — %s", w+1, len(dag.Waves),
				waveSummary(wave, items, skipped)),
			Done:  0,
			Total: len(specs),
			Ok:    true,
		})
	}
	text, summary := renderSpawnWaves(dag, specs, items, skipped)
	r.progress(ProgressEvent{
		Title: "spawn_agents", Label: summary,
		Done: len(specs), Total: len(specs), Ok: true, Final: true,
	})
	return Result{Text: text, Summary: summary}, nil
}

// waveSummary — короткая подпись завершённой волны.
func waveSummary(wave []int, items []batchItem, skipped []bool) string {
	done, fail := 0, 0
	for _, i := range wave {
		switch {
		case skipped[i]:
		case items[i].Failed(), strings.TrimSpace(items[i].warn) != "":
			fail++
		default:
			done++
		}
	}
	if fail > 0 {
		return fmt.Sprintf("%d готовы, %d с ошибкой", done, fail)
	}
	return fmt.Sprintf("%d готовы", done)
}

// depsFailed — в порядке ли запущены зависимости узла.
//
// Ошибка зависимости НЕ блокирует запуск: частичный отчёт всё равно полезен,
// а пустой вход вреден — субагент выдумает недостающее. Блокирует только то,
// что не выполнено по-настоящему: пропущенный узел или отменённый вызов.
func depsFailed(i int, specs []spawnSpec, ok, skipped []bool) (bool, string) {
	var bad []string
	for _, j := range specs[i].depsIdx {
		if ok[j] {
			continue
		}
		bad = append(bad, specs[j].label+" — не выполнен")
	}
	if len(bad) == 0 {
		return false, ""
	}
	return true, "зависимость не выполнена: " + strings.Join(bad, "; ")
}

// depsBlock — собрать вход для субагента: выводы тех, кого он ждёт.
//
// Бюджет общий на узел, а не на каждую зависимость: три отчёта по 2000 символов
// — это уже половина окна субагента. Обрезка идёт по остатку бюджета, иначе
// переполнение окна заметил бы только следующий запрос к модели.
//
// Шапка входит в бюджет, а не добавляется сверху: иначе «лимит» перестал бы быть
// пределом на блок входа и подсчёт сходился бы с фактом на единицы символов.
func depsBlock(i int, specs []spawnSpec, ok []bool, items []batchItem) string {
	if len(specs[i].depsIdx) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(depsBlockHeader)
	used := len([]rune(depsBlockHeader))
	for _, j := range specs[i].depsIdx {
		if used >= spawnDepsBudget {
			break
		}
		if !ok[j] {
			continue
		}
		text := strings.TrimSpace(items[j].res.Text)
		if text == "" {
			continue
		}
		chunk := coreTruncate(text, spawnDepsBudget-used)
		fmt.Fprintf(&b, "\n--- %s ---\n%s\n", specs[j].label, chunk)
		used += len([]rune(chunk))
	}
	if used == len([]rune(depsBlockHeader)) {
		return ""
	}
	return b.String()
}

// renderSpawnWaves — итоговый отчёт пачки с зависимостями.
//
// Порядок разделов — порядок аргументов, а не порядок запуска: к концу пачки
// порядок волн уже ничего не сообщает, а номер сопоставляет отчёт с задачей из
// задания.
func renderSpawnWaves(dag *subagents.DAG, specs []spawnSpec, items []batchItem, skipped []bool) (string, string) {
	var b strings.Builder
	ready, failed, miss := 0, 0, 0
	if dag.WaveCount() > 1 {
		b.WriteString(fmt.Sprintf("# spawn_agents: %d субагентов, %d волн зависимостей\n\n",
			len(specs), dag.WaveCount()))
	} else {
		b.WriteString(fmt.Sprintf("# spawn_agents: %d субагентов\n\n", len(specs)))
	}
	for i, s := range specs {
		it := items[i]
		n := i + 1
		switch {
		case skipped[i]:
			miss++
			fmt.Fprintf(&b, "## [%d] %s\nПРОПУЩЕН — %s\n\n", n, s.label, it.warn)
		case it.Failed():
			failed++
			fmt.Fprintf(&b, "## [%d] %s\nОШИБКА: %v\n\n", n, s.label, it.err)
		case strings.TrimSpace(it.warn) != "":
			failed++
			fmt.Fprintf(&b, "## [%d] %s\n%s\n\n", n, s.label, it.warn)
		default:
			ready++
			fmt.Fprintf(&b, "## [%d] %s\n", n, s.label)
			fmt.Fprintf(&b, "%s\n\n", it.res.Text)
		}
	}
	summary := fmt.Sprintf("%d готовы, %d с ошибкой", ready, failed)
	if miss > 0 {
		summary = fmt.Sprintf("%d готовы, %d с ошибкой, %d пропущено", ready, failed, miss)
	}
	return strings.TrimRight(b.String(), "\n"), summary
}

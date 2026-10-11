# Примеры gcli

Готовые файлы, которые можно скопировать и пользоваться. Ничего
устанавливать не нужно: gcli читает их сам, как только они лежат в
правильном месте.

## Как пользоваться

Копируйте в каталог проекта (только для этого проекта) или в
`~/.gcli/` (для всех проектов). Проектные файлы перекрывают
пользовательские с тем же именем.

```bash
cp examples/skills/code-reviewer.md .gcli/skills/
cp examples/commands/commit.md        .gcli/commands/
cp examples/extensions/dev-tools.json .gcli/extensions/
```

## Что здесь

| Файл | Куда класть | Что делает |
|---|---|---|
| [`skills/code-reviewer.md`](skills/code-reviewer.md) | `.gcli/skills/` | Ревью изменений: ищет баги, дыры в безопасности и места, где код станет хуже со временем |
| [`skills/commit-helper.md`](skills/commit-helper.md) | `.gcli/skills/` | Пишет сообщение коммита по изменениям в git, в стиле Conventional Commits |
| [`skills/project-onboarding.md`](skills/project-onboarding.md) | `.gcli/skills/` | Первое знакомство с незнакомым проектом: назначение, стек, точки входа, чем мерить |
| [`commands/commit.md`](commands/commit.md) | `.gcli/commands/` | Slash-команда `/commit` — сгруппировать изменения и написать коммит |
| [`commands/review.md`](commands/review.md) | `.gcli/commands/` | Slash-команда `/review` — разбор последних изменений |
| [`extensions/dev-tools.json`](extensions/dev-tools.json) | `.gcli/extensions/` | Инструменты на shell: часто повторяющиеся git- и файловые операции |
| [`extensions/web-api.json`](extensions/web-api.json) | `.gcli/extensions/` | Замена частых вызовов `curl` готовыми инструментами |
| [`github/review.yml`](github/review.yml) | `.github/workflows/` | Workflow: ревью каждого PR агентом, комментарий с разбором |
| [`github/mission.yml`](github/mission.yml) | `.github/workflows/` | Workflow: запуск миссии по кнопке, результат приезжает отдельным PR |

Подробнее про GitHub-интеграцию, включая ограничения длинных
прогонов, — в [github/README.md](github/README.md).

## Формат

- **Навык** — Markdown с front matter. Поле `description` объясняет, когда
  навык нужен; `when` работает как триггер; `$ARGUMENTS` подставляет
  аргументы вызова.
- **Команда** — Markdown с front matter. Имя команды берётся из имени
  файла: `commit.md` становится `/commit`.
- **Расширение** — JSON со списком инструментов и их схемами.

Все три формата описаны в разделе
[Расширяемость](../README.md#расширяемость) README.
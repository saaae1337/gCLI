package ui

// Sink — приёмник структурированного вывода для полноэкранного интерфейса.
//
// Обычный UI печатает ANSI-поток в терминал. Новый интерфейс (пакет tui)
// владеет экраном сам, поэтому ему нужны не байты, а события: текст,
// карточка инструмента, границы ответа. Все методы вызываются из горутины
// агента и обязаны быть потокобезопасными.
type Sink interface {
	// Text — обычный текст (может содержать ANSI-разметку старых панелей).
	Text(s string)
	// BeginStream/EndStream — границы ответа модели.
	BeginStream()
	EndStream()
	// SetMeta — подпись под последним ответом (время, токены).
	SetMeta(s string)
	// BeginThink/ThinkText/EndThink — размышления модели.
	BeginThink()
	ThinkText(s string)
	EndThink()
	// ToolStart/ToolEnd — карточка инструмента.
	ToolStart(tc ToolCall)
	ToolEnd(tc ToolCall)
	// SetMetrics — нижняя панель расхода.
	SetMetrics(m Metrics)
	// Clear — очистить ленту.
	Clear()
}

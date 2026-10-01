package core

import "strings"

// ---------- Цены моделей ----------

// Цены указаны в долларах за миллион токенов (ввод/вывод) и нужны
// только для дашборда: показать, во сколько обошлась работа. Если цены
// для модели неизвестны, показывается прочерк, а не выдуманное число.
//
// Значения приблизительные и регулярно меняются у провайдеров —
// это ориентир, а не счёт от провайдера.

// modelPrice — цена одной конкретной модели.
type modelPrice struct {
	price Price
}

// priceTable — цены по моделям, сгруппированные по вендору.
// Ключи в нижнем регистре, имя модели сравнивается по вхождению.
var priceTable = map[string]map[string]Price{
	"zai": {
		"glm-4.6":       {In: 0.60, Out: 2.20},
		"glm-4.5-air":   {In: 0.20, Out: 1.10},
		"glm-4.5-flash": {In: 0.10, Out: 0.40},
		"glm-4.5":       {In: 0.60, Out: 2.20},
	},
	"openrouter": {
		// там же, что у вендора, с наценкой ~5%
		"glm-4.6":          {In: 0.65, Out: 2.30},
		"glm-4.5":          {In: 0.65, Out: 2.30},
		"claude-sonnet":    {In: 3.50, Out: 15.00},
		"claude-opus":      {In: 18.00, Out: 75.00},
		"claude-haiku":     {In: 1.00, Out: 5.00},
		"gpt-4o":           {In: 2.75, Out: 11.00},
		"gpt-4o-mini":      {In: 0.20, Out: 0.70},
		"gpt-5":            {In: 1.30, Out: 10.00},
		"gemini-2.0-flash": {In: 0.10, Out: 0.40},
		"gemini-2.5-pro":   {In: 1.25, Out: 10.00},
		"deepseek-chat":    {In: 0.28, Out: 0.42},
		"deepseek-r1":      {In: 0.55, Out: 2.19},
		"qwen3-coder":      {In: 0.30, Out: 1.20},
	},
	"openai": {
		"gpt-4o":       {In: 2.75, Out: 11.00},
		"gpt-4o-mini":  {In: 0.20, Out: 0.70},
		"gpt-4.1":      {In: 2.00, Out: 8.00},
		"gpt-4.1-mini": {In: 0.40, Out: 1.60},
		"o4-mini":      {In: 1.10, Out: 4.40},
	},
	"anthropic": {
		"claude-sonnet-4":   {In: 3.00, Out: 15.00},
		"claude-opus-4":     {In: 15.00, Out: 75.00},
		"claude-3-5-haiku":  {In: 0.80, Out: 4.00},
		"claude-3-5-sonnet": {In: 3.00, Out: 15.00},
	},
	"deepseek": {
		"deepseek-chat":     {In: 0.28, Out: 0.42},
		"deepseek-reasoner": {In: 0.55, Out: 2.19},
	},
	"groq": {
		"llama-3.3-70b": {In: 0.59, Out: 0.79},
		"llama-3.1-8b":  {In: 0.05, Out: 0.08},
	},
	"mistral": {
		"codestral": {In: 0.30, Out: 0.90},
		"devstral":  {In: 0.40, Out: 1.00},
		"large":     {In: 2.00, Out: 6.00},
	},
	"xai": {
		"grok-4":    {In: 3.00, Out: 15.00},
		"grok-3":    {In: 3.00, Out: 15.00},
		"grok-code": {In: 0.20, Out: 1.50},
	},
	"together": {
		"llama-3.3-70b": {In: 0.88, Out: 0.88},
		"qwen2.5-72b":   {In: 0.90, Out: 0.90},
		"deepseek-v3":   {In: 1.25, Out: 1.25},
	},
}

// lookupPrice — найти цену по id провайдера и имени модели.
func lookupPrice(providerID, model string) (Price, bool) {
	byModel, ok := priceTable[strings.ToLower(strings.TrimSpace(providerID))]
	if !ok {
		return Price{}, false
	}
	m := strings.ToLower(strings.TrimSpace(model))
	// Сначала точное совпадение, затем самый длинный подходящий префикс:
	// «z-ai/glm-4.6» должен найти цену «glm-4.6».
	if p, ok := byModel[m]; ok {
		return p, true
	}
	best, bestLen := Price{}, 0
	for key, p := range byModel {
		if strings.Contains(m, key) && len(key) > bestLen {
			best, bestLen = p, len(key)
		}
	}
	if bestLen == 0 {
		return Price{}, false
	}
	return best, true
}

// ModelPrice — цена модели для указанного провайдера.
// ok=false, если для этой модели цена неизвестна: показывать нечего.
func ModelPrice(providerID, model string) (Price, bool) {
	return lookupPrice(providerID, model)
}

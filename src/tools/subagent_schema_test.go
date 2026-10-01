package tools

import (
	"encoding/json"
	"testing"
)

// Схемы инструментов уходят в модель как есть: любая ошибка в JSON превращает
// инструмент в «неизвестный», и модель молча перестаёт им пользоваться.
func TestSchemaSpawnManyIsValidJSONWithDependsOn(t *testing.T) {
	var v map[string]any
	if err := json.Unmarshal([]byte(schemaSpawnMany), &v); err != nil {
		t.Fatalf("schemaSpawnMany не разбирается как JSON: %v", err)
	}
	agents, ok := v["properties"].(map[string]any)["agents"].(map[string]any)
	if !ok {
		t.Fatal("в схеме нет agents")
	}
	items, ok := agents["items"].(map[string]any)
	if !ok {
		t.Fatal("у agents нет items")
	}
	props, ok := items["properties"].(map[string]any)
	if !ok {
		t.Fatal("у элемента пачки нет properties")
	}
	dep, ok := props["depends_on"]
	if !ok {
		t.Fatal("в схеме нет depends_on — модель не узнает про зависимости")
	}
	// depends_on принимается и строкой, и массивом: модели пишут оба варианта,
	// а ArgStrSlice без строки вернул бы пусто и зависимость потерялась бы.
	types, ok := dep.(map[string]any)["type"].([]any)
	if !ok || len(types) != 2 {
		t.Errorf("тип depends_on = %v, ожидался [string, array]", dep.(map[string]any)["type"])
	}
}

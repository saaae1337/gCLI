package main

// Тесты MCP-сервера: логика методов — на чистых вызовах, NDJSON-фрейминг —
// через подменённый stdio. Хост-клиенты в тестах не участвуют.

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"gcli/core"
)

func TestMCPCallInitializeAndList(t *testing.T) {
	resp := handleMCPCall(mcpSrvRequest{JSONRPC: "2.0", ID: json.RawMessage("1"), Method: "initialize"})
	if resp.Error != nil {
		t.Fatalf("initialize не должен ошибаться: %s", resp.Error.Message)
	}
	if !strings.Contains(string(resp.Result), "gcli") {
		t.Fatalf("serverInfo: %s", string(resp.Result))
	}

	resp2 := handleMCPCall(mcpSrvRequest{JSONRPC: "2.0", ID: json.RawMessage("2"), Method: "tools/list"})
	var out struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(resp2.Result, &out); err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tl := range out.Tools {
		names[tl.Name] = true
	}
	for _, want := range []string{"gcli_prompt", "gcli_mission_status", "gcli_mission_report"} {
		if !names[want] {
			t.Fatalf("в tools/list нет %s: %v", want, names)
		}
	}
}

func TestMCPCallUnknownMethodAndTool(t *testing.T) {
	resp := handleMCPCall(mcpSrvRequest{JSONRPC: "2.0", ID: json.RawMessage("3"), Method: "bad/method"})
	if resp.Error == nil || resp.Error.Code != mcpSrvMethod {
		t.Fatalf("ждали -32601, получили %+v", resp.Error)
	}
	res, err := mcpSrvToolCall("нет_такого", json.RawMessage(`{}`))
	if err == nil || !strings.Contains(res, "") {
		t.Fatalf("неизвестный инструмент должен ошибаться: %q, %v", res, err)
	}
}

func TestMCPTaskMissionStatusEmpty(t *testing.T) {
	dir := t.TempDir()
	out, err := mcpSrvToolCall("gcli_mission_status", mustJSON(map[string]any{"workdir": dir}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "прогона нет") {
		t.Fatalf("пустой каталог: %q", out)
	}
	// С миссией.
	m := core.Mission{Objective: "починить тесты", Mode: core.MissionLongTime}
	m.Apply()
	if _, err := core.SaveMission(core.MissionPath(dir), m); err != nil {
		t.Fatal(err)
	}
	out2, err := mcpSrvToolCall("gcli_mission_status", mustJSON(map[string]any{"workdir": dir}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out2, "починить тесты") || !strings.Contains(out2, string(core.MissionLongTime)) {
		t.Fatalf("статус с миссией: %q", out2)
	}
}

func TestMCPServerNDJSONProtocol(t *testing.T) {
	rIn, wIn, _ := os.Pipe()
	rOut, wOut, _ := os.Pipe()

	done := make(chan struct{})
	go func() {
		runMCPServerStdio(rIn, wOut)
		close(done)
	}()

	lines := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`, // не отвечает
		`{"jsonrpc":"2.0","id":2,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/list"}`,
		`not json`, // битая строка → ответ с id=null
		`{"jsonrpc":"2.0","method":"exit"}`,
	}
	go func() {
		w := bufio.NewWriter(wIn)
		for _, l := range lines {
			w.WriteString(l + "\n")
			w.Flush()
		}
	}()

	// Ответы пишутся с немедленным flush; когда сервер вышел, закрываем
	// его stdout — иначе читатель висит на открытом пайпе.
	<-done
	_ = wOut.Close()

	sc := bufio.NewScanner(rOut)
	got := map[string]string{}
	for sc.Scan() {
		var resp struct {
			ID    *json.RawMessage `json:"id"`
			Error *struct {
				Code int `json:"code"`
			} `json:"error"`
		}
		if json.Unmarshal(sc.Bytes(), &resp) != nil {
			continue
		}
		key := "null"
		if resp.ID != nil {
			key = strings.Trim(string(*resp.ID), `"`)
		}
		got[key] = sc.Text()
	}
	if _, ok := got["1"]; !ok {
		t.Fatal("нет ответа на initialize")
	}
	if _, ok := got["2"]; !ok {
		t.Fatal("нет ответа на ping")
	}
	if _, ok := got["3"]; !ok {
		t.Fatal("нет ответа на tools/list")
	}
	if resp, ok := got["null"]; !ok || !strings.Contains(resp, "-32700") {
		t.Fatalf("битый JSON должен дать parse error: %q", resp)
	}
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

package main

// Тесты запуска прогона через HTTP. Агентский ход не зовётся: проверяется
// слой ответа — каким кодом и с каким телом эндпоинт отвечает на просьбу
// запустить прогон.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestServeMissionPostReportsFailure — POST /v1/mission обязан отличать
// «прогон запущен» от «прогон не запустился».
//
// missionStart печатал причину в терминал и ничего не возвращал, поэтому
// обработчик отвечал 200 в обоих случаях. Клиент, который не видит
// терминала (оболочка, десктоп, внешний инструмент по API), получал
// «всё хорошо» и payload со статусом idle — и не знал, что прогон
// не пошёл. Тишина вместо отказа: худший вид ответа для машины.
func TestServeMissionPostReportsFailure(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"пустое-тело", `{}`},
		{"пустой-текст", ``},
		{"без-режима-и-бюджета", `{"objective":"починить тесты"}`},
		{"неизвестный-режим", `{"mode":"вечность","objective":"починить тесты"}`},
		{"битый-json", `{"mode":`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := testApp(t)
			defer a.closeMissionJournal()
			s := &serveServer{a: a, token: "t", bus: newServeBus()}
			req := httptest.NewRequest("POST", "/v1/mission?token=t", strings.NewReader(c.body))
			rec := httptest.NewRecorder()
			s.mux().ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("ждали 400 с объяснением, получили %d: %s",
					rec.Code, rec.Body.String())
			}
			var resp map[string]string
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("не JSON: %v", err)
			}
			if strings.TrimSpace(resp["error"]) == "" {
				t.Fatalf("ответ без причины: клиент должен знать, что именно не так: %v", resp)
			}
		})
	}
}

// TestServeMissionPostStartsRun — удачный запуск обязан и отвечать 200, и
// действительно поднимать прогон. Проверка состояния, а не «функция не
// упала»: клиент ориентируется на payload.
func TestServeMissionPostStartsRun(t *testing.T) {
	a := testApp(t)
	defer a.closeMissionJournal()
	s := &serveServer{a: a, token: "t", bus: newServeBus()}
	req := httptest.NewRequest("POST", "/v1/mission?token=t",
		strings.NewReader(`{"mode":"long-time","objective":"починить тесты"}`))
	rec := httptest.NewRecorder()
	s.mux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("ждали 200, получили %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Mission struct {
			Objective string `json:"objective"`
			Active    bool   `json:"active"`
			Status    struct {
				State string `json:"state"`
			} `json:"status"`
		} `json:"mission"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("не JSON: %v", err)
	}
	if resp.Mission.Objective != "починить тесты" {
		t.Fatalf("цель не дошла до задания: %q", resp.Mission.Objective)
	}
	if !resp.Mission.Active || resp.Mission.Status.State != "running" {
		t.Fatalf("прогон не запустился, а ответ говорит об успехе: %s", rec.Body.String())
	}
}

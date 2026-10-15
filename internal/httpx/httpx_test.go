package httpx

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeJSON(t *testing.T) {
	type input struct {
		Name string `json:"name"`
	}
	tests := []struct {
		name       string
		body       string
		wantOK     bool
		wantStatus int
		wantError  string
	}{
		{"обычный объект", `{"name":"x"}`, true, 0, ""},
		{"пробелы после JSON — можно", "{\"name\":\"x\"}\n  ", true, 0, ""},
		{"сломанный JSON", `{`, false, 400, "неверный JSON"},
		{"неизвестное поле", `{"nmae":"x"}`, false, 400, `неизвестное поле \"nmae\"`},
		{"два объекта подряд", `{"name":"x"}{"name":"y"}`, false, 400, "лишние данные"},
		{"мусор после JSON", `{"name":"x"} abc`, false, 400, "лишние данные"},
		{"больше 1 МБ", `{"name":"` + strings.Repeat("a", MaxBodyBytes) + `"}`, false, 413, "больше 1 МБ"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			var in input
			ok := DecodeJSON(rec, httptest.NewRequest("POST", "/", strings.NewReader(tt.body)), &in)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, ожидали %v; тело: %s", ok, tt.wantOK, rec.Body)
			}
			if !ok && (rec.Code != tt.wantStatus || !strings.Contains(rec.Body.String(), tt.wantError)) {
				t.Errorf("статус %d, тело %s; ожидали %d и %q", rec.Code, rec.Body, tt.wantStatus, tt.wantError)
			}
		})
	}
}

func TestCheckText(t *testing.T) {
	s := "  Анна  "
	if msg := CheckText("name", &s, true, 10); msg != "" || s != "Анна" {
		t.Errorf("msg=%q s=%q", msg, s)
	}
	empty := "   "
	if msg := CheckText("name", &empty, true, 10); msg != "name обязателен" {
		t.Errorf("пустое: %q", msg)
	}
	if msg := CheckText("description", &empty, false, 10); msg != "" {
		t.Errorf("необязательное пустое: %q", msg)
	}
	// длина считается в символах, а не в байтах: 5 кириллических букв = 10 байт
	long := "Ааааа"
	if msg := CheckText("name", &long, true, 5); msg != "" {
		t.Errorf("5 символов при пределе 5: %q", msg)
	}
	long = "Аааааа"
	if msg := CheckText("name", &long, true, 5); msg != "name: не длиннее 5 символов" {
		t.Errorf("6 символов при пределе 5: %q", msg)
	}
}

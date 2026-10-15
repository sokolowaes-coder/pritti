package httpx

import (
	"net/http/httptest"
	"net/url"
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

func TestIsUUID(t *testing.T) {
	for _, s := range []string{
		"026252ca-c86a-11f1-97a7-345a603609cd",
		"01A140DC-0C34-7ABB-8D6C-36E2D1879E0D",
		"00000000-0000-0000-0000-000000000000",
	} {
		if !IsUUID(s) {
			t.Errorf("IsUUID(%q) = false", s)
		}
	}
	for _, s := range []string{
		"", "1", "abc",
		"026252ca-c86a-11f1-97a7-345a603609c",   // 35 символов
		"026252ca-c86a-11f1-97a7-345a603609cdd", // 37
		"026252cac86a-11f1-97a7-345a603609cd0",  // дефис не на месте
		"026252ca-c86a-11f1-97a7-345a603609cg",  // не hex
	} {
		if IsUUID(s) {
			t.Errorf("IsUUID(%q) = true", s)
		}
	}
}

func TestParsePage(t *testing.T) {
	tests := []struct {
		query   string
		want    Page
		wantErr string
	}{
		{"", Page{Limit: DefaultLimit}, ""},
		{"limit=5&offset=10", Page{Limit: 5, Offset: 10}, ""},
		{"limit=100", Page{Limit: 100}, ""},
		{"limit=101", Page{}, "limit"},
		{"limit=0", Page{}, "limit"},
		{"limit=abc", Page{}, "limit"},
		{"offset=-1", Page{}, "offset"},
		{"offset=abc", Page{}, "offset"},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			q, _ := url.ParseQuery(tt.query)
			got, err := ParsePage(q)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("ошибка = %v, ожидали про %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Errorf("получили %+v, %v; ожидали %+v", got, err, tt.want)
			}
		})
	}
}

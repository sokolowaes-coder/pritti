package httpx

import (
	"context"
	"log/slog"
	"maps"
	"net/http"
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
		{"неизвестное поле", `{"nmae":"x"}`, false, 400, `"code":"invalid_json","error":"неизвестное поле \"nmae\"`},
		{"два объекта подряд", `{"name":"x"}{"name":"y"}`, false, 400, "лишние данные"},
		{"мусор после JSON", `{"name":"x"} abc`, false, 400, "лишние данные"},
		{"больше 1 МБ", `{"name":"` + strings.Repeat("a", MaxBodyBytes) + `"}`, false, 413, `"code":"body_too_large"`},
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

func TestProblemsText(t *testing.T) {
	p := Problems{}
	s := "  Анна  "
	p.Text("name", &s, true, 10)
	if len(p) != 0 || s != "Анна" {
		t.Errorf("p=%v s=%q", p, s)
	}

	empty := "   "
	p.Text("name", &empty, true, 10)
	p.Text("description", &empty, false, 10)
	// длина считается в символах, а не в байтах: 5 кириллических букв = 10 байт
	five, six := "Ааааа", "Аааааа"
	p.Text("five", &five, true, 5)
	p.Text("six", &six, true, 5)
	want := Problems{"name": "обязательное поле", "six": "не длиннее 5 символов"}
	if !maps.Equal(p, want) {
		t.Errorf("получили %v, ожидали %v", p, want)
	}
}

func TestProblemsUUID(t *testing.T) {
	p := Problems{}
	ok, empty, bad := " 01A140DC-0C34-7ABB-8D6C-36E2D1879E0D ", "", "123"
	p.UUID("ok", &ok)
	p.UUID("empty", &empty)
	p.UUID("bad", &bad)
	if ok != "01a140dc-0c34-7abb-8d6c-36e2d1879e0d" {
		t.Errorf("UUID не приведён к виду в нижнем регистре: %q", ok)
	}
	want := Problems{"empty": "обязательное поле", "bad": "неверный id"}
	if !maps.Equal(p, want) {
		t.Errorf("получили %v, ожидали %v", p, want)
	}
}

func TestProblemsWrite(t *testing.T) {
	rec := httptest.NewRecorder()
	if (Problems{}).Write(rec) {
		t.Fatal("без ошибок Write должен вернуть false и ничего не писать")
	}

	p := Problems{}
	p.Add("phone", "неверный номер")
	p.Add("full_name", "обязательное поле")
	p.Add("full_name", "вторая ошибка того же поля не перезаписывает первую")
	if !p.Write(rec) || rec.Code != http.StatusBadRequest {
		t.Fatalf("статус = %d", rec.Code)
	}
	want := `{"success":false,"code":"validation_error","error":"full_name: обязательное поле; phone: неверный номер","fields":{"full_name":"обязательное поле","phone":"неверный номер"}}`
	if got := strings.TrimSpace(rec.Body.String()); got != want {
		t.Errorf("тело:\n%s\nожидали:\n%s", got, want)
	}
}

func TestLogger(t *testing.T) {
	if Logger(context.Background()) != slog.Default() {
		t.Error("без логгера в контексте ожидали slog.Default()")
	}
	l := slog.New(slog.DiscardHandler)
	if Logger(WithLogger(context.Background(), l)) != l {
		t.Error("не вернулся логгер из контекста")
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

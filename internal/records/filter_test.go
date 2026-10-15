package records

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestParseListFilter(t *testing.T) {
	ptr := func(s string) *string { return &s }
	day := func(s string) *time.Time {
		tm, err := time.Parse(time.RFC3339Nano, s)
		if err != nil {
			t.Fatal(err)
		}
		return &tm
	}

	tests := []struct {
		name    string
		query   string
		want    ListFilter
		wantErr string
	}{
		{name: "без фильтров", query: "", want: ListFilter{Limit: 20}},
		{
			name:  "fullText с пробелами обрезается",
			query: "fullText=+привет+",
			want:  ListFilter{FullText: ptr("привет"), Limit: 20},
		},
		{name: "пустой fullText игнорируется", query: "fullText=+++", want: ListFilter{Limit: 20}},
		{
			name:  "fullTextFields через запятую",
			query: "fullText=x&fullTextFields=name,description",
			want:  ListFilter{FullText: ptr("x"), FullTextFields: []string{"name", "description"}, Limit: 20},
		},
		{name: "неизвестное поле поиска", query: "fullTextFields=password", wantErr: "fullTextFields"},
		{
			name:  "id повторами и через запятую",
			query: "id=00000000-0000-0000-0000-000000000001&id=00000000-0000-0000-0000-000000000002,00000000-0000-0000-0000-000000000003",
			want:  ListFilter{IDs: []string{"00000000-0000-0000-0000-000000000001", "00000000-0000-0000-0000-000000000002", "00000000-0000-0000-0000-000000000003"}, Limit: 20},
		},
		{
			name:  "id в верхнем регистре приводится к нижнему",
			query: "id=0190A0B1-C2D3-7E4F-8A5B-6C7D8E9FA0B1",
			want:  ListFilter{IDs: []string{"0190a0b1-c2d3-7e4f-8a5b-6c7d8e9fa0b1"}, Limit: 20},
		},
		{name: "id не UUID", query: "id=abc", wantErr: "id"},
		{name: "id — число", query: "id=1", wantErr: "id"},
		{
			name:  "даты без времени: начало и конец дня",
			query: "dateStart=2026-09-27&dateEnd=2026-09-27",
			want: ListFilter{
				DateStart: day("2026-09-27T00:00:00Z"),
				DateEnd:   day("2026-09-27T23:59:59.999999999Z"),
				Limit:     20,
			},
		},
		{
			name:  "дата со временем, как в примере",
			query: "dateStart=2026-09-27T10:30:00",
			want:  ListFilter{DateStart: day("2026-09-27T10:30:00Z"), Limit: 20},
		},
		{
			name:  "RFC 3339 с поясом переводится в пояс сервера",
			query: "dateStart=2026-09-27T10:00:00%2B03:00",
			want:  ListFilter{DateStart: day("2026-09-27T07:00:00Z"), Limit: 20},
		},
		{
			name:  "мастер, клиент и статусы",
			query: "employeeId=00000000-0000-0000-0000-000000000001&clientId=00000000-0000-0000-0000-000000000002,00000000-0000-0000-0000-000000000003&status=scheduled,no_show",
			want: ListFilter{
				EmployeeIDs: []string{"00000000-0000-0000-0000-000000000001"},
				ClientIDs:   []string{"00000000-0000-0000-0000-000000000002", "00000000-0000-0000-0000-000000000003"},
				Statuses:    []string{"scheduled", "no_show"},
				Limit:       20,
			},
		},
		{name: "employeeId не UUID", query: "employeeId=1", wantErr: "employeeId"},
		{name: "clientId не UUID", query: "clientId=abc", wantErr: "clientId"},
		{name: "неизвестный статус", query: "status=done", wantErr: "status"},
		{name: "кривая дата", query: "dateStart=27.09.2026", wantErr: "dateStart"},
		{name: "начало позже конца", query: "dateStart=2026-09-28&dateEnd=2026-09-27", wantErr: "позже"},
		{name: "limit и offset", query: "limit=5&offset=10", want: ListFilter{Limit: 5, Offset: 10}},
		{name: "limit больше максимума", query: "limit=101", wantErr: "limit"},
		{name: "limit ноль", query: "limit=0", wantErr: "limit"},
		{name: "offset отрицательный", query: "offset=-1", wantErr: "offset"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q, err := url.ParseQuery(tt.query)
			if err != nil {
				t.Fatal(err)
			}
			got, err := ParseListFilter(q, time.UTC)

			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("ошибка = %v, ожидали с %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("неожиданная ошибка: %v", err)
			}
			if !equalFilter(got, tt.want) {
				t.Errorf("получили %s\nожидали  %s", dump(got), dump(tt.want))
			}
		})
	}
}

func equalFilter(a, b ListFilter) bool {
	return dump(a) == dump(b)
}

// dump печатает фильтр со значениями вместо указателей — для сравнения и вывода.
func dump(f ListFilter) string {
	s := "nil"
	if f.FullText != nil {
		s = *f.FullText
	}
	tm := func(p *time.Time) string {
		if p == nil {
			return "nil"
		}
		return p.Format(time.RFC3339Nano)
	}
	return fmt.Sprintf("fullText=%s fields=%v ids=%v employees=%v clients=%v statuses=%v start=%s end=%s limit=%d offset=%d",
		s, f.FullTextFields, f.IDs, f.EmployeeIDs, f.ClientIDs, f.Statuses, tm(f.DateStart), tm(f.DateEnd), f.Limit, f.Offset)
}

func TestParseListFilterLocalTimezone(t *testing.T) {
	msk, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		t.Fatal(err)
	}
	q, _ := url.ParseQuery("dateStart=2026-09-27&dateEnd=2026-09-27")
	f, err := ParseListFilter(q, msk)
	if err != nil {
		t.Fatal(err)
	}
	// 27.09 по Москве — это с 26.09 21:00 до 27.09 20:59:59 UTC.
	if got := f.DateStart.UTC().Format(time.RFC3339); got != "2026-09-26T21:00:00Z" {
		t.Errorf("dateStart в UTC = %s", got)
	}
	if got := f.DateEnd.UTC().Format(time.RFC3339); got != "2026-09-27T20:59:59Z" {
		t.Errorf("dateEnd в UTC = %s", got)
	}
	// В ответе дата — в местном времени и с поясом, чтобы её нельзя было понять двояко.
	if got := *formatDate(f.DateStart, msk); got != "2026-09-27T00:00:00+03:00" {
		t.Errorf("dateStart в ответе = %s", got)
	}
}

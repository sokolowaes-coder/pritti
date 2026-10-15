package records

import (
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"pritti/internal/httpx"
)

// localLayout — дата и время без пояса: такое время считается в поясе сервера (APP_TZ).
const localLayout = "2006-01-02T15:04:05"

// fullTextColumns — поля, по которым можно искать, и их колонки в БД.
// Только этот список попадает в SQL, поэтому подставить туда чужое нельзя.
var fullTextColumns = map[string]string{
	"name":        "name",
	"description": "description",
}

// ListFilter — фильтры и пагинация для списка записей. nil / пусто — фильтр не задан.
type ListFilter struct {
	FullText       *string
	FullTextFields []string // пусто — искать по всем полям из fullTextColumns
	IDs            []string
	EmployeeIDs    []string
	ClientIDs      []string
	Statuses       []string
	DateStart      *time.Time // starts_at >= DateStart
	DateEnd        *time.Time // starts_at <= DateEnd
	Limit          int
	Offset         int
}

// ParseListFilter читает фильтры из query. Даты без пояса считаются в loc (пояс сервера).
func ParseListFilter(q url.Values, loc *time.Location) (ListFilter, error) {
	page, err := httpx.ParsePage(q)
	if err != nil {
		return ListFilter{}, err
	}
	f := ListFilter{Limit: page.Limit, Offset: page.Offset}

	if v := strings.TrimSpace(q.Get("fullText")); v != "" {
		f.FullText = &v
	}
	for _, field := range splitList(q["fullTextFields"]) {
		if _, ok := fullTextColumns[field]; !ok {
			return f, fmt.Errorf("fullTextFields: неизвестное поле %q (можно: name, description)", field)
		}
		f.FullTextFields = append(f.FullTextFields, field)
	}

	if f.IDs, err = parseUUIDs("id", q); err != nil {
		return f, err
	}
	if f.EmployeeIDs, err = parseUUIDs("employeeId", q); err != nil {
		return f, err
	}
	if f.ClientIDs, err = parseUUIDs("clientId", q); err != nil {
		return f, err
	}
	for _, s := range splitList(q["status"]) {
		if !slices.Contains(statuses, s) {
			return f, fmt.Errorf("status: неизвестный статус %q (можно: %s)", s, strings.Join(statuses, ", "))
		}
		f.Statuses = append(f.Statuses, s)
	}

	if f.DateStart, err = parseDate(q.Get("dateStart"), false, loc); err != nil {
		return f, fmt.Errorf("dateStart: %w", err)
	}
	if f.DateEnd, err = parseDate(q.Get("dateEnd"), true, loc); err != nil {
		return f, fmt.Errorf("dateEnd: %w", err)
	}
	if f.DateStart != nil && f.DateEnd != nil && f.DateStart.After(*f.DateEnd) {
		return f, fmt.Errorf("dateStart позже dateEnd")
	}
	return f, nil
}

func parseUUIDs(param string, q url.Values) ([]string, error) {
	var ids []string
	for _, s := range splitList(q[param]) {
		if !httpx.IsUUID(s) {
			return nil, fmt.Errorf("%s: неверное значение %q", param, s)
		}
		ids = append(ids, strings.ToLower(s))
	}
	return ids, nil
}

// splitList собирает значения и из повторов (?id=1&id=2), и через запятую (?id=1,2).
func splitList(values []string) []string {
	var out []string
	for _, v := range values {
		for _, part := range strings.Split(v, ",") {
			if part = strings.TrimSpace(part); part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

// parseTime принимает RFC 3339 с поясом (2026-09-27T10:00:00+03:00)
// или время без пояса (2026-09-27T10:00:00) — тогда оно в поясе loc.
func parseTime(s string, loc *time.Location) (time.Time, bool) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.In(loc), true
	}
	if t, err := time.ParseInLocation(localLayout, s, loc); err == nil {
		return t, true
	}
	return time.Time{}, false
}

// parseDate — как parseTime, но принимает и дату без времени (2026-09-27).
// Для endOfDay такая дата означает конец дня: 2026-09-27T23:59:59.999999999.
func parseDate(s string, endOfDay bool, loc *time.Location) (*time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	if t, ok := parseTime(s, loc); ok {
		return &t, nil
	}
	if t, err := time.ParseInLocation(time.DateOnly, s, loc); err == nil {
		if endOfDay {
			t = t.AddDate(0, 0, 1).Add(-time.Nanosecond)
		}
		return &t, nil
	}
	return nil, fmt.Errorf("неверная дата %q (формат 2026-09-27, 2026-09-27T10:00:00 или 2026-09-27T10:00:00+03:00)", s)
}

// Ответ списка — в формате примера: success, filters (что применилось), count, data.

type ListResponse struct {
	Success    bool           `json:"success"`
	Filters    ListFiltersOut `json:"filters"`
	Count      int            `json:"count"` // сколько записей подходит под фильтры всего
	Pagination httpx.Page     `json:"pagination"`
	Data       []Record       `json:"data"`
}

type ListFiltersOut struct {
	FullText   FullTextFilter        `json:"fullText"`
	ID         FilterValue[[]string] `json:"id"`
	EmployeeID FilterValue[[]string] `json:"employeeId"`
	ClientID   FilterValue[[]string] `json:"clientId"`
	Status     FilterValue[[]string] `json:"status"`
	DateStart  FilterValue[*string]  `json:"dateStart"`
	DateEnd    FilterValue[*string]  `json:"dateEnd"`
}

type FilterValue[T any] struct {
	Value T `json:"value"`
}

type FullTextFilter struct {
	Value  *string  `json:"value"`
	Fields []string `json:"fields"`
}

func newListResponse(f ListFilter, records []Record, total int, loc *time.Location) ListResponse {
	return ListResponse{
		Success: true,
		Filters: ListFiltersOut{
			FullText:   FullTextFilter{Value: f.FullText, Fields: f.FullTextFields},
			ID:         FilterValue[[]string]{Value: f.IDs},
			EmployeeID: FilterValue[[]string]{Value: f.EmployeeIDs},
			ClientID:   FilterValue[[]string]{Value: f.ClientIDs},
			Status:     FilterValue[[]string]{Value: f.Statuses},
			DateStart:  FilterValue[*string]{Value: formatDate(f.DateStart, loc)},
			DateEnd:    FilterValue[*string]{Value: formatDate(f.DateEnd, loc)},
		},
		Count:      total,
		Pagination: httpx.Page{Limit: f.Limit, Offset: f.Offset},
		Data:       records,
	}
}

// formatDate показывает применённую дату в RFC 3339 с поясом — как и все даты в ответах API,
// чтобы клиенту не нужно было знать, в каком поясе работает сервер.
func formatDate(t *time.Time, loc *time.Location) *string {
	if t == nil {
		return nil
	}
	s := t.In(loc).Format(time.RFC3339)
	return &s
}

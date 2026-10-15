package records

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"pritti/internal/employees"
)

// hm — 20.10.2026 (вторник) в UTC: hm(10, 30) — 10:30.
func hm(hour, minute int) time.Time { return time.Date(2026, 10, 20, hour, minute, 0, 0, time.UTC) }

// starts печатает начала окон: [10:00 10:15 …].
func starts(slots []Interval) string {
	var out []string
	for _, s := range slots {
		out = append(out, s.StartsAt.Format("15:04"))
	}
	return fmt.Sprint(out)
}

func TestFreeSlots(t *testing.T) {
	busy := func(from, to time.Time) Interval { return Interval{StartsAt: from, EndsAt: to} }
	tests := []struct {
		name      string
		from, to  time.Time
		busy      []Interval
		d, step   time.Duration
		notBefore time.Time
		want      string
	}{
		{"свободный день, час, шаг час", hm(10, 0), hm(13, 0), nil, time.Hour, time.Hour, time.Time{}, "[10:00 11:00 12:00]"},
		{"последнее окно заканчивается ровно в конце дня", hm(10, 0), hm(11, 0), nil, 30 * time.Minute, 15 * time.Minute, time.Time{}, "[10:00 10:15 10:30]"},
		{"окно длиннее дня", hm(10, 0), hm(10, 30), nil, time.Hour, 15 * time.Minute, time.Time{}, "[]"},
		{"запись посреди дня", hm(10, 0), hm(13, 0), []Interval{busy(hm(11, 0), hm(12, 0))}, time.Hour, 30 * time.Minute, time.Time{}, "[10:00 12:00]"},
		{"впритык к записи — можно", hm(10, 0), hm(12, 0), []Interval{busy(hm(11, 0), hm(12, 0))}, 30 * time.Minute, 30 * time.Minute, time.Time{}, "[10:00 10:30]"},
		{"запись, начатая до рабочего дня", hm(10, 0), hm(12, 0), []Interval{busy(hm(9, 0), hm(10, 30))}, 30 * time.Minute, 30 * time.Minute, time.Time{}, "[10:30 11:00 11:30]"},
		{"две записи", hm(10, 0), hm(14, 0), []Interval{busy(hm(10, 30), hm(11, 0)), busy(hm(12, 0), hm(13, 0))}, time.Hour, 30 * time.Minute, time.Time{}, "[11:00 13:00]"},
		{"прошлое не предлагаем", hm(10, 0), hm(12, 0), nil, 30 * time.Minute, 30 * time.Minute, hm(10, 40), "[11:00 11:30]"},
		{"нулевая длительность", hm(10, 0), hm(12, 0), nil, 0, 30 * time.Minute, time.Time{}, "[]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FreeSlots(tt.from, tt.to, tt.busy, tt.d, tt.step, tt.notBefore)
			if starts(got) != tt.want {
				t.Errorf("получили %s, ожидали %s", starts(got), tt.want)
			}
			for _, s := range got {
				if s.EndsAt.Sub(s.StartsAt) != tt.d {
					t.Errorf("окно %v длиной не %v", s, tt.d)
				}
			}
		})
	}
}

func TestWorkday(t *testing.T) {
	week := []employees.WorkingDay{
		{Weekday: 1, Start: "10:00", End: "19:00"},
		{Weekday: 7, Start: "11:00", End: "15:00"},
	}
	msk, _ := time.LoadLocation("Europe/Moscow")
	berlin, _ := time.LoadLocation("Europe/Berlin")

	tests := []struct {
		name             string
		date             string
		loc              *time.Location
		wantFrom, wantTo string // в UTC; "" — выходной
	}{
		{"понедельник по Москве", "2026-10-19", msk, "07:00", "16:00"},
		{"вторник — выходной", "2026-10-20", msk, "", ""},
		{"воскресенье — день 7, а не 0", "2026-10-25", msk, "08:00", "12:00"},
		// 25.10.2026 в Берлине переводят часы: летние +2 сменяются зимними +1.
		// В сутках 25 часов, но 11:00 по часам салона — это 11:00, а не 10:00 или 12:00.
		{"переход на зимнее время", "2026-10-25", berlin, "10:00", "14:00"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			date, _ := time.ParseInLocation(time.DateOnly, tt.date, tt.loc)
			from, to, ok := workday(week, date, tt.loc)
			if tt.wantFrom == "" {
				if ok {
					t.Errorf("ожидали выходной, получили %v–%v", from, to)
				}
				return
			}
			if !ok || from.UTC().Format("15:04") != tt.wantFrom || to.UTC().Format("15:04") != tt.wantTo {
				t.Errorf("получили %v–%v (ok=%v), ожидали %s–%s UTC", from.UTC(), to.UTC(), ok, tt.wantFrom, tt.wantTo)
			}
		})
	}
}

func TestBookingSlots(t *testing.T) {
	store := newFakeStore("первая") // мастер uid(1), 16.10 10:00–11:00
	b := newTestBooking(store)
	ctx := context.Background()
	day := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC) // пятница, «сейчас» — 09:00

	slots, err := b.Slots(ctx, uid(1), day, 8*time.Hour)
	if err != nil || starts(slots) != "[11:00]" {
		t.Errorf("8 часов после записи 10–11 в день 10–19: %s, %v", starts(slots), err)
	}
	if slots, _ := b.Slots(ctx, uid(1), day.AddDate(0, 0, 2), time.Hour); len(slots) != 0 {
		t.Errorf("воскресенье — выходной, а получили %s", starts(slots))
	}
	if _, err := b.Slots(ctx, uid(999), day, time.Hour); !errors.Is(err, ErrEmployeeNotFound) {
		t.Errorf("нет мастера: %v", err)
	}

	slots, d, err := b.SlotsForService(ctx, uid(2), serviceManicure.ID, day)
	if err != nil || d != 30*time.Minute || len(slots) != 35 { // 10:00–19:00 с шагом 15 минут: 36 начал минус последнее
		t.Errorf("маникюр у uid(2): %d окон по %v, %v", len(slots), d, err)
	}
	if _, _, err := b.SlotsForService(ctx, uid(1), serviceManicure.ID, day); !errors.Is(err, ErrServiceNotProvided) {
		t.Errorf("маникюр у uid(1): %v, ожидали ErrServiceNotProvided", err)
	}
	if _, _, err := b.SlotsForService(ctx, uid(1), uid(999), day); !errors.Is(err, ErrServiceNotFound) {
		t.Errorf("нет услуги: %v, ожидали ErrServiceNotFound", err)
	}
}

func TestSlotsHandler(t *testing.T) {
	q := "/slots?employeeId=" + uid(1) + "&date=2026-10-16"
	tests := []struct {
		name, path string
		wantStatus int
		wantInBody string
	}{
		{"по длительности", q + "&duration=480", 200, `"duration_minutes":480,"data":[{"starts_at":"2026-10-16T11:00:00Z","ends_at":"2026-10-16T19:00:00Z"}]`},
		{"по услуге", q + "&serviceId=" + serviceHaircut.ID, 200, `"duration_minutes":60`},
		{"мастер не оказывает услугу", q + "&serviceId=" + serviceManicure.ID, 400, "не оказывает"},
		{"нет услуги", q + "&serviceId=" + uid(999), 400, "услуга не найдена"},
		{"и услуга, и длительность", q + "&duration=60&serviceId=" + serviceHaircut.ID, 400, "что-то одно"},
		{"ни услуги, ни длительности", q, 400, "что-то одно"},
		{"кривая длительность", q + "&duration=4", 400, "duration"},
		{"без даты", "/slots?employeeId=" + uid(1) + "&duration=60", 400, "date"},
		{"без мастера", "/slots?date=2026-10-16&duration=60", 400, "employeeId"},
		{"нет мастера", "/slots?employeeId=" + uid(999) + "&date=2026-10-16&duration=60", 404, "employee_not_found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(t, newFakeStore("первая"), "GET", tt.path, "")
			if rec.Code != tt.wantStatus || !strings.Contains(rec.Body.String(), tt.wantInBody) {
				t.Errorf("статус %d, тело %s; ожидали %d и %s", rec.Code, rec.Body, tt.wantStatus, tt.wantInBody)
			}
		})
	}

	// Свободное время — массив, даже если пусто (воскресенье).
	rec := do(t, newFakeStore(), "GET", "/slots?employeeId="+uid(1)+"&date=2026-10-18&duration=60", "")
	var got SlotsResponse
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil || rec.Code != http.StatusOK || got.Data == nil || len(got.Data) != 0 {
		t.Errorf("выходной: статус %d, %+v, %v", rec.Code, got, err)
	}
}

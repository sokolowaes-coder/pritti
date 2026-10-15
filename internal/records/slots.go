package records

import (
	"time"

	"pritti/internal/employees"
)

// Interval — промежуток времени [StartsAt, EndsAt): начало входит, конец — нет.
// Поэтому 10:00–11:00 и 11:00–12:00 не пересекаются.
type Interval struct {
	StartsAt time.Time `json:"starts_at"`
	EndsAt   time.Time `json:"ends_at"`
}

func (i Interval) overlaps(start, end time.Time) bool {
	return start.Before(i.EndsAt) && i.StartsAt.Before(end)
}

// FreeSlots подбирает окна длиной d внутри рабочего дня [from, to).
// Начала идут с шагом step от from; окно подходит, если целиком помещается в рабочий день,
// не пересекает ни один занятый интервал и начинается не раньше notBefore (не в прошлом).
//
// Перебор «в лоб»: в дне максимум 96 начал по 15 минут, записей у мастера — единицы,
// поэтому O(начала × записи) здесь проще и надёжнее хитрого слияния интервалов.
func FreeSlots(from, to time.Time, busy []Interval, d, step time.Duration, notBefore time.Time) []Interval {
	slots := []Interval{}
	if d <= 0 || step <= 0 {
		return slots
	}
	for start := from; !start.Add(d).After(to); start = start.Add(step) {
		end := start.Add(d)
		if start.Before(notBefore) || overlapsAny(busy, start, end) {
			continue
		}
		slots = append(slots, Interval{StartsAt: start, EndsAt: end})
	}
	return slots
}

func overlapsAny(busy []Interval, start, end time.Time) bool {
	for _, b := range busy {
		if b.overlaps(start, end) {
			return true
		}
	}
	return false
}

// workday — начало и конец рабочего дня мастера в дату date по поясу loc.
// ok == false — в этот день недели мастер не работает.
//
// Часы собираются через time.Date в поясе loc, а не прибавлением часов к полуночи:
// так «10:00» остаётся 10:00 и в день перехода на летнее время, когда в сутках 23 часа.
func workday(days []employees.WorkingDay, date time.Time, loc *time.Location) (from, to time.Time, ok bool) {
	weekday := isoWeekday(date.In(loc))
	for _, d := range days {
		if d.Weekday != weekday {
			continue
		}
		var err1, err2 error
		from, err1 = clock(date, d.Start, loc)
		to, err2 = clock(date, d.End, loc)
		if err1 != nil || err2 != nil {
			return time.Time{}, time.Time{}, false
		}
		return from, to, true
	}
	return time.Time{}, time.Time{}, false
}

// isoWeekday — день недели по ISO 8601: 1 — понедельник … 7 — воскресенье.
// В Go воскресенье — 0, поэтому его надо перевести.
func isoWeekday(t time.Time) int {
	if wd := t.Weekday(); wd != time.Sunday {
		return int(wd)
	}
	return 7
}

// clock — время hhmm ("10:00") в дату date по поясу loc.
func clock(date time.Time, hhmm string, loc *time.Location) (time.Time, error) {
	c, err := time.Parse("15:04", hhmm)
	if err != nil {
		return time.Time{}, err
	}
	y, m, d := date.In(loc).Date()
	return time.Date(y, m, d, c.Hour(), c.Minute(), 0, 0, loc), nil
}

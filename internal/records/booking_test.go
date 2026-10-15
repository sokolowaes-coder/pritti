package records

import (
	"context"
	"errors"
	"testing"
	"time"
)

// Правила записи — без HTTP: Booking вызывается напрямую.

func draft(changes func(d *Draft)) Draft {
	d := Draft{
		Name:       "визит",
		ClientID:   uid(1),
		EmployeeID: uid(1),
		StartsAt:   time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC),
		EndsAt:     time.Date(2026, 10, 17, 13, 0, 0, 0, time.UTC),
		Status:     StatusScheduled,
	}
	if changes != nil {
		changes(&d)
	}
	return d
}

func TestBookingFillsFromService(t *testing.T) {
	b := newTestBooking(newFakeStore())
	rec, err := b.Create(context.Background(), draft(func(d *Draft) {
		d.Name, d.EndsAt, d.ServiceID = "", time.Time{}, serviceHaircut.ID
	}))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Name != "Стрижка" || rec.EndsAt.Sub(rec.StartsAt) != time.Hour || rec.ServiceID == nil || *rec.ServiceID != serviceHaircut.ID {
		t.Errorf("запись по услуге: %+v", rec)
	}

	// Явно заданные название и конец важнее значений услуги.
	rec, err = b.Create(context.Background(), draft(func(d *Draft) {
		d.ServiceID = serviceHaircut.ID
		d.StartsAt, d.EndsAt = d.StartsAt.Add(2*time.Hour), d.EndsAt.Add(2*time.Hour+30*time.Minute)
	}))
	if err != nil || rec.Name != "визит" || rec.EndsAt.Sub(rec.StartsAt) != 90*time.Minute {
		t.Errorf("явные поля перезаписаны услугой: %+v, %v", rec, err)
	}
}

func TestBookingRules(t *testing.T) {
	tests := []struct {
		name      string
		draft     Draft
		wantField string // какое поле должно быть в ValidationError; "" — ошибки нет
		wantErr   error  // или какая другая ошибка
	}{
		{"всё верно", draft(nil), "", nil},
		{"без услуги нужно название", draft(func(d *Draft) { d.Name = "" }), "name", nil},
		{"без услуги нужен конец", draft(func(d *Draft) { d.EndsAt = time.Time{} }), "ends_at", nil},
		{"конец раньше начала", draft(func(d *Draft) { d.EndsAt = d.StartsAt.Add(-time.Minute) }), "ends_at", nil},
		{"длиннее 12 часов", draft(func(d *Draft) { d.EndsAt = d.StartsAt.Add(12*time.Hour + time.Minute) }), "ends_at", nil},
		{"ровно 12 часов можно", draft(func(d *Draft) { d.EndsAt = d.StartsAt.Add(12 * time.Hour) }), "", nil},
		{"нет такой услуги", draft(func(d *Draft) { d.ServiceID = uid(999) }), "service_id", nil},
		{"мастер не оказывает услугу", draft(func(d *Draft) { d.ServiceID = serviceManicure.ID }), "service_id", nil},
		{"нет мастера", draft(func(d *Draft) { d.EmployeeID, d.ServiceID = uid(999), serviceHaircut.ID }), "", ErrEmployeeNotFound},
		{"completed в будущем", draft(func(d *Draft) { d.Status = StatusCompleted }), "status", nil},
		{"no_show в будущем", draft(func(d *Draft) { d.Status = StatusNoShow }), "status", nil},
		{"completed после начала визита", draft(func(d *Draft) {
			d.Status, d.StartsAt, d.EndsAt = StatusCompleted, testNow.Add(-time.Hour), testNow
		}), "", nil},
		{"отменить будущую можно", draft(func(d *Draft) { d.Status = StatusCancelled }), "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := newTestBooking(newFakeStore()).Create(context.Background(), tt.draft)
			var invalid ValidationError
			switch {
			case tt.wantErr != nil:
				if !errors.Is(err, tt.wantErr) {
					t.Errorf("ошибка %v, ожидали %v", err, tt.wantErr)
				}
			case tt.wantField == "":
				if err != nil {
					t.Errorf("неожиданная ошибка: %v", err)
				}
			case !errors.As(err, &invalid) || invalid[tt.wantField] == "":
				t.Errorf("ошибка %v, ожидали ошибку поля %s", err, tt.wantField)
			}
		})
	}
}

// Мастер перестал оказывать услугу — старую запись на неё всё равно можно отметить,
// а вот перенести её на другого мастера, который услугу не делает, нельзя.
func TestBookingUpdateKeepsServiceOfExistingRecord(t *testing.T) {
	store := newFakeStore()
	b := newTestBooking(store)
	ctx := context.Background()

	past := func(d *Draft) {
		d.ServiceID = serviceManicure.ID
		d.EmployeeID = uid(2)
		d.ClientID = uid(2)
		d.StartsAt, d.EndsAt = testNow.Add(-2*time.Hour), testNow.Add(-time.Hour)
	}
	rec, err := b.Create(ctx, draft(past))
	if err != nil {
		t.Fatal(err)
	}

	// Подменяем Staff: теперь мастер uid(2) маникюр не делает.
	b.staff = noServicesStaff{}
	if _, err := b.Update(ctx, rec.ID, draft(func(d *Draft) { past(d); d.Status = StatusCompleted })); err != nil {
		t.Errorf("отметить старую запись: %v", err)
	}
	_, err = b.Update(ctx, rec.ID, draft(func(d *Draft) { past(d); d.EmployeeID = uid(1) }))
	var invalid ValidationError
	if !errors.As(err, &invalid) || invalid["service_id"] == "" {
		t.Errorf("перенос к мастеру без услуги: %v, ожидали ошибку service_id", err)
	}

	if _, err := b.Update(ctx, uid(999), draft(nil)); !errors.Is(err, ErrNotFound) {
		t.Errorf("изменение несуществующей: %v, ожидали ErrNotFound", err)
	}
}

type noServicesStaff struct{ fakeStaff }

func (noServicesStaff) Provides(ctx context.Context, employeeID, serviceID string) (bool, error) {
	return false, nil
}

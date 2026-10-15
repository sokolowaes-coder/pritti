package records

import (
	"context"
	"errors"
	"time"

	"pritti/internal/employees"
	"pritti/internal/services"
)

// Booking — сервисный слой записей: бизнес-правила между HTTP и SQL.
//
// Хендлер разбирает формат запроса (UUID, время, длина строк), хранилище выполняет SQL,
// а здесь — то, что относится к самой записи к мастеру: подставить длительность
// и название услуги, проверить, что мастер её оказывает, проверить длительность
// и статус, посчитать свободное время.
// Правила не знают про HTTP, поэтому их можно вызывать откуда угодно и тестировать
// без запросов, а хендлер остаётся тонким.
type Booking struct {
	store   RecordStore
	catalog ServiceCatalog
	staff   Staff
	loc     *time.Location   // пояс салона: рабочие часы и даты без пояса — в нём
	now     func() time.Time // текущее время; в тестах подменяется
}

// ServiceCatalog — откуда брать услугу записи. *services.Storage подходит.
type ServiceCatalog interface {
	GetByID(ctx context.Context, id string) (services.Service, error)
}

// Staff — что Booking нужно знать о мастерах: график и услуги. *employees.Storage подходит.
type Staff interface {
	WorkingHours(ctx context.Context, employeeID string) ([]employees.WorkingDay, error)
	Provides(ctx context.Context, employeeID, serviceID string) (bool, error)
}

var (
	_ ServiceCatalog = (*services.Storage)(nil)
	_ Staff          = (*employees.Storage)(nil)
)

func NewBooking(store RecordStore, catalog ServiceCatalog, staff Staff, loc *time.Location) *Booking {
	return &Booking{store: store, catalog: catalog, staff: staff, loc: loc, now: time.Now}
}

// maxDuration — предел длины записи: длиннее — почти наверняка опечатка в дате.
const maxDuration = 12 * time.Hour

// ValidationError — нарушенные правила записи: поле → что не так.
// Хендлер отдаёт их как 400 validation_error с fields, вместе с ошибками формата.
type ValidationError map[string]string

func (e ValidationError) Error() string { return "record validation failed" }

// Draft — запись в том виде, как её прислали: поля уже в правильном формате,
// но правила ещё не проверены и пропуски не заполнены.
type Draft struct {
	Name        string // пусто — название услуги
	Description string
	ClientID    string
	EmployeeID  string
	ServiceID   string // пусто — запись без услуги
	StartsAt    time.Time
	EndsAt      time.Time // ноль — начало плюс длительность услуги
	Status      string
}

func (b *Booking) List(ctx context.Context, f ListFilter) ([]Record, int, error) {
	return b.store.List(ctx, f)
}

func (b *Booking) Get(ctx context.Context, id string) (Record, error) {
	return b.store.GetByID(ctx, id)
}

func (b *Booking) Create(ctx context.Context, d Draft) (Record, error) {
	in, err := b.prepare(ctx, d, nil)
	if err != nil {
		return Record{}, err
	}
	return b.store.Create(ctx, in)
}

func (b *Booking) Update(ctx context.Context, id string, d Draft) (Record, error) {
	// Текущая версия нужна правилам: если мастер и услуга не менялись, не важно,
	// оказывает ли он её сейчас — визит уже назначен (или прошёл), его можно отметить.
	current, err := b.store.GetByID(ctx, id)
	if err != nil {
		return Record{}, err
	}
	in, err := b.prepare(ctx, d, &current)
	if err != nil {
		return Record{}, err
	}
	return b.store.Update(ctx, id, in)
}

func (b *Booking) Delete(ctx context.Context, id string) error {
	return b.store.Delete(ctx, id)
}

// prepare заполняет пропуски из услуги и проверяет правила записи. current — текущая
// версия при изменении, nil — при создании.
// Пересечения по времени здесь не проверяются: это делает БД — атомарно, даже
// при одновременных запросах (см. миграции 000010 и 000011).
func (b *Booking) prepare(ctx context.Context, d Draft, current *Record) (RecordInput, error) {
	problems := ValidationError{}
	in := RecordInput{
		Name:        d.Name,
		Description: d.Description,
		ClientID:    d.ClientID,
		EmployeeID:  d.EmployeeID,
		StartsAt:    d.StartsAt,
		EndsAt:      d.EndsAt,
		Status:      d.Status,
	}

	serviceOK := true
	if d.ServiceID != "" {
		svc, err := b.catalog.GetByID(ctx, d.ServiceID)
		switch {
		case errors.Is(err, services.ErrNotFound):
			problems["service_id"] = "услуга не найдена"
			serviceOK = false
		case err != nil:
			return RecordInput{}, err
		default:
			in.ServiceID = &d.ServiceID
			if in.Name == "" {
				in.Name = svc.Name
			}
			if in.EndsAt.IsZero() {
				in.EndsAt = in.StartsAt.Add(svc.Duration())
			}
			if msg, err := b.checkProvides(ctx, d.EmployeeID, d.ServiceID, current); err != nil {
				return RecordInput{}, err
			} else if msg != "" {
				problems["service_id"] = msg
			}
		}
	}

	// Без услуги название и конец обязательны. С ненайденной услугой об этом не пишем:
	// причина одна — услуга, её и надо исправить.
	if serviceOK && in.Name == "" {
		problems["name"] = "обязательное поле (или укажите service_id)"
	}
	if serviceOK && in.EndsAt.IsZero() {
		problems["ends_at"] = "обязательное поле (или укажите service_id — возьмём длительность услуги)"
	}
	if !in.EndsAt.IsZero() {
		switch dur := in.EndsAt.Sub(in.StartsAt); {
		case dur <= 0:
			problems["ends_at"] = "должно быть позже starts_at"
		case dur > maxDuration:
			problems["ends_at"] = "запись не длиннее 12 часов"
		}
	}

	// Отметить, пришёл клиент или нет, можно только когда визит уже начался.
	if (in.Status == StatusCompleted || in.Status == StatusNoShow) && in.StartsAt.After(b.now()) {
		problems["status"] = in.Status + " — только для визита, который уже начался"
	}

	if len(problems) > 0 {
		return RecordInput{}, problems
	}
	return in, nil
}

// checkProvides проверяет, что мастер оказывает услугу. Возвращает текст ошибки поля
// service_id или "". Не проверяет, если у записи те же мастер и услуга, что и были.
func (b *Booking) checkProvides(ctx context.Context, employeeID, serviceID string, current *Record) (string, error) {
	if current != nil && current.EmployeeID == employeeID && current.ServiceID != nil && *current.ServiceID == serviceID {
		return "", nil
	}
	provides, err := b.staff.Provides(ctx, employeeID, serviceID)
	if errors.Is(err, employees.ErrNotFound) {
		return "", ErrEmployeeNotFound // нет мастера — ошибка поля employee_id, а не услуги
	}
	if err != nil || provides {
		return "", err
	}
	return "мастер не оказывает эту услугу", nil
}

// slotStep — с каким шагом предлагать время начала: 10:00, 10:15, 10:30…
const slotStep = 15 * time.Minute

// Slots — свободные окна длиной duration у мастера в день date (дата в поясе салона):
// внутри рабочих часов, не пересекаясь с его записями и не в прошлом.
func (b *Booking) Slots(ctx context.Context, employeeID string, date time.Time, duration time.Duration) ([]Interval, error) {
	days, err := b.staff.WorkingHours(ctx, employeeID)
	if errors.Is(err, employees.ErrNotFound) {
		return nil, ErrEmployeeNotFound
	}
	if err != nil {
		return nil, err
	}
	from, to, ok := workday(days, date, b.loc)
	if !ok {
		return []Interval{}, nil // выходной
	}
	busy, err := b.store.Busy(ctx, employeeID, from, to)
	if err != nil {
		return nil, err
	}
	return FreeSlots(from, to, busy, duration, slotStep, b.now()), nil
}

// SlotsForService — свободные окна на услугу: её длительности и только у мастера,
// который её оказывает (иначе ErrServiceNotProvided).
func (b *Booking) SlotsForService(ctx context.Context, employeeID, serviceID string, date time.Time) ([]Interval, time.Duration, error) {
	svc, err := b.catalog.GetByID(ctx, serviceID)
	if errors.Is(err, services.ErrNotFound) {
		return nil, 0, ErrServiceNotFound
	}
	if err != nil {
		return nil, 0, err
	}
	msg, err := b.checkProvides(ctx, employeeID, serviceID, nil)
	if err != nil {
		return nil, 0, err
	}
	if msg != "" {
		return nil, 0, ErrServiceNotProvided
	}
	slots, err := b.Slots(ctx, employeeID, date, svc.Duration())
	return slots, svc.Duration(), err
}

// Package migrations хранит SQL-миграции внутри бинарника и применяет их к БД.
package migrations

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

//go:embed *.sql
var files embed.FS

// Up применяет все ещё не применённые миграции.
// Какие уже применены, migrate хранит в таблице schema_migrations.
func Up(databaseURL string) error {
	m, err := open(databaseURL)
	if err != nil {
		return err
	}
	defer m.Close()

	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("применение миграций: %w", err)
	}
	return nil
}

// Check проверяет, что БД на той версии схемы, которую ждёт этот код, и ничего не применяет.
// Нужен, когда миграции запускают отдельным шагом: сервер со старой схемой не стартует,
// а не падает потом на первом же запросе к новой колонке.
func Check(databaseURL string) error {
	want, err := Latest()
	if err != nil {
		return err
	}
	m, err := open(databaseURL)
	if err != nil {
		return err
	}
	defer m.Close()

	got, dirty, err := m.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		got, err = 0, nil
	}
	switch {
	case err != nil:
		return fmt.Errorf("версия схемы БД: %w", err)
	case dirty:
		return fmt.Errorf("миграция %d применилась не до конца (dirty) — нужна ручная починка БД", got)
	case got < want:
		return fmt.Errorf("схема БД на версии %d, код ждёт %d — сначала примените миграции (`server migrate`)", got, want)
	}
	// got > want — БД новее кода (например, откатили деплой): миграции добавляют,
	// а не ломают, поэтому старый код с новой схемой работать может.
	return nil
}

// Latest — номер последней миграции, встроенной в бинарник.
func Latest() (uint, error) {
	names, err := fs.Glob(files, "*.up.sql")
	if err != nil || len(names) == 0 {
		return 0, fmt.Errorf("встроенные миграции не найдены: %v", err)
	}
	var latest uint
	for _, name := range names {
		var v uint
		if _, err := fmt.Sscanf(name, "%d_", &v); err != nil {
			return 0, fmt.Errorf("имя миграции %s: %w", name, err)
		}
		latest = max(latest, v)
	}
	return latest, nil
}

func open(databaseURL string) (*migrate.Migrate, error) {
	src, err := iofs.New(files, ".")
	if err != nil {
		return nil, err
	}
	return migrate.NewWithSourceInstance("iofs", src, toMigrateURL(databaseURL))
}

// toMigrateURL меняет схему postgres:// на pgx5:// — так migrate выбирает драйвер.
func toMigrateURL(databaseURL string) string {
	for _, prefix := range []string{"postgres://", "postgresql://"} {
		if rest, ok := strings.CutPrefix(databaseURL, prefix); ok {
			return "pgx5://" + rest
		}
	}
	return databaseURL
}

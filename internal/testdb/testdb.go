// Package testdb готовит настоящую БД для тестов хранилищ.
package testdb

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"pritti/migrations"
)

// migrationsLockID — ключ advisory-блокировки, под которой тесты применяют миграции.
const migrationsLockID = 7_316_402_115

// New подключается к TEST_DATABASE_URL, создаёт отдельную временную схему
// и применяет в ней все миграции, чтобы не трогать настоящие данные.
// Без TEST_DATABASE_URL тест пропускается.
func New(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := NewSchema(t)
	Migrate(t, dsn)

	db, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	return db
}

// NewSchema создаёт пустую временную схему (удаляется после теста) и возвращает
// строку подключения к ней. Без TEST_DATABASE_URL тест пропускается.
func NewSchema(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL не задан — пропускаю тесты с БД")
	}
	ctx := context.Background()

	schema := fmt.Sprintf("test_%d", time.Now().UnixNano())
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// Не молчим: иначе схемы от упавших удалений будут незаметно копиться в БД.
		if _, err := admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Errorf("удаление тестовой схемы %s: %v", schema, err)
		}
		admin.Close(context.Background())
	})

	// search_path в URL: и миграции, и запросы работают только внутри временной схемы.
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	return u.String()
}

// Migrate применяет все миграции к схеме из dsn.
//
// Пакеты тестов идут параллельно, каждый в своей схеме, а расширения (pg_trgm, btree_gist)
// общие на всю БД: одновременный CREATE EXTENSION падает на гонке. Блокировка
// migrate привязана к схеме и тут не спасает, поэтому миграции — по очереди.
func Migrate(t *testing.T, dsn string) {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)

	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", migrationsLockID); err != nil {
		t.Fatal(err)
	}
	err = migrations.Up(dsn)
	if _, unlockErr := conn.Exec(ctx, "SELECT pg_advisory_unlock($1)", migrationsLockID); unlockErr != nil {
		t.Fatal(unlockErr)
	}
	if err != nil {
		t.Fatal(err)
	}
}

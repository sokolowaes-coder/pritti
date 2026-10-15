package migrations_test

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"pritti/internal/testdb"
	"pritti/migrations"
)

// Номера миграций идут подряд с 1, и у каждой есть и up, и down.
// Дырка в номерах или забытый down обычно значит ошибку при добавлении миграции.
func TestFilesAreConsecutive(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, e := range entries {
		have[e.Name()] = true
	}

	latest, err := migrations.Latest()
	if err != nil {
		t.Fatal(err)
	}
	for v := uint(1); v <= latest; v++ {
		prefix := fmt.Sprintf("%06d_", v)
		var up, down bool
		for name := range have {
			if strings.HasPrefix(name, prefix) {
				up = up || strings.HasSuffix(name, ".up.sql")
				down = down || strings.HasSuffix(name, ".down.sql")
			}
		}
		if !up || !down {
			t.Errorf("миграция %d: up=%v down=%v", v, up, down)
		}
	}
}

func TestCheck(t *testing.T) {
	dsn := testdb.NewSchema(t)

	err := migrations.Check(dsn)
	if err == nil || !strings.Contains(err.Error(), "версии 0") {
		t.Fatalf("пустая БД: %v, ожидали ошибку про версию 0", err)
	}

	testdb.Migrate(t, dsn)
	if err := migrations.Check(dsn); err != nil {
		t.Errorf("после миграций: %v", err)
	}
}

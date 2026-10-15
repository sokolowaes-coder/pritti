package clients

import "testing"

func TestNormalizePhone(t *testing.T) {
	ok := map[string]string{
		"+79161234567":       "+79161234567",
		"79161234567":        "+79161234567",
		"89161234567":        "+79161234567",
		"8 (916) 123-45-67":  "+79161234567",
		"+7 (916) 123-45-67": "+79161234567",
		"9161234567":         "+79161234567",
	}
	for in, want := range ok {
		if got, err := NormalizePhone(in); err != nil || got != want {
			t.Errorf("NormalizePhone(%q) = %q, %v; ожидали %q", in, got, err, want)
		}
	}

	for _, in := range []string{
		"", "12345", "+89161234567", "+9161234567", "791612345678",
		"8916123456a", "8-916-123-45-67 доб. 1", "7+9161234567", "+380501234567",
	} {
		if got, err := NormalizePhone(in); err == nil {
			t.Errorf("NormalizePhone(%q) = %q, ожидали ошибку", in, got)
		}
	}
}

package clients

import "errors"

var errBadPhone = errors.New("неверный номер, ожидается российский, например +79161234567 или 8 (916) 123-45-67")

// NormalizePhone приводит российский номер к виду +79161234567.
// Принимает +7 и 8 в начале, 10 цифр без кода страны, пробелы, скобки и дефисы.
func NormalizePhone(s string) (string, error) {
	var digits []byte
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c >= '0' && c <= '9':
			digits = append(digits, c)
		case c == '+' && len(digits) == 0:
			// «+» допустим только перед первой цифрой
		case c == ' ' || c == '(' || c == ')' || c == '-':
		default:
			return "", errBadPhone
		}
	}
	plus := len(s) > 0 && s[0] == '+'

	switch {
	case len(digits) == 11 && digits[0] == '7':
	case len(digits) == 11 && digits[0] == '8' && !plus:
	case len(digits) == 10 && !plus:
		digits = append([]byte{'7'}, digits...)
	default:
		return "", errBadPhone
	}
	return "+7" + string(digits[1:]), nil
}

#!/usr/bin/env bash
# Демо-данные через API: услуги, мастера с графиком и услугами, клиенты и записи.
# Через API, а не прямым SQL: так данные проходят все проверки, а записи ставятся
# в свободные окна из /slots — как это делал бы настоящий клиент.
#
#   API_KEY=... ./scripts/seed.sh                                   # локально (http://localhost:8080)
#   BASE_URL=https://api.pritti.space API_KEY=... ./scripts/seed.sh  # другой сервер
#
# Нужны curl и jq. Запускается на пустой базе: если услуги уже есть — останавливается.
set -euo pipefail

BASE_URL=${BASE_URL:-http://localhost:8080}
: "${API_KEY:?задайте API_KEY}"

# api МЕТОД ПУТЬ [ТЕЛО] — запрос к API; печатает тело ответа, при ошибке HTTP — выходит.
api() {
	local out status
	out=$(curl -sS -X "$1" "$BASE_URL$2" -H "X-API-Key: $API_KEY" ${3:+-d "$3"} -w $'\n%{http_code}')
	status=${out##*$'\n'}
	out=${out%$'\n'*}
	if ((status >= 400)); then
		echo "$1 $2 → $status: $out" >&2
		return 1
	fi
	printf '%s' "$out"
}

# day +1 — завтра, day -1 — вчера (работает и с GNU date в Linux, и с BSD date в macOS).
day() { date -d "$1 day" +%F 2>/dev/null || date -v"$1"d +%F; }

if (($(api GET /services | jq .count) > 0)); then
	echo "услуги уже есть — похоже, демо-данные уже загружены" >&2
	exit 1
fi

# service НАЗВАНИЕ МИНУТ ЦЕНА_В_РУБЛЯХ → id
service() {
	api POST /services "$(jq -nc --arg n "$1" --argjson d "$2" --argjson p "$3" \
		'{name: $n, duration_minutes: $d, price_kopecks: ($p * 100)}')" | jq -r .id
}

# employee ИМЯ "ДНИ" С ДО УСЛУГА... → id; ДНИ — номера через пробел, 1 — понедельник.
employee() {
	local name=$1 days=$2 from=$3 to=$4 id
	shift 4
	id=$(api POST /employees "$(jq -nc --arg n "$name" '{name: $n}')" | jq -r .id)
	api PUT "/employees/$id/schedule" "$(jq -nc --arg days "$days" --arg f "$from" --arg t "$to" \
		'{days: [$days | split(" ")[] | {weekday: tonumber, start: $f, end: $t}]}')" >/dev/null
	api PUT "/employees/$id/services" "$(jq -nc '{service_ids: $ARGS.positional}' --args "$@")" >/dev/null
	echo "$id"
}

client() {
	api POST /clients "$(jq -nc --arg n "$1" --arg p "$2" '{full_name: $n, phone: $p}')" | jq -r .id
}

# book КЛИЕНТ МАСТЕР УСЛУГА ЧЕРЕЗ_ДНЕЙ НОМЕР_ОКНА — запись в N-е свободное окно дня.
book() {
	local date start
	date=$(day "$4")
	start=$(api GET "/slots?employeeId=$2&serviceId=$3&date=$date" | jq -r ".data[$5].starts_at // empty")
	if [[ -z $start ]]; then
		echo "  $date: свободного окна №$5 нет (выходной?) — пропускаю"
		return
	fi
	api POST /records "$(jq -nc --arg c "$1" --arg e "$2" --arg s "$3" --arg t "$start" \
		'{client_id: $c, employee_id: $e, service_id: $s, starts_at: $t}')" |
		jq -r '"  \(.starts_at)  \(.name)"'
}

# past КЛИЕНТ МАСТЕР УСЛУГА "ГГГГ-ММ-ДДTЧЧ:ММ:СС" СТАТУС — прошедший визит с отметкой.
past() {
	api POST /records "$(jq -nc --arg c "$1" --arg e "$2" --arg s "$3" --arg t "$4" --arg st "$5" \
		'{client_id: $c, employee_id: $e, service_id: $s, starts_at: $t, status: $st}')" |
		jq -r '"  \(.starts_at)  \(.name) — \(.status)"'
}

echo "Услуги"
haircut_w=$(service "Стрижка женская" 60 2500)
haircut_m=$(service "Стрижка мужская" 45 1500)
coloring=$(service "Окрашивание" 120 5000)
styling=$(service "Укладка" 30 1200)
manicure=$(service "Маникюр" 60 1800)
pedicure=$(service "Педикюр" 90 2500)
api GET /services | jq -r '.data[] | "  \(.name): \(.duration_minutes) мин, \(.price_kopecks / 100) ₽"'

echo "Мастера"
anna=$(employee "Анна Смирнова" "1 2 3 4 5" 10:00 19:00 "$haircut_w" "$coloring" "$styling")
olga=$(employee "Ольга Кузнецова" "2 3 4 5 6" 11:00 20:00 "$manicure" "$pedicure")
igor=$(employee "Игорь Волков" "1 3 5 6" 12:00 21:00 "$haircut_m" "$styling")
echo "  Анна — пн–пт 10–19: женская стрижка, окрашивание, укладка"
echo "  Ольга — вт–сб 11–20: маникюр, педикюр"
echo "  Игорь — пн, ср, пт, сб 12–21: мужская стрижка, укладка"

echo "Клиенты"
maria=$(client "Мария Иванова" "+7 916 100-00-01")
elena=$(client "Елена Петрова" "8 (916) 100-00-02")
dmitry=$(client "Дмитрий Соколов" "9161000003")
kate=$(client "Екатерина Орлова" "+79161000004")
echo "  4 клиента"

echo "Прошедшие визиты"
past "$maria" "$anna" "$haircut_w" "$(day -7)T10:00:00" completed
past "$elena" "$olga" "$manicure" "$(day -6)T12:00:00" completed
past "$dmitry" "$igor" "$haircut_m" "$(day -5)T13:00:00" no_show

echo "Будущие записи"
book "$maria" "$anna" "$coloring" +1 0
book "$elena" "$anna" "$haircut_w" +1 0 # первое окно после окрашивания Марии
book "$kate" "$olga" "$manicure" +2 2
book "$dmitry" "$igor" "$haircut_m" +2 0
book "$maria" "$olga" "$pedicure" +5 0 # +5 — вторник: в воскресенье Ольга не работает
book "$kate" "$anna" "$styling" +4 3

echo "Готово."

#!/usr/bin/env bash
# Локальные тестовые данные для редизайна v2: день помощницы Марии.
# Требует запущенный бэкенд (TZ=UTC) на $B и выполненный `go run ./cmd/seed`.
# Логины: owner@example.com / changeme123 (наниматель Анна),
#         maria@example.com / maria12345 (помощница), lan@example.com / lan1234567.
set -euo pipefail
B=${B:-http://localhost:8080}
J='Content-Type: application/json'
PSQL=${PSQL:-/opt/homebrew/opt/postgresql@16/bin/psql}
DBARGS=(-h localhost -U postgres -d helper_db -q)

login() {
  curl -s -X POST "$B/auth/login" -H "$J" -d "{\"email\":\"$1\",\"password\":\"$2\"}" |
    python3 -c 'import sys,json;print(json.load(sys.stdin)["access"])'
}

T=$(login owner@example.com changeme123)
A=(-H "Authorization: Bearer $T" -H "X-Org-Id: 1" -H "$J")

"$PSQL" "${DBARGS[@]}" -c "UPDATE users SET name='Анна' WHERE email='owner@example.com';
  UPDATE organizations SET name='Дом Анны' WHERE id=1;
  INSERT INTO recipe_meal_times (recipe_id, meal_time_id)
    SELECT r.id, m.id FROM recipes r JOIN meal_times m ON m.name = r.category ON CONFLICT DO NOTHING;"

# Помощницы (если ещё нет)
ensure_helper() { # name email password locale
  local exists
  exists=$("$PSQL" "${DBARGS[@]}" -tA -c "SELECT count(*) FROM users WHERE email='$2'")
  if [ "$exists" = "0" ]; then
    local inv
    inv=$(curl -s -X POST "$B/orgs/1/invites" "${A[@]}" -d '{"role":"helper"}' |
      python3 -c 'import sys,json;print(json.load(sys.stdin)["token"])')
    curl -s -o /dev/null -X POST "$B/invites/$inv/accept" -H "$J" -d "{\"name\":\"$1\",\"password\":\"$3\"}"
    "$PSQL" "${DBARGS[@]}" -c "UPDATE users SET email='$2', locale='$4' WHERE name='$1' AND email LIKE '%@invite.local'"
  fi
}
ensure_helper "Мария" maria@example.com maria12345 ru
ensure_helper "Лан" lan@example.com lan1234567 vi
MARIA=$("$PSQL" "${DBARGS[@]}" -tA -c "SELECT id FROM users WHERE email='maria@example.com'")
LAN=$("$PSQL" "${DBARGS[@]}" -tA -c "SELECT id FROM users WHERE email='lan@example.com'")
CAT() { "$PSQL" "${DBARGS[@]}" -tA -c "SELECT id FROM task_categories WHERE organization_id=1 AND name='$1'"; }
FOOD=$(CAT 'Еда'); CLEAN=$(CAT 'Уборка'); KID=$(CAT 'Уход за ребёнком'); OTHER=$(CAT 'Другое')

D=$(date -u +%F)
D1=$(date -u -v+1d +%F 2>/dev/null || date -u -d tomorrow +%F)

curl -s -o /dev/null -X POST "$B/admin/api/regenerate-schedule" "${A[@]}"

post() { curl -s -o /dev/null -w "%{http_code} " -X POST "$B$1" "${A[@]}" -d "$2"; }
post /admin/api/childcare "{\"date\":\"${D}T00:00:00Z\",\"start_time\":\"09:30\",\"end_time\":\"11:00\",\"notes\":\"Прогулка в парке у дома. Взять самокат, воду и шапку.\"}"
post /admin/api/childcare "{\"date\":\"${D}T00:00:00Z\",\"start_time\":\"13:45\",\"end_time\":\"15:30\",\"notes\":\"Дневной сон. Алиса плохо спала ночью — уложите чуть пораньше, проветрите детскую.\"}"
post /admin/api/custom-tasks "{\"date\":\"$D\",\"title\":\"Занятие: цвета и формы\",\"description\":\"Лучше после дневного сна, 10–15 минут. Называйте цвет вслух. Видео, как показать: https://www.youtube.com/results?search_query=sorter+colors\",\"task_category_id\":$KID,\"priority\":\"high\",\"assigned_to_user_id\":$MARIA}"
post /admin/api/custom-tasks "{\"date\":\"$D\",\"title\":\"Убрать кошачий лоток\",\"task_category_id\":$OTHER,\"priority\":\"high\",\"assigned_to_user_id\":$MARIA}"
post /admin/api/custom-tasks "{\"date\":\"$D\",\"title\":\"Стирка детских вещей\",\"description\":\"Белое отдельно, 40°.\",\"task_category_id\":$CLEAN,\"priority\":\"mid\",\"assigned_to_user_id\":$MARIA}"
post /admin/api/custom-tasks "{\"date\":\"$D\",\"title\":\"Испечь печенье к выходным\",\"description\":\"Рецепт песочного печенья — в разделе рецептов.\",\"time\":\"16:00\",\"task_category_id\":$FOOD,\"assigned_to_user_id\":$MARIA}"
post /admin/api/custom-tasks "{\"date\":\"$D\",\"title\":\"Протереть плинтусы в прихожей\",\"task_category_id\":$CLEAN}"
post /admin/api/custom-tasks "{\"date\":\"$D\",\"title\":\"Полить цветы\",\"description\":\"Гостиная и балкон.\",\"task_category_id\":$OTHER,\"assigned_to_user_id\":$LAN}"
post /admin/api/custom-tasks "{\"date\":\"$D1\",\"title\":\"Забрать посылку на почте\",\"time\":\"12:00\",\"end_time\":\"14:00\",\"task_category_id\":$OTHER,\"assigned_to_user_id\":$MARIA}"
echo
echo "Готово: $D (+ $D1)"

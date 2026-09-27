-- name: CreateCase :one
INSERT INTO cases(user_id,case_number,folder_number,title,description,status,notes,tribunal,locality,judicial_unit)
VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
RETURNING id;

-- name: GetCase :one
SELECT id,case_number,folder_number,title,description,status,notes,tribunal,locality,judicial_unit
FROM cases WHERE id=$1 AND user_id=$2;

-- name: ListCases :many
SELECT id,case_number,folder_number,title,description,status,notes,tribunal,locality,judicial_unit
FROM cases
WHERE user_id=sqlc.arg(user_id)
  AND (sqlc.arg(status_filter)::text='' OR status=sqlc.arg(status_filter))
  AND (sqlc.arg(search)::text='' OR case_number ILIKE '%'||sqlc.arg(search)||'%' OR title ILIKE '%'||sqlc.arg(search)||'%' OR folder_number ILIKE '%'||sqlc.arg(search)||'%')
ORDER BY created_at DESC LIMIT sqlc.arg(limit_count)::int OFFSET sqlc.arg(offset_count)::int;

-- name: UpdateCase :execrows
UPDATE cases SET case_number=$3,folder_number=$4,title=$5,description=$6,status=$7,notes=$8,
  tribunal=$9,locality=$10,judicial_unit=$11,updated_at=now()
WHERE id=$1 AND user_id=$2;

-- name: DeleteCase :execrows
DELETE FROM cases WHERE id=$1 AND user_id=$2;

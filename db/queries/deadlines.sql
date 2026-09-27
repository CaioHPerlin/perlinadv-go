-- name: ListDeadlines :many
SELECT id,case_id,publication_id,suggestion_id,title,description,due_on,status,needs_review
FROM deadlines WHERE user_id=$1 ORDER BY due_on ASC LIMIT $2 OFFSET $3;

-- name: GetDeadline :one
SELECT id,case_id,publication_id,suggestion_id,title,description,due_on,status,needs_review
FROM deadlines WHERE id=$1 AND user_id=$2;

-- name: CaseOwnedExists :one
SELECT EXISTS(SELECT 1 FROM cases WHERE id=$1 AND user_id=$2);

-- name: CreateDeadline :one
INSERT INTO deadlines(user_id,case_id,title,description,due_on,status)
VALUES($1,$2,$3,$4,$5,$6) RETURNING id;

-- name: UpdateDeadline :execrows
UPDATE deadlines SET case_id=$3,title=$4,description=$5,due_on=$6,status=$7,
  needs_review=FALSE,updated_at=now() WHERE id=$1 AND user_id=$2;

-- name: DeleteDeadline :execrows
DELETE FROM deadlines WHERE id=$1 AND user_id=$2;

-- name: CreateLawyer :one
INSERT INTO users(name,email,password_hash,oab_number,oab_uf,labor_practice,llm_consent,llm_consented_at)
VALUES($1,$2,$3,$4,$5,$6,$7,now()) RETURNING id;

-- name: GetLoginUser :one
SELECT id,role,password_hash,llm_consent FROM users WHERE email=$1 AND active;

-- name: CreateSession :exec
INSERT INTO sessions(user_id,token_hash,expires_at)
VALUES($1,$2,now()+interval '7 days');

-- name: GetSessionPrincipal :one
SELECT u.id,u.role,u.llm_consent FROM sessions se JOIN users u ON u.id=se.user_id
WHERE se.token_hash=$1 AND se.expires_at>now() AND se.revoked_at IS NULL AND u.active;

-- name: RevokeSession :exec
UPDATE sessions SET revoked_at=now() WHERE token_hash=$1;

-- name: GetSala :one
SELECT 
    "id", "theme"
FROM salas
WHERE id = $1;

-- name: GetSalas :many
SELECT 
    "id", "theme"
FROM salas;

-- name: InsertSalas :one
INSERT INTO salas
    ( "theme" ) VALUES
    ( $1 )
RETURNING "id";

-- name: GetMessage :one
SELECT 
    "id", "sala_id", "message", "reaction_count", "answered"
FROM messages
WHERE 
    id = $1;

-- name: GetSalasMessages :many
SELECT 
    "id", "sala_id", "message", "reaction_count", "answered"
FROM messages
WHERE sala_id = $1;

-- name: InsertMessage :one
INSERT INTO messages 
    ( "sala_id", "message" ) VALUES
    ( $1, $2)
RETURNING "id";

-- NAME: ReactToMessage :one
UPDATE messages
SET 
    reaction_count = reaction_count + 1
WHERE 
    id = $1
RETURNING reaction_count;

-- name: RemoveReactionFromMessage :one
UPDATE messages
SET 
    reaction_count = reaction_count - 1
WHERE 
    id = $1
RETURNING reaction_count;

-- name: MarkMessageAsAnswered :exec
UPDATE messages
SET 
    answered = true
WHERE 
    id = $1;
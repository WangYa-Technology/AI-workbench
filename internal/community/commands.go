package community

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// The key is scoped to the actor and operation; payload includes any target ID.
// Internal callers may omit it. HTTP clients validate and supply one.
func commandTx(ctx context.Context, tx pgx.Tx, actor uuid.UUID, operation string, keys []string, payload any, id uuid.UUID) (uuid.UUID, bool, error) {
	if len(keys) == 0 {
		return id, false, nil
	}
	key := strings.TrimSpace(keys[0])
	if len(key) < 8 || len(key) > 200 {
		return uuid.Nil, false, ErrInvalid
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return uuid.Nil, false, err
	}
	hash := sha256.Sum256(body)
	digest := hex.EncodeToString(hash[:])
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, actor.String()+":"+operation+":"+key); err != nil {
		return uuid.Nil, false, err
	}
	result, err := tx.Exec(ctx, `INSERT INTO community_commands(actor_id,operation,request_key,payload_hash,resource_id) VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, actor, operation, key, digest, id)
	if err != nil {
		return uuid.Nil, false, err
	}
	if result.RowsAffected() == 1 {
		return id, false, nil
	}
	var stored string
	if err = tx.QueryRow(ctx, `SELECT payload_hash,resource_id FROM community_commands WHERE actor_id=$1 AND operation=$2 AND request_key=$3`, actor, operation, key).Scan(&stored, &id); err != nil {
		return uuid.Nil, false, err
	}
	if digest != stored {
		return uuid.Nil, false, ErrConflict
	}
	return id, true, nil
}

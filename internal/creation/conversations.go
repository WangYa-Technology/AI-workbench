package creation

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const defaultConversationTitle = "New conversation"

func (s *Service) CreateConversation(ctx context.Context, ownerID uuid.UUID, input ConversationCreateInput) (Conversation, error) {
	title := strings.TrimSpace(input.Title)
	if title == "" {
		title = defaultConversationTitle
	}
	if len([]rune(title)) > 120 {
		return Conversation{}, ErrInvalid
	}
	id := uuid.New()
	var conversation Conversation
	err := s.pool.QueryRow(ctx, `
		INSERT INTO creation_conversations(id,owner_id,title)
		VALUES($1,$2,$3)
		RETURNING id,title,created_at,updated_at`, id, ownerID, title).Scan(
		&conversation.ID, &conversation.Title, &conversation.CreatedAt, &conversation.UpdatedAt)
	if err != nil {
		return Conversation{}, err
	}
	conversation.Modes = []string{}
	return conversation, nil
}

func (s *Service) ListConversations(ctx context.Context, ownerID uuid.UUID) (ConversationPage, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT c.id,c.title,
		       COALESCE(array_agg(DISTINCT g.mode) FILTER (WHERE g.mode IS NOT NULL), '{}'),
		       count(g.id),max(g.created_at),c.created_at,c.updated_at
		FROM creation_conversations c
		LEFT JOIN generations g ON g.conversation_id=c.id
		WHERE c.owner_id=$1
		GROUP BY c.id,c.title,c.created_at,c.updated_at
		ORDER BY c.updated_at DESC,c.id DESC`, ownerID)
	if err != nil {
		return ConversationPage{}, err
	}
	defer rows.Close()
	page := ConversationPage{Items: make([]Conversation, 0)}
	for rows.Next() {
		var item Conversation
		if err := rows.Scan(&item.ID, &item.Title, &item.Modes, &item.GenerationCount, &item.LatestGenerationAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return ConversationPage{}, err
		}
		page.Items = append(page.Items, item)
	}
	if err := rows.Err(); err != nil {
		return ConversationPage{}, err
	}
	return page, nil
}

func validateConversationOwnership(ctx context.Context, tx pgx.Tx, ownerID, conversationID uuid.UUID) error {
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM creation_conversations WHERE id=$1 AND owner_id=$2)`, conversationID, ownerID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrInvalid
	}
	return nil
}

func (s *Service) ensureConversationTx(ctx context.Context, tx pgx.Tx, ownerID uuid.UUID, conversationID *uuid.UUID) (uuid.UUID, error) {
	if conversationID != nil {
		if err := validateConversationOwnership(ctx, tx, ownerID, *conversationID); err != nil {
			return uuid.Nil, err
		}
		return *conversationID, nil
	}
	id := uuid.New()
	if _, err := tx.Exec(ctx, `INSERT INTO creation_conversations(id,owner_id,title) VALUES($1,$2,$3)`, id, ownerID, defaultConversationTitle); err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

func touchConversationTx(ctx context.Context, tx pgx.Tx, conversationID uuid.UUID, prompt string) error {
	_, err := tx.Exec(ctx, `
		UPDATE creation_conversations
		SET title=CASE WHEN title IN ($2,'新建对话') THEN left($3,120) ELSE title END,
		    updated_at=now()
		WHERE id=$1`, conversationID, defaultConversationTitle, prompt)
	return err
}

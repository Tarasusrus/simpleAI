// Package rag содержит код пакета rag и его задачи.
package rag

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"
)

type Document struct {
	ID          uuid.UUID
	Content     string
	ContentHash string
}

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func (s *Store) ListPending(ctx context.Context, limit int) ([]Document, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, content, content_hash
		FROM rag_document
		WHERE embedding IS NULL OR content_hash IS NULL
		ORDER BY created_at
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var docs []Document
	for rows.Next() {
		var doc Document
		if err := rows.Scan(&doc.ID, &doc.Content, &doc.ContentHash); err != nil {
			return nil, err
		}
		docs = append(docs, doc)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return docs, nil
}

func (s *Store) UpdateEmbedding(ctx context.Context, docID uuid.UUID, embedding []float32, contentHash string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE rag_document
		SET embedding = $1,
		    content_hash = $2,
		    updated_at = $3,
		    embedding_updated_at = $4
		WHERE id = $5
	`, pgvector.NewVector(embedding), contentHash, time.Now().UTC(), time.Now().UTC(), docID)
	return err
}

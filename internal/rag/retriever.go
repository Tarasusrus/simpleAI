package rag

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"
)

type SearchFilter struct {
	ReceiptID  string
	CategoryID string
	Since      *time.Time
	Until      *time.Time
}

type SearchResult struct {
	ID       uuid.UUID
	Content  string
	Metadata map[string]any
	Distance float64
}

type Retriever struct {
	pool *pgxpool.Pool
}

func NewRetriever(pool *pgxpool.Pool) *Retriever {
	return &Retriever{pool: pool}
}

func (r *Retriever) Search(ctx context.Context, embedding []float32, limit int, filter SearchFilter) ([]SearchResult, error) {
	if limit <= 0 {
		limit = 5
	}
	query, args := buildSearchQuery(embedding, limit, filter)
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var (
			id       uuid.UUID
			content  string
			metadata []byte
			distance float64
		)
		if err := rows.Scan(&id, &content, &metadata, &distance); err != nil {
			return nil, err
		}
		meta := map[string]any{}
		if len(metadata) > 0 {
			if err := json.Unmarshal(metadata, &meta); err != nil {
				return nil, err
			}
		}
		results = append(results, SearchResult{
			ID:       id,
			Content:  content,
			Metadata: meta,
			Distance: distance,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

func buildSearchQuery(embedding []float32, limit int, filter SearchFilter) (string, []any) {
	args := []any{pgvector.NewVector(embedding)}
	conditions := []string{"embedding IS NOT NULL"}

	if filter.ReceiptID != "" {
		args = append(args, filter.ReceiptID)
		conditions = append(conditions, fmt.Sprintf("metadata->>'receipt_id' = $%d", len(args)))
	}
	if filter.CategoryID != "" {
		args = append(args, filter.CategoryID)
		conditions = append(conditions, fmt.Sprintf("metadata->>'category_id' = $%d", len(args)))
	}
	if filter.Since != nil {
		args = append(args, filter.Since.Format(time.RFC3339))
		conditions = append(conditions, fmt.Sprintf("metadata->>'purchase_ts' >= $%d", len(args)))
	}
	if filter.Until != nil {
		args = append(args, filter.Until.Format(time.RFC3339))
		conditions = append(conditions, fmt.Sprintf("metadata->>'purchase_ts' <= $%d", len(args)))
	}

	args = append(args, limit)
	query := fmt.Sprintf(`
		SELECT id, content, metadata, embedding <-> $1 AS distance
		FROM rag_document
		WHERE %s
		ORDER BY embedding <-> $1
		LIMIT $%d
	`, strings.Join(conditions, " AND "), len(args))
	return query, args
}

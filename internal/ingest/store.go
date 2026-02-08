package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func (s *Store) IngestReceipt(ctx context.Context, input ReceiptInput) (uuid.UUID, error) {
	if err := input.Validate(); err != nil {
		return uuid.Nil, err
	}

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return uuid.Nil, err
	}
	defer func() {
		if err := tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			_ = err
		}
	}()

	receiptID := uuid.New()
	var storeID *uuid.UUID
	if strings.TrimSpace(input.StoreName) != "" {
		id, err := upsertStore(ctx, tx, input.StoreName)
		if err != nil {
			return uuid.Nil, err
		}
		storeID = &id
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO receipt (id, source, source_ref, purchase_ts, currency, total_amount, raw_text, store_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`, receiptID, input.Source, input.SourceRef, input.PurchaseTS, emptyToNull(input.Currency), input.TotalAmount, input.RawText, storeID)
	if err != nil {
		return uuid.Nil, mapPgError(err)
	}

	itemCount := len(input.Items)
	for _, item := range input.Items {
		itemID := uuid.New()
		var categoryID *uuid.UUID
		if strings.TrimSpace(item.CategoryID) != "" {
			parsed, err := uuid.Parse(item.CategoryID)
			if err != nil {
				return uuid.Nil, fmt.Errorf("invalid category_id %q: %w", item.CategoryID, err)
			}
			categoryID = &parsed
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO receipt_item (id, receipt_id, name, quantity, unit_price, amount, category_id)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
		`, itemID, receiptID, item.Name, item.Quantity, item.UnitPrice, item.Amount, categoryID)
		if err != nil {
			return uuid.Nil, mapPgError(err)
		}

		categoryName, err := fetchCategoryName(ctx, tx, categoryID)
		if err != nil {
			return uuid.Nil, err
		}

		content := buildItemContent(item, input.Currency, categoryName)
		metadata := buildItemMetadata(receiptID, itemID, categoryID, input.PurchaseTS)
		if err := insertRagDocument(ctx, tx, "receipt_item", itemID, content, metadata); err != nil {
			return uuid.Nil, err
		}
	}

	receiptContent := buildReceiptContent(input, itemCount)
	receiptMetadata := buildReceiptMetadata(receiptID, input.PurchaseTS)
	if err := insertRagDocument(ctx, tx, "receipt", receiptID, receiptContent, receiptMetadata); err != nil {
		return uuid.Nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, err
	}
	return receiptID, nil
}

func insertRagDocument(ctx context.Context, tx pgx.Tx, sourceType string, sourceID uuid.UUID, content string, metadata map[string]any) error {
	payload, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO rag_document (id, source_type, source_id, content, metadata)
		VALUES ($1, $2, $3, $4, $5)
	`, uuid.New(), sourceType, sourceID, content, payload)
	return err
}

func upsertStore(ctx context.Context, tx pgx.Tx, name string) (uuid.UUID, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO store (id, name)
		VALUES ($1, $2)
		ON CONFLICT (name) DO UPDATE SET name = EXCLUDED.name
		RETURNING id
	`, uuid.New(), strings.TrimSpace(name)).Scan(&id)
	if err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

func buildReceiptContent(input ReceiptInput, itemCount int) string {
	ts := "unknown"
	if input.PurchaseTS != nil {
		ts = input.PurchaseTS.Format(time.RFC3339)
	}
	total := "unknown"
	if input.TotalAmount != nil {
		total = fmt.Sprintf("%.2f", *input.TotalAmount)
	}
	currency := strings.TrimSpace(input.Currency)
	if currency == "" {
		currency = "unknown"
	}
	return fmt.Sprintf(
		"Receipt | source=%s | ref=%s | ts=%s | total=%s %s | items=%d",
		input.Source,
		input.SourceRef,
		ts,
		total,
		currency,
		itemCount,
	)
}

func buildItemContent(item ReceiptItemInput, currency string, categoryName string) string {
	qty := formatAmount(item.Quantity, 3)
	unit := formatAmount(item.UnitPrice, 2)
	amount := formatAmount(item.Amount, 2)
	cur := strings.TrimSpace(currency)
	if cur == "" {
		cur = "unknown"
	}
	category := categoryName
	if strings.TrimSpace(category) == "" {
		category = "unknown"
	}
	return fmt.Sprintf(
		"Item | name=%s | qty=%s | unit=%s | amount=%s %s | category=%s",
		item.Name,
		qty,
		unit,
		amount,
		cur,
		category,
	)
}

func buildReceiptMetadata(receiptID uuid.UUID, purchaseTS *time.Time) map[string]any {
	metadata := map[string]any{
		"receipt_id": receiptID.String(),
	}
	if purchaseTS != nil {
		metadata["purchase_ts"] = purchaseTS.Format(time.RFC3339)
	}
	return metadata
}

func buildItemMetadata(receiptID uuid.UUID, itemID uuid.UUID, categoryID *uuid.UUID, purchaseTS *time.Time) map[string]any {
	metadata := map[string]any{
		"receipt_id": receiptID.String(),
		"item_id":    itemID.String(),
	}
	if categoryID != nil {
		metadata["category_id"] = categoryID.String()
	}
	if purchaseTS != nil {
		metadata["purchase_ts"] = purchaseTS.Format(time.RFC3339)
	}
	return metadata
}

func fetchCategoryName(ctx context.Context, tx pgx.Tx, categoryID *uuid.UUID) (string, error) {
	if categoryID == nil {
		return "", nil
	}
	var name string
	err := tx.QueryRow(ctx, `SELECT name FROM category WHERE id = $1`, *categoryID).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return name, nil
}

func formatAmount(value *float64, scale int) string {
	if value == nil {
		return "unknown"
	}
	return fmt.Sprintf("%.*f", scale, *value)
}

func emptyToNull(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	v := value
	return &v
}

func mapPgError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		if pgErr.Code == "23505" {
			return fmt.Errorf("receipt already exists: %w", err)
		}
	}
	return err
}

package ingest

import (
	"errors"
	"strconv"
	"strings"
	"time"
)

type ReceiptInput struct {
	Source      string             `json:"source"`
	SourceRef   string             `json:"source_ref"`
	PurchaseTS  *time.Time         `json:"purchase_ts"`
	Currency    string             `json:"currency"`
	TotalAmount *float64           `json:"total_amount"`
	RawText     string             `json:"raw_text"`
	Items       []ReceiptItemInput `json:"items"`
}

type ReceiptItemInput struct {
	Name       string   `json:"name"`
	Quantity   *float64 `json:"quantity"`
	UnitPrice  *float64 `json:"unit_price"`
	Amount     *float64 `json:"amount"`
	CategoryID string   `json:"category_id"`
}

func (r ReceiptInput) Validate() error {
	if strings.TrimSpace(r.Source) == "" {
		return errors.New("source is required")
	}
	if strings.TrimSpace(r.SourceRef) == "" {
		return errors.New("source_ref is required")
	}
	if strings.TrimSpace(r.RawText) == "" {
		return errors.New("raw_text is required")
	}
	for i, item := range r.Items {
		if strings.TrimSpace(item.Name) == "" {
			return errors.New("items[" + itoa(i) + "].name is required")
		}
	}
	return nil
}

func itoa(i int) string {
	return strconv.Itoa(i)
}

// Package ingest содержит код пакета ingest и его задачи.
package ingest

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"simpleAI/internal/constants"
)

type ReceiptInput struct {
	Source      string             `json:"source"`
	SourceRef   string             `json:"source_ref"`
	PurchaseTS  *time.Time         `json:"purchase_ts"`
	Currency    string             `json:"currency"`
	TotalAmount *float64           `json:"total_amount"`
	StoreName   string             `json:"store_name"`
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
		return errors.New(constants.ErrMsgIngestSourceRequired)
	}
	if strings.TrimSpace(r.SourceRef) == "" {
		return errors.New(constants.ErrMsgIngestSourceRefRequired)
	}
	if strings.TrimSpace(r.RawText) == "" {
		return errors.New(constants.ErrMsgIngestRawTextRequired)
	}
	if r.TotalAmount != nil && *r.TotalAmount < 0 {
		return errors.New(constants.ErrMsgIngestTotalNonNegative)
	}
	for i, item := range r.Items {
		if strings.TrimSpace(item.Name) == "" {
			return fmt.Errorf(constants.ErrMsgIngestItemNameRequired, itoa(i))
		}
		if item.Quantity != nil && *item.Quantity < 0 {
			return fmt.Errorf(constants.ErrMsgIngestItemQtyNonNegative, itoa(i))
		}
		if item.UnitPrice != nil && *item.UnitPrice < 0 {
			return fmt.Errorf(constants.ErrMsgIngestItemUnitNonNeg, itoa(i))
		}
		if item.Amount != nil && *item.Amount < 0 {
			return fmt.Errorf(constants.ErrMsgIngestItemAmtNonNegative, itoa(i))
		}
	}
	return nil
}

func itoa(i int) string {
	return strconv.Itoa(i)
}

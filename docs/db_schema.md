# DB Schema (Draft)

Goal: minimal schema to store receipts/expenses and RAG documents.

## Entities
- receipt: a single shopping receipt (metadata + raw text).
- receipt_item: line items linked to a receipt.
- store: normalized store names for receipts.
- receipt_artifact: ссылки на файлы/вложения (фото/сканы).
- category: category tree for analytics and filtering.
- rag_document: text chunks used for retrieval with embeddings.
- budget_category: категории доходов/расходов для бюджет-трекера.
- budget_transaction: операции доходов и расходов.
- budget_goal: цели накоплений с прогрессом.
- budget_debt: долги и кредиты с платежами.

## Notes
- Store raw receipt text for future re-processing.
- Keep items normalized for analytics and connect to categories.
- Store names are normalized via `store` and referenced by `receipt.store_id`.
- Артефакты (фото/сканы) храним во внешнем хранилище, в БД — ссылки.
- RAG documents reference receipts/items via metadata.
- `metadata` is indexed with GIN for filtering.
- Numeric amounts are non-negative via CHECK constraints.

## RAG Metadata (convention)
Recommended `metadata` shape for `rag_document`:

```json
{
  "receipt_id": "uuid",
  "item_id": "uuid",
  "category_id": "uuid",
  "purchase_ts": "RFC3339"
}
```

Required keys:
- For receipt documents: `receipt_id`, `purchase_ts`.
- For item documents: `item_id`, `receipt_id`, `purchase_ts`.

Optional keys:
- `category_id` (when available).

## RAG Document Construction
Minimal strategy (v1):
- For each receipt, create 1 document with:
  - content: `Receipt | store={store} | ts={purchase_ts} | total={total} {currency} | items={item_count}`
  - metadata: `receipt_id`, `purchase_ts`.
- For each receipt item, create 1 document with:
  - content: `Item | name={name} | qty={qty} | unit={unit_price} | amount={amount} {currency} | category={category_name}`
  - metadata: `item_id`, `receipt_id`, `category_id`, `purchase_ts`.

Chunking rules:
- Keep documents small enough for embedding (<= 1-2k chars).
- Prefer per-item documents for precise retrieval.

## Reindex Policy (v1)
- Each `rag_document` stores `content_hash` and `updated_at`.
- Reindex when:
  - `embedding` is NULL, or
  - `content_hash` differs from the current content hash.

## RAG Pipeline (high level)
1) Ingestion:
   - Receive raw receipt text.
   - Validate and store `receipt` (raw_text, source, source_ref, purchase_ts).
2) Normalization:
   - Extract items, totals, currency.
   - Store `receipt_item` rows.
3) Document build:
   - Create `rag_document` for receipt and for each item.
   - Fill `content` using templates; attach `metadata`.
4) Embeddings:
   - Generate embeddings for each `rag_document.content`.
   - Store vectors in `rag_document.embedding`.
5) Retrieval:
   - Query by vector similarity + metadata filters.
   - Use top-k docs as context for LLM.

## Embeddings
- Model: `text-embedding-3-small`
- Vector size: 1536 (matches `vector(1536)` in schema)

## Local Postgres (Docker)
Use the provided `docker-compose.yml`:

```bash
docker compose up -d
```

Install goose:

```bash
go install github.com/pressly/goose/v3/cmd/goose@latest
```

Apply schema:

```bash
goose -dir migrations postgres "postgres://simpleai:simpleai@localhost:5432/simpleai?sslmode=disable" up
```

## Seed Categories
Initial top-level categories are added by `migrations/00002_seed_categories.sql`.

## Ingestion CLI (v1)
Minimal loader that writes `receipt`, `receipt_item`, and `rag_document` (no embeddings yet):

```bash
go run cmd/ingest/main.go -file receipt.json
```

Example payload:

```json
{
  "source": "telegram",
  "source_ref": "msg:123",
  "purchase_ts": "2026-01-24T12:00:00Z",
  "currency": "RUB",
  "total_amount": 1000.00,
  "raw_text": "Чек: мука 1000гр 1000р",
  "items": [
    {
      "name": "Мука",
      "quantity": 1.000,
      "unit_price": 1000.00,
      "amount": 1000.00
    }
  ]
}
```

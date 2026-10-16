-- +goose Up
-- +goose StatementBegin
-- Ручной курс валюты (simpleAI-su6l).
--
-- Автокурс тянется раз в сутки из open.er-api.com — это межбанк. Оператор
-- живёт в Тайланде и меняет наличными, где курс другой; повлиять на цифру он
-- не мог никак, а все конверты считаются именно по ней.
--
-- ОТДЕЛЬНАЯ колонка, а не перезапись rate_to_rub: воркер продолжает писать
-- автокурс каждые сутки, и ручное значение, положенное в ту же колонку, он
-- затёр бы в ближайший тик. Чтение отдаёт manual_rate_to_rub, когда он есть.
-- «курс авто» = обнулить эту колонку, не трогая автокурс.
ALTER TABLE exchange_rate
    ADD COLUMN IF NOT EXISTS manual_rate_to_rub DOUBLE PRECISION;

ALTER TABLE exchange_rate
    ADD COLUMN IF NOT EXISTS manual_set_at TIMESTAMPTZ;

-- Автокурс может отсутствовать: оператор способен задать ручной курс раньше,
-- чем воркер принёс первый. Класть ручное число и в rate_to_rub нельзя — после
-- «курс авто» оно читалось бы как автоматический курс «обновлённый сегодня».
-- NULL здесь честно значит «автокурса ещё нет».
ALTER TABLE exchange_rate
    ALTER COLUMN rate_to_rub DROP NOT NULL;

-- Курс — величина строго положительная. Нулевой курс делит на ноль в ToTHB,
-- отрицательный печатает отрицательные конверты.
ALTER TABLE exchange_rate
    DROP CONSTRAINT IF EXISTS exchange_rate_manual_positive;

ALTER TABLE exchange_rate
    ADD CONSTRAINT exchange_rate_manual_positive
    CHECK (manual_rate_to_rub IS NULL OR manual_rate_to_rub > 0);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM exchange_rate WHERE rate_to_rub IS NULL;
ALTER TABLE exchange_rate ALTER COLUMN rate_to_rub SET NOT NULL;
ALTER TABLE exchange_rate DROP CONSTRAINT IF EXISTS exchange_rate_manual_positive;
ALTER TABLE exchange_rate DROP COLUMN IF EXISTS manual_set_at;
ALTER TABLE exchange_rate DROP COLUMN IF EXISTS manual_rate_to_rub;
-- +goose StatementEnd

// Команда categories — дедупликатор категорий (ADR-010). По умолчанию dry-run: печатает план
// и ничего не пишет. --apply выполняет слияния (одна транзакция БД на пару, журнал, откат).
// --rollback <merge_id> откатывает слияние по журналу.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/google/uuid"

	"simpleAI/config"
	"simpleAI/internal/categories"
	"simpleAI/internal/db"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	apply := flag.Bool("apply", false, "выполнить слияния (без флага — только план)")
	rollback := flag.String("rollback", "", "merge_id слияния для отката")
	flag.Parse()

	cfg, err := config.LoadConfig()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	// Слияние и откат пишут в таблицы миграции 00022: накатываем её (идемпотентно) до работы.
	// План (dry-run) только читает и схему не трогает.
	if *apply || *rollback != "" {
		if err := db.Migrate(ctx, cfg.DB); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	pool, err := db.NewPool(ctx, cfg.DB)
	if err != nil {
		return fmt.Errorf("init db pool: %w", err)
	}
	defer pool.Close()
	st := categories.NewStore(pool)

	if *rollback != "" {
		id, err := uuid.Parse(*rollback)
		if err != nil {
			return fmt.Errorf("merge_id: %w", err)
		}
		if err := st.Rollback(ctx, id); err != nil {
			return err
		}
		fmt.Println("откат выполнен:", id)
		return nil
	}

	d := &categories.Deduplicator{
		Source: st, Finder: categories.SynonymFinder{Synonyms: categories.DefaultSynonyms},
		Decider: categories.RuleDecider{}, Merger: st, Usage: st, Reconciler: st,
	}
	rep, err := d.Run(ctx, *apply)
	if err != nil {
		return err
	}
	mode := "ПЛАН (ничего не записано; для выполнения добавьте --apply)"
	if rep.Applied {
		mode = "ПРИМЕНЕНО"
	}
	fmt.Println(mode)
	if rep.Reconciled > 0 {
		fmt.Printf("исправлено имён в долях конвертов: %d\n", rep.Reconciled)
	}
	for _, it := range rep.Items {
		line := fmt.Sprintf("%-28s → %-12s транзакций %d, плановых %d, повторов %d, долей %d",
			it.Pair.Source.Name, it.Pair.Target.Name, it.Counts.Transactions, it.Counts.Planned, it.Counts.Recurring, it.Counts.Shares)
		switch {
		case it.Err != nil:
			line += "  ОШИБКА: " + it.Err.Error()
		case it.Result != nil:
			line += "  merge_id=" + it.Result.MergeID.String()
		}
		fmt.Println(line)
	}
	if len(rep.Items) == 0 {
		fmt.Println("дублей по правилам не найдено")
	}
	_ = os.Stdout.Sync() //nolint:errcheck // вывод в консоль
	return nil
}

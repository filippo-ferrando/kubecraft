package db

import (
	"context"
	"fmt"
	"io/fs"

	"github.com/jackc/pgx/v5/pgtype"

	"git.fferrando.cc/rdfilippo/kubecraft/internal/db/sqlc"
)

// DefaultPlans are created on first start when the plans table is empty.
var DefaultPlans = []sqlc.CreatePlanParams{
	{Name: "Small", CpuRequestM: 500, CpuLimitM: 1000, MemoryLimitMb: 2048, StorageGb: 5, Active: true},
	{Name: "Medium", CpuRequestM: 1000, CpuLimitM: 2000, MemoryLimitMb: 4096, StorageGb: 10, Active: true},
	{Name: "Large", CpuRequestM: 2000, CpuLimitM: 4000, MemoryLimitMb: 8192, StorageGb: 20, Active: true},
}

// Seed creates the default plans and the default quota row if missing.
func (s *Store) Seed(ctx context.Context, defaultMaxServers int) error {
	return s.InTx(ctx, func(q *sqlc.Queries) error {
		if err := q.EnsureDefaultQuota(ctx, pgtype.Int4{Int32: int32(defaultMaxServers), Valid: true}); err != nil {
			return fmt.Errorf("default quota: %w", err)
		}
		n, err := q.CountPlans(ctx)
		if err != nil {
			return err
		}
		if n > 0 {
			return nil
		}
		for _, p := range DefaultPlans {
			if _, err := q.CreatePlan(ctx, p); err != nil {
				return fmt.Errorf("seed plan %s: %w", p.Name, err)
			}
		}
		return nil
	})
}

func mustSub(f fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(f, dir)
	if err != nil {
		panic(err)
	}
	return sub
}

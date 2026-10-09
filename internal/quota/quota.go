// Package quota computes effective per-user limits and checks them.
package quota

import (
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"

	"git.fferrando.cc/rdfilippo/kubecraft/internal/db/sqlc"
)

// Limits is an effective quota; nil means unlimited.
type Limits struct {
	MaxServers        *int32 `json:"max_servers"`
	MaxTotalCPUM      *int32 `json:"max_total_cpu_m"`
	MaxTotalMemoryMB  *int32 `json:"max_total_memory_mb"`
	MaxTotalStorageGB *int32 `json:"max_total_storage_gb"`
}

// Usage is what a user consumes now. CPU and memory count running servers only.
type Usage struct {
	Servers         int32 `json:"servers"`
	StorageGB       int32 `json:"storage_gb"`
	RunningCPUM     int32 `json:"running_cpu_m"`
	RunningMemoryMB int32 `json:"running_memory_mb"`
}

// Effective merges a user's overrides on top of the default quota.
func Effective(def sqlc.DefaultQuotum, user *sqlc.UserQuota) Limits {
	pick := func(u, d pgtype.Int4) *int32 {
		if u.Valid {
			return &u.Int32
		}
		if d.Valid {
			return &d.Int32
		}
		return nil
	}
	var u sqlc.UserQuota
	if user != nil {
		u = *user
	}
	return Limits{
		MaxServers:        pick(u.MaxServers, def.MaxServers),
		MaxTotalCPUM:      pick(u.MaxTotalCpuM, def.MaxTotalCpuM),
		MaxTotalMemoryMB:  pick(u.MaxTotalMemoryMb, def.MaxTotalMemoryMb),
		MaxTotalStorageGB: pick(u.MaxTotalStorageGb, def.MaxTotalStorageGb),
	}
}

func UsageFromRow(r sqlc.GetUserUsageRow) Usage {
	return Usage{Servers: r.Servers, StorageGB: r.StorageGb, RunningCPUM: r.RunningCpuM, RunningMemoryMB: r.RunningMemoryMb}
}

// ExceededError explains which limit a change would break.
type ExceededError struct {
	What  string
	Used  int32
	Add   int32
	Limit int32
	Unit  string
}

func (e *ExceededError) Error() string {
	if e.What == "servers" {
		return fmt.Sprintf("Quota exceeded: %d/%d servers", e.Used, e.Limit)
	}
	return fmt.Sprintf("Quota exceeded: %s would be %d%s, limit is %d%s", e.What, e.Used+e.Add, e.Unit, e.Limit, e.Unit)
}

// Change is what an action adds on top of current usage.
type Change struct {
	Servers   int32
	StorageGB int32
	CPUM      int32
	MemoryMB  int32
}

// Check returns an ExceededError when usage plus change breaks a limit.
func Check(l Limits, u Usage, c Change) error {
	checks := []struct {
		limit *int32
		used  int32
		add   int32
		what  string
		unit  string
	}{
		{l.MaxServers, u.Servers, c.Servers, "servers", ""},
		{l.MaxTotalStorageGB, u.StorageGB, c.StorageGB, "storage", " GB"},
		{l.MaxTotalCPUM, u.RunningCPUM, c.CPUM, "CPU of running servers", "m"},
		{l.MaxTotalMemoryMB, u.RunningMemoryMB, c.MemoryMB, "memory of running servers", " MB"},
	}
	for _, ch := range checks {
		if ch.limit == nil || ch.add <= 0 {
			continue
		}
		if ch.used+ch.add > *ch.limit {
			return &ExceededError{What: ch.what, Used: ch.used, Add: ch.add, Limit: *ch.limit, Unit: ch.unit}
		}
	}
	return nil
}

// ForCreate is the change made by creating a server on plan p, started right away.
func ForCreate(p sqlc.Plan) Change {
	return Change{Servers: 1, StorageGB: p.StorageGb, CPUM: p.CpuLimitM, MemoryMB: p.MemoryLimitMb}
}

// ForStart is the change made by starting a stopped server on plan p.
func ForStart(p sqlc.Plan) Change {
	return Change{CPUM: p.CpuLimitM, MemoryMB: p.MemoryLimitMb}
}

// ForPlanChange is the change from moving a server from old to next plan.
func ForPlanChange(old, next sqlc.Plan, running bool) Change {
	c := Change{StorageGB: next.StorageGb - old.StorageGb}
	if running {
		c.CPUM = next.CpuLimitM - old.CpuLimitM
		c.MemoryMB = next.MemoryLimitMb - old.MemoryLimitMb
	}
	return c
}

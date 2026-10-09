package quota

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"git.fferrando.cc/rdfilippo/kubecraft/internal/db/sqlc"
)

func i4(v int32) pgtype.Int4 { return pgtype.Int4{Int32: v, Valid: true} }
func p(v int32) *int32       { return &v }

var small = sqlc.Plan{Name: "Small", CpuRequestM: 500, CpuLimitM: 1000, MemoryLimitMb: 2048, StorageGb: 5}
var large = sqlc.Plan{Name: "Large", CpuRequestM: 2000, CpuLimitM: 4000, MemoryLimitMb: 8192, StorageGb: 20}

func TestEffective(t *testing.T) {
	def := sqlc.DefaultQuotum{MaxServers: i4(2), MaxTotalMemoryMb: i4(4096)}
	l := Effective(def, nil)
	if *l.MaxServers != 2 || *l.MaxTotalMemoryMB != 4096 || l.MaxTotalCPUM != nil || l.MaxTotalStorageGB != nil {
		t.Fatalf("defaults wrong: %+v", l)
	}
	l = Effective(def, &sqlc.UserQuota{MaxServers: i4(5), MaxTotalCpuM: i4(3000)})
	if *l.MaxServers != 5 || *l.MaxTotalCPUM != 3000 || *l.MaxTotalMemoryMB != 4096 {
		t.Fatalf("override wrong: %+v", l)
	}
}

func TestCheckServers(t *testing.T) {
	l := Limits{MaxServers: p(3)}
	if err := Check(l, Usage{Servers: 2}, ForCreate(small)); err != nil {
		t.Fatal(err)
	}
	err := Check(l, Usage{Servers: 3}, ForCreate(small))
	var ex *ExceededError
	if !errors.As(err, &ex) || err.Error() != "Quota exceeded: 3/3 servers" {
		t.Fatalf("got %v", err)
	}
}

func TestCheckResources(t *testing.T) {
	l := Limits{MaxTotalMemoryMB: p(6144), MaxTotalCPUM: p(2000), MaxTotalStorageGB: p(20)}
	u := Usage{Servers: 1, StorageGB: 5, RunningCPUM: 1000, RunningMemoryMB: 2048}
	if err := Check(l, u, ForCreate(small)); err != nil {
		t.Fatalf("small should fit: %v", err)
	}
	if err := Check(l, u, ForCreate(large)); err == nil {
		t.Fatal("large should not fit")
	}
	// Starting needs CPU/RAM only, storage is already counted.
	full := Usage{StorageGB: 20, RunningCPUM: 1000, RunningMemoryMB: 2048}
	if err := Check(l, full, ForStart(small)); err != nil {
		t.Fatalf("start should fit: %v", err)
	}
	if err := Check(l, Usage{RunningCPUM: 1500}, ForStart(small)); err == nil {
		t.Fatal("cpu limit ignored")
	}
}

func TestUnlimited(t *testing.T) {
	if err := Check(Limits{}, Usage{Servers: 1000, RunningMemoryMB: 1 << 30}, ForCreate(large)); err != nil {
		t.Fatal(err)
	}
}

func TestPlanChange(t *testing.T) {
	l := Limits{MaxTotalMemoryMB: p(4096), MaxTotalStorageGB: p(10)}
	// Downgrades never fail.
	if err := Check(l, Usage{StorageGB: 50, RunningMemoryMB: 99999}, ForPlanChange(large, small, true)); err != nil {
		t.Fatal(err)
	}
	// Upgrading a stopped server only checks storage.
	if err := Check(Limits{MaxTotalMemoryMB: p(1)}, Usage{}, ForPlanChange(small, large, false)); err != nil {
		t.Fatal(err)
	}
	if err := Check(l, Usage{StorageGB: 5, RunningMemoryMB: 2048}, ForPlanChange(small, large, true)); err == nil {
		t.Fatal("upgrade should exceed")
	}
}

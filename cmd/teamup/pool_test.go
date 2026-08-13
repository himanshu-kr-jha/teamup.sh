package main

import (
	"runtime"
	"testing"
)

const testDSN = "postgres://teamup:teamup@localhost:5432/teamup?sslmode=disable"

// The pool was built with pgxpool.New and no configuration at all, so its size
// was pgx's default of max(4, NumCPU) — four connections on a small container,
// shared by every request, every WebSocket write and the push fan-out. Nothing
// in the repository set pool_max_conns either, so no deployment corrected it.
func TestTheDatabasePoolIsSizedExplicitly(t *testing.T) {
	cfg, err := databasePoolConfig(testDSN, 0)
	if err != nil {
		t.Fatalf("build pool config: %v", err)
	}
	if cfg.MaxConns != DefaultDBMaxConns {
		t.Errorf("MaxConns: got %d want %d", cfg.MaxConns, DefaultDBMaxConns)
	}
	// The failure this exists to prevent: a size that is whatever the host
	// happens to have, which on a one-CPU container is four.
	if cfg.MaxConns == int32(runtime.NumCPU()) && DefaultDBMaxConns != int32(runtime.NumCPU()) {
		t.Errorf("pool size still follows the host's core count (%d)", runtime.NumCPU())
	}
	// A pool that opens every connection on the first burst adds a handshake
	// to the request that triggered it; a warm floor costs nothing to hold.
	if cfg.MinConns <= 0 {
		t.Errorf("MinConns: got %d, want a warm floor", cfg.MinConns)
	}
	if cfg.MinConns > cfg.MaxConns {
		t.Errorf("MinConns %d exceeds MaxConns %d", cfg.MinConns, cfg.MaxConns)
	}
}

func TestAConfiguredPoolSizeIsHonoured(t *testing.T) {
	cfg, err := databasePoolConfig(testDSN, 40)
	if err != nil {
		t.Fatalf("build pool config: %v", err)
	}
	if cfg.MaxConns != 40 {
		t.Errorf("MaxConns: got %d want 40", cfg.MaxConns)
	}
}

// A DSN is the more specific statement of intent, so it wins over the default.
// Somebody who has written pool_max_conns into their connection string has
// already decided.
func TestPoolMaxConnsInTheDSNWins(t *testing.T) {
	cfg, err := databasePoolConfig(testDSN+"&pool_max_conns=8", 0)
	if err != nil {
		t.Fatalf("build pool config: %v", err)
	}
	if cfg.MaxConns != 8 {
		t.Errorf("MaxConns: got %d want the 8 the DSN asked for", cfg.MaxConns)
	}
}

func TestAMalformedDSNIsNamed(t *testing.T) {
	if _, err := databasePoolConfig("://nonsense", 0); err == nil {
		t.Fatal("a DSN pgx cannot parse should be an error")
	}
}

// Go reads the host's core count, not the cgroup's CPU quota, so a pod limited
// to one CPU still ran with GOMAXPROCS set to the whole node — many OS threads
// contending for a quota that admits one.
func TestCPUQuotaIsReadFromTheCgroup(t *testing.T) {
	cases := []struct {
		name string
		max  string
		want int
	}{
		{"one whole cpu", "100000 100000", 1},
		{"half a cpu still needs a thread", "50000 100000", 1},
		{"two and a half cpus round up", "250000 100000", 3},
		{"four cpus", "400000 100000", 4},
		{"no limit set", "max 100000", 0},
		{"unparsable", "nonsense", 0},
		{"empty", "", 0},
		{"zero period", "100000 0", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := parseCPUMax(c.max); got != c.want {
				t.Errorf("parseCPUMax(%q): got %d want %d", c.max, got, c.want)
			}
		})
	}
}

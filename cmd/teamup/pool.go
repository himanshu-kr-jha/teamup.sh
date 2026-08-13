package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
)

// DefaultDBMaxConns is how many PostgreSQL connections one server holds.
//
// pgx defaults to max(4, NumCPU) and nothing in this repository overrode it —
// not the DSN in .env.example, not either compose file, not the Helm chart —
// so a container with one CPU ran the whole application through four
// connections: every HTTP request, every WebSocket handler, and the push
// fan-out that opens a query per recipient.
//
// 25 is chosen from the other end. It covers a 60-person organization's
// ordinary traffic with the fan-out running alongside it, and four replicas of
// it still fit inside PostgreSQL's default max_connections of 100 with room
// for migrations and a psql session. Deployments that run more replicas, or a
// PostgreSQL tuned higher, set TEAMUP_DB_MAX_CONNS.
const DefaultDBMaxConns int32 = 25

// DefaultDBMinConns keeps a few connections open so the first request after a
// quiet spell does not pay for a TCP handshake, a TLS handshake and a
// PostgreSQL startup packet before it can run a query.
const DefaultDBMinConns int32 = 4

// parseCPUMax reads a cgroup v2 cpu.max line — "$QUOTA $PERIOD", or "max" for
// no limit — and returns the whole CPUs it allows, rounded up. It returns 0
// when there is no quota to respect or the line cannot be understood.
func parseCPUMax(line string) int {
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) != 2 || fields[0] == "max" {
		return 0
	}
	quota, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil || quota <= 0 {
		return 0
	}
	period, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil || period <= 0 {
		return 0
	}
	// Rounded up, and never below one: half a CPU of quota still needs a
	// thread to spend it on.
	cores := int((quota + period - 1) / period)
	if cores < 1 {
		cores = 1
	}
	return cores
}

// cgroupCPUMaxPath is where a cgroup v2 container finds its own CPU quota.
// Overridden in tests; there is no such file on a developer's machine, where
// the process is not in a leaf cgroup with a quota.
var cgroupCPUMaxPath = "/sys/fs/cgroup/cpu.max"

// limitGOMAXPROCSToCPUQuota makes the runtime schedule for the CPU the
// container is actually allowed rather than the CPU the node has.
//
// Go reads the host's core count. Under Kubernetes with `limits.cpu: 1000m`
// that means a 64-core node gives a one-CPU pod GOMAXPROCS=64: sixty-four
// runnable OS threads taking turns inside a quota that admits one, which shows
// up as scheduling latency and throttling rather than as an error anywhere.
//
// Reports the value it settled on, and 0 when it left the runtime alone.
func limitGOMAXPROCSToCPUQuota(logger zerolog.Logger) int {
	raw, err := os.ReadFile(cgroupCPUMaxPath)
	if err != nil {
		return 0
	}
	cores := parseCPUMax(string(raw))
	if cores <= 0 || cores >= runtime.NumCPU() {
		return 0
	}
	runtime.GOMAXPROCS(cores)
	logger.Info().
		Int("gomaxprocs", cores).
		Int("host_cpus", runtime.NumCPU()).
		Msg("GOMAXPROCS limited to the cgroup CPU quota")
	return cores
}

// databasePoolConfig builds the connection pool the server runs on.
//
// maxConns of 0 means DefaultDBMaxConns. A `pool_max_conns` written into the
// DSN wins over both: it is the more specific statement, and somebody who put
// it there has already decided.
func databasePoolConfig(dsn string, maxConns int) (*pgxpool.Config, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse TEAMUP_DATABASE_URL: %w", err)
	}

	if !strings.Contains(dsn, "pool_max_conns") {
		cfg.MaxConns = DefaultDBMaxConns
		if maxConns > 0 {
			cfg.MaxConns = int32(maxConns)
		}
	}
	if !strings.Contains(dsn, "pool_min_conns") {
		cfg.MinConns = DefaultDBMinConns
		if cfg.MinConns > cfg.MaxConns {
			cfg.MinConns = cfg.MaxConns
		}
	}
	// A connection that has lived long enough is retired rather than held for
	// ever, so a PostgreSQL restart or a failover behind a proxy is recovered
	// from without a rolling restart of the application.
	if cfg.MaxConnLifetime == 0 {
		cfg.MaxConnLifetime = time.Hour
	}
	if cfg.MaxConnIdleTime == 0 {
		cfg.MaxConnIdleTime = 30 * time.Minute
	}
	return cfg, nil
}

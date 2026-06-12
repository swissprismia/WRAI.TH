package relay

import (
	"context"
	"log"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// observatoryMaterializedViews are refreshed on a schedule by
// refreshObservatoryViews. Order matters only for log readability; each
// refresh is independent. All three carry a UNIQUE index, which
// REFRESH ... CONCURRENTLY requires.
var observatoryMaterializedViews = []string{
	"mv_session_aggregates",
	"mv_daily_burn_by_project",
	"mv_estimate_accuracy",
}

// refreshObservatoryViews periodically refreshes the observatory materialized
// views CONCURRENTLY so the dashboard's burn / budget / session-aggregate
// panels stay live.
//
// Background: the observatory migrations bootstrap these views once (revision
// 0003) but intentionally omit any refresh scheduling (OQ-4). Without this
// loop the views freeze at their first-boot values. CONCURRENTLY keeps reads
// unblocked during the refresh; it is safe because every view has a unique
// index and is already populated by the bootstrap migration.
//
// The loop exits when ctx is cancelled (relay shutdown) and is a no-op when
// interval <= 0 (refresh disabled via WRAITH_OBSERVATORY_MV_REFRESH_SECONDS=0).
func refreshObservatoryViews(ctx context.Context, pool *pgxpool.Pool, interval time.Duration) {
	if interval <= 0 {
		log.Println("observatory: materialized-view refresh disabled")
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for _, view := range observatoryMaterializedViews {
				rctx, cancel := context.WithTimeout(ctx, 60*time.Second)
				_, err := pool.Exec(rctx, "REFRESH MATERIALIZED VIEW CONCURRENTLY "+view)
				cancel()
				if err != nil {
					log.Printf("observatory: refresh %s failed (will retry next tick): %v", view, err)
				}
			}
		}
	}
}

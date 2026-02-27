package event

import (
	"context"
	"log/slog"
	"time"
)

// CronJob defines a single cron schedule.
type CronJob struct {
	Name     string        // human-readable job name
	Interval time.Duration // how often to fire (simpler than cron spec for now)
	Spec     string        // cron spec string (stored in metadata)
}

// CronSource emits periodic tick events.
// Backed by simple time.Ticker for zero external dependencies.
// TODO: upgrade to robfig/cron/v3 for full cron spec support.
type CronSource struct {
	jobs   []CronJob
	logger *slog.Logger
}

// NewCronSource creates a CronSource with the given jobs.
func NewCronSource(jobs []CronJob, logger *slog.Logger) *CronSource {
	if logger == nil {
		logger = slog.Default()
	}
	return &CronSource{jobs: jobs, logger: logger}
}

func (c *CronSource) Name() string { return SourceCron }

// Subscribe starts a goroutine per job, each emitting a TypeTick event.
func (c *CronSource) Subscribe(ctx context.Context, out chan<- Event) error {
	for _, job := range c.jobs {
		go c.runJob(ctx, job, out)
	}
	return nil
}

// Ack is a no-op for cron sources.
func (c *CronSource) Ack(_ context.Context, _ string) error { return nil }

func (c *CronSource) runJob(ctx context.Context, job CronJob, out chan<- Event) {
	ticker := time.NewTicker(job.Interval)
	defer ticker.Stop()

	c.logger.Info("cron job started", "name", job.Name, "interval", job.Interval)

	for {
		select {
		case <-ctx.Done():
			c.logger.Info("cron job stopped", "name", job.Name)
			return
		case t := <-ticker.C:
			payload := CronPayload{
				JobName: job.Name,
				Spec:    job.Spec,
			}
			ev, err := NewEvent(SourceCron, TypeTick, payload, map[string]string{
				"job":  job.Name,
				"time": t.UTC().Format(time.RFC3339),
			})
			if err != nil {
				c.logger.Error("cron event marshal", "job", job.Name, "err", err)
				continue
			}

			select {
			case out <- ev:
				c.logger.Debug("cron tick emitted", "job", job.Name)
			case <-ctx.Done():
				return
			}
		}
	}
}

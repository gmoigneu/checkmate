// Package recurrence materializes recurring tasks from their templates.
//
// A recurrence row is a template, not a task. This turns it into real rows in
// tasks, one per occurrence, which is what keeps completion history: you can see
// that the Monday report was done thirty times, and every list query treats
// recurring and one-shot tasks identically.
//
// Two properties matter and are both tested:
//
//   - Idempotence. A unique index on (recurrence_id, occurrence_on) means running
//     the spawner twice cannot double up, so it is safe on a short tick, at boot,
//     and from cron simultaneously.
//   - Bounded catch-up. After an outage the spawner does not create every missed
//     occurrence back to the start date. See CatchUpDays.
package recurrence

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/teambition/rrule-go"

	"github.com/nls/checkmate/server/internal/database"
	"github.com/nls/checkmate/server/internal/store"
)

// CatchUpDays bounds how far back a missed occurrence is still created.
//
// The trade-off is real in both directions. Backfilling everything means coming
// back from two weeks away to fourteen "daily standup" tasks, which is noise
// that buries the real work. Backfilling nothing silently loses a weekly report
// that was due while the server was down. A week keeps a short outage lossless
// and a long one quiet.
const CatchUpDays = 7

// maxOccurrencesPerRun caps how many rows one template can produce in a single
// pass, so a malformed rule like FREQ=MINUTELY cannot fill the database before
// anyone notices.
const maxOccurrencesPerRun = 60

// Spawner materializes occurrences.
type Spawner struct {
	store *store.Store
	log   *slog.Logger

	// now is injectable so tests can place themselves on a specific day rather
	// than depending on when they run.
	now func() time.Time
}

// New builds a Spawner.
func New(st *store.Store, log *slog.Logger) *Spawner {
	return &Spawner{store: st, log: log, now: func() time.Time { return time.Now().UTC() }}
}

// Result reports what one pass did.
type Result struct {
	// Templates is how many recurrences were considered.
	Templates int

	// Created is how many task rows were written.
	Created int

	// Skipped counts occurrences that already existed, which is the normal
	// outcome of running again before the next occurrence is due.
	Skipped int

	// Missed counts occurrences dropped for falling outside the catch-up window.
	Missed int

	// Completed counts series that reached their end date and were deactivated.
	Completed int

	// Failed counts templates that could not be processed, usually an unparseable
	// rule. One bad template must not stop the others.
	Failed int
}

// Run makes one pass over every active recurrence that is due.
//
// This is a system process rather than a user request, so unlike the rest of the
// store it is not scoped to one user: it walks every account's templates. Each
// task it writes takes its user_id from the template, so the rows it creates stay
// correctly owned.
func (s *Spawner) Run(ctx context.Context) (Result, error) {
	var result Result

	now := s.now()

	// The horizon is the furthest date worth materializing: a template's own
	// lead_days is added per template, so this is only the outer bound.
	horizon := now.AddDate(0, 0, maxLeadDays).Format(database.DateOnly)

	templates, err := s.store.ListDueRecurrences(ctx, horizon)
	if err != nil {
		return result, err
	}

	result.Templates = len(templates)

	for _, template := range templates {
		if err := ctx.Err(); err != nil {
			return result, err
		}

		spawned, err := s.runOne(ctx, template, now)
		if err != nil {
			// A single unparseable rule should not stop every other series.
			result.Failed++

			s.log.Warn("could not spawn occurrences",
				slog.String("recurrence_id", template.ID),
				slog.String("rrule", template.RRule),
				slog.Any("error", err))

			continue
		}

		result.Created += spawned.Created
		result.Skipped += spawned.Skipped
		result.Missed += spawned.Missed
		result.Completed += spawned.Completed
	}

	return result, nil
}

// maxLeadDays bounds the query horizon; a template asking for more lead time than
// this simply gets its occurrences a little later.
const maxLeadDays = 90

// RunTemplate processes a single template, scoped to its owner.
//
// Called right after a template is created or edited so its occurrences appear in
// the same breath, rather than up to a scheduler tick later. Idempotent like the
// full pass, so it composes with the ticker rather than racing it.
//
// A template that is inactive or gone is not an error: the caller only knows it
// asked, and there is nothing to do.
func (s *Spawner) RunTemplate(ctx context.Context, userID, recurrenceID string) (Result, error) {
	template, err := s.store.GetDueRecurrence(ctx, userID, recurrenceID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return Result{}, nil
		}

		return Result{}, err
	}

	result, err := s.runOne(ctx, template, s.now())
	if err != nil {
		return Result{Templates: 1, Failed: 1}, err
	}

	result.Templates = 1

	return result, nil
}

// runOne processes a single template.
func (s *Spawner) runOne(ctx context.Context, template store.DueRecurrence, now time.Time) (Result, error) {
	var result Result

	location, err := time.LoadLocation(template.Timezone)
	if err != nil {
		// A bad zone would otherwise shift every occurrence by hours. UTC is the
		// safe fallback, and the mistake is surfaced rather than hidden.
		s.log.Warn("recurrence has an unknown timezone, falling back to UTC",
			slog.String("recurrence_id", template.ID),
			slog.String("timezone", template.Timezone))

		location = time.UTC
	}

	rule, err := buildRule(template, location)
	if err != nil {
		return result, err
	}

	// Dates are compared as plain YYYY-MM-DD in the template's own zone, which is
	// what makes "today" mean the user's today rather than the server's.
	earliest := now.In(location).AddDate(0, 0, -CatchUpDays).Format(database.DateOnly)
	latest := now.In(location).AddDate(0, 0, int(template.LeadDays)).Format(database.DateOnly)

	// Resume from where the series left off, or from its start.
	cursor := template.StartsOn
	if template.NextOccurrenceOn != nil && *template.NextOccurrenceOn != "" {
		cursor = *template.NextOccurrenceOn
	}

	var (
		nextOn      = cursor
		lastSpawned = template.LastSpawnedOn
		exhausted   bool
	)

	for range maxOccurrencesPerRun {
		occurrence, ok := nextOccurrence(rule, location, nextOn)
		if !ok {
			exhausted = true

			break
		}

		// Past the series end date: the template is finished.
		if template.EndsOn != nil && *template.EndsOn != "" && occurrence > *template.EndsOn {
			exhausted = true

			break
		}

		// Beyond the lead window, so not yet ours to create. Leave the cursor
		// here for the next pass.
		if occurrence > latest {
			nextOn = occurrence

			break
		}

		switch {
		case occurrence < earliest:
			// Older than the catch-up window: step over it without creating a
			// task, otherwise coming back from leave means a wall of stale rows.
			result.Missed++

		default:
			created, err := s.store.SpawnOccurrence(ctx, template, occurrence)
			if err != nil {
				return result, err
			}

			if created {
				result.Created++
			} else {
				// Already present. The unique index makes this the normal
				// outcome of a re-run, not an error.
				result.Skipped++
			}

			spawnedOn := occurrence
			lastSpawned = &spawnedOn
		}

		// Advance past this occurrence.
		following, ok := followingOccurrence(rule, location, occurrence)
		if !ok {
			exhausted = true
			nextOn = occurrence

			break
		}

		nextOn = following
	}

	// A series with nothing left to give is deactivated so it stops being
	// queried, rather than being deleted: its spawned tasks are real history.
	deactivate := exhausted

	if deactivate {
		result.Completed++
	}

	var nextPointer *string
	if !deactivate {
		value := nextOn
		nextPointer = &value
	}

	if err := s.store.AdvanceRecurrence(ctx, template.ID, nextPointer, lastSpawned, deactivate); err != nil {
		return result, err
	}

	return result, nil
}

// buildRule turns a stored rrule string into an evaluator anchored at starts_on.
func buildRule(template store.DueRecurrence, location *time.Location) (*rrule.RRule, error) {
	options, err := rrule.StrToROption(normalizeRRule(template.RRule))
	if err != nil {
		return nil, fmt.Errorf("recurrence: parse rrule %q: %w", template.RRule, err)
	}

	start, err := time.ParseInLocation(database.DateOnly, template.StartsOn, location)
	if err != nil {
		return nil, fmt.Errorf("recurrence: parse starts_on %q: %w", template.StartsOn, err)
	}

	// DTSTART anchors the series. Without it the library would anchor to now, so
	// a weekly rule would drift to whatever weekday the spawner first ran on.
	options.Dtstart = start

	rule, err := rrule.NewRRule(*options)
	if err != nil {
		return nil, fmt.Errorf("recurrence: build rrule %q: %w", template.RRule, err)
	}

	return rule, nil
}

// normalizeRRule tolerates a stored rule with or without the RRULE: prefix.
func normalizeRRule(raw string) string {
	if len(raw) >= 6 && (raw[:6] == "RRULE:" || raw[:6] == "rrule:") {
		return raw
	}

	return "RRULE:" + raw
}

// nextOccurrence returns the first occurrence on or after the given date.
func nextOccurrence(rule *rrule.RRule, location *time.Location, from string) (string, bool) {
	parsed, err := time.ParseInLocation(database.DateOnly, from, location)
	if err != nil {
		return "", false
	}

	// inc=true so a date that is itself an occurrence is returned rather than
	// skipped, which is what makes resuming from the stored cursor correct.
	next := rule.After(parsed.Add(-time.Second), true)
	if next.IsZero() {
		return "", false
	}

	return next.In(location).Format(database.DateOnly), true
}

// followingOccurrence returns the first occurrence strictly after a date.
func followingOccurrence(rule *rrule.RRule, location *time.Location, after string) (string, bool) {
	parsed, err := time.ParseInLocation(database.DateOnly, after, location)
	if err != nil {
		return "", false
	}

	// End of that day, so an intraday rule cannot return the same date twice and
	// stall the loop.
	endOfDay := parsed.Add(24*time.Hour - time.Second)

	next := rule.After(endOfDay, false)
	if next.IsZero() {
		return "", false
	}

	return next.In(location).Format(database.DateOnly), true
}

// ErrNoRule is returned when a template has no usable rule.
var ErrNoRule = errors.New("recurrence: template has no usable rrule")

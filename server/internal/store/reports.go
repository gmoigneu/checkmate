package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/nls/checkmate/server/internal/database"
	"github.com/nls/checkmate/server/internal/id"
	"github.com/nls/checkmate/server/internal/model"
)

const (
	ReportCategoryCompleted = "completed"
	ReportCategoryPending   = "pending"
	ReportCategoryBlocked   = "blocked"
	ReportCategoryDelegated = "delegated"
	ReportCategoryDropped   = "dropped"
	ReportCategoryInbox     = "inbox"
)

type ReportRequest struct {
	StartOn      string
	EndOn        string
	ContextIDs   []string
	IncludeInbox bool
	Focus        string
}

type ReportSource struct {
	StartOn       string                `json:"start_on"`
	EndOn         string                `json:"end_on"`
	Focus         string                `json:"focus,omitempty"`
	Contexts      []model.ReportContext `json:"contexts"`
	IncludeInbox  bool                  `json:"include_inbox"`
	Metrics       model.ReportMetrics   `json:"metrics"`
	Tasks         []ReportSourceTask    `json:"tasks"`
	LegacyHistory bool                  `json:"legacy_history"`
}

type ReportSourceTask struct {
	SourceID          string              `json:"source_id"`
	TaskID            string              `json:"task_id"`
	Title             string              `json:"title"`
	Details           *string             `json:"details,omitempty"`
	Category          string              `json:"category"`
	Status            string              `json:"status"`
	Priority          *string             `json:"priority,omitempty"`
	ContextName       string              `json:"context_name"`
	ProjectName       string              `json:"project_name,omitempty"`
	DelegateName      string              `json:"delegate_name,omitempty"`
	DueOn             *string             `json:"due_on,omitempty"`
	PlannedOn         *string             `json:"planned_on,omitempty"`
	Estimate          *int64              `json:"estimate_minutes,omitempty"`
	ParentID          *string             `json:"parent_id,omitempty"`
	RecurrenceID      *string             `json:"recurrence_id,omitempty"`
	OccurrenceOn      *string             `json:"occurrence_on,omitempty"`
	Reopened          bool                `json:"reopened,omitempty"`
	LaterDeleted      bool                `json:"later_deleted,omitempty"`
	PriorityCandidate bool                `json:"priority_candidate,omitempty"`
	Events            []ReportSourceEvent `json:"events"`
}

type ReportSourceEvent struct {
	Action        string   `json:"action"`
	ChangedFields []string `json:"changed_fields,omitempty"`
	StatusBefore  *string  `json:"status_before,omitempty"`
	StatusAfter   *string  `json:"status_after,omitempty"`
	OccurredAt    string   `json:"occurred_at"`
}

type reportActivity struct {
	TaskID        string
	TaskTitle     string
	Action        string
	ChangedFields string
	StatusBefore  *string
	StatusAfter   *string
	OccurredAt    string
	Snapshot      *model.TaskSnapshot
}

type taskHistory struct {
	state            *model.TaskSnapshot
	events           []reportActivity
	hasRangeActivity bool
	completedInRange bool
	droppedInRange   bool
	deletedInRange   bool
	reopenedInRange  bool
}

// BuildReportSource reconstructs every qualifying task at the report end
// boundary, then condenses its in-range activity into a model-safe source.
func (s *Store) BuildReportSource(
	ctx context.Context,
	userID string,
	in ReportRequest,
	startUTC, endUTC string,
) (ReportSource, error) {
	contexts, contextNames, err := s.reportContexts(ctx, userID, in.ContextIDs)
	if err != nil {
		return ReportSource{}, err
	}

	projects, err := s.reportNameMap(ctx, "projects", userID)
	if err != nil {
		return ReportSource{}, err
	}
	people, err := s.reportNameMap(ctx, "people", userID)
	if err != nil {
		return ReportSource{}, err
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT task_id, task_title, action, changed_fields,
		       status_before, status_after, occurred_at, snapshot_json
		FROM task_activity
		WHERE user_id = ? AND occurred_at < ?
		ORDER BY id`, userID, endUTC)
	if err != nil {
		return ReportSource{}, fmt.Errorf("store: read report activity: %w", err)
	}
	defer rows.Close()

	histories := map[string]*taskHistory{}
	legacy := false
	for rows.Next() {
		var (
			item       reportActivity
			before     sql.NullString
			after      sql.NullString
			snapshotJS sql.NullString
		)
		if err := rows.Scan(
			&item.TaskID, &item.TaskTitle, &item.Action, &item.ChangedFields,
			&before, &after, &item.OccurredAt, &snapshotJS,
		); err != nil {
			return ReportSource{}, fmt.Errorf("store: scan report activity: %w", err)
		}
		if before.Valid {
			item.StatusBefore = &before.String
		}
		if after.Valid {
			item.StatusAfter = &after.String
		}
		if snapshotJS.Valid {
			var snapshot model.TaskSnapshot
			if err := json.Unmarshal([]byte(snapshotJS.String), &snapshot); err != nil {
				return ReportSource{}, fmt.Errorf("store: decode task activity snapshot: %w", err)
			}
			normalizeSnapshotStatus(&snapshot)
			item.Snapshot = &snapshot
		}

		history := histories[item.TaskID]
		if history == nil {
			history = &taskHistory{}
			histories[item.TaskID] = history
		}
		if item.Snapshot != nil {
			copy := *item.Snapshot
			history.state = &copy
			if copy.Status == model.StatusExpired {
				expired := model.StatusExpired
				item.StatusAfter = &expired
			}
		}

		if item.OccurredAt >= startUTC {
			history.hasRangeActivity = true
			history.events = append(history.events, item)
			if item.Snapshot == nil {
				legacy = true
			}
			afterStatus := normalizedActivityStatus(item.StatusAfter, item.Snapshot)
			beforeStatus := normalizedActivityStatus(item.StatusBefore, nil)
			if afterStatus == model.StatusDone {
				history.completedInRange = true
			}
			if beforeStatus == model.StatusDone && isOpenReportStatus(afterStatus) {
				history.reopenedInRange = true
			}
			if afterStatus == model.StatusCancelled || afterStatus == model.StatusExpired {
				history.droppedInRange = true
			}
			if item.Action == "deleted" {
				history.deletedInRange = true
			}
		}
	}
	if err := rows.Err(); err != nil {
		return ReportSource{}, fmt.Errorf("store: read report activity: %w", err)
	}

	source := ReportSource{
		StartOn: in.StartOn, EndOn: in.EndOn, Focus: strings.TrimSpace(in.Focus),
		Contexts: contexts, IncludeInbox: in.IncludeInbox, LegacyHistory: legacy,
	}

	for taskID, history := range histories {
		if history.state == nil {
			continue
		}
		state := history.state
		contextName := "Inbox"
		if state.ContextID == nil {
			if !in.IncludeInbox {
				continue
			}
		} else {
			var selected bool
			contextName, selected = contextNames[*state.ContextID]
			if !selected {
				continue
			}
		}

		category := ""
		laterDeleted := state.DeletedAt != nil
		switch {
		case laterDeleted && state.Status == model.StatusDone && history.completedInRange:
			category = ReportCategoryCompleted
		case laterDeleted && history.deletedInRange:
			category = ReportCategoryDropped
		case laterDeleted:
			continue
		case state.Status == model.StatusDone && history.completedInRange:
			category = ReportCategoryCompleted
		case state.Status == model.StatusDone:
			continue
		case (state.Status == model.StatusCancelled || state.Status == model.StatusExpired) &&
			history.droppedInRange:
			category = ReportCategoryDropped
		case state.Status == model.StatusCancelled || state.Status == model.StatusExpired:
			continue
		default:
			if !history.hasRangeActivity && !dateInRange(state.PlannedOn, in.StartOn, in.EndOn) &&
				!dateInRange(state.DueOn, in.StartOn, in.EndOn) {
				continue
			}
			switch state.Status {
			case model.StatusInbox:
				category = ReportCategoryInbox
			case model.StatusBlocked:
				category = ReportCategoryBlocked
			case model.StatusDelegated:
				category = ReportCategoryDelegated
			default:
				category = ReportCategoryPending
			}
		}

		task := ReportSourceTask{
			TaskID: taskID, Title: state.Title, Details: state.Details,
			Category: category, Status: state.Status, Priority: state.Priority,
			ContextName: contextName, DueOn: state.DueOn, PlannedOn: state.PlannedOn,
			Estimate: state.EstimateMinutes, ParentID: state.ParentID,
			RecurrenceID: state.RecurrenceID, OccurrenceOn: state.OccurrenceOn,
			Reopened: history.reopenedInRange, LaterDeleted: laterDeleted,
		}
		if state.ProjectID != nil {
			task.ProjectName = projects[*state.ProjectID]
		}
		if state.DelegatedToID != nil {
			task.DelegateName = people[*state.DelegatedToID]
		}
		for _, event := range history.events {
			task.Events = append(task.Events, ReportSourceEvent{
				Action: event.Action, ChangedFields: splitChangedFields(event.ChangedFields),
				StatusBefore: event.StatusBefore, StatusAfter: event.StatusAfter,
				OccurredAt: event.OccurredAt,
			})
		}
		source.Tasks = append(source.Tasks, task)
		addReportMetric(&source.Metrics, task)
	}

	sort.Slice(source.Tasks, func(i, j int) bool {
		a, b := source.Tasks[i], source.Tasks[j]
		if categoryRank(a.Category) != categoryRank(b.Category) {
			return categoryRank(a.Category) < categoryRank(b.Category)
		}
		if priorityRank(a.Priority) != priorityRank(b.Priority) {
			return priorityRank(a.Priority) < priorityRank(b.Priority)
		}
		return strings.ToLower(a.Title) < strings.ToLower(b.Title)
	})
	for i := range source.Tasks {
		source.Tasks[i].SourceID = fmt.Sprintf("S%d", i+1)
	}
	markPriorityCandidates(source.Tasks, in.EndOn)

	return source, nil
}

func markPriorityCandidates(tasks []ReportSourceTask, endOn string) {
	type candidate struct {
		index int
		score int
	}
	var candidates []candidate
	nextPeriod := endOn
	if end, err := time.Parse(database.DateOnly, endOn); err == nil {
		nextPeriod = end.AddDate(0, 0, 14).Format(database.DateOnly)
	}
	for i, task := range tasks {
		if task.Category == ReportCategoryCompleted || task.Category == ReportCategoryDropped ||
			task.Category == ReportCategoryInbox {
			continue
		}
		score := priorityRank(task.Priority) * 20
		if task.DueOn != nil {
			switch {
			case *task.DueOn <= endOn:
				score -= 30
			case *task.DueOn <= nextPeriod:
				score -= 20
			}
		}
		if task.PlannedOn != nil && *task.PlannedOn <= nextPeriod {
			score -= 15
		}
		if task.Category == ReportCategoryBlocked || task.Category == ReportCategoryDelegated {
			score -= 10
		}
		candidates = append(candidates, candidate{i, score})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score < candidates[j].score
		}
		return strings.ToLower(tasks[candidates[i].index].Title) <
			strings.ToLower(tasks[candidates[j].index].Title)
	})
	if len(candidates) > 7 {
		candidates = candidates[:7]
	}
	for _, candidate := range candidates {
		tasks[candidate.index].PriorityCandidate = true
	}
}

func (s *Store) reportContexts(
	ctx context.Context,
	userID string,
	requested []string,
) ([]model.ReportContext, map[string]string, error) {
	requested = slices.Compact(requested)
	if len(requested) == 0 {
		return []model.ReportContext{}, map[string]string{}, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name FROM contexts
		WHERE user_id = ? AND deleted_at IS NULL`, userID)
	if err != nil {
		return nil, nil, fmt.Errorf("store: list report contexts: %w", err)
	}
	defer rows.Close()
	all := map[string]string{}
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, nil, fmt.Errorf("store: scan report context: %w", err)
		}
		all[id] = name
	}

	contexts := make([]model.ReportContext, 0, len(requested))
	selected := make(map[string]string, len(requested))
	for _, id := range requested {
		name, ok := all[id]
		if !ok {
			return nil, nil, &InvalidRefError{Field: "context_ids", ID: id}
		}
		contexts = append(contexts, model.ReportContext{ID: id, Name: name})
		selected[id] = name
	}
	sort.Slice(contexts, func(i, j int) bool { return contexts[i].Name < contexts[j].Name })
	return contexts, selected, nil
}

func (s *Store) reportNameMap(ctx context.Context, table, userID string) (map[string]string, error) {
	if table != "projects" && table != "people" {
		return nil, fmt.Errorf("store: unsupported report name table %q", table)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, name FROM `+table+` WHERE user_id = ?`, userID)
	if err != nil {
		return nil, fmt.Errorf("store: list report %s: %w", table, err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, fmt.Errorf("store: scan report %s: %w", table, err)
		}
		out[id] = name
	}
	return out, rows.Err()
}

func normalizeSnapshotStatus(snapshot *model.TaskSnapshot) {
	if snapshot.Status == model.StatusCancelled && snapshot.ExpiredAt != nil {
		snapshot.Status = model.StatusExpired
	}
}

func normalizedActivityStatus(status *string, snapshot *model.TaskSnapshot) string {
	if snapshot != nil {
		return snapshot.Status
	}
	if status == nil {
		return ""
	}
	return *status
}

func isOpenReportStatus(status string) bool {
	return status != "" && status != model.StatusDone && status != model.StatusCancelled &&
		status != model.StatusExpired
}

func dateInRange(value *string, startOn, endOn string) bool {
	return value != nil && *value >= startOn && *value <= endOn
}

func splitChangedFields(raw string) []string {
	if raw == "" {
		return []string{}
	}
	return strings.Split(raw, ",")
}

func categoryRank(category string) int {
	return slices.Index([]string{
		ReportCategoryCompleted, ReportCategoryPending, ReportCategoryBlocked,
		ReportCategoryDelegated, ReportCategoryDropped, ReportCategoryInbox,
	}, category)
}

func priorityRank(priority *string) int {
	if priority == nil {
		return 4
	}
	return slices.Index(model.TaskPriorities, *priority)
}

func addReportMetric(metrics *model.ReportMetrics, task ReportSourceTask) {
	switch task.Category {
	case ReportCategoryCompleted:
		metrics.Completed++
		if task.Estimate == nil {
			metrics.CompletedWithoutEstimate++
		} else {
			metrics.CompletedEstimateMinutes += *task.Estimate
		}
	case ReportCategoryDropped:
		metrics.Dropped++
	default:
		metrics.Open++
		if task.Estimate == nil {
			metrics.OpenWithoutEstimate++
		} else {
			metrics.OpenEstimateMinutes += *task.Estimate
		}
	}
	if task.Category == ReportCategoryBlocked {
		metrics.Blocked++
	}
	if task.Category == ReportCategoryDelegated {
		metrics.Delegated++
	}
	if task.Category == ReportCategoryInbox {
		metrics.Inbox++
	}
}

type ReportVersionCreate struct {
	ContentMarkdown string
	SourceSnapshot  string
	Model           string
	InputTokens     *int64
	OutputTokens    *int64
}

func (s *Store) CreateReport(
	ctx context.Context,
	userID, title string,
	in ReportRequest,
	version ReportVersionCreate,
) (model.Report, error) {
	reportID, versionID := id.New(), id.New()
	err := s.tx(ctx, func(tx *sql.Tx) error {
		includeInbox := 0
		if in.IncludeInbox {
			includeInbox = 1
		}
		_, err := tx.ExecContext(ctx, `
			INSERT INTO reports (id, user_id, title, start_on, end_on, focus, include_inbox)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, reportID, userID, title, in.StartOn, in.EndOn,
			emptyString(in.Focus), includeInbox)
		if err != nil {
			return fmt.Errorf("store: create report: %w", err)
		}
		for _, contextID := range in.ContextIDs {
			if err := assertOwned(ctx, tx, "context_id", contextID, userID); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO report_contexts (report_id, context_id) VALUES (?, ?)`,
				reportID, contextID); err != nil {
				return fmt.Errorf("store: add report context: %w", err)
			}
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO report_versions (
				id, report_id, version_number, content_markdown, source_snapshot,
				model, input_tokens, output_tokens
			) VALUES (?, ?, 1, ?, ?, ?, ?, ?)`,
			versionID, reportID, version.ContentMarkdown, version.SourceSnapshot,
			version.Model, version.InputTokens, version.OutputTokens)
		if err != nil {
			return fmt.Errorf("store: create report version: %w", err)
		}
		return nil
	})
	if err != nil {
		return model.Report{}, err
	}
	return s.GetReport(ctx, userID, reportID)
}

func (s *Store) ListReports(ctx context.Context, userID string) ([]model.Report, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT r.id, r.title, r.start_on, r.end_on, r.focus, r.include_inbox,
		       coalesce(max(v.version_number), 0), r.created_at, r.updated_at
		FROM reports r
		LEFT JOIN report_versions v ON v.report_id = r.id
		WHERE r.user_id = ?
		GROUP BY r.id
		ORDER BY r.updated_at DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("store: list reports: %w", err)
	}
	var reports []model.Report
	for rows.Next() {
		item, err := scanReport(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		reports = append(reports, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range reports {
		reports[i].Contexts, err = s.listReportContexts(ctx, reports[i].ID)
		if err != nil {
			return nil, err
		}
	}
	return reports, nil
}

func (s *Store) GetReport(ctx context.Context, userID, reportID string) (model.Report, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT r.id, r.title, r.start_on, r.end_on, r.focus, r.include_inbox,
		       coalesce(max(v.version_number), 0), r.created_at, r.updated_at
		FROM reports r
		LEFT JOIN report_versions v ON v.report_id = r.id
		WHERE r.id = ? AND r.user_id = ?
		GROUP BY r.id`, reportID, userID)
	report, err := scanReport(row)
	if err != nil {
		return model.Report{}, notFoundOr(err, "get report")
	}
	report.Contexts, err = s.listReportContexts(ctx, reportID)
	if err != nil {
		return model.Report{}, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, version_number, content_markdown, model, input_tokens,
		       output_tokens, created_at, updated_at
		FROM report_versions WHERE report_id = ? ORDER BY version_number DESC`, reportID)
	if err != nil {
		return model.Report{}, fmt.Errorf("store: list report versions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var version model.ReportVersion
		if err := rows.Scan(
			&version.ID, &version.VersionNumber, &version.ContentMarkdown, &version.Model,
			&version.InputTokens, &version.OutputTokens, &version.CreatedAt, &version.UpdatedAt,
		); err != nil {
			return model.Report{}, fmt.Errorf("store: scan report version: %w", err)
		}
		report.Versions = append(report.Versions, version)
	}
	return report, rows.Err()
}

func scanReport(scanner interface{ Scan(...any) error }) (model.Report, error) {
	var report model.Report
	var focus sql.NullString
	var inbox int
	if err := scanner.Scan(
		&report.ID, &report.Title, &report.StartOn, &report.EndOn, &focus, &inbox,
		&report.LatestVersion, &report.CreatedAt, &report.UpdatedAt,
	); err != nil {
		return model.Report{}, err
	}
	if focus.Valid {
		report.Focus = &focus.String
	}
	report.IncludeInbox = inbox == 1
	return report, nil
}

func (s *Store) listReportContexts(ctx context.Context, reportID string) ([]model.ReportContext, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.id, c.name FROM report_contexts rc
		JOIN contexts c ON c.id = rc.context_id
		WHERE rc.report_id = ? ORDER BY c.name`, reportID)
	if err != nil {
		return nil, fmt.Errorf("store: list saved report contexts: %w", err)
	}
	defer rows.Close()
	contexts := []model.ReportContext{}
	for rows.Next() {
		var item model.ReportContext
		if err := rows.Scan(&item.ID, &item.Name); err != nil {
			return nil, fmt.Errorf("store: scan saved report context: %w", err)
		}
		contexts = append(contexts, item)
	}
	return contexts, rows.Err()
}

func (s *Store) OriginalReportSource(ctx context.Context, userID, reportID string) (string, error) {
	var source string
	err := s.db.QueryRowContext(ctx, `
		SELECT v.source_snapshot
		FROM report_versions v JOIN reports r ON r.id = v.report_id
		WHERE r.id = ? AND r.user_id = ? AND v.version_number = 1`, reportID, userID).
		Scan(&source)
	if err != nil {
		return "", notFoundOr(err, "get original report source")
	}
	return source, nil
}

func (s *Store) AddReportVersion(
	ctx context.Context,
	userID, reportID string,
	version ReportVersionCreate,
) (model.Report, error) {
	err := s.tx(ctx, func(tx *sql.Tx) error {
		var next int64
		if err := tx.QueryRowContext(ctx, `
			SELECT coalesce(max(v.version_number), 0) + 1
			FROM reports r LEFT JOIN report_versions v ON v.report_id = r.id
			WHERE r.id = ? AND r.user_id = ?`, reportID, userID).Scan(&next); err != nil {
			return notFoundOr(err, "get next report version")
		}
		if next == 1 {
			return ErrNotFound
		}
		_, err := tx.ExecContext(ctx, `
			INSERT INTO report_versions (
				id, report_id, version_number, content_markdown, source_snapshot,
				model, input_tokens, output_tokens
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, id.New(), reportID, next,
			version.ContentMarkdown, version.SourceSnapshot, version.Model,
			version.InputTokens, version.OutputTokens)
		if err != nil {
			return fmt.Errorf("store: add report version: %w", err)
		}
		_, err = tx.ExecContext(ctx,
			`UPDATE reports SET updated_at = `+nowExpr+` WHERE id = ? AND user_id = ?`,
			reportID, userID)
		return err
	})
	if err != nil {
		return model.Report{}, err
	}
	return s.GetReport(ctx, userID, reportID)
}

func (s *Store) UpdateReportDraft(
	ctx context.Context,
	userID, reportID string,
	title, content *string,
	expectedVersion *int64,
) (model.Report, error) {
	err := s.tx(ctx, func(tx *sql.Tx) error {
		var latest int64
		if err := tx.QueryRowContext(ctx, `
			SELECT coalesce(max(v.version_number), 0)
			FROM reports r LEFT JOIN report_versions v ON v.report_id = r.id
			WHERE r.id = ? AND r.user_id = ?`, reportID, userID).Scan(&latest); err != nil {
			return err
		}
		if latest == 0 {
			return ErrNotFound
		}
		if title != nil {
			trimmed := strings.TrimSpace(*title)
			if trimmed == "" {
				return &ConflictError{Field: "title", Detail: "cannot be empty"}
			}
			if _, err := tx.ExecContext(ctx,
				`UPDATE reports SET title = ?, updated_at = `+nowExpr+` WHERE id = ? AND user_id = ?`,
				trimmed, reportID, userID); err != nil {
				return fmt.Errorf("store: update report title: %w", err)
			}
		}
		if content != nil {
			if expectedVersion == nil || *expectedVersion != latest {
				return &ConflictError{
					Field: "version_number", Detail: "the editable report version changed; reload before saving",
				}
			}
			if _, err := tx.ExecContext(ctx, `
				UPDATE report_versions SET content_markdown = ?, updated_at = `+nowExpr+`
				WHERE report_id = ? AND version_number = ?`, *content, reportID, latest); err != nil {
				return fmt.Errorf("store: update report draft: %w", err)
			}
			if _, err := tx.ExecContext(ctx,
				`UPDATE reports SET updated_at = `+nowExpr+` WHERE id = ? AND user_id = ?`,
				reportID, userID); err != nil {
				return fmt.Errorf("store: touch report: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return model.Report{}, err
	}
	return s.GetReport(ctx, userID, reportID)
}

func (s *Store) DeleteReport(ctx context.Context, userID, reportID string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM reports WHERE id = ? AND user_id = ?`, reportID, userID)
	if err != nil {
		return fmt.Errorf("store: delete report: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: delete report: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func emptyString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return strings.TrimSpace(value)
}

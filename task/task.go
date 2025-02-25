// task service manages tasks associated with boards, including creation, updates,
// listing, and deletion. It subscribes to board deletion events to handle cascading
// deletes and uses a separate database for tasks.
package task

import (
	"context"
	"time"

	"encore.app/board"
	"encore.dev/beta/auth"
	"encore.dev/beta/errs"
	"encore.dev/pubsub"
	"encore.dev/storage/sqldb"
)

// taskDB is the database instance for the task service, managing the tasks table.
var taskDB = sqldb.NewDatabase("tasks", sqldb.DatabaseConfig{
	Migrations: "./migrations",
})

// init subscribes to the BoardDeletedTopic to delete tasks when a board is removed.
// This ensures cascading deletion across separate databases.
var _ = pubsub.NewSubscription(
	board.BoardDeletedTopic, "delete-tasks-on-board-deletion",
	pubsub.SubscriptionConfig[*board.BoardDeletedEvent]{
		Handler: handleBoardDeleteEvent,
	},
)

// board-delete event handler
func handleBoardDeleteEvent(ctx context.Context, event *board.BoardDeletedEvent) error {
	_, err := taskDB.Exec(ctx, `
		DELETE FROM tasks
		WHERE board_id = $1
	`, event.BoardID)
	if err != nil {
		return errs.B().Code(errs.Internal).Msg("failed to delete tasks for board").Cause(err).Err()
	}
	return nil
}

// CreateTaskParams defines the input parameters for creating a new task.
type CreateTaskParams struct {
	BoardID     string `json:"board_id"`              // target board id
	Title       string `json:"title"`                 // task title
	Description string `json:"description,omitempty"` // task description (optional)
	AssigneeID  string `json:"assignee_id,omitempty"` // user id of assignee (optional)
	Stage       string `json:"stage,omitempty"`       // stage of the task only ('To Do' -- default, 'In Progress', 'Done') (optional)
}

// TaskResponse represents the response returned when a task is created or updated.
type TaskResponse struct {
	ID          string `json:"id"`                    // task id
	BoardID     string `json:"board_id"`              // target board id
	Title       string `json:"title"`                 // task title
	Description string `json:"description,omitempty"` // task description
	CreatedBy   string `json:"created_by"`            // owner id
	AssigneeID  string `json:"assignee_id,omitempty"` // user id of assignee
	Stage       string `json:"stage,omitempty"`       // task stage
	CreatedAt   string `json:"created_at"`            // time of task creation
	UpdatedAt   string `json:"updated_at,omitempty"`  // time of last updation
}

// CreateTask creates a new task on a board, restricted to Admins and Members.
//
//encore:api auth method=POST path=/task
func CreateTask(ctx context.Context, p *CreateTaskParams) (*TaskResponse, error) {
	uid, ok := auth.UserID()
	if !ok {
		return nil, errs.B().Code(errs.Unauthenticated).Msg("authentication required").Err()
	}

	if p.BoardID == "" || p.Title == "" {
		return nil, errs.B().Code(errs.InvalidArgument).Msg("board_id and title are required").Err()
	}

	membership, err := board.CheckMembership(ctx, p.BoardID)
	if err != nil {
		return nil, errs.B().Code(errs.Internal).Msg("failed to check membership").Cause(err).Err()
	}
	if !membership.IsMember {
		return nil, errs.B().Code(errs.PermissionDenied).Msg("access denied: must be a board member").Err()
	}
	if membership.Role == "Viewer" {
		return nil, errs.B().Code(errs.PermissionDenied).Msg("access denied: only Admins and Members can create tasks").Err()
	}

	stage := p.Stage
	if stage == "" {
		stage = "To Do"
	}
	if stage != "To Do" && stage != "In Progress" && stage != "Done" {
		return nil, errs.B().Code(errs.InvalidArgument).Msg("stage must be 'To Do', 'In Progress', or 'Done'").Err()
	}

	var id string
	now := time.Now().Format(time.RFC3339)
	err = taskDB.QueryRow(ctx, `
        INSERT INTO tasks (board_id, title, description, created_by, assignee_id, stage, created_at, updated_at)
        VALUES ($1, $2, $3, $4, $5, $6, $7, $7)
        RETURNING id
    `, p.BoardID, p.Title, p.Description, uid, p.AssigneeID, stage, now).Scan(&id)
	if err != nil {
		return nil, errs.B().Code(errs.Internal).Msg("failed to create task").Cause(err).Err()
	}

	return &TaskResponse{
		ID:          id,
		BoardID:     p.BoardID,
		Title:       p.Title,
		Description: p.Description,
		CreatedBy:   string(uid),
		AssigneeID:  p.AssigneeID,
		Stage:       stage,
		CreatedAt:   now,
	}, nil
}

// UpdateTaskParams defines the input parameters for updating an existing task.
type UpdateTaskParams struct {
	Title       string `json:"title,omitempty"`       // new title
	Description string `json:"description,omitempty"` // new description
	AssigneeID  string `json:"assignee_id,omitempty"` // new assigned user id
	Stage       string `json:"stage,omitempty"`       // new stage of the task
}

// UpdateTask updates an existing task, restricted to Admins or the task creator.
//
//encore:api auth method=PUT path=/task/:taskID
func UpdateTask(ctx context.Context, taskID string, p *UpdateTaskParams) (*TaskResponse, error) {
	uid, ok := auth.UserID()
	if !ok {
		return nil, errs.B().Code(errs.Unauthenticated).Msg("authentication required").Err()
	}

	var boardID, createdBy, currentTitle, currentDesc, currentAssignee, currentStage string
	var createdAt, updatedAt time.Time
	err := taskDB.QueryRow(ctx, `
        SELECT board_id, title, description, created_by, assignee_id, stage, created_at, updated_at
        FROM tasks
        WHERE id = $1
    `, taskID).Scan(&boardID, &currentTitle, &currentDesc, &createdBy, &currentAssignee, &currentStage, &createdAt, &updatedAt)
	if err != nil {
		if err == sqldb.ErrNoRows {
			return nil, errs.B().Code(errs.NotFound).Msg("task not found").Err()
		}
		return nil, errs.B().Code(errs.Internal).Msg("failed to fetch task").Cause(err).Err()
	}

	membership, err := board.CheckMembership(ctx, boardID)
	if err != nil {
		return nil, errs.B().Code(errs.Internal).Msg("failed to check membership").Cause(err).Err()
	}
	if !membership.IsMember {
		return nil, errs.B().Code(errs.PermissionDenied).Msg("access denied: must be a board member to update task").Err()
	}
	if membership.Role != "Admin" && createdBy != string(uid) {
		return nil, errs.B().Code(errs.PermissionDenied).Msg("access denied: only Admin or creator can update task").Err()
	}

	newTitle := currentTitle
	if p.Title != "" {
		newTitle = p.Title
	}
	newDesc := currentDesc
	if p.Description != "" {
		newDesc = p.Description
	}
	newAssignee := currentAssignee
	if p.AssigneeID != "" {
		newAssignee = p.AssigneeID
	}
	newStage := currentStage
	if p.Stage != "" {
		if p.Stage != "To Do" && p.Stage != "In Progress" && p.Stage != "Done" {
			return nil, errs.B().Code(errs.InvalidArgument).Msg("stage must be 'To Do', 'In Progress', or 'Done'").Err()
		}
		newStage = p.Stage
	}
	newUpdatedAt := time.Now().Format(time.RFC3339)

	_, err = taskDB.Exec(ctx, `
        UPDATE tasks
        SET title = $1, description = $2, assignee_id = $3, stage = $4, updated_at = $5
        WHERE id = $6
    `, newTitle, newDesc, newAssignee, newStage, newUpdatedAt, taskID)
	if err != nil {
		return nil, errs.B().Code(errs.Internal).Msg("failed to update task").Cause(err).Err()
	}

	return &TaskResponse{
		ID:          taskID,
		BoardID:     boardID,
		Title:       newTitle,
		Description: newDesc,
		CreatedBy:   createdBy,
		AssigneeID:  newAssignee,
		Stage:       newStage,
		CreatedAt:   createdAt.Format(time.RFC3339),
		UpdatedAt:   newUpdatedAt,
	}, nil
}

// ListTasksParams defines the query parameters for filtering and paginating tasks.
type ListTasksParams struct {
	Stage  string `query:"stage,omitempty"`    // Filter by stage: "To Do", "In Progress", "Done"
	Limit  int    `query:"limit" default:"10"` // Number of tasks to return
	Offset int    `query:"offset" default:"0"` // Number of tasks to skip
}

// ListTasksResponse represents a paginated list of tasks for a board.
type ListTasksResponse struct {
	Tasks []TaskResponse `json:"tasks"` // List of tasks
	Total int            `json:"total"` // Total number of matching tasks
}

// ListTasks retrieves a paginated list of tasks for a board, optionally filtered by stage,
// accessible to Admins and Members only.
//
//encore:api auth method=GET path=/board/:boardID/tasks
func ListTasks(ctx context.Context, boardID string, p *ListTasksParams) (*ListTasksResponse, error) {
	_, ok := auth.UserID()
	if !ok {
		return nil, errs.B().Code(errs.Unauthenticated).Msg("authentication required").Err()
	}

	membership, err := board.CheckMembership(ctx, boardID)
	if err != nil {
		return nil, errs.B().Code(errs.Internal).Msg("failed to check membership").Cause(err).Err()
	}
	if !membership.IsMember {
		return nil, errs.B().Code(errs.PermissionDenied).Msg("access denied: must be a board member to list tasks").Err()
	}
	if membership.Role == "Viewer" {
		return nil, errs.B().Code(errs.PermissionDenied).Msg("access denied: only Admins and Members can list tasks").Err()
	}

	if p.Stage != "" && p.Stage != "To Do" && p.Stage != "In Progress" && p.Stage != "Done" {
		return nil, errs.B().Code(errs.InvalidArgument).Msg("stage must be 'To Do', 'In Progress', or 'Done'").Err()
	}
	if p.Limit <= 0 || p.Offset < 0 {
		return nil, errs.B().Code(errs.InvalidArgument).Msg("limit must be positive and offset non-negative").Err()
	}

	// Count total tasks for pagination
	var total int
	countQuery := `
        SELECT COUNT(*) FROM tasks
        WHERE board_id = $1
    `
	if p.Stage != "" {
		countQuery += " AND stage = $2"
	}
	countArgs := []any{boardID}
	if p.Stage != "" {
		countArgs = append(countArgs, p.Stage)
	}
	err = taskDB.QueryRow(ctx, countQuery, countArgs...).Scan(&total)
	if err != nil {
		return nil, errs.B().Code(errs.Internal).Msg("failed to count tasks").Cause(err).Err()
	}

	// Fetch paginated tasks
	query := `
        SELECT id, board_id, title, description, created_by, assignee_id, stage, created_at, updated_at
        FROM tasks
        WHERE board_id = $1
    `
	args := []any{boardID}
	if p.Stage != "" {
		query += " AND stage = $2"
		args = append(args, p.Stage)
	}
	query += " ORDER BY created_at LIMIT $2 OFFSET $3"
	if p.Stage == "" {
		query = `
            SELECT id, board_id, title, description, created_by, assignee_id, stage, created_at, updated_at
            FROM tasks
            WHERE board_id = $1
            ORDER BY created_at LIMIT $2 OFFSET $3
        `
		args = append(args, p.Limit, p.Offset)
	} else {
		args = append(args, p.Limit, p.Offset)
	}

	rows, err := taskDB.Query(ctx, query, args...)
	if err != nil {
		return nil, errs.B().Code(errs.Internal).Msg("failed to fetch tasks").Cause(err).Err()
	}
	defer rows.Close()

	var tasks []TaskResponse
	for rows.Next() {
		var t TaskResponse
		var createdAt, updatedAt time.Time
		if err := rows.Scan(&t.ID, &t.BoardID, &t.Title, &t.Description, &t.CreatedBy, &t.AssigneeID, &t.Stage, &createdAt, &updatedAt); err != nil {
			return nil, errs.B().Code(errs.Internal).Msg("failed to scan task").Cause(err).Err()
		}
		t.CreatedAt = createdAt.Format(time.RFC3339)
		t.UpdatedAt = updatedAt.Format(time.RFC3339)
		tasks = append(tasks, t)
	}

	if err := rows.Err(); err != nil {
		return nil, errs.B().Code(errs.Internal).Msg("error reading tasks").Cause(err).Err()
	}

	return &ListTasksResponse{
		Tasks: tasks,
		Total: total,
	}, nil
}

// DeleteTaskResponse represents the response when a task is deleted.
type DeleteTaskResponse struct {
	Message string `json:"message"`
}

// DeleteTask deletes a specific task, restricted to Admins or the task creator.
//
//encore:api auth method=DELETE path=/task/:taskID
func DeleteTask(ctx context.Context, taskID string) (*DeleteTaskResponse, error) {
	uid, ok := auth.UserID()
	if !ok {
		return nil, errs.B().Code(errs.Unauthenticated).Msg("authentication required").Err()
	}

	var boardID, createdBy string
	err := taskDB.QueryRow(ctx, `
        SELECT board_id, created_by
        FROM tasks
        WHERE id = $1
    `, taskID).Scan(&boardID, &createdBy)
	if err != nil {
		if err == sqldb.ErrNoRows {
			return nil, errs.B().Code(errs.NotFound).Msg("task not found").Err()
		}
		return nil, errs.B().Code(errs.Internal).Msg("failed to fetch task").Cause(err).Err()
	}

	membership, err := board.CheckMembership(ctx, boardID)
	if err != nil {
		return nil, errs.B().Code(errs.Internal).Msg("failed to check membership").Cause(err).Err()
	}
	if !membership.IsMember {
		return nil, errs.B().Code(errs.PermissionDenied).Msg("access denied: must be a board member to delete task").Err()
	}
	if membership.Role != "Admin" && createdBy != string(uid) {
		return nil, errs.B().Code(errs.PermissionDenied).Msg("access denied: only Admin or creator can delete task").Err()
	}

	result, err := taskDB.Exec(ctx, `
        DELETE FROM tasks
        WHERE id = $1
    `, taskID)
	if err != nil {
		return nil, errs.B().Code(errs.Internal).Msg("failed to delete task").Cause(err).Err()
	}

	rowsAffected := result.RowsAffected()

	if rowsAffected == 0 {
		return nil, errs.B().Code(errs.NotFound).Msg("task not found").Err()
	}

	return &DeleteTaskResponse{Message: "Task deleted successfully"}, nil
}

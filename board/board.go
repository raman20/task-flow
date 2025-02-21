// board service handles board-related operations such as creation, membership management,
// invitations, and deletion. It uses a separate database for boards and publishes events
// for cascading deletes to other services.
package board

import (
	"context"
	"time"

	"encore.dev/beta/auth"
	"encore.dev/beta/errs"
	"encore.dev/pubsub"
	"encore.dev/storage/sqldb"
)

// boardDB is the database instance for the board service, managing tables like boards,
// board_members, and invitations.
var boardDB = sqldb.NewDatabase("boards", sqldb.DatabaseConfig{
	Migrations: "./migrations",
})

// BoardDeletedEvent represents an event published when a board is deleted, used for
// cascading deletes in other services (e.g., tasks).
type BoardDeletedEvent struct {
	BoardID string `json:"board_id"`
}

// BoardDeletedTopic is a Pub/Sub topic for notifying subscribers (e.g., task service)
// when a board is deleted.
var BoardDeletedTopic = pubsub.NewTopic[*BoardDeletedEvent]("board-deleted", pubsub.TopicConfig{
	DeliveryGuarantee: pubsub.AtLeastOnce,
})

// CreateBoardParams defines the input parameters for creating a new board.
type CreateBoardParams struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// BoardResponse represents the response returned when a board is created or retrieved.
type BoardResponse struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	CreatedBy   string `json:"created_by"`
	CreatedAt   string `json:"created_at"` // ISO 8601 string
}

// CreateBoard creates a new board and assigns the authenticated user as its Admin.
//
//encore:api auth method=POST path=/board
func CreateBoard(ctx context.Context, p *CreateBoardParams) (*BoardResponse, error) {
	uid, ok := auth.UserID()
	if !ok {
		return nil, errs.B().Code(errs.Unauthenticated).Msg("authentication required").Err()
	}

	if p.Name == "" {
		return nil, errs.B().Code(errs.InvalidArgument).Msg("name is required").Err()
	}

	var boardID string
	err := boardDB.QueryRow(ctx, `
        INSERT INTO boards (name, description, created_by)
        VALUES ($1, $2, $3)
        RETURNING id
    `, p.Name, p.Description, uid).Scan(&boardID)
	if err != nil {
		return nil, errs.B().Code(errs.Internal).Msg("failed to create board").Cause(err).Err()
	}

	_, err = boardDB.Exec(ctx, `
        INSERT INTO board_members (board_id, user_id, role)
        VALUES ($1, $2, 'Admin')
    `, boardID, uid)
	if err != nil {
		return nil, errs.B().Code(errs.Internal).Msg("failed to assign admin role").Cause(err).Err()
	}

	return &BoardResponse{
		ID:          boardID,
		Name:        p.Name,
		Description: p.Description,
		CreatedBy:   string(uid),
		CreatedAt:   time.Now().Format(time.RFC3339),
	}, nil
}

// InviteUserParams defines the input parameters for inviting a user to a board.
type InviteUserParams struct {
	BoardID   string `json:"board_id"`
	InviteeID string `json:"invitee_id"`
	Role      string `json:"role"` // Must be "Member" or "Viewer"
}

// InviteResponse represents the response when an invitation is created.
type InviteResponse struct {
	InvitationID string `json:"invitation_id"`
}

// InviteUser invites a user to a board, restricted to Admins only.
//
//encore:api auth method=POST path=/board/invite
func InviteUser(ctx context.Context, p *InviteUserParams) (*InviteResponse, error) {
	uid, ok := auth.UserID()
	if !ok {
		return nil, errs.B().Code(errs.Unauthenticated).Msg("authentication required").Err()
	}

	if p.BoardID == "" || p.InviteeID == "" || p.Role == "" {
		return nil, errs.B().Code(errs.InvalidArgument).Msg("board_id, invitee_id, and role are required").Err()
	}

	var role string
	err := boardDB.QueryRow(ctx, `
        SELECT role FROM board_members
        WHERE board_id = $1 AND user_id = $2
    `, p.BoardID, uid).Scan(&role)
	if err != nil || role != "Admin" {
		return nil, errs.B().Code(errs.PermissionDenied).Msg("only Admin can invite users").Err()
	}

	if p.Role != "Member" && p.Role != "Viewer" {
		return nil, errs.B().Code(errs.InvalidArgument).Msg("role must be 'Member' or 'Viewer'").Err()
	}

	var invitationID string
	err = boardDB.QueryRow(ctx, `
        INSERT INTO invitations (board_id, inviter_id, invitee_id, role, status)
        VALUES ($1, $2, $3, $4, 'Pending')
        RETURNING id
    `, p.BoardID, uid, p.InviteeID, p.Role).Scan(&invitationID)
	if err != nil {
		return nil, errs.B().Code(errs.Internal).Msg("failed to create invitation").Cause(err).Err()
	}

	return &InviteResponse{InvitationID: invitationID}, nil
}

// HandleInvitationParams defines the input for accepting or rejecting an invitation.
type HandleInvitationParams struct {
	InvitationID string `json:"invitation_id"`
	Action       string `json:"action"` // "accept" or "reject"
}

// HandleInvitationResponse represents the response when an invitation is handled.
type HandleInvitationResponse struct {
	BoardID string `json:"board_id"`
}

// HandleInvitation allows the invitee to accept or reject an invitation.
//
//encore:api auth method=PATCH path=/board/invitation
func HandleInvitation(ctx context.Context, p *HandleInvitationParams) (*HandleInvitationResponse, error) {
	uid, ok := auth.UserID()
	if !ok {
		return nil, errs.B().Code(errs.Unauthenticated).Msg("authentication required").Err()
	}

	if p.InvitationID == "" || p.Action == "" {
		return nil, errs.B().Code(errs.InvalidArgument).Msg("invitation_id and action are required").Err()
	}

	if p.Action != "Accepted" && p.Action != "Rejected" {
		return nil, errs.B().Code(errs.InvalidArgument).Msg("action must be 'Accepted' or 'Rejected'").Err()
	}

	var boardID, status, role string
	err := boardDB.QueryRow(ctx, `
        SELECT board_id, status, role
        FROM invitations
        WHERE id = $1 AND invitee_id = $2
    `, p.InvitationID, uid).Scan(&boardID, &status, &role)
	if err != nil {
		if err == sqldb.ErrNoRows {
			return nil, errs.B().Code(errs.NotFound).Msg("invitation not found or not for this user").Err()
		}
		return nil, errs.B().Code(errs.Internal).Msg("failed to fetch invitation").Cause(err).Err()
	}
	if status != "Pending" {
		return nil, errs.B().Code(errs.FailedPrecondition).Msg("invitation already processed").Err()
	}

	if p.Action == "Accepted" {
		_, err = boardDB.Exec(ctx, `
            INSERT INTO board_members (board_id, user_id, role)
            VALUES ($1, $2, $3)
            ON CONFLICT DO NOTHING
        `, boardID, uid, role)
		if err != nil {
			return nil, errs.B().Code(errs.Internal).Msg("failed to add user to board").Cause(err).Err()
		}
	}

	_, err = boardDB.Exec(ctx, `
        UPDATE invitations
        SET status = $1
        WHERE id = $2
    `, p.Action, p.InvitationID)
	if err != nil {
		return nil, errs.B().Code(errs.Internal).Msg("failed to update invitation status").Cause(err).Err()
	}

	return &HandleInvitationResponse{BoardID: boardID}, nil
}

// GetBoard retrieves the details of a specific board, accessible only to its members.
//
//encore:api auth method=GET path=/board/:boardID
func GetBoard(ctx context.Context, boardID string) (*BoardResponse, error) {
	uid, ok := auth.UserID()
	if !ok {
		return nil, errs.B().Code(errs.Unauthenticated).Msg("authentication required").Err()
	}

	var exists bool
	err := boardDB.QueryRow(ctx, `
        SELECT EXISTS (
            SELECT 1 FROM board_members
            WHERE board_id = $1 AND user_id = $2
        )
    `, boardID, uid).Scan(&exists)
	if err != nil {
		return nil, errs.B().Code(errs.Internal).Msg("failed to check membership").Cause(err).Err()
	}
	if !exists {
		return nil, errs.B().Code(errs.PermissionDenied).Msg("access denied: not a member of this board").Err()
	}

	var resp BoardResponse
	err = boardDB.QueryRow(ctx, `
        SELECT id, name, description, created_by, created_at
        FROM boards
        WHERE id = $1
    `, boardID).Scan(&resp.ID, &resp.Name, &resp.Description, &resp.CreatedBy, &resp.CreatedAt)
	if err != nil {
		if err == sqldb.ErrNoRows {
			return nil, errs.B().Code(errs.NotFound).Msg("board not found").Err()
		}
		return nil, errs.B().Code(errs.Internal).Msg("failed to fetch board").Cause(err).Err()
	}

	return &resp, nil
}

// InvitationResponse represents a single invitation with board details.
type InvitationResponse struct {
	InvitationID string `json:"invitation_id"`
	BoardID      string `json:"board_id"`
	BoardName    string `json:"board_name"`
	InviterID    string `json:"inviter_id"`
	CreatedAt    string `json:"created_at"` // ISO 8601 string
}

// ListInvitationsResponse represents a list of invitations for the authenticated user.
type ListInvitationsResponse struct {
	Invitations []InvitationResponse `json:"invitations"`
}

// ListInvitations retrieves all invitations for the authenticated user by status.
//
//encore:api auth method=GET path=/invitations/:status
func ListInvitations(ctx context.Context, status string) (*ListInvitationsResponse, error) {
	uid, ok := auth.UserID()
	if !ok {
		return nil, errs.B().Code(errs.Unauthenticated).Msg("authentication required").Err()
	}

	if status != "Pending" && status != "Accepted" && status != "Rejected" {
		return nil, errs.B().Code(errs.InvalidArgument).Msg("status must be 'Pending', 'Accepted', or 'Rejected'").Err()
	}

	rows, err := boardDB.Query(ctx, `
        SELECT i.id, i.board_id, b.name, i.inviter_id, i.created_at
        FROM invitations i
        JOIN boards b ON i.board_id = b.id
        WHERE i.invitee_id = $1 AND i.status = $2
        ORDER BY i.created_at DESC
    `, uid, status)
	if err != nil {
		return nil, errs.B().Code(errs.Internal).Msg("failed to fetch invitations").Cause(err).Err()
	}
	defer rows.Close()

	var invitations []InvitationResponse
	for rows.Next() {
		var inv InvitationResponse
		if err := rows.Scan(&inv.InvitationID, &inv.BoardID, &inv.BoardName, &inv.InviterID, &inv.CreatedAt); err != nil {
			return nil, errs.B().Code(errs.Internal).Msg("failed to scan invitation").Cause(err).Err()
		}
		invitations = append(invitations, inv)
	}

	if err := rows.Err(); err != nil {
		return nil, errs.B().Code(errs.Internal).Msg("error reading invitations").Cause(err).Err()
	}

	return &ListInvitationsResponse{Invitations: invitations}, nil
}

// RemoveUserResponse represents the response when a user is removed from a board.
type RemoveUserResponse struct {
	Message string `json:"message"`
}

// RemoveUser removes a user from a board, allowed by Admins or the user themselves.
//
//encore:api auth method=DELETE path=/board/:boardID/user/:userID
func RemoveUser(ctx context.Context, boardID, userID string) (*RemoveUserResponse, error) {
	uid, ok := auth.UserID()
	if !ok {
		return nil, errs.B().Code(errs.Unauthenticated).Msg("authentication required").Err()
	}

	var role string
	err := boardDB.QueryRow(ctx, `
        SELECT role FROM board_members
        WHERE board_id = $1 AND user_id = $2
    `, boardID, uid).Scan(&role)
	if err != nil {
		return nil, errs.B().Code(errs.PermissionDenied).Msg("access denied: not a member or insufficient permissions").Err()
	}
	if role != "Admin" && string(uid) != userID {
		return nil, errs.B().Code(errs.PermissionDenied).Msg("only Admin or the user themselves can remove a user").Err()
	}

	var exists bool
	err = boardDB.QueryRow(ctx, `
        SELECT EXISTS (
            SELECT 1 FROM board_members
            WHERE board_id = $1 AND user_id = $2
        )
    `, boardID, userID).Scan(&exists)
	if err != nil {
		return nil, errs.B().Code(errs.Internal).Msg("failed to check membership").Cause(err).Err()
	}
	if !exists {
		return nil, errs.B().Code(errs.NotFound).Msg("user not a member of this board").Err()
	}

	var adminCount int
	err = boardDB.QueryRow(ctx, `
        SELECT COUNT(*) FROM board_members
        WHERE board_id = $1 AND role = 'Admin'
    `, boardID).Scan(&adminCount)
	if err != nil {
		return nil, errs.B().Code(errs.Internal).Msg("failed to count admins").Cause(err).Err()
	}

	var targetRole string
	err = boardDB.QueryRow(ctx, `
        SELECT role FROM board_members
        WHERE board_id = $1 AND user_id = $2
    `, boardID, userID).Scan(&targetRole)
	if err != nil {
		return nil, errs.B().Code(errs.Internal).Msg("failed to fetch target role").Cause(err).Err()
	}

	if targetRole == "Admin" && adminCount <= 1 {
		return nil, errs.B().Code(errs.FailedPrecondition).Msg("cannot remove the last Admin").Err()
	}

	_, err = boardDB.Exec(ctx, `
        DELETE FROM board_members
        WHERE board_id = $1 AND user_id = $2
    `, boardID, userID)
	if err != nil {
		return nil, errs.B().Code(errs.Internal).Msg("failed to remove user").Cause(err).Err()
	}

	return &RemoveUserResponse{Message: "User removed successfully"}, nil
}

// RemoveBoardResponse represents the response when a board is deleted.
type RemoveBoardResponse struct {
	Message string `json:"message"`
}

// RemoveBoard deletes a board and publishes a deletion event, restricted to Admins.
//
//encore:api auth method=DELETE path=/board/:boardID
func RemoveBoard(ctx context.Context, boardID string) (*RemoveBoardResponse, error) {
	uid, ok := auth.UserID()
	if !ok {
		return nil, errs.B().Code(errs.Unauthenticated).Msg("authentication required").Err()
	}

	var role string
	err := boardDB.QueryRow(ctx, `
        SELECT role FROM board_members
        WHERE board_id = $1 AND user_id = $2
    `, boardID, uid).Scan(&role)
	if err != nil || role != "Admin" {
		return nil, errs.B().Code(errs.PermissionDenied).Msg("only Admin can delete a board").Err()
	}

	result, err := boardDB.Exec(ctx, `
        DELETE FROM boards
        WHERE id = $1
    `, boardID)
	if err != nil {
		return nil, errs.B().Code(errs.Internal).Msg("failed to delete board").Cause(err).Err()
	}

	rowsAffected := result.RowsAffected()

	if rowsAffected == 0 {
		return nil, errs.B().Code(errs.NotFound).Msg("board not found").Err()
	}

	_, err = BoardDeletedTopic.Publish(ctx, &BoardDeletedEvent{BoardID: boardID})
	if err != nil {
		return nil, errs.B().Code(errs.Internal).Msg("failed to publish board deletion event").Cause(err).Err()
	}

	return &RemoveBoardResponse{Message: "Board deleted successfully"}, nil
}

// MemberResponse represents a single board member.
type MemberResponse struct {
	UserID string `json:"user_id"`
	Role   string `json:"role"`
}

// ListBoardMembersResponse represents a list of board members.
type ListBoardMembersResponse struct {
	Members []MemberResponse `json:"members"`
}

// ListBoardMembers retrieves all members of a board, accessible only to its members.
//
//encore:api auth method=GET path=/board/:boardID/users
func ListBoardMembers(ctx context.Context, boardID string) (*ListBoardMembersResponse, error) {
	uid, ok := auth.UserID()
	if !ok {
		return nil, errs.B().Code(errs.Unauthenticated).Msg("authentication required").Err()
	}

	var exists bool
	err := boardDB.QueryRow(ctx, `
        SELECT EXISTS (
            SELECT 1 FROM board_members
            WHERE board_id = $1 AND user_id = $2
        )
    `, boardID, uid).Scan(&exists)
	if err != nil {
		return nil, errs.B().Code(errs.Internal).Msg("failed to check membership").Cause(err).Err()
	}
	if !exists {
		return nil, errs.B().Code(errs.PermissionDenied).Msg("access denied: not a member of this board").Err()
	}

	rows, err := boardDB.Query(ctx, `
        SELECT user_id, role
        FROM board_members
        WHERE board_id = $1
        ORDER BY role, user_id
    `, boardID)
	if err != nil {
		return nil, errs.B().Code(errs.Internal).Msg("failed to fetch members").Cause(err).Err()
	}
	defer rows.Close()

	var members []MemberResponse
	for rows.Next() {
		var m MemberResponse
		if err := rows.Scan(&m.UserID, &m.Role); err != nil {
			return nil, errs.B().Code(errs.Internal).Msg("failed to scan member").Cause(err).Err()
		}
		members = append(members, m)
	}

	if err := rows.Err(); err != nil {
		return nil, errs.B().Code(errs.Internal).Msg("error reading members").Cause(err).Err()
	}

	return &ListBoardMembersResponse{Members: members}, nil
}

// CheckMembershipResponse indicates whether a user is a member of a board and their role.
type CheckMembershipResponse struct {
	IsMember bool   `json:"is_member"`
	Role     string `json:"role,omitempty"` // Empty if not a member
}

// CheckMembership checks if the authenticated user is a member of a board and returns their role.
//
//encore:api auth method=GET path=/board/:boardID/membership
func CheckMembership(ctx context.Context, boardID string) (*CheckMembershipResponse, error) {
	uid, ok := auth.UserID()
	if !ok {
		return nil, errs.B().Code(errs.Unauthenticated).Msg("authentication required").Err()
	}

	var role string
	err := boardDB.QueryRow(ctx, `
        SELECT role FROM board_members
        WHERE board_id = $1 AND user_id = $2
    `, boardID, uid).Scan(&role)
	if err != nil {
		if err == sqldb.ErrNoRows {
			return &CheckMembershipResponse{IsMember: false}, nil
		}
		return nil, errs.B().Code(errs.Internal).Msg("failed to check membership").Cause(err).Err()
	}

	return &CheckMembershipResponse{IsMember: true, Role: role}, nil
}

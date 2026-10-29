package identity

import (
	"context"
	"fmt"
	"time"
)

type SecurityEvent struct {
	ID        int64     `json:"id"`
	ActorID   string    `json:"actor_id"`
	Action    string    `json:"action"`
	Target    string    `json:"target"`
	CreatedAt time.Time `json:"created_at"`
}

// WorkspaceEvents is deliberately bounded and joins the current membership in
// the same query. Events from other workspaces and global login events are not
// returned to a workspace administrator.
func (s *Store) WorkspaceEvents(ctx context.Context, session Session, workspace string) ([]SecurityEvent, error) {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	rows, err := s.pool.Query(ctx, `SELECT e.id,COALESCE(e.actor_user_id,''),e.action,e.target,e.created_at
	 FROM saas_security_events e JOIN saas_memberships m ON m.workspace_id=e.workspace_id AND m.user_id=$2
	 JOIN saas_workspaces w ON w.id=m.workspace_id WHERE e.workspace_id=$1 AND m.revoked_at IS NULL
	 AND m.role IN ('OWNER','ADMIN') AND NOT w.legacy_unclaimed ORDER BY e.id DESC LIMIT 50`, workspace, session.User.ID)
	if err != nil {
		return nil, fmt.Errorf("list workspace security events: %w", err)
	}
	defer rows.Close()
	events := make([]SecurityEvent, 0)
	for rows.Next() {
		var e SecurityEvent
		if err = rows.Scan(&e.ID, &e.ActorID, &e.Action, &e.Target, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan workspace security event: %w", err)
		}
		events = append(events, e)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("list workspace security events: %w", err)
	}
	return events, nil
}

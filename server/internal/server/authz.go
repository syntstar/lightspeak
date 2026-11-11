package server

import (
	"context"
	"database/sql"
	"errors"
	"slices"
)

// Central authorization. Every handler resolves the resource through one of
// these helpers; a resource the caller may not see is reported as not_found
// so existence is not leaked.

type querier interface {
	QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row
	QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error)
}

// requireSpace returns the caller's access to the space, not_found if they
// are no member and forbidden if they lack need (0 = membership only).
func (s *Server) requireSpace(ctx context.Context, se *Session, spaceID int64, need Perm) (Access, error) {
	a, err := spaceAccess(ctx, s.db.R, spaceID, se.UserID)
	if err != nil {
		return a, err
	}
	if !a.Member {
		return a, notFound("unknown_space")
	}
	if !a.Has(need) {
		return a, errForbidden
	}
	return a, nil
}

// requireOwner: only the owner and server admins may delete or transfer a space.
func (s *Server) requireOwner(ctx context.Context, se *Session, spaceID int64) (Access, error) {
	a, err := s.requireSpace(ctx, se, spaceID, 0)
	if err == nil && !a.Owner && !a.ServerAdmin {
		err = errForbidden
	}
	return a, err
}

type Channel struct {
	ID            int64
	SpaceID       int64 // 0 for DMs
	Kind          string
	ParentID      int64
	Name          string
	Topic         string
	Position      int
	UserLimit     int
	E2EEEpoch     int64
	OwnerID       int64
	LastMessageID int64
	// Channel settings; Overrides is only set after fillOverrides.
	Mode      string
	Slowmode  int
	AwayAfter int
	Audio     string
	Icon      string
	Overrides map[int64]override
}

func (c *Channel) isDM() bool { return c.Kind == "dm" || c.Kind == "group_dm" }

// containerID is the E2EE key container: the space for space channels, the
// channel itself for DMs.
func (c *Channel) containerID() int64 {
	if c.isDM() {
		return c.ID
	}
	return c.SpaceID
}

const channelCols = `id, COALESCE(space_id,0), kind, COALESCE(parent_id,0), name, topic, position, COALESCE(user_limit,0), e2ee_epoch, COALESCE(owner_id,0), COALESCE(last_message_id,0), mode, slowmode, away_after, audio, icon`

func scanChannel(sc interface{ Scan(...any) error }) (*Channel, error) {
	var c Channel
	err := sc.Scan(&c.ID, &c.SpaceID, &c.Kind, &c.ParentID, &c.Name, &c.Topic, &c.Position, &c.UserLimit, &c.E2EEEpoch, &c.OwnerID, &c.LastMessageID, &c.Mode, &c.Slowmode, &c.AwayAfter, &c.Audio, &c.Icon)
	return &c, err
}

func (s *Server) loadChannel(ctx context.Context, id int64) (*Channel, error) {
	c, err := scanChannel(s.db.R.QueryRowContext(ctx, `SELECT `+channelCols+` FROM channels WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, notFound("unknown_channel")
	}
	return c, err
}

// channelAccess loads the channel (with its overrides) and the caller's
// access. DM participants are plain members without permissions. Callers
// that cannot see the channel – no member, or a private channel without
// the view permission – get not_found.
func (s *Server) channelAccess(ctx context.Context, se *Session, channelID int64) (*Channel, Access, error) {
	c, err := s.loadChannel(ctx, channelID)
	if err != nil {
		return nil, Access{}, err
	}
	if c.isDM() {
		var n int
		if err := s.db.R.QueryRowContext(ctx, `SELECT COUNT(*) FROM channel_members WHERE channel_id=? AND user_id=?`, c.ID, se.UserID).Scan(&n); err != nil {
			return nil, Access{}, err
		}
		if n == 0 {
			return nil, Access{}, notFound("unknown_channel")
		}
		return c, Access{Member: true}, nil
	}
	a, err := spaceAccess(ctx, s.db.R, c.SpaceID, se.UserID)
	if err != nil {
		return nil, a, err
	}
	if !a.Member {
		return nil, a, notFound("unknown_channel")
	}
	if err := s.fillOverrides(ctx, s.db.R, c); err != nil {
		return nil, a, err
	}
	if !c.visible(a) {
		return nil, Access{}, notFound("unknown_channel")
	}
	return c, a, nil
}

// requireTextChannel: channel must accept messages (text, dm, group_dm).
// The channel comes with its permission overrides.
func (s *Server) requireTextChannel(ctx context.Context, se *Session, channelID int64) (*Channel, Access, error) {
	c, a, err := s.channelAccess(ctx, se, channelID)
	if err != nil {
		return nil, a, err
	}
	if c.Kind != "text" && !c.isDM() {
		return nil, a, badRequest("not_text_channel")
	}
	return c, a, nil
}

// containerAccess: caller must be a member of the space or DM (E2EE container).
// Returns whether the container is a DM and its current epoch.
func (s *Server) containerAccess(ctx context.Context, se *Session, cid int64) (isDM bool, epoch int64, err error) {
	err = s.db.R.QueryRowContext(ctx, `SELECT e2ee_epoch FROM spaces WHERE id=?`, cid).Scan(&epoch)
	if err == nil {
		var n int
		if err = s.db.R.QueryRowContext(ctx, `SELECT COUNT(*) FROM space_members WHERE space_id=? AND user_id=?`, cid, se.UserID).Scan(&n); err != nil {
			return
		}
		if n == 0 {
			err = notFound("unknown_container")
		}
		return false, epoch, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return
	}
	c, _, err := s.channelAccess(ctx, se, cid)
	if err != nil || !c.isDM() {
		return false, 0, notFound("unknown_container")
	}
	return true, c.E2EEEpoch, nil
}

// containerMembers lists user IDs that belong to an E2EE container.
func (s *Server) containerMembers(ctx context.Context, cid int64, isDM bool) ([]int64, error) {
	q := `SELECT user_id FROM space_members WHERE space_id=?`
	if isDM {
		q = `SELECT user_id FROM channel_members WHERE channel_id=?`
	}
	return queryIDs(ctx, s.db.R, q, cid)
}

func queryIDs(ctx context.Context, db querier, q string, args ...any) ([]int64, error) {
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// blockedEither reports whether a blocked b or b blocked a.
func (s *Server) blockedEither(ctx context.Context, a, b int64) (bool, error) {
	var n int
	err := s.db.R.QueryRowContext(ctx, `SELECT COUNT(*) FROM relationships WHERE kind='blocked' AND ((user_id=? AND other_id=?) OR (user_id=? AND other_id=?))`, a, b, b, a).Scan(&n)
	return n > 0, err
}

// areFriends reports a confirmed friendship (1:1 DMs and calls only
// between friends).
func (s *Server) areFriends(ctx context.Context, a, b int64) (bool, error) {
	var n int
	err := s.db.R.QueryRowContext(ctx, `SELECT COUNT(*) FROM relationships WHERE kind='friend' AND ((user_id=? AND other_id=?) OR (user_id=? AND other_id=?))`, a, b, b, a).Scan(&n)
	return n == 2, err
}

// related reports whether two users share a space, a DM or a relationship;
// only related users may see each other's profile.
func (s *Server) related(ctx context.Context, a, b int64) (bool, error) {
	if a == b {
		return true, nil
	}
	var n int
	err := s.db.R.QueryRowContext(ctx, `SELECT
		EXISTS(SELECT 1 FROM space_members x JOIN space_members y ON x.space_id=y.space_id WHERE x.user_id=? AND y.user_id=?) OR
		EXISTS(SELECT 1 FROM channel_members x JOIN channel_members y ON x.channel_id=y.channel_id WHERE x.user_id=? AND y.user_id=?) OR
		EXISTS(SELECT 1 FROM relationships WHERE user_id=? AND other_id=? AND kind<>'blocked')`, a, b, a, b, a, b).Scan(&n)
	return n == 1, err
}

// relatedUsers returns every user who shares a space, DM or friendship with
// uid (used for presence fan-out).
func (s *Server) relatedUsers(ctx context.Context, uid int64) ([]int64, error) {
	return queryIDs(ctx, s.db.R, `
		SELECT user_id FROM space_members WHERE space_id IN (SELECT space_id FROM space_members WHERE user_id=?)
		UNION SELECT user_id FROM channel_members WHERE channel_id IN (SELECT channel_id FROM channel_members WHERE user_id=?)
		UNION SELECT other_id FROM relationships WHERE user_id=? AND kind='friend'`, uid, uid, uid)
}

// channelAudience returns the users allowed to receive events of a
// channel: DM participants, or the space members who may view it.
func (s *Server) channelAudience(ctx context.Context, c *Channel) ([]int64, error) {
	if c.isDM() {
		return queryIDs(ctx, s.db.R, `SELECT user_id FROM channel_members WHERE channel_id=?`, c.ID)
	}
	if c.Overrides == nil {
		if err := s.fillOverrides(ctx, s.db.R, c); err != nil {
			return nil, err
		}
	}
	all, err := spaceAccesses(ctx, s.db.R, c.SpaceID)
	if err != nil {
		return nil, err
	}
	out := make([]int64, 0, len(all))
	for uid, a := range all {
		if c.visible(a) {
			out = append(out, uid)
		}
	}
	slices.Sort(out)
	return out, nil
}

func (s *Server) spaceAudience(ctx context.Context, spaceID int64) ([]int64, error) {
	return queryIDs(ctx, s.db.R, `SELECT user_id FROM space_members WHERE space_id=?`, spaceID)
}

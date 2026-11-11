package server

import (
	"cmp"
	"context"
	"database/sql"
	"slices"
	"strings"
	"unicode/utf8"
)

// JSON shapes shared by REST and the gateway (mirrored in
// client/src/lib/api/types.ts). URLs are relative to the server base URL.

type User struct {
	ID          ID     `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
	AvatarURL   string `json:"avatarUrl,omitempty"`
	// Deleted: the account deleted itself; clients show a
	// neutral name.
	Deleted bool `json:"deleted,omitempty"`
}

type Me struct {
	User
	Status        string `json:"status"`
	StatusText    string `json:"statusText"`
	IsServerAdmin bool   `json:"isServerAdmin"`
	CreatedAt     int64  `json:"createdAt"`
}

type SpaceDTO struct {
	ID        ID     `json:"id"`
	Name      string `json:"name"`
	IconURL   string `json:"iconUrl,omitempty"`
	OwnerID   ID     `json:"ownerId"`
	Role      string `json:"role"` // legacy, see Access.legacyRole
	E2EEEpoch int64  `json:"e2eeEpoch"`
	// Clients derive permissions from roles, @everyone and their
	// own role IDs (owner and server admins have all).
	Roles         []RoleDTO `json:"roles"`
	EveryonePerms Perm      `json:"everyonePerms"`
	RoleIDs       []ID      `json:"roleIds"`
}

type ChannelDTO struct {
	ID            ID     `json:"id"`
	SpaceID       *ID    `json:"spaceId,omitempty"`
	Kind          string `json:"kind"`
	ParentID      *ID    `json:"parentId,omitempty"`
	Name          string `json:"name"`
	Topic         string `json:"topic"`
	Position      int    `json:"position"`
	UserLimit     int    `json:"userLimit,omitempty"`
	LastMessageID *ID    `json:"lastMessageId,omitempty"`
	OwnerID       *ID    `json:"ownerId,omitempty"`
	E2EEEpoch     int64  `json:"e2eeEpoch,omitempty"`
	MemberIDs     []ID   `json:"memberIds,omitempty"`
	// Space channels only.
	Mode      string        `json:"mode,omitempty"`
	Slowmode  int           `json:"slowmode,omitempty"`
	AwayAfter int           `json:"awayAfter,omitempty"`
	Audio     string        `json:"audio,omitempty"`
	Overrides []OverrideDTO `json:"overrides,omitempty"`
	Icon      string        `json:"icon,omitempty"` // empty = default
}

func optID(v int64) *ID {
	if v == 0 {
		return nil
	}
	x := ID(v)
	return &x
}

func (c *Channel) dto() ChannelDTO {
	d := ChannelDTO{ID: ID(c.ID), SpaceID: optID(c.SpaceID), Kind: c.Kind, ParentID: optID(c.ParentID), Name: c.Name,
		Topic: c.Topic, Position: c.Position, UserLimit: c.UserLimit, LastMessageID: optID(c.LastMessageID), OwnerID: optID(c.OwnerID)}
	if c.isDM() {
		d.E2EEEpoch = c.E2EEEpoch
	} else if c.Kind != "category" {
		d.Mode, d.Slowmode, d.AwayAfter, d.Icon = c.Mode, c.Slowmode, c.AwayAfter, c.Icon
		if c.Kind == "voice" {
			d.Audio = c.Audio
		}
		for rid, o := range c.Overrides {
			d.Overrides = append(d.Overrides, OverrideDTO{RoleID: ID(rid), Allow: o.allow, Deny: o.deny})
		}
		slices.SortFunc(d.Overrides, func(x, y OverrideDTO) int { return cmp.Compare(x.RoleID, y.RoleID) })
	}
	return d
}

func avatarURL(key sql.NullString) string {
	if !key.Valid || key.String == "" {
		return ""
	}
	return "/avatars/" + key.String
}

const userCols = `id, username, display_name, avatar_key, deleted_at IS NOT NULL`

func scanUser(sc interface{ Scan(...any) error }) (User, error) {
	var u User
	var id int64
	var av sql.NullString
	err := sc.Scan(&id, &u.Username, &u.DisplayName, &av, &u.Deleted)
	u.ID = ID(id)
	u.AvatarURL = avatarURL(av)
	return u, err
}

func (s *Server) loadUser(ctx context.Context, id int64) (User, error) {
	return scanUser(s.db.R.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE id=?`, id))
}

func (s *Server) loadMe(ctx context.Context, id int64) (Me, error) {
	var m Me
	var uid int64
	var av sql.NullString
	var admin int
	err := s.db.R.QueryRowContext(ctx, `SELECT id, username, display_name, avatar_key, status, status_text, is_server_admin, created_at FROM users WHERE id=?`, id).
		Scan(&uid, &m.Username, &m.DisplayName, &av, &m.Status, &m.StatusText, &admin, &m.CreatedAt)
	m.ID = ID(uid)
	m.AvatarURL = avatarURL(av)
	m.IsServerAdmin = admin == 1
	return m, err
}

// loadUsers fetches several users in one query.
func (s *Server) loadUsers(ctx context.Context, ids []int64) ([]User, error) {
	if len(ids) == 0 {
		return []User{}, nil
	}
	seen := map[int64]bool{}
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			args = append(args, id)
		}
	}
	rows, err := s.db.R.QueryContext(ctx, `SELECT `+userCols+` FROM users WHERE id IN (`+placeholders(len(args))+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat("?,", n-1) + "?"
}

// clampText trims whitespace, strips control characters (except newline and
// tab) and limits the length in runes.
func clampText(s string, max int) string {
	s = strings.TrimSpace(s)
	var b strings.Builder
	n := 0
	for _, r := range s {
		if r == utf8.RuneError || (r < 0x20 && r != '\n' && r != '\t') || r == 0x7f {
			continue
		}
		if n >= max {
			break
		}
		b.WriteRune(r)
		n++
	}
	return b.String()
}

// cleanName is clampText for single-line names.
func cleanName(s string, max int) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\n", " "), "\t", " ")
	return clampText(s, max)
}

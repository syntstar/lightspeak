package server

// Channel settings and permissions. A channel may override a few channel permissions per role and
// for @everyone: deny or allow, otherwise the space-wide value applies.
// Resolution follows Discord: base permissions, then the @everyone
// override, then all role overrides together (an allow beats a deny of
// another role). Owners, server admins and Administrators are never
// restricted – except that nobody speaks or streams in the away channel,
// which in turn everyone may enter.

import (
	"context"
	"database/sql"
	"net/http"
	"slices"
	"sync"
	"time"
)

const (
	modeOpen = "open"
	modeAway = "away"
)

var (
	slowmodeSteps  = []int{0, 5, 30, 60, 300} // seconds
	awayAfterSteps = []int{0, 5, 15, 30, 60}  // minutes
	audioLevels    = []string{"speech", "high", "music"}
	// Icons a channel may pick instead of # / the speaker; the
	// client maps the names to its icon set.
	channelIcons = map[string][]string{
		"text": {"megaphone", "message-circle", "book-open", "code", "image", "link", "lightbulb", "calendar",
			"star", "info", "gamepad-2", "music", "shield", "coffee", "bot", "newspaper"},
		"voice": {"mic", "headphones", "gamepad-2", "music", "radio", "coffee", "users", "monitor",
			"tv", "star", "briefcase", "book-open", "code", "party-popper", "film", "bot"},
	}
)

// Channel permissions a channel of the kind may override.
const (
	voiceOverridable = PermView | PermConnect | PermSpeak | PermStream
	textOverridable  = PermView | PermSend | PermAttach | PermReact | PermMentionEveryone
	// channelBits is everything that only makes sense inside a channel:
	// whoever may not view a channel holds none of it there.
	channelBits = voiceOverridable | textOverridable
)

func overridable(kind string) Perm {
	switch kind {
	case "voice":
		return voiceOverridable
	case "text":
		return textOverridable
	}
	return 0
}

// override is one role's deviation in a channel.
type override struct{ allow, deny Perm }

// perms returns a's effective permissions in c (space channels only;
// Overrides must be loaded).
func (c *Channel) perms(a Access) Perm {
	p := a.Perms
	if !a.Has(PermAdministrator) {
		if o, ok := c.Overrides[c.SpaceID]; ok {
			p = p&^o.deny | o.allow
		}
		var allow, deny Perm
		for _, rid := range a.RoleIDs {
			if o, ok := c.Overrides[rid]; ok {
				allow |= o.allow
				deny |= o.deny
			}
		}
		p = p&^deny | allow
	}
	switch {
	case c.Mode == modeAway:
		// Everyone sees and may enter it (idle people are moved there),
		// nobody talks.
		p = p&^(PermSpeak|PermStream) | PermConnect | PermView
	case c.Kind == "category":
		// Categories only group channels; their names are visible to all
		// members, the client hides empty ones.
		p |= PermView
	case p&PermView == 0:
		p &^= channelBits
	}
	return p
}

// visible: a may see c at all (overrides loaded).
func (c *Channel) visible(a Access) bool { return c.may(a, PermView) }

// may: a holds p in c. DMs know no permissions; their members may do all
// of it (they are gated by friendship and blocks instead).
func (c *Channel) may(a Access, p Perm) bool { return c.isDM() || c.perms(a)&p == p }

// fillOverrides loads the overrides of one space channel.
func (s *Server) fillOverrides(ctx context.Context, q querier, c *Channel) error {
	if c.isDM() {
		return nil
	}
	m, err := loadOverrides(ctx, q, `channel_id=?`, c.ID)
	if err != nil {
		return err
	}
	c.setOverrides(m[c.ID])
	return nil
}

func (c *Channel) setOverrides(m map[int64]override) {
	if m == nil {
		m = map[int64]override{}
	}
	c.Overrides = m
}

// loadOverrides returns the overrides by channel, filtered by where (on
// channel_overrides).
func loadOverrides(ctx context.Context, q querier, where string, arg int64) (map[int64]map[int64]override, error) {
	rows, err := q.QueryContext(ctx, `SELECT channel_id, role_id, allow, deny FROM channel_overrides WHERE `+where, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]map[int64]override{}
	for rows.Next() {
		var cid, rid int64
		var o override
		if err := rows.Scan(&cid, &rid, &o.allow, &o.deny); err != nil {
			return nil, err
		}
		if out[cid] == nil {
			out[cid] = map[int64]override{}
		}
		out[cid][rid] = o
	}
	return out, rows.Err()
}

// spaceOverrides loads the overrides of every channel in a space.
func spaceOverrides(ctx context.Context, q querier, spaceID int64) (map[int64]map[int64]override, error) {
	return loadOverrides(ctx, q, `channel_id IN (SELECT id FROM channels WHERE space_id=?)`, spaceID)
}

// OverrideDTO is one role's deviation in a channel; roleId equals the space
// ID for @everyone.
type OverrideDTO struct {
	RoleID ID   `json:"roleId"`
	Allow  Perm `json:"allow"`
	Deny   Perm `json:"deny"`
}

// rulesReq are the optional settings of channel create/patch.
type rulesReq struct {
	Mode      *string        `json:"mode"`
	Slowmode  *int           `json:"slowmode"`
	AwayAfter *int           `json:"awayAfter"`
	Audio     *string        `json:"audio"`
	Overrides *[]OverrideDTO `json:"overrides"`
	Icon      *string        `json:"icon"`
}

func (r *rulesReq) any() bool {
	return r.Mode != nil || r.Slowmode != nil || r.AwayAfter != nil || r.Audio != nil || r.Overrides != nil || r.Icon != nil
}

// applyRules validates the request against the actor's access and updates
// c; the caller saves with saveRules.
func (s *Server) applyRules(ctx context.Context, a Access, c *Channel, r *rulesReq) error {
	if c.isDM() || c.Kind == "category" {
		if r.any() {
			return badRequest("invalid_channel")
		}
		return nil
	}
	if r.Mode != nil {
		if *r.Mode != modeOpen && (*r.Mode != modeAway || c.Kind != "voice") {
			return badRequest("invalid_mode")
		}
		c.Mode = *r.Mode
	}
	if r.Slowmode != nil {
		if c.Kind != "text" || !slices.Contains(slowmodeSteps, *r.Slowmode) {
			return badRequest("invalid_slowmode")
		}
		c.Slowmode = *r.Slowmode
	}
	if r.AwayAfter != nil {
		if c.Kind != "voice" || !slices.Contains(awayAfterSteps, *r.AwayAfter) {
			return badRequest("invalid_away_after")
		}
		c.AwayAfter = *r.AwayAfter
	}
	if r.Audio != nil {
		if c.Kind != "voice" || !slices.Contains(audioLevels, *r.Audio) {
			return badRequest("invalid_audio")
		}
		c.Audio = *r.Audio
	}
	if r.Icon != nil {
		if *r.Icon != "" && !slices.Contains(channelIcons[c.Kind], *r.Icon) {
			return badRequest("invalid_icon")
		}
		c.Icon = *r.Icon
	}
	if r.Overrides != nil {
		if err := s.applyOverrides(ctx, a, c, *r.Overrides); err != nil {
			return err
		}
	}
	if c.Mode != modeAway {
		c.AwayAfter = 0
	}
	return nil
}

// applyOverrides replaces c's overrides. Like role editing, it
// needs Manage roles; an actor may only touch roles below their rank (and
// @everyone) and only bits they hold themselves, so nobody grants
// themselves more through a channel.
func (s *Server) applyOverrides(ctx context.Context, a Access, c *Channel, list []OverrideDTO) error {
	if len(list) > s.cfg.MaxSpaceRoles+1 {
		return badRequest("invalid_overrides")
	}
	mask := overridable(c.Kind)
	next := map[int64]override{}
	for _, o := range list {
		rid := int64(o.RoleID)
		if _, dup := next[rid]; dup || o.Allow&^mask != 0 || o.Deny&^mask != 0 || o.Allow&o.Deny != 0 {
			return badRequest("invalid_overrides")
		}
		if o.Allow|o.Deny != 0 {
			next[rid] = override{allow: o.Allow, deny: o.Deny}
		}
	}
	changed := func(rid int64) Perm {
		was, now := c.Overrides[rid], next[rid]
		return was.allow ^ now.allow | was.deny ^ now.deny
	}
	ids := map[int64]bool{}
	for rid := range c.Overrides {
		ids[rid] = true
	}
	for rid := range next {
		ids[rid] = true
	}
	for rid := range ids {
		bits := changed(rid)
		if bits == 0 {
			continue
		}
		if !a.Has(PermManageRoles) || !grantable(a, bits) {
			return errForbidden
		}
		if rid == c.SpaceID {
			continue
		}
		var pos int64
		err := s.db.R.QueryRowContext(ctx, `SELECT position FROM roles WHERE id=? AND space_id=?`, rid, c.SpaceID).Scan(&pos)
		if err == sql.ErrNoRows {
			if _, still := next[rid]; !still {
				continue // a role deleted meanwhile
			}
			return badRequest("unknown_role")
		}
		if err != nil {
			return err
		}
		if pos >= a.Rank {
			return errForbidden
		}
	}
	c.Overrides = next
	return nil
}

// saveRules writes c's settings and overrides. Making c the away channel
// turns a previous one back into a normal channel; their IDs are returned
// so the caller can announce them.
func saveRules(ctx context.Context, tx *sql.Tx, c *Channel) ([]int64, error) {
	if c.isDM() || c.Kind == "category" {
		return nil, nil
	}
	var demoted []int64
	if c.Mode == modeAway {
		var err error
		demoted, err = queryIDs(ctx, tx, `SELECT id FROM channels WHERE space_id=? AND mode='away' AND id<>?`, c.SpaceID, c.ID)
		if err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE channels SET mode='open', away_after=0 WHERE space_id=? AND mode='away' AND id<>?`, c.SpaceID, c.ID); err != nil {
			return nil, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE channels SET mode=?, slowmode=?, away_after=?, audio=?, icon=? WHERE id=?`,
		c.Mode, c.Slowmode, c.AwayAfter, c.Audio, c.Icon, c.ID); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM channel_overrides WHERE channel_id=?`, c.ID); err != nil {
		return nil, err
	}
	for rid, o := range c.Overrides {
		if _, err := tx.ExecContext(ctx, `INSERT INTO channel_overrides(channel_id, role_id, allow, deny) VALUES (?,?,?,?)`, c.ID, rid, o.allow, o.deny); err != nil {
			return nil, err
		}
	}
	return demoted, nil
}

// emitChannelIDs announces channels whose settings changed as a side effect.
func (s *Server) emitChannelIDs(ctx context.Context, ids []int64) {
	for _, id := range ids {
		c, err := s.loadChannel(ctx, id)
		if err != nil || s.fillOverrides(ctx, s.db.R, c) != nil {
			continue
		}
		s.emitChannel(ctx, c, "channel_update", c.dto())
	}
}

// ---------- slow mode ----------

// slowGate remembers the last message per (channel, user) in memory: a
// restart forgets it, which is fine for a courtesy limit.
type slowGate struct {
	mu   sync.Mutex
	last map[[2]int64]time.Time
}

// take reports how many seconds the user still has to wait; 0 means the
// message may go out and is counted.
func (g *slowGate) take(cid, uid int64, every int, now time.Time) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.last == nil {
		g.last = map[[2]int64]time.Time{}
	}
	k := [2]int64{cid, uid}
	if t, ok := g.last[k]; ok {
		if wait := t.Add(time.Duration(every) * time.Second).Sub(now); wait > 0 {
			return int((wait + time.Second - 1) / time.Second)
		}
	}
	if len(g.last) > 4096 { // drop what can no longer matter
		for key, t := range g.last {
			if now.Sub(t) > 5*time.Minute {
				delete(g.last, key)
			}
		}
	}
	g.last[k] = now
	return 0
}

// postCheck enforces the send/attach permissions and slow mode for a new
// message. c must have its overrides loaded.
func (s *Server) postCheck(c *Channel, a Access, uid int64, attachments bool) error {
	if c.isDM() {
		return nil
	}
	if !c.may(a, PermSend) {
		return errf(http.StatusForbidden, "send_denied", "you cannot write here")
	}
	if attachments && !c.may(a, PermAttach) {
		return errf(http.StatusForbidden, "attach_denied", "you cannot send files here")
	}
	if c.Slowmode > 0 && !a.Has(PermManageMessages) && !a.Has(PermManageChannels) {
		if wait := s.slow.take(c.ID, uid, c.Slowmode, s.now()); wait > 0 {
			return errf(http.StatusTooManyRequests, "slow_mode", "slow mode")
		}
	}
	return nil
}

package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

type readyPayload struct {
	SessionID        string          `json:"sessionId"`
	ProtocolVersion  int             `json:"protocolVersion"`
	MinClientVersion string          `json:"minClientVersion"`
	ServerVersion    string          `json:"serverVersion"`
	Limits           limitsDTO       `json:"limits"`
	User             Me              `json:"user"`
	DeviceID         ID              `json:"deviceId"`
	Spaces           []SpaceDTO      `json:"spaces"`
	Channels         []ChannelDTO    `json:"channels"`
	DMs              []ChannelDTO    `json:"dms"`
	Users            []User          `json:"users"`
	ReadStates       []readStateDTO  `json:"readStates"`
	Presences        []presence      `json:"presences"`
	VoiceStates      []VoiceState    `json:"voiceStates"`
	Relationships    []relDTO        `json:"relationships"`
	Settings         json.RawMessage `json:"settings"`
	PendingRotations []ID            `json:"pendingRotations"`
}

// limitsDTO tells clients the operator's settings, so their checks and error
// messages match this server.
type limitsDTO struct {
	MessageChars int    `json:"messageChars"`
	UploadBytes  int64  `json:"uploadBytes"`
	SpaceRoles   int    `json:"spaceRoles"`
	Invites      string `json:"invites"`
}

// buildReady assembles the initial gateway snapshot for a user.
func (s *Server) buildReady(ctx context.Context, se *Session, sessionID string) (*readyPayload, error) {
	me, err := s.loadMe(ctx, se.UserID)
	if err != nil {
		return nil, err
	}
	p := &readyPayload{SessionID: sessionID, ProtocolVersion: ProtocolVersion, MinClientVersion: s.cfg.MinClientVersion,
		ServerVersion: Version, Limits: limitsDTO{s.cfg.MaxMessageChars, s.cfg.MaxUploadBytes, s.cfg.MaxSpaceRoles, s.cfg.Invites},
		User: me, DeviceID: ID(se.DeviceID), Spaces: []SpaceDTO{}, Channels: []ChannelDTO{},
		ReadStates: []readStateDTO{}, Presences: []presence{}}

	spaceIDs, err := queryIDs(ctx, s.db.R, `SELECT space_id FROM space_members WHERE user_id=? ORDER BY joined_at`, se.UserID)
	if err != nil {
		return nil, err
	}
	for _, id := range spaceIDs {
		sp, err := s.spaceDTO(ctx, id, se.UserID)
		if err != nil {
			return nil, err
		}
		p.Spaces = append(p.Spaces, sp)
		chans, err := s.spaceChannels(ctx, id, se.UserID)
		if err != nil {
			return nil, err
		}
		p.Channels = append(p.Channels, chans...)
	}

	dms, dmUsers, err := s.userDMs(ctx, se.UserID)
	if err != nil {
		return nil, err
	}
	p.DMs = dms

	p.Relationships, err = s.relationships(ctx, se.UserID)
	if err != nil {
		return nil, err
	}
	userIDs := append([]int64{}, dmUsers...)
	for _, r := range p.Relationships {
		userIDs = append(userIDs, int64(r.UserID))
	}
	p.Users, err = s.loadUsers(ctx, userIDs)
	if err != nil {
		return nil, err
	}

	shown := map[int64]bool{}
	for _, c := range p.Channels {
		shown[int64(c.ID)] = true
	}
	hidden := map[int64]bool{}
	for _, id := range spaceIDs {
		all, err := queryIDs(ctx, s.db.R, `SELECT id FROM channels WHERE space_id=?`, id)
		if err != nil {
			return nil, err
		}
		for _, c := range all {
			hidden[c] = !shown[c]
		}
	}
	rows, err := s.db.R.QueryContext(ctx, `SELECT channel_id, last_read_id, mention_count FROM read_states WHERE user_id=?`, se.UserID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var rs readStateDTO
		var c, l int64
		if err := rows.Scan(&c, &l, &rs.MentionCount); err != nil {
			rows.Close()
			return nil, err
		}
		rs.ChannelID, rs.LastReadID = ID(c), ID(l)
		if hidden[c] {
			continue // private channel the user may no longer view
		}
		p.ReadStates = append(p.ReadStates, rs)
	}
	rows.Close()

	related, err := s.relatedUsers(ctx, se.UserID)
	if err != nil {
		return nil, err
	}
	for _, uid := range related {
		if uid == se.UserID || !s.hub.isOnline(uid) {
			continue
		}
		if pr := s.effectivePresence(ctx, uid); pr.Status != "offline" {
			p.Presences = append(p.Presences, pr)
		}
	}

	p.VoiceStates = s.voice.statesVisibleTo(ctx, se.UserID)

	var js string
	err = s.db.R.QueryRowContext(ctx, `SELECT json FROM user_settings WHERE user_id=?`, se.UserID).Scan(&js)
	if errors.Is(err, sql.ErrNoRows) {
		js = "{}"
	} else if err != nil {
		return nil, err
	}
	p.Settings = json.RawMessage(js)
	p.PendingRotations = s.pendingRotations(ctx, se.UserID)
	return p, nil
}

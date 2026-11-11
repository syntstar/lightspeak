package server

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	lkauth "github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/livekit"
	"github.com/twitchtv/twirp"
	"google.golang.org/protobuf/encoding/protojson"
)

// Voice keeps the authoritative in-memory voice state (who is in which
// channel). It is fed by /voice/join (optimistic), LiveKit webhooks and a
// reconcile loop that only runs while somebody is in voice.

const (
	voiceTokenTTL    = 10 * time.Minute
	reconcileEvery   = 30 * time.Second
	joinGrace        = 20 * time.Second
	ringTimeout      = 45 * time.Second
	roomPrefix       = "ch_"
	livekitAPITimout = 5 * time.Second
)

type VoiceState struct {
	UserID      ID   `json:"userId"`
	ChannelID   *ID  `json:"channelId"`
	SpaceID     *ID  `json:"spaceId,omitempty"`
	Muted       bool `json:"muted"`
	Deafened    bool `json:"deafened"`
	Streaming   bool `json:"streaming"`
	ServerMuted bool `json:"serverMuted"`
	// Listener: the channel lets this user only listen (no Speak permission
	// there, away channel). NoScreen: no Stream permission there.
	Listener bool `json:"listener,omitempty"`
	NoScreen bool `json:"noScreen,omitempty"`
	// Watching lists the streamers in the same channel whose screen this
	// user is subscribed to (so streamers can show their viewers).
	Watching []ID `json:"watching,omitempty"`
	// Stream describes the own stream while Streaming (viewers show whether
	// full quality arrives yet).
	Stream *StreamInfo `json:"stream,omitempty"`

	joinedAt  time.Time
	confirmed bool // seen in LiveKit
	admitted  bool // moved in by a moderator: a locked channel keeps them
}

// StreamInfo is what a streamer reports about its stream: the height of the
// full layer, the chosen frame rate and whether the encoder still ramps up
// to its target bitrate.
type StreamInfo struct {
	Height int  `json:"height"`
	FPS    int  `json:"fps"`
	Ramp   bool `json:"ramp"`
}

// validStream drops implausible stream info (and any while not streaming).
func validStream(streaming bool, si *StreamInfo) *StreamInfo {
	if !streaming || si == nil || si.Height < 1 || si.Height > 4320 || (si.FPS != 15 && si.FPS != 30 && si.FPS != 60) {
		return nil
	}
	c := *si
	return &c
}

func sameStream(a, b *StreamInfo) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

type ring struct {
	from  int64
	timer *time.Timer
}

type Voice struct {
	s       *Server
	mu      sync.Mutex
	states  map[int64]*VoiceState // by user
	muted   map[[2]int64]bool     // server mute per (space or dm channel, user)
	rings   map[int64]*ring       // by DM channel
	tickets map[int64]ticket      // moderator moves into locked channels, by user
	rs      livekit.RoomService
	recon   *time.Ticker
	stopCh  chan struct{}
	stopped bool
}

func newVoice(s *Server) *Voice {
	v := &Voice{s: s, states: map[int64]*VoiceState{}, muted: map[[2]int64]bool{}, rings: map[int64]*ring{}, tickets: map[int64]ticket{}, stopCh: make(chan struct{})}
	v.rs = livekit.NewRoomServiceProtobufClient(s.cfg.LiveKitAPIURL, &http.Client{Timeout: livekitAPITimout})
	return v
}

func (v *Voice) stop() {
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.stopped {
		v.stopped = true
		close(v.stopCh)
	}
	for _, r := range v.rings {
		r.timer.Stop()
	}
}

func (s *Server) routesVoice(r chi.Router) {
	r.Post("/voice/join", s.h(s.voiceJoin))
	r.Post("/voice/leave", s.h(s.voiceLeave))
	r.Post("/voice/{id}/moderate", s.h(s.voiceModerate))
	r.Post("/calls/{id}/ring", s.h(s.callRing))
	r.Post("/calls/{id}/decline", s.h(s.callDecline))
}

func roomName(channelID int64) string { return roomPrefix + strconv.FormatInt(channelID, 10) }

func roomChannel(name string) int64 {
	if !strings.HasPrefix(name, roomPrefix) {
		return 0
	}
	id, _ := strconv.ParseInt(name[len(roomPrefix):], 10, 64)
	return id
}

// adminCtx attaches a short-lived RoomAdmin token for one room.
func (v *Voice) adminCtx(ctx context.Context, room string) (context.Context, error) {
	at := lkauth.NewAccessToken(v.s.cfg.LiveKitKey, v.s.cfg.LiveKitSecret).SetValidFor(time.Minute)
	at.SetVideoGrant(&lkauth.VideoGrant{RoomAdmin: true, Room: room})
	tok, err := at.ToJWT()
	if err != nil {
		return nil, err
	}
	h := http.Header{}
	h.Set("Authorization", "Bearer "+tok)
	return twirp.WithHTTPRequestHeaders(ctx, h)
}

// voicePermission derives the LiveKit publish rights from a voice state.
// Who may publish nothing gets CanPublish=false: an empty source list would
// mean "all sources" to LiveKit.
func voicePermission(st *VoiceState) *livekit.ParticipantPermission {
	var src []livekit.TrackSource
	if !st.Listener && !st.ServerMuted {
		src = append(src, livekit.TrackSource_MICROPHONE)
	}
	if !st.NoScreen {
		src = append(src, livekit.TrackSource_SCREEN_SHARE, livekit.TrackSource_SCREEN_SHARE_AUDIO)
	}
	if len(src) == 0 {
		return &livekit.ParticipantPermission{CanSubscribe: true, CanPublish: false}
	}
	return &livekit.ParticipantPermission{CanSubscribe: true, CanPublish: true, CanPublishSources: src}
}

// applyTalk sets what the channel lets this user publish and reports
// whether that changed.
func (st *VoiceState) applyTalk(c *Channel, a Access) bool {
	listener, noScreen := !c.may(a, PermSpeak), !c.may(a, PermStream)
	if st.Listener == listener && st.NoScreen == noScreen {
		return false
	}
	st.Listener, st.NoScreen = listener, noScreen
	if noScreen {
		st.Streaming, st.Stream = false, nil
	}
	return true
}

type ticket struct {
	channel int64
	until   time.Time
}

const ticketTTL = 30 * time.Second

// updatePermission pushes the publish rights of a participant to LiveKit.
func (v *Voice) updatePermission(ctx context.Context, cid, uid int64, st *VoiceState) {
	actx, err := v.adminCtx(ctx, roomName(cid))
	if err == nil {
		_, err = v.rs.UpdateParticipant(actx, &livekit.UpdateParticipantRequest{
			Room: roomName(cid), Identity: ID(uid).String(), Permission: voicePermission(st),
		})
	}
	if err != nil {
		v.s.log.Warn("livekit update participant", "err", err)
	}
}

// refreshSpace re-applies the channel permissions to everyone in a
// voice channel of the space after channels, roles or role assignments
// changed: speaking and streaming rights follow at once, and who may no
// longer connect is disconnected.
func (v *Voice) refreshSpace(ctx context.Context, spaceID int64) {
	s := v.s
	type entry struct{ uid, cid int64 }
	var es []entry
	v.mu.Lock()
	for uid, st := range v.states {
		if st.SpaceID != nil && int64(*st.SpaceID) == spaceID && st.ChannelID != nil {
			es = append(es, entry{uid, int64(*st.ChannelID)})
		}
	}
	v.mu.Unlock()
	chans := map[int64]*Channel{}
	for _, e := range es {
		c := chans[e.cid]
		if c == nil {
			var err error
			if c, err = s.loadChannel(ctx, e.cid); err != nil || s.fillOverrides(ctx, s.db.R, c) != nil {
				continue
			}
			chans[e.cid] = c
		}
		a, err := spaceAccess(ctx, s.db.R, spaceID, e.uid)
		if err != nil || !a.Member {
			continue
		}
		v.mu.Lock()
		st := v.states[e.uid]
		if st == nil || st.ChannelID == nil || int64(*st.ChannelID) != e.cid {
			v.mu.Unlock()
			continue
		}
		// A moderator's move admits into a locked channel, never into one
		// the user may not even see.
		if !c.visible(a) || !st.admitted && !c.may(a, PermConnect) {
			v.mu.Unlock()
			v.removeUserFromChannel(ctx, e.cid, e.uid)
			continue
		}
		if !st.applyTalk(c, a) {
			v.mu.Unlock()
			continue
		}
		snap := *st
		v.mu.Unlock()
		v.updatePermission(ctx, e.cid, e.uid, &snap)
		v.emitState(ctx, snap, e.cid)
	}
}

// emitState sends voice_state_update to everyone who can see the channel.
func (v *Voice) emitState(ctx context.Context, st VoiceState, prevChannel int64) {
	s := v.s
	var targets []int64
	add := func(cid int64) {
		if cid == 0 {
			return
		}
		c, err := s.loadChannel(ctx, cid)
		if err != nil {
			return
		}
		ids, err := s.channelAudience(ctx, c)
		if err == nil {
			targets = append(targets, ids...)
		}
	}
	if st.ChannelID != nil {
		add(int64(*st.ChannelID))
	}
	seen := map[int64]bool{int64(st.UserID): true}
	uniq := []int64{int64(st.UserID)}
	for _, t := range targets {
		if !seen[t] {
			seen[t] = true
			uniq = append(uniq, t)
		}
	}
	s.hub.toUsers(uniq, "voice_state_update", st)
	// Who saw the previous channel but may not see the new one
	// only learns that the user left.
	if prevChannel != 0 && (st.ChannelID == nil || int64(*st.ChannelID) != prevChannel) {
		targets = targets[:0]
		add(prevChannel)
		var rest []int64
		for _, t := range targets {
			if !seen[t] {
				seen[t] = true
				rest = append(rest, t)
			}
		}
		if len(rest) > 0 {
			left := st
			left.ChannelID, left.SpaceID = nil, nil
			left.Streaming, left.Watching = false, nil
			s.hub.toUsers(rest, "voice_state_update", left)
		}
	}
}

// statesVisibleTo lists voice states in channels the user can see (ready).
func (v *Voice) statesVisibleTo(ctx context.Context, uid int64) []VoiceState {
	v.mu.Lock()
	all := make([]VoiceState, 0, len(v.states))
	for _, st := range v.states {
		all = append(all, *st)
	}
	v.mu.Unlock()
	out := []VoiceState{}
	if len(all) == 0 {
		return out
	}
	spaces, _ := queryIDs(ctx, v.s.db.R, `SELECT space_id FROM space_members WHERE user_id=?`, uid)
	dms, _ := queryIDs(ctx, v.s.db.R, `SELECT channel_id FROM channel_members WHERE user_id=?`, uid)
	vis := map[int64]bool{}
	for _, x := range spaces {
		vis[x] = true
	}
	for _, x := range dms {
		vis[x] = true
	}
	// Private channels: only states in channels uid may view.
	access := map[int64]Access{}
	seesChannel := map[int64]bool{}
	sees := func(spaceID, cid int64) bool {
		if ok, done := seesChannel[cid]; done {
			return ok
		}
		a, done := access[spaceID]
		if !done {
			a, _ = spaceAccess(ctx, v.s.db.R, spaceID, uid)
			access[spaceID] = a
		}
		c, err := v.s.loadChannel(ctx, cid)
		ok := err == nil && v.s.fillOverrides(ctx, v.s.db.R, c) == nil && c.visible(a)
		seesChannel[cid] = ok
		return ok
	}
	for _, st := range all {
		if st.ChannelID == nil {
			continue
		}
		container := int64(*st.ChannelID)
		if st.SpaceID != nil {
			container = int64(*st.SpaceID)
			if vis[container] && !sees(container, int64(*st.ChannelID)) {
				continue
			}
		}
		if vis[container] {
			out = append(out, st)
		}
	}
	return out
}

func (v *Voice) channelCount(cid int64, except int64) int {
	n := 0
	for uid, st := range v.states {
		if uid != except && st.ChannelID != nil && int64(*st.ChannelID) == cid {
			n++
		}
	}
	return n
}

func (v *Voice) ensureReconcile() {
	if v.recon != nil || v.stopped {
		return
	}
	v.recon = time.NewTicker(reconcileEvery)
	t := v.recon
	go func() {
		for {
			select {
			case <-v.stopCh:
				t.Stop()
				return
			case <-t.C:
				if !v.reconcile() {
					return
				}
			}
		}
	}()
}

// ---------- join / leave ----------

func (s *Server) voiceJoin(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		ChannelID ID `json:"channelId"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	se, ctx := sess(r), r.Context()
	c, a, err := s.channelAccess(ctx, se, int64(req.ChannelID))
	if err != nil {
		return err
	}
	if c.Kind != "voice" && !c.isDM() {
		return badRequest("not_voice_channel")
	}
	if c.Kind == "dm" {
		if err := s.checkDMAllowed(ctx, c, se.UserID); err != nil {
			return err
		}
	}
	if s.cfg.LiveKitURL == "" {
		return errf(http.StatusServiceUnavailable, "voice_unavailable", "voice not configured")
	}
	var epoch int64
	if c.isDM() {
		epoch = c.E2EEEpoch
	} else if err := s.db.R.QueryRowContext(ctx, `SELECT e2ee_epoch FROM spaces WHERE id=?`, c.SpaceID).Scan(&epoch); err != nil {
		return err
	}
	if err := s.fillOverrides(ctx, s.db.R, c); err != nil {
		return err
	}
	name := ""
	_ = s.db.R.QueryRowContext(ctx, `SELECT display_name FROM users WHERE id=?`, se.UserID).Scan(&name)

	v := s.voice
	container := c.containerID()
	v.mu.Lock()
	count := v.channelCount(c.ID, se.UserID)
	// The operator's limit also binds moderators: LiveKit enforces it anyway
	// and would otherwise fail the join with a less helpful error.
	if count >= s.cfg.MaxVoiceParticipants || (c.UserLimit > 0 && !a.Has(PermVoiceModerate) && count >= c.UserLimit) {
		v.mu.Unlock()
		return errf(http.StatusForbidden, "channel_full", "channel is full")
	}
	admitted := false
	if !c.isDM() && !c.may(a, PermConnect) {
		tk, ok := v.tickets[se.UserID]
		if !ok || tk.channel != c.ID || time.Now().After(tk.until) {
			v.mu.Unlock()
			return errf(http.StatusForbidden, "channel_locked", "channel is locked")
		}
		admitted = true
	}
	delete(v.tickets, se.UserID)
	serverMuted := v.muted[[2]int64{container, se.UserID}]
	prev := int64(0)
	st := v.states[se.UserID]
	if st != nil && st.ChannelID != nil {
		prev = int64(*st.ChannelID)
	}
	cid := ID(c.ID)
	st = &VoiceState{UserID: ID(se.UserID), ChannelID: &cid, SpaceID: optID(c.SpaceID), ServerMuted: serverMuted, joinedAt: time.Now(), admitted: admitted}
	if old := v.states[se.UserID]; old != nil && prev == c.ID {
		st.Muted, st.Deafened, st.Streaming, st.Stream, st.confirmed = old.Muted, old.Deafened, old.Streaming, old.Stream, old.confirmed
		st.admitted = st.admitted || old.admitted
	}
	if !c.isDM() {
		st.applyTalk(c, a)
	}
	v.states[se.UserID] = st
	snap := *st
	v.ensureReconcile()
	answered := v.rings[c.ID] != nil && v.rings[c.ID].from != se.UserID
	if answered {
		v.rings[c.ID].timer.Stop()
		delete(v.rings, c.ID)
	}
	v.mu.Unlock()

	grant := &lkauth.VideoGrant{RoomJoin: true, Room: roomName(c.ID)}
	perm := voicePermission(&snap)
	grant.SetCanPublish(perm.CanPublish)
	if perm.CanPublish {
		grant.SetCanPublishSources(perm.CanPublishSources)
	}
	grant.SetCanPublishData(false)
	grant.SetCanSubscribe(true)
	grant.SetCanUpdateOwnMetadata(false)
	at := lkauth.NewAccessToken(s.cfg.LiveKitKey, s.cfg.LiveKitSecret).
		SetIdentity(ID(se.UserID).String()).SetName(name).SetValidFor(voiceTokenTTL)
	at.SetVideoGrant(grant)
	tok, err := at.ToJWT()
	if err != nil {
		return err
	}
	if prev != 0 && prev != c.ID {
		go v.removeFromRoom(prev, se.UserID)
	}
	v.emitState(ctx, snap, prev)
	if answered {
		s.emitChannel(ctx, c, "call_end", map[string]any{"channelId": ID(c.ID), "reason": "answered", "userId": ID(se.UserID)})
	}
	writeJSON(w, 200, map[string]any{
		"livekitUrl": s.cfg.LiveKitURL, "token": tok, "roomName": roomName(c.ID),
		"e2ee": map[string]any{"containerId": ID(container), "epoch": epoch},
	})
	return nil
}

func (s *Server) voiceLeave(w http.ResponseWriter, r *http.Request) error {
	se := sess(r)
	s.voice.clear(r.Context(), se.UserID, 0)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// clear removes the user's voice state (only if in channel cid, when cid≠0).
func (v *Voice) clear(ctx context.Context, uid, cid int64) {
	v.mu.Lock()
	st := v.states[uid]
	if st == nil || st.ChannelID == nil || (cid != 0 && int64(*st.ChannelID) != cid) {
		v.mu.Unlock()
		return
	}
	prev := int64(*st.ChannelID)
	delete(v.states, uid)
	v.mu.Unlock()
	v.emitState(ctx, VoiceState{UserID: ID(uid)}, prev)
	v.maybeEndCall(ctx, prev)
}

// maybeEndCall ends ringing when the last participant left a DM call.
func (v *Voice) maybeEndCall(ctx context.Context, cid int64) {
	v.mu.Lock()
	n := v.channelCount(cid, 0)
	r := v.rings[cid]
	if n == 0 && r != nil {
		r.timer.Stop()
		delete(v.rings, cid)
	}
	v.mu.Unlock()
	if n == 0 && r != nil {
		if c, err := v.s.loadChannel(ctx, cid); err == nil {
			v.s.emitChannel(ctx, c, "call_end", map[string]any{"channelId": ID(cid), "reason": "ended"})
		}
	}
}

func (v *Voice) removeFromRoom(cid, uid int64) {
	ctx, cancel := context.WithTimeout(context.Background(), livekitAPITimout)
	defer cancel()
	actx, err := v.adminCtx(ctx, roomName(cid))
	if err != nil {
		return
	}
	_, _ = v.rs.RemoveParticipant(actx, &livekit.RoomParticipantIdentity{Room: roomName(cid), Identity: ID(uid).String()})
}

// removeUserFromSpace disconnects a user from any voice channel of the space.
func (v *Voice) removeUserFromSpace(ctx context.Context, spaceID, uid int64) {
	v.mu.Lock()
	st := v.states[uid]
	in := st != nil && st.SpaceID != nil && int64(*st.SpaceID) == spaceID && st.ChannelID != nil
	var cid int64
	if in {
		cid = int64(*st.ChannelID)
	}
	delete(v.muted, [2]int64{spaceID, uid})
	v.mu.Unlock()
	if in {
		v.clear(ctx, uid, cid)
		go v.removeFromRoom(cid, uid)
	}
}

func (v *Voice) removeUserFromChannel(ctx context.Context, cid, uid int64) {
	v.mu.Lock()
	st := v.states[uid]
	in := st != nil && st.ChannelID != nil && int64(*st.ChannelID) == cid
	v.mu.Unlock()
	if in {
		v.clear(ctx, uid, cid)
		go v.removeFromRoom(cid, uid)
	}
}

// closeChannel drops all states of a deleted channel and closes the room.
func (v *Voice) closeChannel(ctx context.Context, cid int64) {
	v.mu.Lock()
	var users []int64
	for uid, st := range v.states {
		if st.ChannelID != nil && int64(*st.ChannelID) == cid {
			users = append(users, uid)
			delete(v.states, uid)
		}
	}
	v.mu.Unlock()
	for _, uid := range users {
		v.s.hub.toUser(uid, "voice_state_update", VoiceState{UserID: ID(uid)})
	}
	if len(users) > 0 {
		go func() {
			c, cancel := context.WithTimeout(context.Background(), livekitAPITimout)
			defer cancel()
			actx, err := v.adminCtx(c, roomName(cid))
			if err == nil {
				_, _ = v.rs.DeleteRoom(actx, &livekit.DeleteRoomRequest{Room: roomName(cid)})
			}
		}()
	}
}

// ---------- client-reported flags ----------

// maxWatching bounds the watching list of one user.
const maxWatching = 8

func (v *Voice) wsVoiceStateSet(se *Session, raw json.RawMessage) {
	var d struct {
		Muted     bool        `json:"muted"`
		Deafened  bool        `json:"deafened"`
		Streaming bool        `json:"streaming"`
		Watching  *[]ID       `json:"watching"`
		Stream    *StreamInfo `json:"stream"`
	}
	if json.Unmarshal(raw, &d) != nil {
		return
	}
	v.mu.Lock()
	st := v.states[se.UserID]
	if st == nil || st.ChannelID == nil {
		v.mu.Unlock()
		return
	}
	watching := st.Watching
	if d.Watching != nil {
		watching = v.validWatching(se.UserID, *st.ChannelID, *d.Watching)
	}
	if st.NoScreen {
		d.Streaming = false // the channel does not let this user stream
	}
	stream := validStream(d.Streaming, d.Stream)
	if st.Muted == d.Muted && st.Deafened == d.Deafened && st.Streaming == d.Streaming && slices.Equal(st.Watching, watching) && sameStream(st.Stream, stream) {
		v.mu.Unlock()
		return
	}
	st.Muted, st.Deafened, st.Streaming, st.Watching, st.Stream = d.Muted || d.Deafened, d.Deafened, d.Streaming, watching, stream
	snap := *st
	v.mu.Unlock()
	v.emitState(context.Background(), snap, int64(*snap.ChannelID))
}

// validWatching keeps only distinct other users that are in the same channel
// and currently streaming. Caller holds v.mu.
func (v *Voice) validWatching(uid int64, cid ID, ids []ID) []ID {
	out := []ID{}
	for _, id := range ids {
		if len(out) == maxWatching {
			break
		}
		o := v.states[int64(id)]
		if int64(id) == uid || slices.Contains(out, id) || o == nil || o.ChannelID == nil || *o.ChannelID != cid || !o.Streaming {
			continue
		}
		out = append(out, id)
	}
	return out
}

// ---------- moderation ----------

func (s *Server) voiceModerate(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		UserID          ID     `json:"userId"`
		Action          string `json:"action"`
		TargetChannelID ID     `json:"targetChannelId"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	se, ctx := sess(r), r.Context()
	cid, err := pathID(r, "id")
	if err != nil {
		return err
	}
	c, a, err := s.channelAccess(ctx, se, cid)
	if err != nil {
		return err
	}
	if c.isDM() || c.Kind != "voice" {
		return badRequest("not_voice_channel")
	}
	if !a.Has(PermVoiceModerate) {
		return errForbidden
	}
	target := int64(req.UserID)
	ta, err := spaceAccess(ctx, s.db.R, c.SpaceID, target)
	if err != nil {
		return err
	}
	if !ta.Member {
		return notFound("unknown_member")
	}
	if target != se.UserID && !a.Outranks(ta) {
		return errForbidden
	}
	v := s.voice
	v.mu.Lock()
	st := v.states[target]
	inChannel := st != nil && st.ChannelID != nil && int64(*st.ChannelID) == cid
	v.mu.Unlock()

	switch req.Action {
	case "mute", "unmute":
		mute := req.Action == "mute"
		v.mu.Lock()
		if mute {
			v.muted[[2]int64{c.SpaceID, target}] = true
		} else {
			delete(v.muted, [2]int64{c.SpaceID, target})
		}
		var snap *VoiceState
		if st := v.states[target]; st != nil && st.SpaceID != nil && int64(*st.SpaceID) == c.SpaceID {
			st.ServerMuted = mute
			cp := *st
			snap = &cp
		}
		v.mu.Unlock()
		if snap != nil {
			v.updatePermission(ctx, int64(*snap.ChannelID), target, snap)
			v.emitState(ctx, *snap, int64(*snap.ChannelID))
		}
	case "disconnect":
		if !inChannel {
			return notFound("not_in_channel")
		}
		v.clear(ctx, target, cid)
		go v.removeFromRoom(cid, target)
	case "move":
		if !inChannel {
			return notFound("not_in_channel")
		}
		dst, _, err := s.channelAccess(ctx, se, int64(req.TargetChannelID))
		if err != nil || dst.SpaceID != c.SpaceID || dst.Kind != "voice" || !dst.visible(ta) {
			return badRequest("invalid_target_channel")
		}
		v.mu.Lock()
		v.tickets[target] = ticket{channel: dst.ID, until: time.Now().Add(ticketTTL)}
		v.mu.Unlock()
		s.hub.toUser(target, "voice_move", map[string]any{"channelId": ID(dst.ID), "byUserId": ID(se.UserID)})
	default:
		return badRequest("invalid_action")
	}
	s.audit(ctx, se.UserID, c.SpaceID, "voice_"+req.Action, target, map[string]any{"channelId": ID(cid)})
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// ---------- DM calls ----------

func (s *Server) callRing(w http.ResponseWriter, r *http.Request) error {
	se, ctx := sess(r), r.Context()
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	c, _, err := s.channelAccess(ctx, se, id)
	if err != nil {
		return err
	}
	if !c.isDM() {
		return badRequest("not_dm")
	}
	if c.Kind == "dm" {
		if err := s.checkDMAllowed(ctx, c, se.UserID); err != nil {
			return err
		}
	}
	v := s.voice
	v.mu.Lock()
	if old := v.rings[id]; old != nil {
		old.timer.Stop()
	}
	rg := &ring{from: se.UserID}
	rg.timer = time.AfterFunc(ringTimeout, func() {
		v.mu.Lock()
		cur := v.rings[id]
		if cur == rg {
			delete(v.rings, id)
		}
		v.mu.Unlock()
		if cur == rg {
			s.emitChannel(context.Background(), c, "call_end", map[string]any{"channelId": ID(id), "reason": "timeout"})
		}
	})
	v.rings[id] = rg
	v.mu.Unlock()
	ids, err := s.channelAudience(ctx, c)
	if err != nil {
		return err
	}
	var others []int64
	for _, u := range ids {
		if u != se.UserID {
			others = append(others, u)
		}
	}
	s.hub.toUsers(others, "call_ring", map[string]any{"channelId": ID(id), "fromUserId": ID(se.UserID), "expiresAt": s.nowMs() + ringTimeout.Milliseconds()})
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) callDecline(w http.ResponseWriter, r *http.Request) error {
	se, ctx := sess(r), r.Context()
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	c, _, err := s.channelAccess(ctx, se, id)
	if err != nil {
		return err
	}
	if !c.isDM() {
		return badRequest("not_dm")
	}
	v := s.voice
	v.mu.Lock()
	rg := v.rings[id]
	end := rg != nil && c.Kind == "dm" // a 1:1 decline ends the call attempt
	if end {
		rg.timer.Stop()
		delete(v.rings, id)
	}
	v.mu.Unlock()
	reason := "declined"
	if !end {
		// Group DM: only the decliner stops ringing; others keep ringing.
		s.hub.toUser(se.UserID, "call_end", map[string]any{"channelId": ID(id), "reason": reason, "userId": ID(se.UserID)})
	} else {
		s.emitChannel(ctx, c, "call_end", map[string]any{"channelId": ID(id), "reason": reason, "userId": ID(se.UserID)})
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// ---------- LiveKit webhooks & reconcile ----------

func (v *Voice) webhook(w http.ResponseWriter, r *http.Request) {
	s := v.s
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	ev, err := v.receiveWebhook(r)
	if err != nil {
		http.Error(w, "invalid", http.StatusUnauthorized)
		return
	}
	ctx := r.Context()
	if ev.Id != "" {
		res, err := s.db.W.ExecContext(ctx, `INSERT OR IGNORE INTO webhook_events(id, received_at) VALUES (?,?)`, ev.Id, s.nowMs())
		if err == nil {
			if n, _ := res.RowsAffected(); n == 0 {
				w.WriteHeader(http.StatusOK) // duplicate
				return
			}
		}
	}
	if ev.Room == nil || ev.Participant == nil && ev.Event != "room_finished" {
		w.WriteHeader(http.StatusOK)
		return
	}
	cid := roomChannel(ev.Room.Name)
	var uid int64
	if ev.Participant != nil {
		uid, _ = strconv.ParseInt(ev.Participant.Identity, 10, 64)
	}
	switch ev.Event {
	case "participant_joined":
		v.confirm(ctx, cid, uid)
	case "participant_left", "participant_connection_aborted":
		v.clear(ctx, uid, cid)
	case "track_published", "track_unpublished":
		if ev.Track != nil && ev.Track.Source == livekit.TrackSource_SCREEN_SHARE {
			v.setStreaming(ctx, uid, cid, ev.Event == "track_published")
		}
	case "room_finished":
		v.mu.Lock()
		var users []int64
		for u, st := range v.states {
			if st.ChannelID != nil && int64(*st.ChannelID) == cid && st.confirmed {
				users = append(users, u)
			}
		}
		v.mu.Unlock()
		for _, u := range users {
			v.clear(ctx, u, cid)
		}
	}
	w.WriteHeader(http.StatusOK)
}

func (v *Voice) confirm(ctx context.Context, cid, uid int64) {
	if cid == 0 || uid == 0 {
		return
	}
	v.mu.Lock()
	st := v.states[uid]
	if st != nil && st.ChannelID != nil && int64(*st.ChannelID) == cid {
		st.confirmed = true
		v.mu.Unlock()
		return
	}
	v.mu.Unlock()
	// Joined without a fresh /voice/join (e.g. after a server restart):
	// adopt it if the user may still be there, otherwise kick.
	c, err := v.s.loadChannel(ctx, cid)
	if err != nil {
		go v.removeFromRoom(cid, uid)
		return
	}
	allowed := false
	if c.isDM() {
		var n int
		_ = v.s.db.R.QueryRowContext(ctx, `SELECT COUNT(*) FROM channel_members WHERE channel_id=? AND user_id=?`, cid, uid).Scan(&n)
		allowed = n > 0
	} else if a, err := spaceAccess(ctx, v.s.db.R, c.SpaceID, uid); err == nil && a.Member {
		allowed = true
	}
	if !allowed {
		go v.removeFromRoom(cid, uid)
		return
	}
	id := ID(cid)
	v.mu.Lock()
	prev := int64(0)
	if old := v.states[uid]; old != nil && old.ChannelID != nil {
		prev = int64(*old.ChannelID)
	}
	st = &VoiceState{UserID: ID(uid), ChannelID: &id, SpaceID: optID(c.SpaceID), ServerMuted: v.muted[[2]int64{c.containerID(), uid}], joinedAt: time.Now(), confirmed: true}
	v.states[uid] = st
	snap := *st
	v.ensureReconcile()
	v.mu.Unlock()
	v.emitState(ctx, snap, prev)
}

func (v *Voice) setStreaming(ctx context.Context, uid, cid int64, on bool) {
	v.mu.Lock()
	st := v.states[uid]
	if st == nil || st.ChannelID == nil || int64(*st.ChannelID) != cid || st.Streaming == on {
		v.mu.Unlock()
		return
	}
	st.Streaming = on
	if !on {
		st.Stream = nil
	}
	snap := *st
	v.mu.Unlock()
	v.emitState(ctx, snap, cid)
}

// reconcile compares in-memory state with LiveKit. Returns false (stopping
// the ticker) once nobody is in voice.
func (v *Voice) reconcile() bool {
	v.mu.Lock()
	rooms := map[int64][]int64{}
	for uid, st := range v.states {
		if st.ChannelID != nil {
			rooms[int64(*st.ChannelID)] = append(rooms[int64(*st.ChannelID)], uid)
		}
	}
	if len(rooms) == 0 {
		v.recon.Stop()
		v.recon = nil
		v.mu.Unlock()
		return false
	}
	v.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for cid, users := range rooms {
		actx, err := v.adminCtx(ctx, roomName(cid))
		if err != nil {
			continue
		}
		res, err := v.rs.ListParticipants(actx, &livekit.ListParticipantsRequest{Room: roomName(cid)})
		present := map[int64]bool{}
		if err != nil {
			var te twirp.Error
			if !asTwirp(err, &te) || te.Code() != twirp.NotFound {
				continue // LiveKit unreachable: keep state
			}
		} else {
			for _, p := range res.Participants {
				id, _ := strconv.ParseInt(p.Identity, 10, 64)
				present[id] = true
			}
		}
		for _, uid := range users {
			v.mu.Lock()
			st := v.states[uid]
			stale := st != nil && st.ChannelID != nil && int64(*st.ChannelID) == cid && !present[uid] && time.Since(st.joinedAt) > joinGrace
			if st != nil && present[uid] {
				st.confirmed = true
			}
			v.mu.Unlock()
			if stale {
				v.clear(ctx, uid, cid)
			}
		}
	}
	return true
}

func asTwirp(err error, out *twirp.Error) bool {
	te, ok := err.(twirp.Error)
	if ok {
		*out = te
	}
	return ok
}

// receiveWebhook verifies the LiveKit webhook signature (JWT signed with the
// API secret whose sha256 claim covers the body) and decodes the event.
// Mirrors github.com/livekit/protocol/webhook.ReceiveWebhookEvent (Apache-2.0)
// without importing that package's notifier dependencies.
func (v *Voice) receiveWebhook(r *http.Request) (*livekit.WebhookEvent, error) {
	data, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	tok := r.Header.Get("Authorization")
	if tok == "" {
		return nil, errors.New("missing authorization")
	}
	ver, err := lkauth.ParseAPIToken(tok)
	if err != nil {
		return nil, err
	}
	if subtle.ConstantTimeCompare([]byte(ver.APIKey()), []byte(v.s.cfg.LiveKitKey)) != 1 {
		return nil, errors.New("unknown api key")
	}
	_, claims, err := ver.Verify(v.s.cfg.LiveKitSecret)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	if subtle.ConstantTimeCompare([]byte(claims.Sha256), []byte(base64.StdEncoding.EncodeToString(sum[:]))) != 1 {
		return nil, errors.New("checksum mismatch")
	}
	ev := &livekit.WebhookEvent{}
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true, AllowPartial: true}).Unmarshal(data, ev); err != nil {
		return nil, err
	}
	return ev, nil
}

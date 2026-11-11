package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/syntstar/lightspeak/server/internal/auth"
)

// WebSocket gateway: identify/ready/resume, heartbeat and event fan-out.
// Each gateway session keeps a small replay buffer so a client that lost its
// connection for up to resumeWindow gets the missed events instead of a full
// ready payload.

const (
	resumeWindow   = 2 * time.Minute
	resumeBufSize  = 500
	identifyWait   = 10 * time.Second
	readIdle       = 75 * time.Second // client pings every 30 s
	writeTimeout   = 10 * time.Second
	outQueue       = 256
	offlineGrace   = 10 * time.Second
	wsReadLimit    = 64 << 10
	closeAuthFail  = 4004
	closeInvalid   = 4001
	closeRateLimit = 4008
	closeRevoked   = 4005
	closeSlow      = 4009
)

type bufFrame struct {
	seq  int64
	data []byte
}

type wsConn struct {
	c         *websocket.Conn
	out       chan []byte
	closeOnce sync.Once
	closed    chan struct{}
}

func (w *wsConn) close(code websocket.StatusCode, reason string) {
	w.closeOnce.Do(func() {
		close(w.closed)
		go func() { _ = w.c.Close(code, reason) }()
	})
}

type gwSession struct {
	id            string
	userID        int64
	authSessionID int64
	deviceID      int64

	mu     sync.Mutex
	seq    int64
	buf    []bufFrame
	conn   *wsConn
	expire *time.Timer
}

type Hub struct {
	s        *Server
	mu       sync.Mutex
	sessions map[string]*gwSession
	byUser   map[int64]map[*gwSession]struct{}
	online   map[int64]int         // connected sockets per user
	offline  map[int64]*time.Timer // pending offline transitions
}

func newHub(s *Server) *Hub {
	return &Hub{s: s, sessions: map[string]*gwSession{}, byUser: map[int64]map[*gwSession]struct{}{},
		online: map[int64]int{}, offline: map[int64]*time.Timer{}}
}

func frameBytes(op string, raw []byte, seq int64) []byte {
	var b bytes.Buffer
	b.Grow(len(raw) + len(op) + 32)
	b.WriteString(`{"op":`)
	opj, _ := json.Marshal(op)
	b.Write(opj)
	if raw != nil {
		b.WriteString(`,"d":`)
		b.Write(raw)
	}
	if seq > 0 {
		b.WriteString(`,"s":`)
		b.WriteString(strconv.FormatInt(seq, 10))
	}
	b.WriteByte('}')
	return b.Bytes()
}

// deliver appends to the replay buffer (unless ephemeral) and pushes to the
// live socket. Must not block.
func (g *gwSession) deliver(op string, raw []byte, ephemeral bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	var data []byte
	if ephemeral {
		if g.conn == nil {
			return
		}
		data = frameBytes(op, raw, 0)
	} else {
		g.seq++
		data = frameBytes(op, raw, g.seq)
		g.buf = append(g.buf, bufFrame{g.seq, data})
		if len(g.buf) > resumeBufSize {
			g.buf = append(g.buf[:0:0], g.buf[len(g.buf)-resumeBufSize:]...)
		}
	}
	if g.conn != nil {
		select {
		case g.conn.out <- data:
		default:
			g.conn.close(closeSlow, "slow consumer")
		}
	}
}

// ---------- fan-out API ----------

func (h *Hub) send(userIDs []int64, op string, d any, ephemeral bool) {
	raw, err := json.Marshal(d)
	if err != nil {
		h.s.log.Error("marshal event", "op", op, "err", err)
		return
	}
	h.mu.Lock()
	var targets []*gwSession
	for _, uid := range userIDs {
		for g := range h.byUser[uid] {
			targets = append(targets, g)
		}
	}
	h.mu.Unlock()
	for _, g := range targets {
		g.deliver(op, raw, ephemeral)
	}
}

func (h *Hub) toUsers(ids []int64, op string, d any) { h.send(ids, op, d, false) }
func (h *Hub) toUser(id int64, op string, d any)     { h.send([]int64{id}, op, d, false) }

func (s *Server) emitChannel(ctx context.Context, c *Channel, op string, d any) {
	ids, err := s.channelAudience(ctx, c)
	if err != nil {
		s.log.Warn("audience", "err", err)
		return
	}
	s.hub.toUsers(ids, op, d)
}

func (s *Server) emitSpace(ctx context.Context, spaceID int64, op string, d any) {
	ids, err := s.spaceAudience(ctx, spaceID)
	if err != nil {
		s.log.Warn("audience", "err", err)
		return
	}
	s.hub.toUsers(ids, op, d)
}

// isOnline reports whether the user has at least one live socket.
func (h *Hub) isOnline(uid int64) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.online[uid] > 0
}

// anyDeviceOnline reports whether one of the devices has a live socket.
func (h *Hub) anyDeviceOnline(devices map[int64]struct{}) bool {
	h.mu.Lock()
	var cand []*gwSession
	for _, g := range h.sessions {
		if _, ok := devices[g.deviceID]; ok {
			cand = append(cand, g)
		}
	}
	h.mu.Unlock()
	for _, g := range cand {
		g.mu.Lock()
		live := g.conn != nil
		g.mu.Unlock()
		if live {
			return true
		}
	}
	return false
}

// dropAuthSession disconnects all gateway sessions bound to a revoked login.
func (h *Hub) dropAuthSession(authSessionID int64) {
	h.dropWhere(func(g *gwSession) bool { return g.authSessionID == authSessionID }, closeRevoked, "session revoked")
}

func (h *Hub) dropWhere(pred func(*gwSession) bool, code websocket.StatusCode, reason string) {
	h.mu.Lock()
	var victims []*gwSession
	for _, g := range h.sessions {
		if pred(g) {
			victims = append(victims, g)
		}
	}
	for _, g := range victims {
		h.removeLocked(g)
	}
	h.mu.Unlock()
	for _, g := range victims {
		g.mu.Lock()
		if g.conn != nil {
			g.conn.close(code, reason)
		}
		g.mu.Unlock()
	}
}

func (h *Hub) removeLocked(g *gwSession) {
	delete(h.sessions, g.id)
	if m := h.byUser[g.userID]; m != nil {
		delete(m, g)
		if len(m) == 0 {
			delete(h.byUser, g.userID)
		}
	}
	if g.expire != nil {
		g.expire.Stop()
	}
}

func (h *Hub) closeAll() {
	// Shutdown/restart (e.g. an update): clients reconnect and keep their
	// login. Never closeRevoked here – that logs every client out.
	h.dropWhere(func(*gwSession) bool { return true }, websocket.StatusServiceRestart, "server restart")
}

// ---------- presence ----------

func (h *Hub) connUp(uid int64) {
	h.mu.Lock()
	h.online[uid]++
	first := h.online[uid] == 1
	if t := h.offline[uid]; t != nil {
		t.Stop()
		delete(h.offline, uid)
		first = false // never went offline from the others' point of view
	}
	h.mu.Unlock()
	if first {
		h.s.broadcastPresence(context.Background(), uid)
	}
}

func (h *Hub) connDown(uid int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.online[uid]--
	if h.online[uid] > 0 {
		return
	}
	delete(h.online, uid)
	if h.offline[uid] != nil {
		return
	}
	h.offline[uid] = time.AfterFunc(offlineGrace, func() {
		h.mu.Lock()
		delete(h.offline, uid)
		still := h.online[uid] > 0
		h.mu.Unlock()
		if !still {
			h.s.broadcastPresence(context.Background(), uid)
		}
	})
}

// ---------- connection handling ----------

type inFrame struct {
	Op string          `json:"op"`
	D  json.RawMessage `json:"d"`
}

type identifyData struct {
	Token           string `json:"token"`
	ProtocolVersion int    `json:"protocolVersion"`
	Resume          *struct {
		SessionID string `json:"sessionId"`
		LastSeq   int64  `json:"lastSeq"`
	} `json:"resume"`
}

func (h *Hub) serveWS(w http.ResponseWriter, r *http.Request) {
	s := h.s
	if o := r.Header.Get("Origin"); o != "" && !s.originAllowed(o) && !s.sameOrigin(r) {
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return
	}
	// Browser client: the session cookie is only honoured when the
	// upgrade comes from our own page (browsers always send Origin here).
	cookieTok := ""
	if s.sameOrigin(r) {
		if c, err := r.Cookie(s.webCookieName()); err == nil {
			cookieTok = c.Value
		}
	}
	if !s.lim.ws.Allow("ip" + s.clientIP(r)) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
		return
	}
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true, // origin checked above against the allow-list
		CompressionMode:    websocket.CompressionDisabled,
	})
	if err != nil {
		return
	}
	c.SetReadLimit(wsReadLimit)
	conn := &wsConn{c: c, out: make(chan []byte, outQueue), closed: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// identify
	ictx, icancel := context.WithTimeout(ctx, identifyWait)
	_, data, err := c.Read(ictx)
	icancel()
	if err != nil {
		_ = c.Close(closeInvalid, "identify expected")
		return
	}
	var f inFrame
	var id identifyData
	if json.Unmarshal(data, &f) != nil || f.Op != "identify" || json.Unmarshal(f.D, &id) != nil {
		_ = c.Close(closeInvalid, "identify expected")
		return
	}
	tok := id.Token
	if tok == "" {
		tok = cookieTok
	}
	se, err := s.authenticate(ctx, tok)
	if err != nil {
		_ = c.Close(closeAuthFail, "authentication failed")
		return
	}

	g := h.attach(se, id, conn)
	if g == nil {
		return
	}

	// writer
	go func() {
		for {
			select {
			case b := <-conn.out:
				wctx, wc := context.WithTimeout(ctx, writeTimeout)
				err := c.Write(wctx, websocket.MessageText, b)
				wc()
				if err != nil {
					conn.close(websocket.StatusGoingAway, "write failed")
					return
				}
			case <-conn.closed:
				return
			}
		}
	}()

	h.connUp(se.UserID)
	defer func() {
		conn.close(websocket.StatusNormalClosure, "")
		h.detach(g, conn)
		h.connDown(se.UserID)
	}()

	for {
		rctx, rc := context.WithTimeout(ctx, readIdle)
		_, data, err := c.Read(rctx)
		rc()
		if err != nil {
			return
		}
		select {
		case <-conn.closed:
			return
		default:
		}
		var f inFrame
		if json.Unmarshal(data, &f) != nil {
			conn.close(closeInvalid, "bad frame")
			return
		}
		if !s.lim.general.Allow("ws" + strconv.FormatInt(se.UserID, 10)) {
			conn.close(closeRateLimit, "rate limited")
			return
		}
		switch f.Op {
		case "ping":
			select {
			case conn.out <- []byte(`{"op":"pong"}`):
			default:
			}
		case "presence_set":
			s.wsPresenceSet(ctx, se, f.D)
		case "voice_state_set":
			s.voice.wsVoiceStateSet(se, f.D)
		default:
			// unknown ops are ignored for forward compatibility
		}
	}
}

// attach binds the socket to a new or resumed gateway session and queues the
// ready/resumed payload. Returns nil if the socket was closed.
func (h *Hub) attach(se *Session, id identifyData, conn *wsConn) *gwSession {
	s := h.s
	if id.Resume != nil && id.Resume.SessionID != "" {
		if g := h.tryResume(se, id.Resume.SessionID, id.Resume.LastSeq, conn); g != nil {
			return g
		}
		// The writer is not running yet: write directly, then close. The
		// client identifies again on a new socket.
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
			defer cancel()
			_ = conn.c.Write(ctx, websocket.MessageText, []byte(`{"op":"invalid_session"}`))
			conn.close(websocket.StatusNormalClosure, "invalid session")
		}()
		return nil
	}

	// Register first (events are buffered while conn is nil), then build the
	// snapshot, then flush ready + anything that arrived meanwhile. Clients
	// treat events for already-known objects idempotently.
	g := &gwSession{id: auth.RandomCode(24), userID: se.UserID, authSessionID: se.ID, deviceID: se.DeviceID}
	h.mu.Lock()
	h.sessions[g.id] = g
	if h.byUser[g.userID] == nil {
		h.byUser[g.userID] = map[*gwSession]struct{}{}
	}
	h.byUser[g.userID][g] = struct{}{}
	h.mu.Unlock()

	ready, err := s.buildReady(context.Background(), se, g.id)
	if err != nil {
		s.log.Error("ready failed", "err", err)
		h.mu.Lock()
		h.removeLocked(g)
		h.mu.Unlock()
		_ = conn.c.Close(websocket.StatusInternalError, "ready failed")
		return nil
	}
	raw, _ := json.Marshal(ready)
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.buf) >= outQueue-1 {
		g.buf = g.buf[len(g.buf)-(outQueue-2):]
	}
	conn.out <- frameBytes("ready", raw, 0)
	for _, b := range g.buf {
		conn.out <- b.data
	}
	g.conn = conn
	return g
}

// tryResume re-binds a detached (or superseded) session if its buffer still
// covers everything after lastSeq.
func (h *Hub) tryResume(se *Session, sessionID string, lastSeq int64, conn *wsConn) *gwSession {
	h.mu.Lock()
	g := h.sessions[sessionID]
	h.mu.Unlock()
	if g == nil || g.userID != se.UserID || g.authSessionID != se.ID {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if lastSeq > g.seq || (len(g.buf) > 0 && g.buf[0].seq > lastSeq+1) {
		return nil
	}
	var replay [][]byte
	for _, b := range g.buf {
		if b.seq > lastSeq {
			replay = append(replay, b.data)
		}
	}
	if len(replay) >= outQueue-1 {
		return nil // too much missed traffic: a fresh ready is cheaper
	}
	if g.conn != nil {
		g.conn.close(closeInvalid, "replaced by resume")
	}
	if g.expire != nil {
		g.expire.Stop()
		g.expire = nil
	}
	conn.out <- frameBytes("resumed", []byte(`{"sessionId":"`+g.id+`"}`), 0)
	for _, b := range replay {
		conn.out <- b
	}
	g.conn = conn
	return g
}

// detach keeps the session for resumeWindow after the socket is gone.
// A resumed socket that already took over is left alone.
func (h *Hub) detach(g *gwSession, conn *wsConn) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.conn != conn {
		return // already replaced by a resumed socket
	}
	g.conn = nil
	g.expire = time.AfterFunc(resumeWindow, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		g.mu.Lock()
		dead := g.conn == nil
		g.mu.Unlock()
		if dead {
			h.removeLocked(g)
		}
	})
}

// ---------- presence ops ----------

type presence struct {
	UserID     ID     `json:"userId"`
	Status     string `json:"status"`
	StatusText string `json:"statusText"`
}

// effectivePresence hides invisible users and reports offline users.
func (s *Server) effectivePresence(ctx context.Context, uid int64) presence {
	p := presence{UserID: ID(uid), Status: "offline"}
	if !s.hub.isOnline(uid) {
		return p
	}
	var st, txt string
	if err := s.db.R.QueryRowContext(ctx, `SELECT status, status_text FROM users WHERE id=?`, uid).Scan(&st, &txt); err != nil {
		return p
	}
	if st == "invisible" {
		return p
	}
	p.Status, p.StatusText = st, txt
	return p
}

func (s *Server) broadcastPresence(ctx context.Context, uid int64) {
	ids, err := s.relatedUsers(ctx, uid)
	if err != nil {
		return
	}
	p := s.effectivePresence(ctx, uid)
	s.hub.send(ids, "presence_update", p, true)
	// The user's own sessions always see their real status.
	if p.Status == "offline" && s.hub.isOnline(uid) {
		var st, txt string
		_ = s.db.R.QueryRowContext(ctx, `SELECT status, status_text FROM users WHERE id=?`, uid).Scan(&st, &txt)
		s.hub.send([]int64{uid}, "presence_update", presence{ID(uid), st, txt}, true)
	}
}

func validStatus(st string) bool {
	switch st {
	case "online", "idle", "dnd", "invisible":
		return true
	}
	return false
}

func (s *Server) wsPresenceSet(ctx context.Context, se *Session, raw json.RawMessage) {
	var d struct {
		Status     string  `json:"status"`
		StatusText *string `json:"statusText"`
	}
	if json.Unmarshal(raw, &d) != nil || !validStatus(d.Status) {
		return
	}
	if d.StatusText != nil {
		t := clampText(*d.StatusText, 128)
		_, _ = s.db.W.ExecContext(ctx, `UPDATE users SET status=?, status_text=? WHERE id=?`, d.Status, t, se.UserID)
	} else {
		_, _ = s.db.W.ExecContext(ctx, `UPDATE users SET status=? WHERE id=?`, d.Status, se.UserID)
	}
	s.broadcastPresence(ctx, se.UserID)
}

package server

import (
	"context"
	"database/sql"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/syntstar/lightspeak/server/internal/store"
)

const (
	maxAttachmentsPerMsg = 10
	maxMentions          = 50
	maxReactionKinds     = 20
	maxPins              = 50
)

func (s *Server) routesMessages(r chi.Router) {
	r.Get("/channels/{id}/messages", s.h(s.listMessages))
	r.Post("/channels/{id}/messages", s.h(s.createMessage))
	r.Patch("/channels/{id}/messages/{mid}", s.h(s.editMessage))
	r.Delete("/channels/{id}/messages/{mid}", s.h(s.deleteMessage))
	r.Put("/channels/{id}/messages/{mid}/reactions/{emoji}", s.h(s.addReaction))
	r.Delete("/channels/{id}/messages/{mid}/reactions/{emoji}", s.h(s.removeReaction))
	r.Get("/channels/{id}/pins", s.h(s.listPins))
	r.Put("/channels/{id}/pins/{mid}", s.h(s.pin))
	r.Delete("/channels/{id}/pins/{mid}", s.h(s.unpin))
	r.Post("/channels/{id}/typing", s.h(s.typingStart))
	r.Post("/channels/{id}/ack", s.h(s.ack))
	r.Get("/search", s.h(s.search))
}

type reactionDTO struct {
	Emoji string `json:"emoji"`
	Count int    `json:"count"`
	Me    bool   `json:"me"`
}

type refMessage struct {
	ID       ID     `json:"id"`
	AuthorID ID     `json:"authorId"`
	Content  string `json:"content"`
	Deleted  bool   `json:"deleted,omitempty"`
}

type MessageDTO struct {
	ID                ID              `json:"id"`
	ChannelID         ID              `json:"channelId"`
	AuthorID          ID              `json:"authorId"`
	Author            User            `json:"author"`
	Content           string          `json:"content"`
	ReplyToID         *ID             `json:"replyToId,omitempty"`
	ReferencedMessage *refMessage     `json:"referencedMessage,omitempty"`
	Mentions          []ID            `json:"mentions"`
	MentionsEveryone  bool            `json:"mentionsEveryone"`
	Attachments       []AttachmentDTO `json:"attachments"`
	Reactions         []reactionDTO   `json:"reactions"`
	Pinned            bool            `json:"pinned"`
	EditedAt          *int64          `json:"editedAt,omitempty"`
	CreatedAt         int64           `json:"createdAt"`
	Nonce             string          `json:"nonce,omitempty"`
}

type msgRow struct {
	ID, ChannelID, AuthorID, ReplyTo int64
	Content                          string
	Everyone                         bool
	EditedAt                         sql.NullInt64
	CreatedAt                        int64
}

const msgCols = `id, channel_id, author_id, COALESCE(reply_to_id,0), content, mentions_everyone, edited_at, created_at`

func scanMsgs(rows *sql.Rows) ([]msgRow, error) {
	defer rows.Close()
	var out []msgRow
	for rows.Next() {
		var m msgRow
		var ev int
		if err := rows.Scan(&m.ID, &m.ChannelID, &m.AuthorID, &m.ReplyTo, &m.Content, &ev, &m.EditedAt, &m.CreatedAt); err != nil {
			return nil, err
		}
		m.Everyone = ev == 1
		out = append(out, m)
	}
	return out, rows.Err()
}

func idArgs(ids []int64) []any {
	a := make([]any, len(ids))
	for i, v := range ids {
		a[i] = v
	}
	return a
}

// hydrate turns rows into DTOs with authors, attachments, reactions, mentions,
// pins and referenced messages, using one query per kind.
func (s *Server) hydrate(ctx context.Context, viewer int64, rows []msgRow) ([]MessageDTO, error) {
	out := make([]MessageDTO, len(rows))
	if len(rows) == 0 {
		return out, nil
	}
	ids := make([]int64, len(rows))
	var authors, refs []int64
	for i, m := range rows {
		ids[i] = m.ID
		authors = append(authors, m.AuthorID)
		if m.ReplyTo != 0 {
			refs = append(refs, m.ReplyTo)
		}
	}
	in := placeholders(len(ids))
	args := idArgs(ids)

	refMap := map[int64]*refMessage{}
	if len(refs) > 0 {
		rr, err := s.db.R.QueryContext(ctx, `SELECT id, author_id, substr(content,1,200), deleted_at IS NOT NULL FROM messages WHERE id IN (`+placeholders(len(refs))+`)`, idArgs(refs)...)
		if err != nil {
			return nil, err
		}
		for rr.Next() {
			var id, a int64
			var c string
			var del bool
			if err := rr.Scan(&id, &a, &c, &del); err != nil {
				rr.Close()
				return nil, err
			}
			refMap[id] = &refMessage{ID: ID(id), AuthorID: ID(a), Content: c, Deleted: del}
			authors = append(authors, a)
		}
		rr.Close()
	}

	users, err := s.loadUsers(ctx, authors)
	if err != nil {
		return nil, err
	}
	userMap := map[int64]User{}
	for _, u := range users {
		userMap[int64(u.ID)] = u
	}

	mentions := map[int64][]ID{}
	mr, err := s.db.R.QueryContext(ctx, `SELECT message_id, user_id FROM message_mentions WHERE message_id IN (`+in+`)`, args...)
	if err != nil {
		return nil, err
	}
	for mr.Next() {
		var m, u int64
		if err := mr.Scan(&m, &u); err != nil {
			mr.Close()
			return nil, err
		}
		mentions[m] = append(mentions[m], ID(u))
	}
	mr.Close()

	reactions := map[int64][]reactionDTO{}
	rx, err := s.db.R.QueryContext(ctx, `SELECT message_id, emoji, COUNT(*), MAX(user_id=?), MIN(created_at) AS first FROM reactions
		WHERE message_id IN (`+in+`) GROUP BY message_id, emoji ORDER BY first`, append([]any{viewer}, args...)...)
	if err != nil {
		return nil, err
	}
	for rx.Next() {
		var m, first int64
		var d reactionDTO
		var me int
		if err := rx.Scan(&m, &d.Emoji, &d.Count, &me, &first); err != nil {
			rx.Close()
			return nil, err
		}
		d.Me = me == 1
		reactions[m] = append(reactions[m], d)
	}
	rx.Close()

	pinned := map[int64]bool{}
	pr, err := s.db.R.QueryContext(ctx, `SELECT message_id FROM pins WHERE message_id IN (`+in+`)`, args...)
	if err != nil {
		return nil, err
	}
	for pr.Next() {
		var m int64
		if err := pr.Scan(&m); err != nil {
			pr.Close()
			return nil, err
		}
		pinned[m] = true
	}
	pr.Close()

	atts, err := s.files.attachmentsFor(ctx, ids)
	if err != nil {
		return nil, err
	}

	for i, m := range rows {
		d := MessageDTO{ID: ID(m.ID), ChannelID: ID(m.ChannelID), AuthorID: ID(m.AuthorID), Author: userMap[m.AuthorID],
			Content: m.Content, MentionsEveryone: m.Everyone, CreatedAt: m.CreatedAt, Pinned: pinned[m.ID],
			Mentions: mentions[m.ID], Reactions: reactions[m.ID], Attachments: atts[m.ID]}
		if d.Mentions == nil {
			d.Mentions = []ID{}
		}
		if d.Reactions == nil {
			d.Reactions = []reactionDTO{}
		}
		if d.Attachments == nil {
			d.Attachments = []AttachmentDTO{}
		}
		if m.EditedAt.Valid {
			d.EditedAt = &m.EditedAt.Int64
		}
		if m.ReplyTo != 0 {
			d.ReplyToID = optID(m.ReplyTo)
			if ref := refMap[m.ReplyTo]; ref != nil {
				if ref.Deleted {
					ref.Content = ""
				}
				d.ReferencedMessage = ref
			}
		}
		out[i] = d
	}
	return out, nil
}

func queryInt(r *http.Request, k string) (int64, bool) {
	v := r.URL.Query().Get(k)
	if v == "" {
		return 0, false
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

func (s *Server) listMessages(w http.ResponseWriter, r *http.Request) error {
	se, ctx := sess(r), r.Context()
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if _, _, err := s.requireTextChannel(ctx, se, id); err != nil {
		return err
	}
	limit := int64(50)
	if l, ok := queryInt(r, "limit"); ok && l > 0 {
		limit = min(l, 100)
	}
	const base = `SELECT ` + msgCols + ` FROM messages WHERE channel_id=? AND deleted_at IS NULL `
	var rows []msgRow
	if around, ok := queryInt(r, "around"); ok {
		half := limit / 2
		older, err := s.db.R.QueryContext(ctx, base+`AND id<=? ORDER BY id DESC LIMIT ?`, id, around, limit-half)
		if err != nil {
			return err
		}
		a, err := scanMsgs(older)
		if err != nil {
			return err
		}
		newer, err := s.db.R.QueryContext(ctx, base+`AND id>? ORDER BY id ASC LIMIT ?`, id, around, half)
		if err != nil {
			return err
		}
		b, err := scanMsgs(newer)
		if err != nil {
			return err
		}
		rows = append(a, b...)
	} else if after, ok := queryInt(r, "after"); ok {
		q, err := s.db.R.QueryContext(ctx, base+`AND id>? ORDER BY id ASC LIMIT ?`, id, after, limit)
		if err != nil {
			return err
		}
		if rows, err = scanMsgs(q); err != nil {
			return err
		}
	} else {
		before, ok := queryInt(r, "before")
		if !ok {
			before = 1<<63 - 1
		}
		q, err := s.db.R.QueryContext(ctx, base+`AND id<? ORDER BY id DESC LIMIT ?`, id, before, limit)
		if err != nil {
			return err
		}
		if rows, err = scanMsgs(q); err != nil {
			return err
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	out, err := s.hydrate(ctx, se.UserID, rows)
	if err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}

var mentionRe = regexp.MustCompile(`<@(\d{1,20})>`)

// parseMentions returns mentioned user IDs that belong to the audience.
func parseMentions(content string, audience map[int64]bool) []int64 {
	seen := map[int64]bool{}
	var out []int64
	for _, m := range mentionRe.FindAllStringSubmatch(content, -1) {
		id, err := strconv.ParseInt(m[1], 10, 64)
		if err != nil || seen[id] || !audience[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
		if len(out) >= maxMentions {
			break
		}
	}
	return out
}

func cleanMessage(s string, maxRunes int) (string, bool) {
	s = strings.TrimRight(strings.TrimLeft(s, "\n\r "), "\n\r\t ")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	if !utf8.ValidString(s) {
		return "", false
	}
	if utf8.RuneCountInString(s) > maxRunes {
		return "", false
	}
	var b strings.Builder
	for _, r := range s {
		if (r < 0x20 && r != '\n' && r != '\t') || r == 0x7f {
			continue
		}
		b.WriteRune(r)
	}
	return b.String(), true
}

func (s *Server) createMessage(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Content       string `json:"content"`
		ReplyToID     ID     `json:"replyToId"`
		AttachmentIDs []ID   `json:"attachmentIds"`
		Nonce         string `json:"nonce"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	se, ctx := sess(r), r.Context()
	if !s.lim.messages.Allow("u" + ID(se.UserID).String()) {
		return errRateLimited
	}
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	c, a, err := s.requireTextChannel(ctx, se, id)
	if err != nil {
		return err
	}
	content, ok := cleanMessage(req.Content, s.cfg.MaxMessageChars)
	if !ok {
		return badRequest("message_too_long")
	}
	if len(req.AttachmentIDs) > maxAttachmentsPerMsg {
		return badRequest("too_many_attachments")
	}
	if content == "" && len(req.AttachmentIDs) == 0 {
		return badRequest("empty_message")
	}
	if err := s.postCheck(c, a, se.UserID, len(req.AttachmentIDs) > 0); err != nil {
		return err
	}
	if len(req.Nonce) > 64 {
		req.Nonce = ""
	}
	if c.Kind == "dm" {
		if err := s.checkDMAllowed(ctx, c, se.UserID); err != nil {
			return err
		}
	}
	audienceIDs, err := s.channelAudience(ctx, c)
	if err != nil {
		return err
	}
	audience := map[int64]bool{}
	for _, u := range audienceIDs {
		audience[u] = true
	}
	mentions := parseMentions(content, audience)
	everyone := !c.isDM() && c.may(a, PermMentionEveryone) && strings.Contains(content, "@everyone")

	msgID, now := store.NewID(), s.nowMs()
	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		var reply any
		if req.ReplyToID != 0 {
			var n int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages WHERE id=? AND channel_id=?`, int64(req.ReplyToID), id).Scan(&n); err != nil {
				return err
			}
			if n == 0 {
				return badRequest("invalid_reply")
			}
			reply = int64(req.ReplyToID)
		}
		ev := 0
		if everyone {
			ev = 1
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO messages(id, channel_id, author_id, content, reply_to_id, mentions_everyone, created_at) VALUES (?,?,?,?,?,?,?)`,
			msgID, id, se.UserID, content, reply, ev, now); err != nil {
			return err
		}
		if len(req.AttachmentIDs) > 0 {
			ids := idsOf(req.AttachmentIDs)
			res, err := tx.ExecContext(ctx, `UPDATE attachments SET message_id=?, attached=1 WHERE id IN (`+placeholders(len(ids))+`)
				AND uploader_id=? AND channel_id=? AND attached=0`, append(append([]any{msgID}, idArgs(ids)...), se.UserID, id)...)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n != int64(len(ids)) {
				return badRequest("invalid_attachments")
			}
		}
		for _, u := range mentions {
			if _, err := tx.ExecContext(ctx, `INSERT INTO message_mentions(message_id, user_id) VALUES (?,?)`, msgID, u); err != nil {
				return err
			}
		}
		// In DMs every message counts like a mention (badge per message).
		bump := mentions
		if everyone || c.isDM() {
			bump = audienceIDs
		}
		for _, u := range bump {
			if u == se.UserID {
				continue
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO read_states(user_id, channel_id, last_read_id, mention_count) VALUES (?,?,0,1)
				ON CONFLICT(user_id, channel_id) DO UPDATE SET mention_count=mention_count+1`, u, id); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO read_states(user_id, channel_id, last_read_id, mention_count) VALUES (?,?,?,0)
			ON CONFLICT(user_id, channel_id) DO UPDATE SET last_read_id=excluded.last_read_id, mention_count=0`, se.UserID, id, msgID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE channels SET last_message_id=? WHERE id=?`, msgID, id)
		return err
	})
	if err != nil {
		return err
	}
	msgs, err := s.hydrate(ctx, se.UserID, []msgRow{{ID: msgID, ChannelID: id, AuthorID: se.UserID, ReplyTo: int64(req.ReplyToID), Content: content, Everyone: everyone, CreatedAt: now}})
	if err != nil {
		return err
	}
	m := msgs[0]
	m.Nonce = req.Nonce
	s.hub.toUsers(audienceIDs, "message_create", m)
	writeJSON(w, 201, m)
	return nil
}

// checkDMAllowed forbids messages/calls in a 1:1 DM when either side blocked
// or the two are not (or no longer) friends. The history stays
// readable.
func (s *Server) checkDMAllowed(ctx context.Context, c *Channel, uid int64) error {
	ids, err := queryIDs(ctx, s.db.R, `SELECT user_id FROM channel_members WHERE channel_id=? AND user_id<>?`, c.ID, uid)
	if err != nil {
		return err
	}
	for _, o := range ids {
		b, err := s.blockedEither(ctx, uid, o)
		if err != nil {
			return err
		}
		if b {
			return errf(http.StatusForbidden, "blocked", "cannot message this user")
		}
		f, err := s.areFriends(ctx, uid, o)
		if err != nil {
			return err
		}
		if !f {
			return errf(http.StatusForbidden, "not_friends", "direct messages and calls need a friendship")
		}
	}
	return nil
}

func (s *Server) loadMessage(ctx context.Context, channelID, mid int64) (*msgRow, error) {
	rows, err := s.db.R.QueryContext(ctx, `SELECT `+msgCols+` FROM messages WHERE id=? AND channel_id=? AND deleted_at IS NULL`, mid, channelID)
	if err != nil {
		return nil, err
	}
	ms, err := scanMsgs(rows)
	if err != nil {
		return nil, err
	}
	if len(ms) == 0 {
		return nil, notFound("unknown_message")
	}
	return &ms[0], nil
}

func (s *Server) editMessage(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		Content string `json:"content"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	se, ctx := sess(r), r.Context()
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	mid, err := pathID(r, "mid")
	if err != nil {
		return err
	}
	c, a, err := s.requireTextChannel(ctx, se, id)
	if err != nil {
		return err
	}
	m, err := s.loadMessage(ctx, id, mid)
	if err != nil {
		return err
	}
	if m.AuthorID != se.UserID {
		return errForbidden
	}
	content, ok := cleanMessage(req.Content, s.cfg.MaxMessageChars)
	if !ok {
		return badRequest("message_too_long")
	}
	var natt int
	if err := s.db.R.QueryRowContext(ctx, `SELECT COUNT(*) FROM attachments WHERE message_id=?`, mid).Scan(&natt); err != nil {
		return err
	}
	if content == "" && natt == 0 {
		return badRequest("empty_message")
	}
	audienceIDs, err := s.channelAudience(ctx, c)
	if err != nil {
		return err
	}
	audience := map[int64]bool{}
	for _, u := range audienceIDs {
		audience[u] = true
	}
	mentions := parseMentions(content, audience)
	everyone := m.Everyone || (!c.isDM() && c.may(a, PermMentionEveryone) && strings.Contains(content, "@everyone"))
	now := s.nowMs()
	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		ev := 0
		if everyone {
			ev = 1
		}
		if _, err := tx.ExecContext(ctx, `UPDATE messages SET content=?, edited_at=?, mentions_everyone=? WHERE id=?`, content, now, ev, mid); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM message_mentions WHERE message_id=?`, mid); err != nil {
			return err
		}
		for _, u := range mentions {
			if _, err := tx.ExecContext(ctx, `INSERT INTO message_mentions(message_id, user_id) VALUES (?,?)`, mid, u); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	mids := make([]ID, len(mentions))
	for i, u := range mentions {
		mids[i] = ID(u)
	}
	upd := map[string]any{"id": ID(mid), "channelId": ID(id), "content": content, "editedAt": now, "mentions": mids, "mentionsEveryone": everyone}
	s.hub.toUsers(audienceIDs, "message_update", upd)
	writeJSON(w, 200, upd)
	return nil
}

func (s *Server) deleteMessage(w http.ResponseWriter, r *http.Request) error {
	se, ctx := sess(r), r.Context()
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	mid, err := pathID(r, "mid")
	if err != nil {
		return err
	}
	c, a, err := s.requireTextChannel(ctx, se, id)
	if err != nil {
		return err
	}
	m, err := s.loadMessage(ctx, id, mid)
	if err != nil {
		return err
	}
	if m.AuthorID != se.UserID && (c.isDM() || !a.Has(PermManageMessages)) {
		return errForbidden
	}
	keys, err := s.files.keysForMessage(ctx, mid)
	if err != nil {
		return err
	}
	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		for _, q := range []string{
			`UPDATE messages SET content='', deleted_at=? WHERE id=?`,
			`DELETE FROM reactions WHERE message_id=?`,
			`DELETE FROM pins WHERE message_id=?`,
			`DELETE FROM message_mentions WHERE message_id=?`,
			`DELETE FROM attachments WHERE message_id=?`,
		} {
			args := []any{mid}
			if strings.HasPrefix(q, "UPDATE") {
				args = []any{s.nowMs(), mid}
			}
			if _, err := tx.ExecContext(ctx, q, args...); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.files.removeKeys(keys)
	if m.AuthorID != se.UserID {
		s.audit(ctx, se.UserID, c.SpaceID, "message_delete", mid, map[string]any{"authorId": ID(m.AuthorID)})
	}
	s.emitChannel(ctx, c, "message_delete", map[string]any{"id": ID(mid), "channelId": ID(id)})
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// ---------- reactions ----------

func validEmoji(e string) bool {
	if e == "" || len(e) > 32 || !utf8.ValidString(e) {
		return false
	}
	for _, r := range e {
		if r >= 0x80 {
			continue
		}
		if r >= '0' && r <= '9' || r == '#' || r == '*' {
			continue // keycap sequences
		}
		return false
	}
	return true
}

func (s *Server) reactionTarget(r *http.Request) (*Channel, Access, int64, string, error) {
	se, ctx := sess(r), r.Context()
	id, err := pathID(r, "id")
	if err != nil {
		return nil, Access{}, 0, "", err
	}
	mid, err := pathID(r, "mid")
	if err != nil {
		return nil, Access{}, 0, "", err
	}
	emoji, err := url.PathUnescape(chi.URLParam(r, "emoji"))
	if err != nil || !validEmoji(emoji) {
		return nil, Access{}, 0, "", badRequest("invalid_emoji")
	}
	c, a, err := s.requireTextChannel(ctx, se, id)
	if err != nil {
		return nil, Access{}, 0, "", err
	}
	if _, err := s.loadMessage(ctx, id, mid); err != nil {
		return nil, Access{}, 0, "", err
	}
	return c, a, mid, emoji, nil
}

func (s *Server) addReaction(w http.ResponseWriter, r *http.Request) error {
	se, ctx := sess(r), r.Context()
	if !s.lim.messages.Allow("u" + ID(se.UserID).String()) {
		return errRateLimited
	}
	c, a, mid, emoji, err := s.reactionTarget(r)
	if err != nil {
		return err
	}
	if !c.may(a, PermReact) {
		return errf(http.StatusForbidden, "react_denied", "you cannot react here")
	}
	var kinds, mine int
	if err := s.db.R.QueryRowContext(ctx, `SELECT COUNT(DISTINCT emoji), COALESCE(SUM(user_id=? AND emoji=?),0) FROM reactions WHERE message_id=?`, se.UserID, emoji, mid).Scan(&kinds, &mine); err != nil {
		return err
	}
	if mine > 0 {
		w.WriteHeader(http.StatusNoContent)
		return nil
	}
	if kinds >= maxReactionKinds {
		var exists int
		_ = s.db.R.QueryRowContext(ctx, `SELECT COUNT(*) FROM reactions WHERE message_id=? AND emoji=?`, mid, emoji).Scan(&exists)
		if exists == 0 {
			return errf(http.StatusForbidden, "reaction_limit", "too many reactions")
		}
	}
	if _, err := s.db.W.ExecContext(ctx, `INSERT OR IGNORE INTO reactions(message_id, user_id, emoji, created_at) VALUES (?,?,?,?)`, mid, se.UserID, emoji, s.nowMs()); err != nil {
		return err
	}
	s.emitChannel(ctx, c, "reaction_add", map[string]any{"channelId": ID(c.ID), "messageId": ID(mid), "userId": ID(se.UserID), "emoji": emoji})
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) removeReaction(w http.ResponseWriter, r *http.Request) error {
	se, ctx := sess(r), r.Context()
	c, _, mid, emoji, err := s.reactionTarget(r)
	if err != nil {
		return err
	}
	res, err := s.db.W.ExecContext(ctx, `DELETE FROM reactions WHERE message_id=? AND user_id=? AND emoji=?`, mid, se.UserID, emoji)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		s.emitChannel(ctx, c, "reaction_remove", map[string]any{"channelId": ID(c.ID), "messageId": ID(mid), "userId": ID(se.UserID), "emoji": emoji})
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// ---------- pins ----------

func (s *Server) listPins(w http.ResponseWriter, r *http.Request) error {
	se, ctx := sess(r), r.Context()
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if _, _, err := s.requireTextChannel(ctx, se, id); err != nil {
		return err
	}
	q, err := s.db.R.QueryContext(ctx, `SELECT `+msgCols+` FROM messages WHERE id IN (SELECT message_id FROM pins WHERE channel_id=?) AND deleted_at IS NULL ORDER BY id DESC`, id)
	if err != nil {
		return err
	}
	rows, err := scanMsgs(q)
	if err != nil {
		return err
	}
	out, err := s.hydrate(ctx, se.UserID, rows)
	if err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}

func (s *Server) pinTarget(r *http.Request) (*Channel, int64, error) {
	se, ctx := sess(r), r.Context()
	id, err := pathID(r, "id")
	if err != nil {
		return nil, 0, err
	}
	mid, err := pathID(r, "mid")
	if err != nil {
		return nil, 0, err
	}
	c, a, err := s.requireTextChannel(ctx, se, id)
	if err != nil {
		return nil, 0, err
	}
	if !c.isDM() && !a.Has(PermManageMessages) {
		return nil, 0, errForbidden
	}
	if _, err := s.loadMessage(ctx, id, mid); err != nil {
		return nil, 0, err
	}
	return c, mid, nil
}

func (s *Server) pin(w http.ResponseWriter, r *http.Request) error {
	se, ctx := sess(r), r.Context()
	c, mid, err := s.pinTarget(r)
	if err != nil {
		return err
	}
	var n int
	if err := s.db.R.QueryRowContext(ctx, `SELECT COUNT(*) FROM pins WHERE channel_id=?`, c.ID).Scan(&n); err != nil {
		return err
	}
	if n >= maxPins {
		return errf(http.StatusForbidden, "pin_limit", "too many pins")
	}
	if _, err := s.db.W.ExecContext(ctx, `INSERT OR IGNORE INTO pins(channel_id, message_id, pinned_by, created_at) VALUES (?,?,?,?)`, c.ID, mid, se.UserID, s.nowMs()); err != nil {
		return err
	}
	s.emitChannel(ctx, c, "channel_pins_update", map[string]any{"channelId": ID(c.ID), "messageId": ID(mid), "pinned": true})
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) unpin(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	c, mid, err := s.pinTarget(r)
	if err != nil {
		return err
	}
	if _, err := s.db.W.ExecContext(ctx, `DELETE FROM pins WHERE channel_id=? AND message_id=?`, c.ID, mid); err != nil {
		return err
	}
	s.emitChannel(ctx, c, "channel_pins_update", map[string]any{"channelId": ID(c.ID), "messageId": ID(mid), "pinned": false})
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// ---------- typing, read states ----------

func (s *Server) typingStart(w http.ResponseWriter, r *http.Request) error {
	se, ctx := sess(r), r.Context()
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	c, _, err := s.requireTextChannel(ctx, se, id)
	if err != nil {
		return err
	}
	if s.typing.Allow(ID(se.UserID).String() + ":" + ID(id).String()) {
		ids, err := s.channelAudience(ctx, c)
		if err != nil {
			return err
		}
		others := ids[:0]
		for _, u := range ids {
			if u != se.UserID {
				others = append(others, u)
			}
		}
		s.hub.send(others, "typing_start", map[string]any{"channelId": ID(id), "userId": ID(se.UserID)}, true)
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) ack(w http.ResponseWriter, r *http.Request) error {
	var req struct {
		MessageID ID `json:"messageId"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	se, ctx := sess(r), r.Context()
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if _, _, err := s.requireTextChannel(ctx, se, id); err != nil {
		return err
	}
	if _, err := s.db.W.ExecContext(ctx, `INSERT INTO read_states(user_id, channel_id, last_read_id, mention_count) VALUES (?,?,?,0)
		ON CONFLICT(user_id, channel_id) DO UPDATE SET last_read_id=MAX(last_read_id, excluded.last_read_id), mention_count=0`,
		se.UserID, id, int64(req.MessageID)); err != nil {
		return err
	}
	var last int64
	_ = s.db.R.QueryRowContext(ctx, `SELECT last_read_id FROM read_states WHERE user_id=? AND channel_id=?`, se.UserID, id).Scan(&last)
	s.hub.toUser(se.UserID, "read_state_update", readStateDTO{ChannelID: ID(id), LastReadID: ID(last), MentionCount: 0})
	w.WriteHeader(http.StatusNoContent)
	return nil
}

type readStateDTO struct {
	ChannelID    ID  `json:"channelId"`
	LastReadID   ID  `json:"lastReadId"`
	MentionCount int `json:"mentionCount"`
}

// ---------- search ----------

// ftsQuery turns free text into a safe FTS5 query: every token becomes a
// quoted prefix term, terms are ANDed. No user-controlled FTS syntax.
func ftsQuery(q string) string {
	var parts []string
	for _, tok := range strings.Fields(q) {
		tok = strings.ReplaceAll(tok, `"`, "")
		if tok == "" {
			continue
		}
		parts = append(parts, `"`+tok+`"*`)
		if len(parts) >= 8 {
			break
		}
	}
	return strings.Join(parts, " ")
}

func (s *Server) search(w http.ResponseWriter, r *http.Request) error {
	se, ctx := sess(r), r.Context()
	if !s.lim.messages.Allow("search" + ID(se.UserID).String()) {
		return errRateLimited
	}
	qv := r.URL.Query()
	fq := ftsQuery(clampText(qv.Get("q"), 200))
	visible, err := s.visibleChannelIDs(ctx, se.UserID)
	if err != nil {
		return err
	}
	// Only text channels the caller may view and their DMs.
	where := []string{`m.deleted_at IS NULL`, `(m.channel_id IN (SELECT value FROM json_each(?))
		OR m.channel_id IN (SELECT channel_id FROM channel_members WHERE user_id=?))`}
	args := []any{idsJSON(visible), se.UserID}
	from := `messages m`
	if fq != "" {
		from = `messages_fts f JOIN messages m ON m.id=f.rowid`
		where = append(where, `messages_fts MATCH ?`)
		args = append(args, fq)
	}
	if v, ok := queryInt(r, "spaceId"); ok {
		where = append(where, `m.channel_id IN (SELECT id FROM channels WHERE space_id=?)`)
		args = append(args, v)
	}
	if v, ok := queryInt(r, "channelId"); ok {
		where = append(where, `m.channel_id=?`)
		args = append(args, v)
	}
	if v, ok := queryInt(r, "authorId"); ok {
		where = append(where, `m.author_id=?`)
		args = append(args, v)
	}
	if qv.Get("has") == "file" {
		where = append(where, `EXISTS(SELECT 1 FROM attachments a WHERE a.message_id=m.id)`)
	}
	if v, ok := queryInt(r, "before"); ok {
		where = append(where, `m.id<?`)
		args = append(args, v)
	}
	if fq == "" && len(where) == 2 {
		return badRequest("empty_query")
	}
	q := `SELECT m.id, m.channel_id, m.author_id, COALESCE(m.reply_to_id,0), m.content, m.mentions_everyone, m.edited_at, m.created_at FROM ` +
		from + ` WHERE ` + strings.Join(where, " AND ") + ` ORDER BY m.id DESC LIMIT 25`
	rows, err := s.db.R.QueryContext(ctx, q, args...)
	if err != nil {
		return err
	}
	ms, err := scanMsgs(rows)
	if err != nil {
		return err
	}
	out, err := s.hydrate(ctx, se.UserID, ms)
	if err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}

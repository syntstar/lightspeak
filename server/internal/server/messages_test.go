package server

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
)

func TestMessagesAuthorization(t *testing.T) {
	e := newEnv(t)
	owner := e.register("owner", false)
	bob := e.register("bob", false)
	carol := e.register("carol", false)
	out := e.register("outsider", false)
	_, text, voice := e.spaceWithMembers(owner, bob, carol)
	base := "/api/v1/channels/" + text

	m := expect(t, bob.do("POST", base+"/messages", map[string]any{"content": "hello <@" + carol.id + "> <@" + out.id + "> @everyone", "nonce": "n1"}), 201).json()
	if m["nonce"] != "n1" || m["mentionsEveryone"] != false {
		t.Fatalf("message: %v", m)
	}
	mentions := m["mentions"].([]any)
	if len(mentions) != 1 || mentions[0] != carol.id {
		t.Fatalf("mentions must be limited to channel members: %v", mentions)
	}
	mid := m["id"].(string)
	expect(t, out.do("POST", base+"/messages", map[string]any{"content": "x"}), 404)
	expect(t, bob.do("POST", "/api/v1/channels/"+voice+"/messages", map[string]any{"content": "x"}), 400, "not_text_channel")
	expect(t, bob.do("POST", base+"/messages", map[string]any{"content": "   "}), 400, "empty_message")
	expect(t, bob.do("POST", base+"/messages", map[string]any{"content": strings.Repeat("a", 4001)}), 400, "message_too_long")
	expect(t, bob.do("POST", base+"/messages", map[string]any{"content": "r", "replyToId": "123"}), 400, "invalid_reply")

	// admin @everyone
	ev := expect(t, owner.do("POST", base+"/messages", map[string]any{"content": "@everyone meeting", "replyToId": mid}), 201).json()
	if ev["mentionsEveryone"] != true || str(ev, "referencedMessage", "id") != mid {
		t.Fatalf("everyone/reply: %v", ev)
	}

	// edit: only author
	expect(t, carol.do("PATCH", base+"/messages/"+mid, map[string]any{"content": "hacked"}), 403)
	expect(t, owner.do("PATCH", base+"/messages/"+mid, map[string]any{"content": "hacked"}), 403)
	up := expect(t, bob.do("PATCH", base+"/messages/"+mid, map[string]any{"content": "edited"}), 200).json()
	if up["content"] != "edited" || up["editedAt"] == nil {
		t.Fatal("edit")
	}
	// delete: author or admin, not other members
	expect(t, carol.do("DELETE", base+"/messages/"+mid, nil), 403)
	list := expect(t, carol.do("GET", base+"/messages", nil), 200).list()
	if len(list) != 2 {
		t.Fatalf("list: %d", len(list))
	}
	expect(t, owner.do("DELETE", base+"/messages/"+mid, nil), 204)
	expect(t, owner.do("DELETE", base+"/messages/"+mid, nil), 404)
	list = expect(t, carol.do("GET", base+"/messages", nil), 200).list()
	if len(list) != 1 || str(list[0].(map[string]any), "referencedMessage", "deleted") != "true" {
		t.Fatalf("after delete: %v", list)
	}

	// reactions
	mid2 := ev["id"].(string)
	heart := url.PathEscape("❤️")
	expect(t, carol.do("PUT", base+"/messages/"+mid2+"/reactions/"+heart, nil), 204)
	expect(t, carol.do("PUT", base+"/messages/"+mid2+"/reactions/"+heart, nil), 204)
	expect(t, bob.do("PUT", base+"/messages/"+mid2+"/reactions/"+heart, nil), 204)
	expect(t, bob.do("PUT", base+"/messages/"+mid2+"/reactions/abc", nil), 400, "invalid_emoji")
	expect(t, bob.do("PUT", base+"/messages/"+mid2+"/reactions/"+url.PathEscape("<img>"), nil), 400, "invalid_emoji")
	expect(t, out.do("PUT", base+"/messages/"+mid2+"/reactions/"+heart, nil), 404)
	msgs := expect(t, carol.do("GET", base+"/messages", nil), 200).list()
	rx := msgs[0].(map[string]any)["reactions"].([]any)[0].(map[string]any)
	if rx["count"].(float64) != 2 || rx["me"] != true || rx["emoji"] != "❤️" {
		t.Fatalf("reactions: %v", rx)
	}
	expect(t, carol.do("DELETE", base+"/messages/"+mid2+"/reactions/"+heart, nil), 204)

	// pins: admins only in spaces
	expect(t, bob.do("PUT", base+"/pins/"+mid2, nil), 403)
	expect(t, owner.do("PUT", base+"/pins/"+mid2, nil), 204)
	pins := expect(t, bob.do("GET", base+"/pins", nil), 200).list()
	if len(pins) != 1 {
		t.Fatal("pins")
	}
	expect(t, out.do("GET", base+"/pins", nil), 404)
	expect(t, owner.do("DELETE", base+"/pins/"+mid2, nil), 204)

	// typing & ack
	expect(t, bob.do("POST", base+"/typing", nil), 204)
	expect(t, out.do("POST", base+"/typing", nil), 404)
	expect(t, carol.do("POST", base+"/ack", map[string]any{"messageId": mid2}), 204)
	expect(t, out.do("POST", base+"/ack", map[string]any{"messageId": mid2}), 404)
}

func TestMessagePagination(t *testing.T) {
	e := newEnv(t)
	a := e.register("alice", false)
	_, text, _ := e.spaceWithMembers(a)
	base := "/api/v1/channels/" + text + "/messages"
	var ids []string
	for i := 0; i < 120; i++ {
		ids = append(ids, expect(t, a.do("POST", base, map[string]any{"content": fmt.Sprintf("m%d", i)}), 201).json()["id"].(string))
	}
	first := expect(t, a.do("GET", base, nil), 200).list()
	if len(first) != 50 || first[49].(map[string]any)["content"] != "m119" || first[0].(map[string]any)["content"] != "m70" {
		t.Fatalf("latest page wrong: %d", len(first))
	}
	older := expect(t, a.do("GET", base+"?before="+ids[70]+"&limit=100", nil), 200).list()
	if len(older) != 70 || older[0].(map[string]any)["content"] != "m0" {
		t.Fatalf("before: %d", len(older))
	}
	after := expect(t, a.do("GET", base+"?after="+ids[100], nil), 200).list()
	if len(after) != 19 {
		t.Fatalf("after: %d", len(after))
	}
	around := expect(t, a.do("GET", base+"?around="+ids[60]+"&limit=10", nil), 200).list()
	if len(around) != 10 || around[4].(map[string]any)["id"] != ids[60] {
		t.Fatalf("around: %v", len(around))
	}
}

func TestSearchRespectsAccess(t *testing.T) {
	e := newEnv(t)
	a := e.register("alice", false)
	b := e.register("bob", false)
	_, textA, _ := e.spaceWithMembers(a)
	_, textB, _ := e.spaceWithMembers(b)
	expect(t, a.do("POST", "/api/v1/channels/"+textA+"/messages", map[string]any{"content": "the pineapple plan"}), 201)
	expect(t, b.do("POST", "/api/v1/channels/"+textB+"/messages", map[string]any{"content": "pineapple secret of bob"}), 201)
	res := expect(t, a.do("GET", "/api/v1/search?q=pineap", nil), 200).list()
	if len(res) != 1 || !strings.Contains(res[0].(map[string]any)["content"].(string), "plan") {
		t.Fatalf("search leaked or missed: %v", res)
	}
	// filters cannot widen access
	res = expect(t, a.do("GET", "/api/v1/search?q=pineapple&channelId="+textB, nil), 200).list()
	if len(res) != 0 {
		t.Fatal("channel filter leaked")
	}
	// FTS syntax is neutralised
	expect(t, a.do("GET", "/api/v1/search?q="+url.QueryEscape(`"pine* OR NEAR(`), nil), 200)
	expect(t, a.do("GET", "/api/v1/search", nil), 400, "empty_query")
}

func TestDMMessagesCountAsMentions(t *testing.T) {
	e := newEnv(t)
	a := e.register("anna", false)
	b := e.register("ben", false)
	e.spaceWithMembers(a, b)
	e.befriend(a, b)
	dm := expect(t, a.do("POST", "/api/v1/dms", map[string]any{"userIds": []string{b.id}}), 201).json()
	base := "/api/v1/channels/" + dm["id"].(string)
	expect(t, a.do("POST", base+"/messages", map[string]any{"content": "one"}), 201)
	last := expect(t, a.do("POST", base+"/messages", map[string]any{"content": "two"}), 201).json()
	count := func(uid string) int {
		var n int
		err := e.db.R.QueryRow(`SELECT COALESCE((SELECT mention_count FROM read_states WHERE user_id=? AND channel_id=?),0)`, uid, dm["id"]).Scan(&n)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	if got := count(b.id); got != 2 {
		t.Fatalf("recipient mention count = %d, want 2", got)
	}
	if got := count(a.id); got != 0 {
		t.Fatalf("author mention count = %d, want 0", got)
	}
	expect(t, b.do("POST", base+"/ack", map[string]any{"messageId": last["id"]}), 204)
	if got := count(b.id); got != 0 {
		t.Fatalf("after ack = %d, want 0", got)
	}
}

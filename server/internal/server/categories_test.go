package server

import "testing"

// TestSpaceCategories: new spaces may come with categories, and
// reordering moves channels between categories and out of them.
func TestSpaceCategories(t *testing.T) {
	e := newEnv(t)
	owner := e.register("owner", false)
	mem := e.register("member", false)
	parent := func(i int) *int { return &i }
	bad := []map[string]any{{"kind": "text", "name": "a", "parent": 0}}
	expect(t, owner.do("POST", "/api/v1/spaces", map[string]any{"name": "S", "channels": bad}), 400, "invalid_parent")
	bad = []map[string]any{{"kind": "category", "name": "C"}, {"kind": "category", "name": "D", "parent": 0}}
	expect(t, owner.do("POST", "/api/v1/spaces", map[string]any{"name": "S", "channels": bad}), 400, "invalid_parent")

	r := expect(t, owner.do("POST", "/api/v1/spaces", map[string]any{"name": "S", "channels": []map[string]any{
		{"kind": "category", "name": "Text"}, {"kind": "text", "name": "general", "parent": parent(0)},
		{"kind": "category", "name": "Voice"}, {"kind": "voice", "name": "Lounge", "parent": parent(2)},
	}}), 201).json()
	sp := str(r, "space", "id")
	byName := map[string]map[string]any{}
	for _, c := range r["channels"].([]any) {
		m := c.(map[string]any)
		byName[m["name"].(string)] = m
	}
	if byName["general"]["parentId"] != byName["Text"]["id"] || byName["Lounge"]["parentId"] != byName["Voice"]["id"] {
		t.Fatalf("parents: %v", r["channels"])
	}
	inv := expect(t, owner.do("POST", "/api/v1/spaces/"+sp+"/invites", map[string]any{}), 201).json()["code"].(string)
	expect(t, mem.do("POST", "/api/v1/invites/"+inv+"/accept", nil), 200)

	order := "/api/v1/spaces/" + sp + "/channels/order"
	gen, lounge, text, voice := byName["general"]["id"], byName["Lounge"]["id"], byName["Text"]["id"], byName["Voice"]["id"]
	// voice category first, general into it, Lounge out of any category
	move := []map[string]any{
		{"id": lounge, "position": 0, "parentId": "0"},
		{"id": voice, "position": 1},
		{"id": gen, "position": 2, "parentId": voice},
		{"id": text, "position": 3},
	}
	expect(t, mem.do("PATCH", order, move), 403)
	got := expect(t, owner.do("PATCH", order, move), 200).list()
	pos := map[any]map[string]any{}
	for _, c := range got {
		pos[c.(map[string]any)["id"]] = c.(map[string]any)
	}
	if pos[lounge]["parentId"] != nil || pos[gen]["parentId"] != voice || pos[voice]["position"] != float64(1) {
		t.Fatalf("order: %v", got)
	}
	// categories cannot be nested
	expect(t, owner.do("PATCH", order, []map[string]any{{"id": text, "position": 0, "parentId": voice}}), 400, "invalid_parent")
}

// TestChannelIcon: only icons of the channel's kind, "" resets.
func TestChannelIcon(t *testing.T) {
	e := newEnv(t)
	owner := e.register("owner", false)
	mem := e.register("member", false)
	_, text, voice := e.spaceWithMembers(owner, mem)
	ch := "/api/v1/channels/" + text
	expect(t, mem.do("PATCH", ch, map[string]any{"icon": "megaphone"}), 403)
	expect(t, owner.do("PATCH", ch, map[string]any{"icon": "headphones"}), 400, "invalid_icon")
	expect(t, owner.do("PATCH", ch, map[string]any{"icon": "<svg>"}), 400, "invalid_icon")
	if d := expect(t, owner.do("PATCH", ch, map[string]any{"icon": "megaphone"}), 200).json(); d["icon"] != "megaphone" {
		t.Fatalf("icon: %v", d)
	}
	if d := expect(t, owner.do("PATCH", "/api/v1/channels/"+voice, map[string]any{"icon": "headphones"}), 200).json(); d["icon"] != "headphones" {
		t.Fatalf("voice icon: %v", d)
	}
	if d := expect(t, owner.do("PATCH", ch, map[string]any{"icon": ""}), 200).json(); d["icon"] != nil {
		t.Fatalf("reset: %v", d)
	}
}

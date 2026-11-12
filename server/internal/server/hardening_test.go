package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/syntstar/lightspeak/server/internal/store"
)

func TestVoiceRecoveryChecksCurrentPermissions(t *testing.T) {
	for _, deny := range []Perm{PermView, PermConnect, 0} {
		t.Run(fmt.Sprint(deny), func(t *testing.T) {
			e := newEnv(t)
			owner := e.register("owner", false)
			bob := e.register("bob", false)
			sp, _, voice := e.spaceWithMembers(owner, bob)
			perms := permEveryoneDefault &^ deny &^ PermSpeak &^ PermStream
			if _, err := e.db.W.Exec(`UPDATE spaces SET everyone_perms=? WHERE id=?`, perms, mustID(sp)); err != nil {
				t.Fatal(err)
			}
			e.s.voice.confirm(t.Context(), mustID(voice), mustID(bob.id))
			e.s.voice.mu.Lock()
			defer e.s.voice.mu.Unlock()
			st := e.s.voice.states[mustID(bob.id)]
			if deny != 0 {
				if st != nil {
					t.Fatal("unauthorized participant adopted")
				}
				return
			}
			if st == nil || !st.Listener || !st.NoScreen {
				t.Fatalf("publish restrictions missing: %+v", st)
			}
		})
	}
}

func TestConcurrentAdminDeletionKeepsOneAdmin(t *testing.T) {
	e := newEnv(t)
	a := e.register("alice", true)
	b := e.register("bob", true)
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, uid := range []int64{mustID(a.id), mustID(b.id)} {
		go func() { <-start; results <- e.s.removeAccount(t.Context(), uid) }()
	}
	close(start)
	successes := 0
	for range 2 {
		err := <-results
		if err == nil {
			successes++
		} else {
			var ae *apiError
			if !errors.As(err, &ae) || ae.Code != "last_server_admin" {
				t.Fatal(err)
			}
		}
	}
	if successes != 1 {
		t.Fatalf("deleted %d admins", successes)
	}
}

func TestKeyTransactionRejectsRevokedDevice(t *testing.T) {
	e := newEnv(t)
	a := e.register("alice", false)
	se, err := e.s.authenticate(t.Context(), a.token)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.W.Exec(`UPDATE devices SET revoked_at=1 WHERE id=?`, se.DeviceID); err != nil {
		t.Fatal(err)
	}
	err = e.db.Tx(t.Context(), func(tx *sql.Tx) error { return requireKeySession(t.Context(), tx, se, e.s.nowMs()) })
	if !errors.Is(err, errUnauthorized) {
		t.Fatalf("stale session accepted: %v", err)
	}
}

// Hold the writer so validation happens first, then revoke the device before
// the handler can acquire its transaction. This reproduced the stale check.
func TestEnvelopeWriteRechecksDeviceAfterWaitingForWriter(t *testing.T) {
	e := newEnv(t)
	a := e.register("alice", false)
	sp, _, _ := e.spaceWithMembers(a)
	tx, err := e.db.W.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	before := e.db.W.Stats().WaitCount
	result := make(chan resp, 1)
	go func() {
		result <- a.do("POST", "/api/v1/e2ee/"+sp+"/envelopes", map[string]any{"epoch": 1, "envelopes": []map[string]any{{"deviceId": a.deviceID, "ciphertext": envelope(1)}}})
	}()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for e.db.W.Stats().WaitCount == before {
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatal("handler did not reach write transaction")
		}
	}
	if _, err := tx.Exec(`UPDATE devices SET revoked_at=1 WHERE id=?`, mustID(a.deviceID)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	expect(t, <-result, 401)
	var n int
	if err := e.db.R.QueryRow(`SELECT COUNT(*) FROM media_key_envelopes WHERE container_id=?`, mustID(sp)).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("revoked device wrote an envelope")
	}
}

func TestManualPurgeDrainsAllBatches(t *testing.T) {
	e := newEnv(t)
	a := e.register("alice", false)
	_, text, _ := e.spaceWithMembers(a)
	err := e.db.Tx(t.Context(), func(tx *sql.Tx) error {
		for range 1001 {
			_, err := tx.Exec(`INSERT INTO attachments(id,uploader_id,channel_id,filename,mime,size_bytes,sha256,storage_key,created_at) VALUES (?,?,?,'test','application/octet-stream',1,?, ?,0)`, store.NewID(), mustID(a.id), mustID(text), make([]byte, 32), randomKey())
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	n, err := e.s.PurgeAttachmentsOlderThan(t.Context(), 1)
	if err != nil || n != 1001 {
		t.Fatalf("purged %d: %v", n, err)
	}
}

func TestConcurrentReservationsRespectQuota(t *testing.T) {
	e := newEnv(t)
	e.cfg.QuotaBytes = 10
	var wg sync.WaitGroup
	releases := make(chan func(), 50)
	for range 50 {
		wg.Go(func() {
			release, err := e.s.files.reserve(context.Background(), 1)
			if err == nil {
				releases <- release
			}
		})
	}
	wg.Wait()
	close(releases)
	if len(releases) != 10 {
		t.Fatalf("admitted %d reservations", len(releases))
	}
	for release := range releases {
		release()
	}
}

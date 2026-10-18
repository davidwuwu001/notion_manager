package proxy

import (
	"testing"
	"time"
)

func TestSessionAccountIDDeletionAndConflict(t *testing.T) {
	sm := NewSessionManager(time.Minute)
	sm.Set("one", &Session{AccountID: "workspace-one", AccountEmail: "same@example.com", LastUsedAt: time.Now()})
	sm.Set("two", &Session{AccountID: "workspace-two", AccountEmail: "same@example.com", LastUsedAt: time.Now()})
	release, ok := sm.Acquire("one")
	if !ok {
		t.Fatal("first turn rejected")
	}
	if _, ok := sm.Acquire("one"); ok {
		t.Fatal("concurrent same-session request accepted")
	}
	otherRelease, ok := sm.Acquire("two")
	if !ok {
		t.Fatal("unrelated session blocked")
	}
	otherRelease()
	release()
	release()
	again, ok := sm.Acquire("one")
	if !ok {
		t.Fatal("released session stayed locked")
	}
	again()
	sm.DeleteByAccountID("workspace-one")
	if sm.Get("one") != nil || sm.Get("two") == nil {
		t.Fatal("AccountID deletion affected another workspace")
	}
}

func TestSessionSnapshotsDoNotAliasCallerState(t *testing.T) {
	sm := NewSessionManager(time.Minute)
	created := time.Now()
	input := &Session{AccountID: "one", LastUsedAt: created, UpdatedConfigIDs: []string{"original"}}
	sm.Set("fp", input)
	input.LastUsedAt = created.Add(-time.Hour)
	input.UpdatedConfigIDs[0] = "caller mutation"
	snapshot := sm.Get("fp")
	if snapshot == nil || snapshot.UpdatedConfigIDs[0] != "original" {
		t.Fatal("stored session aliases Set caller")
	}
	snapshot.LastUsedAt = created.Add(-time.Hour)
	snapshot.UpdatedConfigIDs[0] = "reader mutation"
	again := sm.Get("fp")
	if again == nil || again.UpdatedConfigIDs[0] != "original" {
		t.Fatal("stored session aliases Get caller")
	}
	snapshot.LastUsedAt = created
	sm.Set("fp", snapshot)
	if sm.Get("fp").UpdatedConfigIDs[0] != "reader mutation" {
		t.Fatal("explicit session update was not saved")
	}
}

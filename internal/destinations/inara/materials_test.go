package inara

import (
	"context"
	"testing"

	"github.com/pequalsnp/ed-colonization-reporter/internal/journal"
	"github.com/pequalsnp/ed-colonization-reporter/internal/state"
)

func TestApplyMaterialEvent_SnapshotReplaces(t *testing.T) {
	m := map[string]int{"stale": 99}
	changed := applyMaterialEvent(m, raw(t, journal.EventMaterials, map[string]any{
		"Raw":          []any{map[string]any{"Name": "iron", "Count": 300}},
		"Manufactured": []any{map[string]any{"Name": "wornshieldemitters", "Count": 50}},
		"Encoded":      []any{map[string]any{"Name": "shielddensityreports", "Count": 12}},
	}))
	if !changed {
		t.Fatal("snapshot should report a change")
	}
	if _, ok := m["stale"]; ok {
		t.Error("snapshot must replace, not merge: stale key survived")
	}
	if m["iron"] != 300 || m["wornshieldemitters"] != 50 || m["shielddensityreports"] != 12 {
		t.Errorf("snapshot tally wrong: %v", m)
	}
}

func TestApplyMaterialEvent_Increments(t *testing.T) {
	m := map[string]int{"iron": 10}

	if !applyMaterialEvent(m, raw(t, journal.EventMaterialCollected, map[string]any{"Name": "iron", "Count": 3})) || m["iron"] != 13 {
		t.Errorf("MaterialCollected: iron = %d, want 13", m["iron"])
	}
	if !applyMaterialEvent(m, raw(t, journal.EventMaterialDiscarded, map[string]any{"Name": "iron", "Count": 5})) || m["iron"] != 8 {
		t.Errorf("MaterialDiscarded: iron = %d, want 8", m["iron"])
	}
	// EngineerCraft consumes ingredients.
	if !applyMaterialEvent(m, raw(t, journal.EventEngineerCraft, map[string]any{
		"Ingredients": []any{map[string]any{"Name": "iron", "Count": 2}},
	})) || m["iron"] != 6 {
		t.Errorf("EngineerCraft: iron = %d, want 6", m["iron"])
	}
	// Synthesis consumes materials.
	if !applyMaterialEvent(m, raw(t, journal.EventSynthesis, map[string]any{
		"Materials": []any{map[string]any{"Name": "iron", "Count": 1}},
	})) || m["iron"] != 5 {
		t.Errorf("Synthesis: iron = %d, want 5", m["iron"])
	}
	// MissionCompleted material rewards add.
	if !applyMaterialEvent(m, raw(t, journal.EventMissionCompleted, map[string]any{
		"MaterialsReward": []any{map[string]any{"Name": "germanium", "Count": 4}},
	})) || m["germanium"] != 4 {
		t.Errorf("MissionCompleted reward: germanium = %d, want 4", m["germanium"])
	}
}

func TestApplyMaterialEvent_Trade(t *testing.T) {
	m := map[string]int{"shieldpattern": 6}
	changed := applyMaterialEvent(m, raw(t, journal.EventMaterialTrade, map[string]any{
		"Paid":     map[string]any{"Material": "shieldpattern", "Quantity": 6},
		"Received": map[string]any{"Material": "focuscrystals", "Quantity": 1},
	}))
	if !changed {
		t.Fatal("trade should report a change")
	}
	if _, ok := m["shieldpattern"]; ok {
		t.Errorf("fully-paid material should be pruned: %v", m)
	}
	if m["focuscrystals"] != 1 {
		t.Errorf("received focuscrystals = %d, want 1", m["focuscrystals"])
	}
}

func TestAddMaterial_ClampsAndPrunes(t *testing.T) {
	m := map[string]int{"iron": 2}
	// Over-consume: clamp at zero and prune the key.
	if !addMaterial(m, "iron", -5) {
		t.Error("clamp-to-zero should report a change")
	}
	if _, ok := m["iron"]; ok {
		t.Errorf("zeroed material should be pruned: %v", m)
	}
	// Subtracting from an absent (zero) material is a no-op.
	if addMaterial(m, "iron", -1) {
		t.Error("subtracting from absent material should not report a change")
	}
}

func TestBuildMaterialsEvent_SortedRows(t *testing.T) {
	e := buildMaterialsEvent(map[string]int{"iron": 3, "carbon": 1, "germanium": 2}, "2026-05-21T12:00:00Z")
	if e.Name != EventSetCommanderInventoryMaterials {
		t.Fatalf("event name = %q", e.Name)
	}
	rows := e.Data.([]map[string]any)
	want := []string{"carbon", "germanium", "iron"}
	for i, w := range want {
		if rows[i]["itemName"] != w {
			t.Errorf("row %d = %v, want %s (rows must be sorted)", i, rows[i]["itemName"], w)
		}
	}
}

// replayed wraps the raw helper to mark an event as backfill.
func replayed(r journal.Raw) journal.Raw { r.Replayed = true; return r }

func TestUploader_MaterialsSeedFromReplayThenFlushOnce(t *testing.T) {
	sess := state.New()
	sess.SetCommander("Jameson", "F1")
	c := &serverCapture{}
	u := setupEnabled(t, sess, c)

	// Backfill: the startup Materials snapshot arrives replayed. It must seed
	// the tally without enqueuing any normal events.
	snap := replayed(raw(t, journal.EventMaterials, map[string]any{
		"Raw":          []any{map[string]any{"Name": "iron", "Count": 300}},
		"Manufactured": []any{map[string]any{"Name": "wornshieldemitters", "Count": 50}},
	}))
	if err := u.HandleEvent(context.Background(), snap); err != nil {
		t.Fatal(err)
	}
	// A replayed FSDJump must still be skipped (no travel-log duplication).
	if err := u.HandleEvent(context.Background(), replayed(raw(t, journal.EventFSDJump, map[string]any{
		"StarSystem": "Sol", "StarPos": []any{0, 0, 0},
	}))); err != nil {
		t.Fatal(err)
	}
	// Only the materials snapshot is pending — as a dirty flag, not a queued event.
	if u.QueueLen() != 0 {
		t.Fatalf("replayed events must not enqueue; queue = %d", u.QueueLen())
	}

	if err := u.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	reqs := c.snapshot()
	if len(reqs) != 1 || len(reqs[0].Events) != 1 {
		t.Fatalf("want one flush with one materials event; got %d reqs", len(reqs))
	}
	ev := reqs[0].Events[0]
	if ev.Name != EventSetCommanderInventoryMaterials {
		t.Fatalf("flushed event = %q, want materials", ev.Name)
	}

	// Nothing changed since: a second flush must not re-send the materials.
	if err := u.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := len(c.snapshot()); got != 1 {
		t.Errorf("unchanged materials must not re-flush; saw %d requests", got)
	}
}

func TestUploader_MaterialsLiveIncrementCoalesces(t *testing.T) {
	sess := state.New()
	sess.SetCommander("Jameson", "F1")
	c := &serverCapture{}
	u := setupEnabled(t, sess, c)

	// Seed via replayed snapshot, drain it.
	_ = u.HandleEvent(context.Background(), replayed(raw(t, journal.EventMaterials, map[string]any{
		"Raw": []any{map[string]any{"Name": "iron", "Count": 10}},
	})))
	_ = u.Flush(context.Background())

	// A burst of live pickups + a craft, all before the next flush.
	for i := 0; i < 5; i++ {
		_ = u.HandleEvent(context.Background(), raw(t, journal.EventMaterialCollected, map[string]any{"Name": "iron", "Count": 1}))
	}
	_ = u.HandleEvent(context.Background(), raw(t, journal.EventEngineerCraft, map[string]any{
		"Ingredients": []any{map[string]any{"Name": "iron", "Count": 3}},
	}))

	if err := u.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	reqs := c.snapshot()
	// reqs[0] was the seed flush; reqs[1] is the coalesced burst.
	if len(reqs) != 2 {
		t.Fatalf("want 2 flushes (seed + burst); got %d", len(reqs))
	}
	last := reqs[1]
	if len(last.Events) != 1 || last.Events[0].Name != EventSetCommanderInventoryMaterials {
		t.Fatalf("burst must coalesce to one materials event; got %d events", len(last.Events))
	}
	// 10 + 5 collected - 3 crafted = 12.
	rows := last.Events[0].Data.([]any)
	found := false
	for _, r := range rows {
		row := r.(map[string]any)
		if row["itemName"] == "iron" {
			found = true
			if int(row["itemCount"].(float64)) != 12 {
				t.Errorf("iron = %v, want 12", row["itemCount"])
			}
		}
	}
	if !found {
		t.Errorf("iron missing from flushed materials: %v", rows)
	}
}

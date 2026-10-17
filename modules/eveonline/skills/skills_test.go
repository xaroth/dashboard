package skills_test

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"
	"go.yaml.in/yaml/v3"

	"github.com/xaroth/dashboard/module"
	"github.com/xaroth/dashboard/modules/eveonline"
	"github.com/xaroth/dashboard/modules/eveonline/skills"
	"github.com/xaroth/dashboard/storage"
)

var scopes = []string{"esi-skills.read_skillqueue.v1", "esi-skills.read_skills.v1"}

// fakeEVE is EVE SSO and ESI. Login code "<id>:<name>" logs in that
// character; its refresh token is "refresh-<id>". queues are the
// skillqueue bodies by character ID; a missing one is a 500. skills are the
// skills bodies; a missing one is no skills.
type fakeEVE struct {
	mu          sync.Mutex
	queues      map[string]string
	skills      map[string]string
	nameLookups [][]int64
}

func (f *fakeEVE) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	path := r.URL.Host + r.URL.Path
	switch {
	case path == "login.eveonline.com/v2/oauth/token":
		r.ParseForm()
		id, name, _ := strings.Cut(r.PostForm.Get("code"), ":")
		if rt := r.PostForm.Get("refresh_token"); rt != "" {
			id, name = strings.TrimPrefix(rt, "refresh-"), "Refreshed"
		}
		json.NewEncoder(w).Encode(map[string]any{
			"access_token":  jwt(map[string]any{"sub": "CHARACTER:EVE:" + id, "name": name, "scp": scopes}),
			"expires_in":    1199,
			"token_type":    "Bearer",
			"refresh_token": "refresh-" + id,
		})
	case path == "esi.evetech.net/universe/names":
		var ids []int64
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &ids)
		f.nameLookups = append(f.nameLookups, ids)
		var out []map[string]any
		for _, id := range ids {
			out = append(out, map[string]any{"id": id, "name": fmt.Sprintf("Skill %d", id), "category": "inventory_type"})
		}
		json.NewEncoder(w).Encode(out)
	case strings.HasPrefix(path, "esi.evetech.net/characters/") && strings.HasSuffix(path, "/skillqueue"):
		id := strings.Split(r.URL.Path, "/")[2]
		if r.Header.Get("Authorization") == "" {
			http.Error(w, `{"error":"not authenticated"}`, http.StatusUnauthorized)
			return
		}
		body, ok := f.queues[id]
		if !ok {
			http.Error(w, `{"error":"boom"}`, http.StatusInternalServerError)
			return
		}
		fmt.Fprint(w, body)
	case strings.HasPrefix(path, "esi.evetech.net/characters/") && strings.HasSuffix(path, "/skills"):
		body, ok := f.skills[strings.Split(r.URL.Path, "/")[2]]
		if !ok {
			body = `{"skills":[],"total_sp":0}`
		}
		fmt.Fprint(w, body)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeEVE) setQueue(id, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if body == "" {
		delete(f.queues, id)
		return
	}
	f.queues[id] = body
}

func (f *fakeEVE) lookups() [][]int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.nameLookups
}

func jwt(claims map[string]any) string {
	b, _ := json.Marshal(claims)
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"RS256"}`)) + "." + enc(b) + "." + enc([]byte("sig"))
}

func load(tb testing.TB, fake *fakeEVE) *module.Set {
	tb.Helper()
	db, err := storage.Open(tb.Context(), tb.TempDir())
	if err != nil {
		tb.Fatalf("storage: %v", err)
	}
	tb.Cleanup(func() { db.Close() })
	var configs map[string]yaml.Node
	cfg := `eveonline: {contact: [x], client_id: app, client_secret: shh, callback_url: "http://admin/eveonline/callback"}`
	if err := yaml.Unmarshal([]byte(cfg), &configs); err != nil {
		tb.Fatalf("config: %v", err)
	}
	set, err := module.Load(tb.Context(), module.Options{
		Registrations: []module.Registration{skills.Module},
		Configs:       configs,
		Migrator:      db,
		Writer:        db.Writer,
		Reader:        db.Reader,
		Log:           slog.New(slog.DiscardHandler),
		HTTPClient:    eveonline.NewFakeClient(fake),
		Now:           time.Now,
	})
	if err != nil {
		tb.Fatalf("Load: %v", err)
	}
	return set
}

// login runs the SSO flow with code, "<id>:<name>".
func login(tb testing.TB, set *module.Set, code string) {
	tb.Helper()
	mux := http.NewServeMux()
	set.RegisterShared(mux)
	get := func(target string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequestWithContext(tb.Context(), http.MethodGet, target, nil))
		return rec
	}
	loc, err := url.Parse(get("/eveonline/login").Header().Get("Location"))
	if err != nil {
		tb.Fatalf("login redirect: %v", err)
	}
	if rec := get("/eveonline/callback?code=" + url.QueryEscape(code) + "&state=" + loc.Query().Get("state")); rec.Code != http.StatusSeeOther {
		tb.Fatalf("callback: got %d %s, want 303", rec.Code, rec.Body)
	}
}

func at(base time.Time, d time.Duration) *time.Time {
	t := base.Add(d).UTC()
	return &t
}

func entry(pos, skill, level int, start, finish *time.Time) string {
	s := fmt.Sprintf(`{"queue_position":%d,"skill_id":%d,"finished_level":%d,"level_end_sp":%d`, pos, skill, level, level*100_000)
	if start != nil {
		s += fmt.Sprintf(`,"start_date":%q,"finish_date":%q`, start.Format(time.RFC3339), finish.Format(time.RFC3339))
	}
	return s + "}"
}

func queue(tb testing.TB, set *module.Set, params map[string]any) []skills.Character {
	tb.Helper()
	res := set.Resolve(tb.Context(), "eveonline_skills", "queue", params)
	if res.Error != "" {
		tb.Fatalf("queue: %s", res.Error)
	}
	return res.Value.([]skills.Character)
}

func TestQueue(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		now := time.Now()
		fake := &fakeEVE{queues: map[string]string{
			// Finished an hour ago, but ESI keeps it until Alice logs in.
			// Out of order, as positions are what count.
			"1": "[" + strings.Join([]string{
				entry(2, 102, 3, at(now, 2*time.Hour), at(now, 5*time.Hour)),
				entry(0, 100, 5, at(now, -3*time.Hour), at(now, -time.Hour)),
				entry(1, 101, 4, at(now, -time.Hour), at(now, 2*time.Hour)),
			}, ",") + "]",
			"2": "[" + entry(0, 101, 1, nil, nil) + "]",
			"3": "[]",
		}, skills: map[string]string{
			// Skill 100 at level 4 with 450,000 points of 500,000 for V.
			"1": `{"skills":[{"skill_id":100,"skillpoints_in_skill":450000,"trained_skill_level":4,"active_skill_level":4}],"total_sp":5000000,"unallocated_sp":1234}`,
		}}
		set := load(t, fake)
		login(t, set, "1:Alice")
		login(t, set, "2:Bob")
		login(t, set, "3:Carol")
		if err := set.Update(t.Context(), "eveonline_skills"); err != nil {
			t.Fatalf("Update: %v", err)
		}
		fetched := at(now, 0)
		want := []skills.Character{
			{
				// Skill 100 finished: its last 50,000 points count.
				ID: 1, Name: "Alice", Ends: at(now, 5*time.Hour), Remaining: 2, TotalSP: 5_050_000, UnallocatedSP: 1234, FetchedAt: fetched,
				Skills: []skills.Skill{{ID: 101, Name: "Skill 101", Level: 4, Start: at(now, -time.Hour), Finish: at(now, 2*time.Hour)}},
			},
			{ID: 2, Name: "Bob", Paused: true, Remaining: 1, FetchedAt: fetched, Skills: []skills.Skill{{ID: 101, Name: "Skill 101", Level: 1}}},
			{ID: 3, Name: "Carol", FetchedAt: fetched, Skills: []skills.Skill{}},
		}
		if diff := cmp.Diff(want, queue(t, set, map[string]any{"skills": 1})); diff != "" {
			t.Errorf("queue (-want +got):\n%s", diff)
		}

		// Between updates the queue counts down from the stored dates.
		time.Sleep(3 * time.Hour)
		got := queue(t, set, nil)
		if got[0].Remaining != 1 || len(got[0].Skills) != 1 || got[0].Skills[0].ID != 102 || got[0].TotalSP != 5_050_000+400_000 {
			t.Errorf("Alice after 3 hours: got %+v, want only skill 102 left and skill 101's 400,000 points added", got[0])
		}
		time.Sleep(3 * time.Hour)
		if got := queue(t, set, nil)[0]; got.Remaining != 0 || got.Ends != nil || len(got.Skills) != 0 {
			t.Errorf("Alice after 6 hours: got %+v, want an empty queue", got)
		}

		// Names are looked up once.
		if err := set.Update(t.Context(), "eveonline_skills"); err != nil {
			t.Fatalf("second Update: %v", err)
		}
		if diff := cmp.Diff([][]int64{{100, 101, 102}}, fake.lookups()); diff != "" {
			t.Errorf("name lookups (-want +got):\n%s", diff)
		}
	})
}

func TestQueueErrors(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		now := time.Now()
		fake := &fakeEVE{queues: map[string]string{
			"1": "[" + entry(0, 100, 5, at(now, 0), at(now, time.Hour)) + "]",
			"2": "[]",
		}}
		set := load(t, fake)
		login(t, set, "1:Alice")
		login(t, set, "2:Bob")
		if err := set.Update(t.Context(), "eveonline_skills"); err != nil {
			t.Fatalf("Update: %v", err)
		}

		// One character failing keeps its previous queue.
		time.Sleep(10 * time.Minute)
		fake.setQueue("1", "")
		if err := set.Update(t.Context(), "eveonline_skills"); err != nil {
			t.Fatalf("Update with Alice failing: %v", err)
		}
		got := queue(t, set, nil)
		if a := got[0]; a.Error != "requesting skill queue: 500 Internal Server Error: boom" || a.Remaining != 1 || !a.FetchedAt.Equal(now) {
			t.Errorf("Alice: got %+v, want her previous queue with the error", a)
		}
		if b := got[1]; b.Error != "" || !b.FetchedAt.Equal(now.Add(10*time.Minute)) {
			t.Errorf("Bob: got %+v, want a fresh fetch", b)
		}

		// All failing fails the update.
		fake.setQueue("2", "")
		err := set.Update(t.Context(), "eveonline_skills")
		if err == nil || !strings.Contains(err.Error(), "Alice: requesting skill queue") || !strings.Contains(err.Error(), "Bob: requesting skill queue") {
			t.Errorf("got Update error %v, want both characters' errors", err)
		}
	})
}

func TestQueueParams(t *testing.T) {
	t.Parallel()
	set := load(t, &fakeEVE{})
	if err := set.Check("eveonline_skills", "queue", map[string]any{"skills": 51}); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if res := set.Resolve(t.Context(), "eveonline_skills", "queue", map[string]any{"skills": 51}); res.Error == "" {
		t.Errorf("skills 51: got no error")
	}
	if got := queue(t, set, nil); len(got) != 0 || got == nil {
		t.Errorf("before the first update: got %#v, want no characters", got)
	}
}

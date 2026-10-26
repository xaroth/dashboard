package skills_test

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/xaroth/dashboard/module"
	"github.com/xaroth/dashboard/module/moduletest"
	"github.com/xaroth/dashboard/modules/eveonline/esi"
	"github.com/xaroth/dashboard/modules/eveonline/sde"
	"github.com/xaroth/dashboard/modules/eveonline/skills"
)

var scopes = []string{"esi-skills.read_skillqueue.v1", "esi-skills.read_skills.v1"}

// fakeEVE is EVE SSO, ESI and the SDE downloads. Login codes "alice",
// "bob" and "carol" log in characters 1 to 3. queues are the skillqueue
// bodies by character ID; a missing one is a 500. skills are the skills
// bodies; a missing one is no skills. denied characters' skillqueue
// requests get that status.
type fakeEVE struct {
	mu     sync.Mutex
	sde    *sde.Fake
	sso    *esi.FakeSSO
	denied map[string]int
	queues map[string]string
	skills map[string]string
}

func (f *fakeEVE) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Host {
	case "developers.eveonline.com":
		f.sde.ServeHTTP(w, r)
		return
	case "login.eveonline.com":
		f.sso.ServeHTTP(w, r)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	path := r.URL.Host + r.URL.Path
	switch {
	case strings.HasPrefix(path, "esi.evetech.net/characters/") && strings.HasSuffix(path, "/skillqueue"):
		id := strings.Split(r.URL.Path, "/")[2]
		if r.Header.Get("Authorization") == "" {
			http.Error(w, `{"error":"not authenticated"}`, http.StatusUnauthorized)
			return
		}
		if code := f.denied[id]; code != 0 {
			http.Error(w, `{"error":"token is not valid"}`, code)
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

// skillTypes is a types.jsonl with "Skill <id>" for each ID.
func skillTypes(ids ...int) sde.FakeOption {
	var lines []string
	for _, id := range ids {
		lines = append(lines, fmt.Sprintf(`{"_key": %d, "groupID": 255, "name": {"en": "Skill %d"}, "published": true}`, id, id))
	}
	return sde.ExpectFile("types.jsonl", strings.Join(lines, "\n"))
}

// load loads the module with the SDE imported: skills 100 to 102.
func load(tb testing.TB, fake *fakeEVE) *module.Set {
	tb.Helper()
	fake.sde = sde.NewFake(tb,
		sde.ExpectFile("categories.jsonl", `{"_key": 16, "name": {"en": "Skill"}, "published": true}`),
		sde.ExpectFile("groups.jsonl", `{"_key": 255, "categoryID": 16, "name": {"en": "Gunnery"}, "published": true}`),
		skillTypes(100, 101, 102))
	fake.sso = esi.NewFake(tb,
		esi.ExpectCharacter("alice", esi.Character{ID: 1, Name: "Alice"}, scopes...),
		esi.ExpectCharacter("bob", esi.Character{ID: 2, Name: "Bob"}, scopes...),
		esi.ExpectCharacter("carol", esi.Character{ID: 3, Name: "Carol"}, scopes...))
	set := moduletest.Load(tb, nil, esi.FakeSSOConfig, module.Options{
		Registrations: []module.Registration{skills.Module},
		HTTPClient:    moduletest.HTTPClient(fake),
	})
	if err := set.Update(tb.Context(), "eveonline_sde"); err != nil {
		tb.Fatalf("SDE Update: %v", err)
	}
	return set
}

// login runs the SSO flow with code.
func login(tb testing.TB, set *module.Set, code string) {
	tb.Helper()
	mux := http.NewServeMux()
	set.RegisterAdmin(mux)
	get := func(target string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequestWithContext(tb.Context(), http.MethodGet, target, nil))
		return rec
	}
	loc, err := url.Parse(get("/eveonline_esi/login").Header().Get("Location"))
	if err != nil {
		tb.Fatalf("login redirect: %v", err)
	}
	if rec := get("/eveonline_esi/callback?code=" + url.QueryEscape(code) + "&state=" + loc.Query().Get("state")); rec.Code != http.StatusSeeOther {
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
		login(t, set, "alice")
		login(t, set, "bob")
		login(t, set, "carol")
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
		login(t, set, "alice")
		login(t, set, "bob")
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

func TestQueueNames(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		now := time.Now()
		fake := &fakeEVE{queues: map[string]string{"1": "[" + entry(0, 103, 1, at(now, 0), at(now, time.Hour)) + "]"}}
		set := load(t, fake)
		login(t, set, "alice")
		if err := set.Update(t.Context(), "eveonline_skills"); err != nil {
			t.Fatalf("Update: %v", err)
		}
		if got := queue(t, set, nil)[0].Skills; len(got) != 1 || got[0].Name != "" {
			t.Errorf("got %+v, want the skill without a name: the SDE does not have it", got)
		}
		// A new SDE build names it without a skills update.
		fake.sde.Set(t, sde.ExpectBuild(2), skillTypes(100, 101, 102, 103))
		if err := set.Update(t.Context(), "eveonline_sde"); err != nil {
			t.Fatalf("SDE Update: %v", err)
		}
		if got := queue(t, set, nil)[0].Skills; len(got) != 1 || got[0].Name != "Skill 103" {
			t.Errorf("got %+v, want the skill named", got)
		}
	})
}

func TestQueueRemoved(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		fake := &fakeEVE{queues: map[string]string{"1": "[]", "2": "[]"}}
		set := load(t, fake)
		login(t, set, "alice")
		login(t, set, "bob")
		if err := set.Update(t.Context(), "eveonline_skills"); err != nil {
			t.Fatalf("Update: %v", err)
		}
		mux := http.NewServeMux()
		set.RegisterAdmin(mux)
		remove := func(id string) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/eveonline_esi/remove", strings.NewReader("character_id="+id))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusSeeOther {
				t.Fatalf("remove %s: got %d %s, want 303", id, rec.Code, rec.Body)
			}
		}

		// Left out before the next update.
		remove("1")
		if got := queue(t, set, nil); len(got) != 1 || got[0].Name != "Bob" {
			t.Errorf("got %+v, want only Bob", got)
		}
		// With none left the module is disabled and keeps its snapshot.
		remove("2")
		if got := queue(t, set, nil); len(got) != 0 {
			t.Errorf("got %+v, want no characters", got)
		}
	})
}

func TestQueueAuth(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		fake := &fakeEVE{queues: map[string]string{}}
		set := load(t, fake)
		login(t, set, "alice")
		login(t, set, "bob")
		fake.sso.Set(t, esi.ExpectToken_Revoked(1))
		// Past expiry, so Alice's token is refreshed and rejected.
		time.Sleep(30 * time.Minute)

		// Bob failing otherwise must not pause the module.
		err := set.Update(t.Context(), "eveonline_skills")
		if err == nil || errors.Is(err, module.ErrAuth) {
			t.Errorf("got Update error %v, want one that is not ErrAuth", err)
		}
		fake.sso.Set(t, esi.ExpectToken_Revoked(2))
		time.Sleep(30 * time.Minute)
		if err := set.Update(t.Context(), "eveonline_skills"); !errors.Is(err, module.ErrAuth) {
			t.Errorf("got Update error %v, want ErrAuth: every character must log in again", err)
		}
	})
}

func TestQueueDenied(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name string
		code int
	}{
		{"revoked", http.StatusUnauthorized},
		{"scope removed", http.StatusForbidden},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				fake := &fakeEVE{denied: map[string]int{"1": testCase.code}, queues: map[string]string{}}
				set := load(t, fake)
				login(t, set, "alice")
				if err := set.Update(t.Context(), "eveonline_skills"); !errors.Is(err, module.ErrAuth) {
					t.Errorf("got Update error %v, want ErrAuth", err)
				}
			})
		})
	}
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
